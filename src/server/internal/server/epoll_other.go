//go:build !linux

package server

import (
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"

	"pixi_game_server/internal/clock"
	"pixi_game_server/internal/metrics"
)

// goroutineReadHandler is the non-Linux readHandler fallback.
// It spawns one goroutine per connection (identical to the original design).
// Goroutine count: one per connected client.
type goroutineReadHandler struct{}

func newGoroutineReadHandler() *goroutineReadHandler {
	slog.Info("goroutine-per-connection read handler started (non-Linux fallback)")
	return &goroutineReadHandler{}
}

func (g *goroutineReadHandler) register(svr *Server, c *Connection) {
	go g.readLoop(svr, c)
}

func (g *goroutineReadHandler) remove(_ *Connection) {}

func (g *goroutineReadHandler) readLoop(svr *Server, c *Connection) {
	defer svr.cleanupConnection(c)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.rawConn.SetReadDeadline(time.Now().Add(60 * time.Second))

		hdr, err := ws.ReadHeader(c.rawConn)
		if err != nil {
			if err != io.EOF {
				metrics.WSReadErrors.Inc()
				slog.Debug("websocket read closed", "player_id", c.player.ID, "err", err)
			}
			return
		}
		if !validClientHeader(hdr) {
			metrics.WSReadErrors.Inc()
			svr.logRejectedFrame(c, hdr)
			return
		}

		var payload []byte
		if hdr.Length > 0 {
			payload = make([]byte, hdr.Length)
			if _, err := io.ReadFull(c.rawConn, payload); err != nil {
				metrics.WSReadErrors.Inc()
				return
			}
		}
		ws.Cipher(payload, hdr.Mask, 0)

		atomic.StoreInt64(&c.lastActivity, clock.Now())

		switch hdr.OpCode {
		case ws.OpClose:
			return
		case ws.OpPing:
			pongFrame, compErr := ws.CompileFrame(ws.NewPongFrame(payload))
			if compErr == nil {
				select {
				case c.writeCh <- writeJob{direct: pongFrame, timeout: directWriteTimeout}:
				default:
				}
			}
		case ws.OpBinary:
			metrics.BytesReceived.Add(float64(len(payload)))
			if !c.rateLimiter.Allow() {
				// Never continue an ordered transition stream after dropping a command.
				metrics.MessagesRateLimited.Inc()
				return
			}
			svr.processMessage(c, payload)
		}
	}
}
