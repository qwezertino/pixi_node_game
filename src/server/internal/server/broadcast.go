package server

import (
	"log/slog"
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
		for {
			select {
			case job := <-c.writeCh:
				var frameBytes []byte
				if job.frame != nil {
					frameBytes = job.frame.frame
				} else {
					frameBytes = job.direct
				}
				c.rawConn.SetWriteDeadline(time.Now().Add(job.timeout))
				_, err := c.rawConn.Write(frameBytes)
				if job.frame != nil {
					job.frame.release()
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
					metrics.BytesSent.Add(float64(len(frameBytes)))
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

// ── Broadcast ─────────────────────────────────────────────────────────────────

// broadcastTick encodes the game state once and fans it out to every connection's
// writeQueue. Zero-allocation hot path after warm-up (buffer from sync.Pool, ref-counted).
// Each connection's drain goroutine calls f.release() after writing; when refs→0 the
// buffer returns to the pool.
func (s *Server) broadcastTick(allPlayers []types.PlayerState, changed []types.PlayerState, fullSync bool) {
	if len(allPlayers) == 0 {
		return
	}

	t0 := time.Now()
	f := broadcastFramePool.Get().(*tickFrame)
	f.data = f.data[:0]
	// Reserve 10 bytes at front for the WS binary frame header (filled by wsFrameSlice below).
	f.data = append(f.data, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if fullSync || len(changed) == 0 {
		f.data = s.protocol.AppendGameState(f.data, allPlayers)
	} else {
		f.data = s.protocol.AppendDeltaGameState(f.data, changed)
	}
	f.frame = wsFrameSlice(f.data)
	metrics.TickPhaseDuration.WithLabelValues("encode").Observe(time.Since(t0).Seconds())

	t1 := time.Now()
	// Snapshot connection count and set refs atomically before any send can call release().
	// RLock prevents additions/removals during the fan-out loop.
	s.connectionsMu.RLock()
	n := len(s.connections)
	if n == 0 {
		s.connectionsMu.RUnlock()
		f.data = f.data[:0]
		f.frame = nil
		broadcastFramePool.Put(f)
		return
	}
	atomic.StoreInt32(&f.refs, int32(n))
	for _, conn := range s.connections {
		// Non-blocking send into the connection's persistent write-loop goroutine.
		// Zero allocation: writeJob is a 40-byte value — no closure, no heap alloc.
		select {
		case conn.writeCh <- writeJob{frame: f, timeout: broadcastWriteTimeout}:
		default:
			// Channel full: write goroutine is busy with a previous frame (backpressure).
			// This is NOT a connection error — do not increment writeFailures.
			// Dead connections are caught by: write timeout (broadcastWriteTimeout ×
			// maxWriteFailures) and the ping loop (90s inactivity).
			f.release()
			metrics.BroadcastsDropped.Inc()
		}
	}
	s.connectionsMu.RUnlock()
	metrics.TickPhaseDuration.WithLabelValues("shard_send").Observe(time.Since(t1).Seconds())
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
