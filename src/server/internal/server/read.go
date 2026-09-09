package server

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"
	"pixi_game_server/internal/clock"
	"pixi_game_server/internal/metrics"
)

func (s *Server) readLoop(c *Connection, reader io.Reader) {
	var buffer [125]byte
	for {
		if c.ctx.Err() != nil {
			return
		}
		if err := c.rawConn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			return
		}
		hdr, err := ws.ReadHeader(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				metrics.WSReadErrors.Inc()
			}
			return
		}
		if !validClientHeader(hdr) {
			metrics.WSReadErrors.Inc()
			s.logRejectedFrame(c, hdr)
			return
		}
		if err := c.rawConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return
		}
		payload := buffer[:hdr.Length]
		if _, err := io.ReadFull(reader, payload); err != nil {
			metrics.WSReadErrors.Inc()
			return
		}
		ws.Cipher(payload, hdr.Mask, 0)
		atomic.StoreInt64(&c.lastActivity, clock.Now())
		if hdr.OpCode != ws.OpBinary && !c.controlLimiter.Allow() {
			metrics.MessagesRateLimited.Inc()
			return
		}
		switch hdr.OpCode {
		case ws.OpClose:
			return
		case ws.OpPing:
			frame, err := ws.CompileFrame(ws.NewPongFrame(payload))
			if err != nil || !c.enqueue(writeJob{direct: frame, timeout: directWriteTimeout}) {
				return
			}
		case ws.OpBinary:
			metrics.BytesReceived.Add(float64(len(payload)))
			if !c.rateLimiter.Allow() {
				metrics.MessagesRateLimited.Inc()
				return
			}
			s.processMessage(c, payload)
		}
	}
}
