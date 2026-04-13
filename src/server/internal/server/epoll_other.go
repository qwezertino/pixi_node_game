//go:build !linux

package server

import (
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"

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

		var payload []byte
		if hdr.Length > 0 {
			payload = make([]byte, hdr.Length)
			if _, err := io.ReadFull(c.rawConn, payload); err != nil {
				metrics.WSReadErrors.Inc()
				return
			}
		}
		if hdr.Masked {
			ws.Cipher(payload, hdr.Mask, 0)
		}

		atomic.StoreInt64(&c.lastActivity, time.Now().UnixNano())

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
		case ws.OpBinary, ws.OpText:
			metrics.BytesReceived.Add(float64(len(payload)))
			if !c.rateLimiter.Allow() {
				metrics.MessagesRateLimited.Inc()
			} else {
				svr.processMessage(c, payload)
			}
		}
	}
}
