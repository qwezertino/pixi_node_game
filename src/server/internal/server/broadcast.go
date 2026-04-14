package server

import (
	"container/heap"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"

	"pixi_game_server/internal/metrics"
	"pixi_game_server/internal/types"
)

// wsFrameSlice fills the 10-byte WS binary frame header into the start of slot
// and returns the sub-slice [headerStart:] containing header+payload.
// slot layout: [10 reserved bytes][payload bytes].
// No allocation: returns a slice into slot's existing backing array.
func wsFrameSlice(slot []byte) []byte {
	payloadLen := len(slot) - 10
	switch {
	case payloadLen < 126:
		slot[8] = 0x82 // FIN + binary opcode
		slot[9] = byte(payloadLen)
		return slot[8:]
	case payloadLen <= 65535:
		slot[6] = 0x82 // FIN + binary opcode
		slot[7] = 0x7E // extended 16-bit length
		slot[8] = byte(payloadLen >> 8)
		slot[9] = byte(payloadLen)
		return slot[6:]
	default:
		slot[0] = 0x82 // FIN + binary opcode
		slot[1] = 0x7F // extended 64-bit length
		slot[2] = byte(payloadLen >> 56)
		slot[3] = byte(payloadLen >> 48)
		slot[4] = byte(payloadLen >> 40)
		slot[5] = byte(payloadLen >> 32)
		slot[6] = byte(payloadLen >> 24)
		slot[7] = byte(payloadLen >> 16)
		slot[8] = byte(payloadLen >> 8)
		slot[9] = byte(payloadLen)
		return slot
	}
}

// tickFrame — reference-counted broadcast frame buffer obtained from broadcastFramePool.
// broadcastTick fills it once per tick; each shard calls release() after writing its connections.
// When the last shard releases (refs reaches 0), the buffer returns to the pool.
// This replaces the ring buffer which had an unsafe data race: shards held slices into the
// ring slot's backing array while broadcastTick could overwrite it 32 ticks later.
type tickFrame struct {
	data  []byte // pre-allocated: [10 WS header prefix bytes][payload bytes]
	frame []byte // actual WS frame bytes to write: sub-slice of data
	refs  int32  // atomic countdown; when 0 → return to pool
}

func (f *tickFrame) release() {
	if atomic.AddInt32(&f.refs, -1) == 0 {
		f.data = f.data[:0]
		f.frame = nil
		broadcastFramePool.Put(f)
	}
}

// broadcastFramePool holds pre-allocated 64 KB tickFrame buffers.
// After the first few ticks, no allocations occur on the hot broadcast path.
var broadcastFramePool = sync.Pool{
	New: func() any {
		return &tickFrame{data: make([]byte, 0, 65536)}
	},
}

var connectionSlicePool = sync.Pool{
	New: func() any {
		s := make([]*Connection, 0, 4096)
		return &s
	},
}

type scoredConnection struct {
	conn    *Connection
	score   int64
	overdue bool
}

type topKMinHeap []scoredConnection

func (h topKMinHeap) Len() int           { return len(h) }
func (h topKMinHeap) Less(i, j int) bool { return h[i].score < h[j].score }
func (h topKMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *topKMinHeap) Push(x any) {
	*h = append(*h, x.(scoredConnection))
}

func (h *topKMinHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// Write timeouts.
const (
	// broadcastWriteTimeout — per-connection deadline during mass-write.
	// 100ms = 3× tick budget (33ms). A goroutine parks via Go netpoller waiting
	// for TCP window; if the client can't accept data within 100ms it is dead.
	broadcastWriteTimeout = 100 * time.Millisecond

	// directWriteTimeout — deadline for ACK, pong, initial-state writes.
	directWriteTimeout = 30 * time.Millisecond

	// maxWriteFailures — consecutive write failures before declaring a connection dead.
	// At 30 Hz ticks with broadcastWriteTimeout=100ms: 150 × 100ms = 15s of sustained
	// inability to write before disconnect.
	maxWriteFailures = 150

	// writeChanSize — per-connection channel buffer depth.
	// 32 slots × 33ms/tick ≈ 1s of broadcast frames before dropping.
	// With broadcastWriteTimeout=100ms the write goroutine is busy ≤3 ticks = 3 slots,
	// so the channel will not fill under normal load.
	writeChanSize = 32

	// maxWriteBatchSizeLimit clamps WRITE_BATCH_SIZE from env.
	maxWriteBatchSizeLimit = 64
)

// writeJob is the value type sent over Connection.writeCh.
// Using a value type (not a closure) eliminates one heap allocation per broadcast per connection.
//
//   - Broadcast tick:  frame != nil, direct == nil. Write loop writes frame.frame,
//     then calls frame.release() to decrement the ref-count.
//   - Direct write:    frame == nil, direct != nil. Write loop writes direct bytes.
type writeJob struct {
	frame   *tickFrame // non-nil for broadcast (shared, ref-counted)
	direct  []byte     // non-nil for ACK / pong / initial-state
	timeout time.Duration
}

type fanoutJob struct {
	conns    []*Connection
	frame    *tickFrame
	sentAtNs int64
	dropped  *int64
	wg       *sync.WaitGroup
}

func (s *Server) initFanoutWorkers() {
	workers := s.cfg.Net.FanoutWorkers
	if workers <= 0 {
		workers = s.cfg.Server.Workers * 2
	}
	if workers < 1 {
		workers = 1
	}

	s.fanoutWorkers = workers
	s.fanoutJobs = make(chan fanoutJob, workers*2)

	for i := 0; i < workers; i++ {
		go s.runFanoutWorker()
	}
}

func (s *Server) runFanoutWorker() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case job := <-s.fanoutJobs:
			localDropped := 0
			for _, conn := range job.conns {
				if !s.enqueueBroadcastJob(conn, job.frame, job.sentAtNs) {
					localDropped++
				}
			}
			if localDropped > 0 {
				atomic.AddInt64(job.dropped, int64(localDropped))
			}
			job.wg.Done()
		}
	}
}

func (s *Server) enqueueBroadcastJob(conn *Connection, frame *tickFrame, sentAtNs int64) bool {
	if !atomic.CompareAndSwapInt32(&conn.pendingBroadcast, 0, 1) {
		// Keep latest-state semantics: if one world-state frame is already queued/in-flight,
		// skip enqueuing older snapshots for this connection.
		frame.release()
		return true
	}

	select {
	case conn.writeCh <- writeJob{frame: frame, timeout: broadcastWriteTimeout}:
		atomic.StoreInt64(&conn.lastWorldStateSentNs, sentAtNs)
		if atomic.LoadInt32(&conn.fanoutDrops) != 0 {
			atomic.StoreInt32(&conn.fanoutDrops, 0)
		}
		return true
	default:
		atomic.StoreInt32(&conn.pendingBroadcast, 0)
		frame.release()
		metrics.BroadcastsDropped.Inc()
		if atomic.AddInt32(&conn.fanoutDrops, 1) == s.fanoutDropLimit {
			go s.cleanupConnection(conn)
		}
		return false
	}
}

// startWriteLoop starts the persistent write goroutine for conn.
//
// Design rationale (vs gws lazy-goroutine / connWriteQueue pattern):
//
// The lazy-goroutine pattern spawns a new goroutine each time push() is called on an empty
// queue and exits when the queue drains. At 30 Hz broadcast with N connections that means
// N goroutines spawned and destroyed every tick: 36 000 goroutine lifecycle events/s at
// 1 200 clients. Each spawn allocates a stack; together they cause constant GC mark-assist
// ("soft pauses") even with GOGC=400, producing 80–112 ms observed GC pauses and 80 ms
// p99 tick duration.
//
// One persistent goroutine per connection eliminates all per-tick goroutine creation and
// all closure allocations on the broadcast hot path. Channel sends are O(1), ~50 ns, and
// carry no heap allocation (writeJob is a 40-byte struct passed by copy via the channel).
//
// Goroutine count: 1 per connection (same instantaneous peak as the lazy pattern), but
// long-lived. GC only scans these stacks during STW — it does not create/destroy them.
func (s *Server) startWriteLoop(c *Connection) {
	go func() {
		batchSize := s.writeBatchSize
		if batchSize < 1 {
			batchSize = 1
		} else if batchSize > maxWriteBatchSizeLimit {
			batchSize = maxWriteBatchSizeLimit
		}

		jobs := make([]writeJob, batchSize)
		frames := make([][]byte, batchSize)

		for {
			select {
			case first := <-c.writeCh:
				jobs[0] = first
				if first.frame != nil {
					frames[0] = first.frame.frame
				} else {
					frames[0] = first.direct
				}

				count := 1
				maxTimeout := first.timeout
				for count < batchSize {
					select {
					case job := <-c.writeCh:
						jobs[count] = job
						if job.frame != nil {
							frames[count] = job.frame.frame
						} else {
							frames[count] = job.direct
						}
						if job.timeout > maxTimeout {
							maxTimeout = job.timeout
						}
						count++
					default:
						goto writeBatch
					}
				}

			writeBatch:
				writeStart := time.Now()
				c.rawConn.SetWriteDeadline(time.Now().Add(maxTimeout))
				buffers := net.Buffers(frames[:count])
				n, err := buffers.WriteTo(c.rawConn)
				metrics.WSWriteBatchDuration.Observe(time.Since(writeStart).Seconds())
				metrics.WSWriteBatchJobs.Observe(float64(count))

				for i := 0; i < count; i++ {
					if jobs[i].frame != nil {
						atomic.StoreInt32(&c.pendingBroadcast, 0)
						jobs[i].frame.release()
					}
					frames[i] = nil
					jobs[i] = writeJob{}
				}

				if err != nil {
					metrics.WSWriteErrors.Inc()
					if atomic.AddInt32(&c.writeFailures, 1) >= maxWriteFailures {
						go s.cleanupConnection(c)
						// Drain any tickFrame refs that are already buffered before
						// exiting. cleanupConnection will drain whatever arrives after
						// the map removal (see drainWriteCh in cleanupConnection).
						drainWriteCh(c.writeCh)
						return
					}
				} else {
					atomic.StoreInt32(&c.writeFailures, 0)
					metrics.BytesSent.Add(float64(n))
				}

			case <-c.ctx.Done():
				// Connection is shutting down. Release any tickFrame refs still buffered
				// in the channel so they can return to broadcastFramePool.
				drainWriteCh(c.writeCh)
				return
			}
		}
	}()
}

// drainWriteCh releases all tickFrame refs currently buffered in ch and discards
// direct-write jobs (their frameBytes are owned by the caller, not the pool).
// Must be called after the write-loop goroutine has decided to exit so that
// broadcastFramePool can reclaim all ref-counted 64 KB buffers.
func drainWriteCh(ch chan writeJob) {
	for {
		select {
		case job := <-ch:
			if job.frame != nil {
				job.frame.release()
			}
		default:
			return
		}
	}
}

func (s *Server) selectRecipients(conns []*Connection, nowNs int64) ([]*Connection, int) {
	n := len(conns)
	if n == 0 {
		return conns[:0], 0
	}

	limit := n
	if s.fanoutMaxRecipients > 0 {
		curr := int(atomic.LoadInt64(&s.fanoutRecipientLimit))
		if curr < s.fanoutMinRecipients {
			curr = s.fanoutMinRecipients
		}
		if curr > s.fanoutMaxRecipients {
			curr = s.fanoutMaxRecipients
		}
		if curr < limit {
			limit = curr
		}
	}

	selected := conns[:0]
	activeWindowNs := s.activeWindowNs
	activeStalenessNs := s.activeStalenessNs
	idleStalenessNs := s.idleStalenessNs
	if idleStalenessNs < activeStalenessNs {
		idleStalenessNs = activeStalenessNs
	}

	top := make(topKMinHeap, 0, limit)
	heap.Init(&top)

	for _, conn := range conns {
		stalenessNs := nowNs - atomic.LoadInt64(&conn.lastWorldStateSentNs)
		if stalenessNs < 0 {
			stalenessNs = 0
		}

		idleForNs := nowNs - atomic.LoadInt64(&conn.lastActivity)
		deadlineNs := idleStalenessNs
		active := idleForNs <= activeWindowNs
		if active {
			deadlineNs = activeStalenessNs
		}

		isOverdue := stalenessNs >= deadlineNs

		score := stalenessNs
		if active {
			score += deadlineNs / 2
		}
		if isOverdue {
			// Keep overdue clients prioritized while still respecting the global per-tick cap.
			score += deadlineNs
		}
		drops := int64(atomic.LoadInt32(&conn.fanoutDrops))
		if drops > 0 {
			penalty := drops * (deadlineNs / 8)
			if penalty > score/2 {
				penalty = score / 2
			}
			score -= penalty
		}

		item := scoredConnection{conn: conn, score: score, overdue: isOverdue}
		if top.Len() < limit {
			heap.Push(&top, item)
			continue
		}
		if item.score > top[0].score {
			top[0] = item
			heap.Fix(&top, 0)
		}
	}

	overdueSelected := 0
	for i := range top {
		selected = append(selected, top[i].conn)
		if top[i].overdue {
			overdueSelected++
		}
	}

	return selected, overdueSelected
}

func (s *Server) tuneRecipientLimit(total, selected, overdue, dropped int, fanoutDur time.Duration) {
	if s.fanoutMaxRecipients <= 0 {
		return
	}

	rawCurr := int(atomic.LoadInt64(&s.fanoutRecipientLimit))
	if rawCurr < 1 {
		rawCurr = min(total, s.fanoutMinRecipients)
		if rawCurr < 1 {
			rawCurr = 1
		}
	}
	curr := rawCurr
	next := curr

	if total >= s.fanoutMinRecipients && curr < s.fanoutMinRecipients {
		// Restore to floor quickly when load returns after an idle window.
		next = s.fanoutMinRecipients
	}

	if overdue > next {
		next = overdue
	}

	if dropped > 0 || fanoutDur > s.fanoutTarget*3/2 {
		next = int(float64(next) * 0.9)
	} else if fanoutDur > s.fanoutTarget {
		next = int(float64(next) * 0.95)
	} else if fanoutDur < s.fanoutTarget/2 && selected >= curr*9/10 {
		next = int(float64(next) * 1.05)
		if next == curr {
			next++
		}
	}

	if next < s.fanoutMinRecipients {
		next = s.fanoutMinRecipients
	}
	if next > s.fanoutMaxRecipients {
		next = s.fanoutMaxRecipients
	}
	if next > total {
		next = total
	}
	if next < s.fanoutMinRecipients && total >= s.fanoutMinRecipients {
		next = s.fanoutMinRecipients
	}

	if next != rawCurr {
		atomic.StoreInt64(&s.fanoutRecipientLimit, int64(next))
		metrics.FanoutRecipientLimit.Set(float64(next))

		nowNano := time.Now().UnixNano()
		prev := atomic.LoadInt64(&s.lastFanoutTuneLog)
		if nowNano-prev >= int64(5*time.Second) &&
			atomic.CompareAndSwapInt64(&s.lastFanoutTuneLog, prev, nowNano) {
			slog.Info("fanout recipient limit updated",
				"from", rawCurr,
				"to", next,
				"selected", selected,
				"overdue", overdue,
				"fanout_ms", fanoutDur.Milliseconds(),
				"target_ms", s.fanoutTarget.Milliseconds(),
				"dropped_jobs", dropped)
		}
	}
}

// ── Broadcast ─────────────────────────────────────────────────────────────────

// broadcastTick encodes the game state once and fans it out to every connection's
// writeQueue. Zero-allocation hot path after warm-up (buffer from sync.Pool, ref-counted).
// Each connection's drain goroutine calls f.release() after writing; when refs→0 the
// buffer returns to the pool.
func (s *Server) broadcastTick(allPlayers []types.PlayerState, changed []types.PlayerState, fullSync bool) {
	if len(allPlayers) == 0 {
		return
	}
	if !fullSync && len(changed) == 0 {
		return
	}

	if !fullSync {
		batchNs := atomic.LoadInt64(&s.adaptiveBatchNs)
		if batchNs > 0 {
			now := time.Now().UnixNano()
			last := atomic.LoadInt64(&s.lastBroadcastNs)
			if now-last < batchNs {
				return
			}
			atomic.StoreInt64(&s.lastBroadcastNs, now)
		}
	}

	t0 := time.Now()
	f := broadcastFramePool.Get().(*tickFrame)
	f.data = f.data[:0]
	// Reserve 10 bytes at front for the WS binary frame header (filled by wsFrameSlice below).
	f.data = append(f.data, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if fullSync {
		f.data = s.protocol.AppendGameState(f.data, allPlayers)
	} else {
		f.data = s.protocol.AppendDeltaGameState(f.data, changed)
	}
	f.frame = wsFrameSlice(f.data)
	if payloadSize := len(f.data) - 10; payloadSize > 0 {
		metrics.BroadcastPayloadBytes.Observe(float64(payloadSize))
	}
	metrics.TickPhaseDuration.WithLabelValues("encode").Observe(time.Since(t0).Seconds())

	t1 := time.Now()
	sentAtNs := t1.UnixNano()
	// Snapshot connections under RLock, then release the lock before fanout.
	// This avoids holding the map lock while enqueueing O(N) jobs.
	s.connectionsMu.RLock()
	n := len(s.connections)
	if n == 0 {
		s.connectionsMu.RUnlock()
		f.data = f.data[:0]
		f.frame = nil
		broadcastFramePool.Put(f)
		return
	}
	buf := connectionSlicePool.Get().(*[]*Connection)
	conns := (*buf)[:0]
	if cap(conns) < n {
		conns = make([]*Connection, 0, n)
	}
	for _, conn := range s.connections {
		conns = append(conns, conn)
	}
	metrics.BroadcastTargets.Observe(float64(n))
	s.connectionsMu.RUnlock()

	selectStart := time.Now()
	recipients, overdue := s.selectRecipients(conns, sentAtNs)
	selectDur := time.Since(selectStart)
	metrics.TickFanoutSelectDuration.Observe(selectDur.Seconds())
	metrics.TickPhaseDuration.WithLabelValues("fanout_select").Observe(selectDur.Seconds())
	m := len(recipients)
	if m == 0 {
		for i := range conns {
			conns[i] = nil
		}
		*buf = conns[:0]
		connectionSlicePool.Put(buf)

		f.data = f.data[:0]
		f.frame = nil
		broadcastFramePool.Put(f)
		return
	}

	metrics.BroadcastRecipients.Observe(float64(m))
	metrics.BroadcastOverdueRecipients.Observe(float64(overdue))
	if deferred := n - m; deferred > 0 {
		metrics.BroadcastDeferred.Add(float64(deferred))
	}

	atomic.StoreInt32(&f.refs, int32(m))

	enqueueStart := time.Now()
	dropped := 0
	if s.fanoutWorkers <= 1 || m < s.fanoutWorkers*64 {
		for _, conn := range recipients {
			if !s.enqueueBroadcastJob(conn, f, sentAtNs) {
				dropped++
			}
		}
	} else {
		chunkSize := (m + s.fanoutWorkers - 1) / s.fanoutWorkers
		var wg sync.WaitGroup
		var droppedAtomic int64

		for start := 0; start < m; start += chunkSize {
			end := start + chunkSize
			if end > m {
				end = m
			}
			wg.Add(1)
			s.fanoutJobs <- fanoutJob{
				conns:    recipients[start:end],
				frame:    f,
				sentAtNs: sentAtNs,
				dropped:  &droppedAtomic,
				wg:       &wg,
			}
		}
		wg.Wait()
		dropped = int(atomic.LoadInt64(&droppedAtomic))
	}
	enqueueDur := time.Since(enqueueStart)
	metrics.TickFanoutEnqueueDuration.Observe(enqueueDur.Seconds())
	metrics.TickPhaseDuration.WithLabelValues("fanout_enqueue").Observe(enqueueDur.Seconds())

	for i := range conns {
		conns[i] = nil
	}
	*buf = conns[:0]
	connectionSlicePool.Put(buf)

	fanoutDur := time.Since(t1)
	metrics.TickPhaseDuration.WithLabelValues("fanout_send").Observe(fanoutDur.Seconds())
	metrics.TickFanoutDuration.Observe(fanoutDur.Seconds())
	s.tuneRecipientLimit(n, m, overdue, dropped, fanoutDur)

	if base := s.cfg.Game.BatchInterval; base > 0 {
		curr := time.Duration(atomic.LoadInt64(&s.adaptiveBatchNs))
		if curr <= 0 {
			curr = base
		}

		next := curr
		if fanoutDur > 30*time.Millisecond {
			next = minDuration(curr+10*time.Millisecond, 120*time.Millisecond)
		} else if fanoutDur > 15*time.Millisecond {
			next = minDuration(curr+5*time.Millisecond, 120*time.Millisecond)
		} else if fanoutDur < 6*time.Millisecond && curr > base {
			next = maxDuration(curr-3*time.Millisecond, base)
		}

		if next != curr {
			atomic.StoreInt64(&s.adaptiveBatchNs, next.Nanoseconds())
			metrics.AdaptiveBatchIntervalMs.Set(float64(next.Milliseconds()))

			nowNano := time.Now().UnixNano()
			prev := atomic.LoadInt64(&s.lastBatchTuneLog)
			if nowNano-prev >= int64(5*time.Second) &&
				atomic.CompareAndSwapInt64(&s.lastBatchTuneLog, prev, nowNano) {
				slog.Info("adaptive batch interval updated",
					"from_ms", curr.Milliseconds(),
					"to_ms", next.Milliseconds(),
					"fanout_ms", fanoutDur.Milliseconds())
			}
		}
	}

	if fanoutDur > 20*time.Millisecond {
		nowNano := time.Now().UnixNano()
		prev := atomic.LoadInt64(&s.lastSlowFanoutLog)
		if nowNano-prev >= int64(5*time.Second) &&
			atomic.CompareAndSwapInt64(&s.lastSlowFanoutLog, prev, nowNano) {
			slog.Warn("slow broadcast fanout",
				"duration_ms", fanoutDur.Milliseconds(),
				"connections", n,
				"dropped_jobs", dropped,
				"payload_bytes", len(f.data)-10,
				"full_sync", fullSync,
				"changed_players", len(changed),
				"all_players", len(allPlayers))
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// broadcastEvent sends a pre-compiled WS frame to every connected client.
// Used for join/left notifications. push() returns immediately (non-blocking).
func (s *Server) broadcastEvent(frameBytes []byte) {
	s.connectionsMu.RLock()
	for _, conn := range s.connections {
		select {
		case conn.writeCh <- writeJob{direct: frameBytes, timeout: directWriteTimeout}:
		default:
			metrics.BroadcastsDropped.Inc()
		}
	}
	s.connectionsMu.RUnlock()
}

// ── Per-connection sends ──────────────────────────────────────────────────────

// sendInitialState sends the full game state to a newly connected client.
// Uses the broadcast frame pool + wsFrameSlice to avoid intermediate allocations:
// eliminates the AppendGameState nil-dst alloc and the ws.CompileFrame alloc.
// Remaining allocs: GetAllPlayers ([]PlayerState) + the final frame copy.
func (s *Server) sendInitialState(conn *Connection) {
	allPlayers := s.gameWorld.GetAllPlayers()

	// Borrow a pooled 64 KB buffer — same pool used by broadcastTick.
	f := broadcastFramePool.Get().(*tickFrame)
	f.data = f.data[:0]
	f.data = append(f.data, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)   // reserve 10-byte WS header
	f.data = s.protocol.AppendGameState(f.data, allPlayers) // zero-alloc into pool buf
	frame := wsFrameSlice(f.data)                           // zero-alloc sub-slice

	// Copy frame bytes before returning pool buffer: write loop reads them later.
	frameBytes := make([]byte, len(frame))
	copy(frameBytes, frame)

	f.data = f.data[:0]
	f.frame = nil
	broadcastFramePool.Put(f)

	select {
	case conn.writeCh <- writeJob{direct: frameBytes, timeout: directWriteTimeout}:
		atomic.StoreInt64(&conn.lastWorldStateSentNs, time.Now().UnixNano())
	default:
		metrics.BroadcastsDropped.Inc()
	}
}

// sendDirect wraps data in a WS binary frame and enqueues it on conn's writeQueue.
func (s *Server) sendDirect(conn *Connection, data []byte) {
	frameBytes, err := ws.CompileFrame(ws.NewBinaryFrame(data))
	if err != nil {
		return
	}
	select {
	case conn.writeCh <- writeJob{direct: frameBytes, timeout: directWriteTimeout}:
	default:
		metrics.BroadcastsDropped.Inc()
	}
}

// notifyPlayerJoined notifies all clients that a new player has joined.
// The client filters its own join by player ID.
func (s *Server) notifyPlayerJoined(newPlayer *types.Player) {
	playerState := types.PlayerState{
		ID:          newPlayer.ID,
		X:           uint16(newPlayer.GetX()),
		Y:           uint16(newPlayer.GetY()),
		FacingRight: true,
	}
	data := s.protocol.EncodePlayerJoined(playerState)
	frameBytes, err := ws.CompileFrame(ws.NewBinaryFrame(data))
	if err != nil {
		slog.Error("failed to compile player joined frame", "error", err)
		return
	}
	s.broadcastEvent(frameBytes)
}

// notifyPlayerLeft notifies all clients that a player has disconnected.
func (s *Server) notifyPlayerLeft(leftPlayerID uint32) {
	data := s.protocol.EncodePlayerLeft(leftPlayerID)
	frameBytes, err := ws.CompileFrame(ws.NewBinaryFrame(data))
	if err != nil {
		slog.Error("failed to compile player left frame", "error", err)
		return
	}
	s.broadcastEvent(frameBytes)
}

// runPingLoop periodically checks for stale connections and sends WS pings.
// Replaces the per-shard ping ticker. Runs for the lifetime of the server context.
func (s *Server) runPingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	pingFrame, _ := ws.CompileFrame(ws.NewPingFrame(nil))

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-90 * time.Second).UnixNano()
			s.connectionsMu.RLock()
			for _, conn := range s.connections {
				if atomic.LoadInt64(&conn.lastActivity) < cutoff {
					// No pong within two ping intervals — treat as dead.
					go s.cleanupConnection(conn)
					continue
				}
				select {
				case conn.writeCh <- writeJob{direct: pingFrame, timeout: directWriteTimeout}:
				default:
				}
			}
			s.connectionsMu.RUnlock()

		case <-s.ctx.Done():
			return
		}
	}
}
