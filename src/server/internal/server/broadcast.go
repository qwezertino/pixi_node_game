package server

import (
	"container/heap"
	"encoding/binary"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"

	"pixi_game_server/internal/metrics"
	"pixi_game_server/internal/protocol"
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

var recipientSlicePool = sync.Pool{
	New: func() any {
		s := make([]*Connection, 0, 4096)
		return &s
	},
}

// scoredConnPool pools topKMinHeap slices for selectRecipients.
// selectRecipients is called every tick; pooling eliminates the per-tick
// make(topKMinHeap, 0, N) allocation and the heap.Push interface-boxing allocations
// (32-byte scoredConnection > 16-byte inline threshold → each Push allocates).
var scoredConnPool = sync.Pool{
	New: func() any {
		s := make(topKMinHeap, 0, 4096)
		return &s
	},
}

type scoredConnection struct {
	conn    *Connection
	score   int64
	rrBias  int64
	overdue bool
}

type topKMinHeap []scoredConnection

func (h topKMinHeap) Len() int { return len(h) }
func (h topKMinHeap) Less(i, j int) bool {
	if h[i].score == h[j].score {
		return h[i].rrBias < h[j].rrBias
	}
	return h[i].score < h[j].score
}
func (h topKMinHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

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

	// Time dilation bounds (EVE-style TiDi), in basis points: 10000 = 100% (nominal
	// tick rate), floor at 1000 = 10%, matching EVE's own floor. Below this the
	// simulation would be too slow to be meaningfully playable — a real overload
	// past this point needs a different fix (fewer players, more instances), not a
	// deeper time slowdown.
	dilationBpsFull = 10000
	minDilationBps  = 1000

	// maxWriteFailures — consecutive write failures before declaring a connection dead.
	// With broadcastWriteTimeout=100ms: 150 failures = 15s of sustained
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
	frame          *tickFrame // non-nil for broadcast (shared, ref-counted)
	direct         []byte     // non-nil for pong / initial-state / lifecycle events
	ack            bool       // movement ACK encoded by the writer into a reusable batch buffer
	ackID          uint32
	ackX           uint16
	ackY           uint16
	ackSeq         uint32
	pong           bool
	pongNonce      uint32
	stateCreatedNs int64 // non-zero for world-state jobs
	enqueuedNs     int64
	timeout        time.Duration
}

func writeJobFrame(job *writeJob, ackBuffer *[15]byte) []byte {
	if job.frame != nil {
		return job.frame.frame
	}
	if job.pong {
		ackBuffer[0] = 0x82
		ackBuffer[1] = 5
		ackBuffer[2] = protocol.MessagePong
		binary.LittleEndian.PutUint32(ackBuffer[3:], job.pongNonce)
		return ackBuffer[:7]
	}
	if !job.ack {
		return job.direct
	}

	// Server frames are unmasked. MOVEMENT_ACK payload is 13 bytes, so the WS
	// header is the compact two-byte form. The buffer belongs to the persistent
	// writer batch and is reused after Write/WriteTo returns.
	ackBuffer[0] = 0x82
	ackBuffer[1] = 13
	ackBuffer[2] = protocol.MessageMovementAck
	binary.LittleEndian.PutUint32(ackBuffer[3:], job.ackID)
	binary.LittleEndian.PutUint16(ackBuffer[7:], job.ackX)
	binary.LittleEndian.PutUint16(ackBuffer[9:], job.ackY)
	binary.LittleEndian.PutUint32(ackBuffer[11:], job.ackSeq)
	return ackBuffer[:]
}

// sendPong is encoded by the persistent writer into its reusable buffer, so
// periodic RTT measurement does not allocate a compiled WebSocket frame per ping.
func (s *Server) sendPong(conn *Connection, nonce uint32) {
	select {
	case conn.writeCh <- writeJob{pong: true, pongNonce: nonce, timeout: directWriteTimeout}:
	default:
		metrics.BroadcastsDropped.Inc()
	}
}

func (s *Server) enqueueBroadcastJob(conn *Connection, frame *tickFrame, stateCreatedNs int64) bool {
	if s.fanoutQueueShedDepth > 0 {
		depth := len(conn.writeCh)
		if depth >= s.fanoutQueueShedDepth {
			metrics.WSWriteQueueDepth.Observe(float64(depth))
			// Queue-aware shedding: skip stale world-state for overloaded clients.
			frame.release()
			metrics.BroadcastsShed.Inc()
			return false
		}
	}

	if !atomic.CompareAndSwapInt32(&conn.pendingBroadcast, 0, 1) {
		// Keep latest-state semantics: if one world-state frame is already queued/in-flight,
		// skip enqueuing older snapshots for this connection.
		frame.release()
		metrics.BroadcastsShed.Inc()
		return true
	}

	select {
	case conn.writeCh <- writeJob{
		frame:          frame,
		stateCreatedNs: stateCreatedNs,
		enqueuedNs:     time.Now().UnixNano(),
		timeout:        broadcastWriteTimeout,
	}:
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
// queue and exits when the queue drains. With N connections that means
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
		ackBuffers := make([][15]byte, batchSize)

		for {
			select {
			case first := <-c.writeCh:
				jobs[0] = first
				frames[0] = writeJobFrame(&jobs[0], &ackBuffers[0])

				count := 1
				maxTimeout := first.timeout
				for count < batchSize {
					select {
					case job := <-c.writeCh:
						jobs[count] = job
						frames[count] = writeJobFrame(&jobs[count], &ackBuffers[count])
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
				writeStartNs := writeStart.UnixNano()
				for i := 0; i < count; i++ {
					if jobs[i].stateCreatedNs == 0 {
						continue
					}
					queueDelay := time.Duration(writeStartNs - jobs[i].enqueuedNs)
					stateAge := time.Duration(writeStartNs - jobs[i].stateCreatedNs)
					metrics.WorldStateQueueDelay.Observe(queueDelay.Seconds())
					metrics.WorldStateAgeAtWriteStart.Observe(stateAge.Seconds())
				}
				c.rawConn.SetWriteDeadline(time.Now().Add(maxTimeout))

				// net.Buffers.WriteTo escapes the slice header to the heap on every
				// call (Go escape analysis sees &buffers flow into writeBuffers →
				// pfd.Writev and conservatively heap-allocates the 24-byte header).
				// With pendingBroadcast CAS, count==1 is the common case (one world-
				// state frame per tick). Use rawConn.Write for single frames to avoid
				// the allocation entirely; fall back to net.Buffers only for batches.
				var n int64
				var err error
				if count == 1 {
					var nn int
					nn, err = c.rawConn.Write(frames[0])
					n = int64(nn)
				} else {
					buffers := net.Buffers(frames[:count])
					n, err = buffers.WriteTo(c.rawConn)
				}
				metrics.WSWriteBatchDuration.Observe(time.Since(writeStart).Seconds())
				metrics.WSWriteBatchJobs.Observe(float64(count))

				writeEndNs := time.Now().UnixNano()
				for i := 0; i < count; i++ {
					if jobs[i].stateCreatedNs == 0 {
						continue
					}
					ageNs := writeEndNs - jobs[i].stateCreatedNs
					metrics.WorldStateAgeAtWriteEnd.Observe(time.Duration(ageNs).Seconds())
					updateAtomicMax(&s.writePressurePeakNs, ageNs)
				}
				fatalWriteFailure := false
				if err != nil {
					metrics.WSWriteErrors.Inc()
					if atomic.AddInt32(&c.writeFailures, 1) >= maxWriteFailures {
						fatalWriteFailure = true
					}
				} else {
					atomic.StoreInt32(&c.writeFailures, 0)
					metrics.BytesSent.Add(float64(n))
					for i := 0; i < count; i++ {
						if jobs[i].stateCreatedNs == 0 {
							continue
						}
						atomic.StoreInt64(&c.lastWorldStateSentNs, writeEndNs)
					}
				}

				for i := 0; i < count; i++ {
					if jobs[i].frame != nil {
						atomic.StoreInt32(&c.pendingBroadcast, 0)
						jobs[i].frame.release()
					}
					frames[i] = nil
					jobs[i] = writeJob{}
				}

				if fatalWriteFailure {
					go s.cleanupConnection(c)
					// Drain refs buffered before exit. cleanupConnection handles the
					// narrow race after map removal as well.
					drainWriteCh(c.writeCh)
					return
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

func updateAtomicMax(target *int64, value int64) {
	for {
		current := atomic.LoadInt64(target)
		if value <= current || atomic.CompareAndSwapInt64(target, current, value) {
			return
		}
	}
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

func (s *Server) selectRecipients(conns []*Connection, nowNs int64, hardLimit int) ([]*Connection, int, *[]*Connection) {
	n := len(conns)
	if n == 0 {
		return conns[:0], 0, nil
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
	if hardLimit > 0 && hardLimit < limit {
		limit = hardLimit
	}

	// Common/default path: everybody receives the frame. Avoid scoring, heap work,
	// and a second slice when neither recipient cap nor byte budget trims the set.
	if limit >= n {
		overdue := 0
		for _, conn := range conns {
			stalenessNs := nowNs - atomic.LoadInt64(&conn.lastWorldStateSentNs)
			idleForNs := nowNs - atomic.LoadInt64(&conn.lastActivity)
			deadlineNs := s.idleStalenessNs
			if idleForNs <= s.activeWindowNs {
				deadlineNs = s.activeStalenessNs
			}
			if stalenessNs >= deadlineNs {
				overdue++
			}
		}
		return conns, overdue, nil
	}

	selectedPtr := recipientSlicePool.Get().(*[]*Connection)
	selected := (*selectedPtr)[:0]
	if cap(selected) < limit {
		selected = make([]*Connection, 0, limit)
	}
	activeWindowNs := s.activeWindowNs
	activeStalenessNs := s.activeStalenessNs
	idleStalenessNs := s.idleStalenessNs
	debtWeightNs := s.fanoutFairDebtWeightNs
	roundRobinWeightNs := s.fanoutRoundRobinWeightNs
	criticalBoostNs := s.fanoutCriticalBoostNs
	rrEpoch := atomic.AddInt64(&s.fanoutRoundRobinEpoch, 1)
	modBase := int64(n)
	if modBase <= 0 {
		modBase = 1
	}
	if idleStalenessNs < activeStalenessNs {
		idleStalenessNs = activeStalenessNs
	}

	// Pool topKMinHeap to avoid per-tick make([]scoredConnection, 0, limit).
	// Two-phase fill avoids heap.Push interface-boxing (scoredConnection is 32 bytes,
	// above the 16-byte inline threshold → each Push would heap-allocate).
	// Phase 1: direct append until heap is full, then heap.Init once (O(limit), no boxing).
	// Phase 2: for remaining conns, direct comparison + heap.Fix (no boxing).
	topPtr := scoredConnPool.Get().(*topKMinHeap)
	top := (*topPtr)[:0]
	if cap(top) < limit {
		top = make(topKMinHeap, 0, limit)
	}

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
			score += deadlineNs
		}
		if criticalBoostNs > 0 && nowNs <= atomic.LoadInt64(&conn.criticalUntilNs) {
			score += criticalBoostNs
		}
		if debtWeightNs > 0 {
			debt := int64(atomic.LoadInt32(&conn.fanoutFairDebt))
			if debt > 0 {
				score += debt * debtWeightNs
			}
		}
		rrBias := int64(0)
		if roundRobinWeightNs > 0 {
			rrRank := (int64(conn.player.ID) + rrEpoch) % modBase
			rrBias = (modBase - rrRank) * roundRobinWeightNs / modBase
			score += rrBias
		}
		drops := int64(atomic.LoadInt32(&conn.fanoutDrops))
		if drops > 0 {
			penalty := drops * (deadlineNs / 8)
			if penalty > score/2 {
				penalty = score / 2
			}
			score -= penalty
		}

		item := scoredConnection{conn: conn, score: score, rrBias: rrBias, overdue: isOverdue}
		if len(top) < limit {
			// Phase 1: fill — direct append, no interface boxing.
			top = append(top, item)
			if len(top) == limit {
				// Heap is full for the first time: establish heap property in O(limit).
				heap.Init(&top)
			}
			continue
		}
		// Phase 2: replace minimum if current item scores higher.
		if item.score > top[0].score || (item.score == top[0].score && item.rrBias > top[0].rrBias) {
			top[0] = item
			heap.Fix(&top, 0)
		}
	}
	// If we never filled the heap (n < limit), establish heap property now.
	if len(top) > 0 && len(top) < limit {
		heap.Init(&top)
	}

	overdueSelected := 0
	for i := range top {
		selected = append(selected, top[i].conn)
		if top[i].overdue {
			overdueSelected++
		}
		top[i] = scoredConnection{} // clear pointer refs before returning to pool
	}
	*topPtr = top[:0]
	scoredConnPool.Put(topPtr)

	*selectedPtr = selected
	return selected, overdueSelected, selectedPtr
}

// releaseConnSlice clears the element pointers before returning the backing array to
// the pool: a pooled slice keeps them alive and would pin closed connections.
func releaseConnSlice(conns []*Connection, buf *[]*Connection) {
	for i := range conns {
		conns[i] = nil
	}
	*buf = conns[:0]
	connectionSlicePool.Put(buf)
}

func releaseRecipientSlice(recipients []*Connection, recipientPtr *[]*Connection) {
	for i := range recipients {
		recipients[i] = nil
	}
	*recipientPtr = recipients[:0]
	recipientSlicePool.Put(recipientPtr)
}

func (s *Server) updateFairDebt(conns []*Connection, recipients []*Connection) {
	if s.fanoutFairDebtMax <= 0 || len(conns) == 0 {
		return
	}
	epoch := atomic.AddUint32(&s.fanoutDebtEpoch, 1)
	if epoch == 0 {
		epoch = atomic.AddUint32(&s.fanoutDebtEpoch, 1)
	}
	for _, conn := range recipients {
		atomic.StoreUint32(&conn.fanoutDebtEpoch, epoch)
	}

	for _, conn := range conns {
		if atomic.LoadUint32(&conn.fanoutDebtEpoch) == epoch {
			if s.fanoutFairDebtDec <= 0 {
				atomic.StoreInt32(&conn.fanoutFairDebt, 0)
				continue
			}
			for {
				current := atomic.LoadInt32(&conn.fanoutFairDebt)
				if current <= 0 {
					break
				}
				next := current - s.fanoutFairDebtDec
				if next < 0 {
					next = 0
				}
				if atomic.CompareAndSwapInt32(&conn.fanoutFairDebt, current, next) {
					break
				}
			}
			continue
		}

		if s.fanoutFairDebtInc <= 0 {
			continue
		}
		for {
			current := atomic.LoadInt32(&conn.fanoutFairDebt)
			if current >= s.fanoutFairDebtMax {
				break
			}
			next := current + s.fanoutFairDebtInc
			if next > s.fanoutFairDebtMax {
				next = s.fanoutFairDebtMax
			}
			if atomic.CompareAndSwapInt32(&conn.fanoutFairDebt, current, next) {
				break
			}
		}
	}
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

// tuneTimeDilation is EVE-style TiDi: when the zone can't keep up, slow the
// simulation's own tick rate instead of silently letting replication go stale. It
// replaces the old adaptive-batch-interval backoff — that only throttled how often
// state was *sent*; this actually reduces how often a tick is *computed*, and any
// client watching knows exactly why movement/cooldowns feel slower (a dilation
// factor accompanies every state frame — see protocol.AppendGameState/DeltaGameState).
//
// Trigger is deliberately broad — both compute overrun (tick() itself running long,
// EVE's classic trigger) and write/fanout pressure (this project's actually observed
// bottleneck) — since either means the zone cannot sustain full-rate simulation right
// now. Steps down fast, recovers slowly, same hysteresis shape the old batch-interval
// controller used, floored at 10% (minDilationBps) same as EVE's own floor.
func (s *Server) tuneTimeDilation(writePressure, fanoutDur, computeDur time.Duration) {
	nominal := s.gameWorld.GetNominalTickInterval()
	if nominal <= 0 {
		return
	}

	curr := atomic.LoadInt64(&s.dilationBps)
	if curr <= 0 {
		curr = dilationBpsFull
	}
	next := curr

	switch {
	case writePressure > 75*time.Millisecond || fanoutDur > 30*time.Millisecond ||
		computeDur > nominal+nominal/2:
		next = curr - 1000 // -10%
	case writePressure > 30*time.Millisecond || fanoutDur > 15*time.Millisecond ||
		computeDur > nominal:
		next = curr - 500 // -5%
	case writePressure < 10*time.Millisecond && fanoutDur < 6*time.Millisecond &&
		computeDur < nominal/2 && curr < dilationBpsFull:
		next = curr + 200 // +2%, slower than the drop — recovery should not overshoot
	}

	if next < minDilationBps {
		next = minDilationBps
	}
	if next > dilationBpsFull {
		next = dilationBpsFull
	}

	if next == curr {
		return
	}
	atomic.StoreInt64(&s.dilationBps, next)
	metrics.TimeDilationPercent.Set(float64(next) / 100)

	newInterval := time.Duration(int64(nominal) * dilationBpsFull / next)
	s.gameWorld.SetTickInterval(newInterval)
	metrics.TickIntervalMs.Set(float64(newInterval.Milliseconds()))

	nowNano := time.Now().UnixNano()
	prev := atomic.LoadInt64(&s.lastDilationLog)
	if nowNano-prev >= int64(5*time.Second) &&
		atomic.CompareAndSwapInt64(&s.lastDilationLog, prev, nowNano) {
		slog.Info("time dilation updated",
			"from_pct", curr/100,
			"to_pct", next/100,
			"tick_interval_ms", newInterval.Milliseconds(),
			"compute_ms", computeDur.Milliseconds(),
			"fanout_ms", fanoutDur.Milliseconds(),
			"write_pressure_ms", writePressure.Milliseconds())
	}
}

// currentDilationBps returns the current time-dilation factor in basis points
// (10000 = 100%, nominal), for encoding into each outgoing state frame.
func (s *Server) currentDilationBps() uint16 {
	v := atomic.LoadInt64(&s.dilationBps)
	if v <= 0 {
		v = dilationBpsFull
	}
	if v > 65535 {
		v = 65535
	}
	return uint16(v)
}

// shouldEmitFrame decides whether a paced tick puts a frame on the wire.
//
// Under velocity replication an empty delta still has to go out. The client
// dead-reckons omitted players when a frame arrives, using the worldTick in its header
// as the step count — so suppressing empty frames would freeze every remote player
// until somebody happened to change direction. The heartbeat is a bare 13-byte header,
// roughly 4.2 Mbit/s across 2000 clients at 20 Hz, against the hundreds of Mbit/s the
// omitted records would otherwise have cost.
//
// In legacy mode nothing dead-reckons, so an empty delta carries no information and is
// suppressed as before.
func shouldEmitFrame(fullSync bool, changedCount int, velocityReplication bool) bool {
	return fullSync || changedCount > 0 || velocityReplication
}

// ── Broadcast ─────────────────────────────────────────────────────────────────

// broadcastTick encodes the game state once and fans it out to every connection's
// writeQueue. Zero-allocation hot path after warm-up (buffer from sync.Pool, ref-counted).
// Each connection's drain goroutine calls f.release() after writing; when refs→0 the
// buffer returns to the pool.
func (s *Server) broadcastTick(allPlayers []types.PlayerState, changed []types.PlayerState, fullSync bool, worldTick uint32) bool {
	if len(allPlayers) == 0 {
		return false
	}

	// Replication cadence is now simply "once per simulation tick": there is no
	// separate send-interval backoff on top of it any more. Under pressure, time
	// dilation (see tuneTimeDilation below) slows the tick itself, which slows
	// replication along with it — a single lever instead of two independent ones.
	hasState := shouldEmitFrame(fullSync, len(changed), s.cfg.Net.VelocityReplication)

	t1 := time.Now()
	sentAtNs := t1.UnixNano()
	// Snapshot connections under RLock, then release the lock before fanout.
	// This avoids holding the map lock while enqueueing O(N) jobs.
	s.connectionsMu.RLock()
	n := len(s.connections)
	if n == 0 {
		s.connectionsMu.RUnlock()
		return true
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

	// ACKs have their own delivery path and may coalesce several transitions that
	// were applied inside one replication interval.
	s.enqueueAuthoritativeMovementAcks(conns)

	if !hasState {
		releaseConnSlice(conns, buf)
		return false
	}

	t0 := time.Now()
	// Increment only once a state frame is actually produced: the client treats a
	// sequence distance != 1 as a gap and asks for a full resync.
	stateSequence := atomic.AddUint32(&s.worldStateSeq, 1)
	f := broadcastFramePool.Get().(*tickFrame)
	f.data = f.data[:0]
	// Reserve 10 bytes at front for the WS binary frame header (filled by wsFrameSlice below).
	f.data = append(f.data, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	dilationBps := s.currentDilationBps()
	if fullSync {
		f.data = s.protocol.AppendGameState(f.data, allPlayers, stateSequence, worldTick, dilationBps)
	} else {
		f.data = s.protocol.AppendDeltaGameState(f.data, changed, stateSequence, worldTick, dilationBps)
	}
	f.frame = wsFrameSlice(f.data)
	payloadSize := len(f.data) - 10
	if payloadSize > 0 {
		metrics.BroadcastPayloadBytes.Observe(float64(payloadSize))
	}
	if fullSync {
		metrics.BroadcastRecords.Observe(float64(len(allPlayers)))
	} else {
		metrics.BroadcastRecords.Observe(float64(len(changed)))
	}
	metrics.TickPhaseDuration.WithLabelValues("encode").Observe(time.Since(t0).Seconds())

	selectionLimit := n
	if s.fanoutMaxRecipients > 0 {
		selectionLimit = int(atomic.LoadInt64(&s.fanoutRecipientLimit))
		if selectionLimit < s.fanoutMinRecipients {
			selectionLimit = s.fanoutMinRecipients
		}
		if selectionLimit > s.fanoutMaxRecipients {
			selectionLimit = s.fanoutMaxRecipients
		}
		if selectionLimit > n {
			selectionLimit = n
		}
	}

	budgetLimit := 0
	if budgetBytes := s.fanoutMaxBroadcastBytesPerTick; budgetBytes > 0 {
		frameBytes := len(f.frame)
		if frameBytes > 0 {
			budgetRecipients := budgetBytes / frameBytes
			if budgetRecipients < 1 {
				budgetRecipients = 1
			}
			metrics.BroadcastBudgetRecipients.Observe(float64(budgetRecipients))
			if budgetRecipients < selectionLimit {
				budgetLimit = budgetRecipients
				metrics.BroadcastBudgetHits.Inc()
				metrics.BroadcastBudgetTrimmed.Add(float64(selectionLimit - budgetRecipients))
			}
		}
	}

	selectStart := time.Now()
	recipients, overdue, recipientPtr := s.selectRecipients(conns, sentAtNs, budgetLimit)
	selectDur := time.Since(selectStart)
	metrics.TickFanoutSelectDuration.Observe(selectDur.Seconds())
	metrics.TickPhaseDuration.WithLabelValues("fanout_select").Observe(selectDur.Seconds())
	m := len(recipients)
	s.updateFairDebt(conns, recipients)
	if m == 0 {
		if recipientPtr != nil {
			releaseRecipientSlice(recipients, recipientPtr)
		}
		releaseConnSlice(conns, buf)

		f.data = f.data[:0]
		f.frame = nil
		broadcastFramePool.Put(f)
		return false
	}

	metrics.BroadcastRecipients.Observe(float64(m))
	metrics.BroadcastOverdueRecipients.Observe(float64(overdue))
	if deferred := n - m; deferred > 0 {
		metrics.BroadcastDeferred.Add(float64(deferred))
	}

	atomic.StoreInt32(&f.refs, int32(m))

	enqueueStart := time.Now()
	dropped := 0
	for _, conn := range recipients {
		if !s.enqueueBroadcastJob(conn, f, sentAtNs) {
			dropped++
		}
	}
	enqueueDur := time.Since(enqueueStart)
	metrics.TickFanoutEnqueueDuration.Observe(enqueueDur.Seconds())
	metrics.TickPhaseDuration.WithLabelValues("fanout_enqueue").Observe(enqueueDur.Seconds())

	if recipientPtr != nil {
		releaseRecipientSlice(recipients, recipientPtr)
	}
	releaseConnSlice(conns, buf)

	fanoutDur := time.Since(t1)
	metrics.TickPhaseDuration.WithLabelValues("fanout_send").Observe(fanoutDur.Seconds())
	metrics.TickFanoutDuration.Observe(fanoutDur.Seconds())
	s.tuneRecipientLimit(n, m, overdue, dropped, fanoutDur)

	pressure := time.Duration(atomic.SwapInt64(&s.writePressurePeakNs, 0))
	s.tuneTimeDilation(pressure, fanoutDur, s.gameWorld.GetTickDuration())

	if fanoutDur > 20*time.Millisecond {
		nowNano := time.Now().UnixNano()
		prev := atomic.LoadInt64(&s.lastSlowFanoutLog)
		if nowNano-prev >= int64(5*time.Second) &&
			atomic.CompareAndSwapInt64(&s.lastSlowFanoutLog, prev, nowNano) {
			slog.Warn("slow broadcast fanout",
				"duration_ms", fanoutDur.Milliseconds(),
				"connections", n,
				"dropped_jobs", dropped,
				"payload_bytes", payloadSize,
				"full_sync", fullSync,
				"changed_players", len(changed),
				"all_players", len(allPlayers))
		}
	}
	return true
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
	f.data = append(f.data, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0) // reserve 10-byte WS header
	seq := atomic.LoadUint32(&s.worldStateSeq)
	// The tick is read next to the snapshot, so it may be one tick stale if a
	// simulation step lands between the two. A one-step dead-reckoning offset is
	// corrected by the next record or keyframe for that player.
	worldTick := s.gameWorld.GetTickCount()
	f.data = s.protocol.AppendGameState(f.data, allPlayers, seq, worldTick, s.currentDilationBps()) // zero-alloc into pool buf
	frame := wsFrameSlice(f.data)                                           // zero-alloc sub-slice

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

// sendMovementAck queues ACK fields as a value. Encoding happens in the persistent
// writer, avoiding ws.CompileFrame and a heap-backed []byte for every MOVE.
func (s *Server) sendMovementAck(conn *Connection, playerID uint32, x, y uint16, inputSequence uint32) bool {
	select {
	case conn.writeCh <- writeJob{
		ack:     true,
		ackID:   playerID,
		ackX:    x,
		ackY:    y,
		ackSeq:  inputSequence,
		timeout: directWriteTimeout,
	}:
		return true
	default:
		metrics.BroadcastsDropped.Inc()
		return false
	}
}

func (s *Server) enqueueAuthoritativeMovementAcks(conns []*Connection) {
	for _, conn := range conns {
		sequence := conn.player.GetAppliedInputSequence()
		if sequence == atomic.LoadUint32(&conn.lastMovementAckSeq) {
			continue
		}
		x, y := conn.player.GetMovementAckPosition()
		if s.sendMovementAck(conn, conn.player.ID, x, y, sequence) {
			atomic.StoreUint32(&conn.lastMovementAckSeq, sequence)
		}
	}
}

func (s *Server) sendWelcome(conn *Connection) {
	data := s.protocol.EncodeWelcome(conn.player.ID, uint16(s.cfg.Game.TickRate))
	s.sendDirect(conn, data)
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
