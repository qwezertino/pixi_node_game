//go:build linux

package server

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gobwas/ws"
	"golang.org/x/sys/unix"

	"pixi_game_server/internal/clock"
	"pixi_game_server/internal/metrics"
)

const readBufPoolSize = 64

var readBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, readBufPoolSize)
		return &b
	},
}

type epollPoller struct {
	efd int
	mu  sync.RWMutex
	fds map[int]*Connection

	jobs chan *Connection
	svr  *Server
}

func newEpollPoller(svr *Server) *epollPoller {
	efd, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		panic(fmt.Sprintf("EpollCreate1: %v", err))
	}

	workers := runtime.GOMAXPROCS(0) * 2
	ep := &epollPoller{
		efd:  efd,
		fds:  make(map[int]*Connection, 4096),
		jobs: make(chan *Connection, 8192),
		svr:  svr,
	}

	go ep.waitLoop()
	for i := 0; i < workers; i++ {
		go ep.worker()
	}

	slog.Info("epoll read pool started", "workers", workers)
	return ep
}

func connFd(nc net.Conn) (int, error) {
	sc, ok := nc.(syscall.Conn)
	if !ok {
		return 0, fmt.Errorf("conn %T does not implement syscall.Conn", nc)
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var fd int
	if err := raw.Control(func(f uintptr) { fd = int(f) }); err != nil {
		return 0, err
	}
	return fd, nil
}

func (ep *epollPoller) register(_ *Server, c *Connection) {
	fd, err := connFd(c.rawConn)
	if err != nil {
		slog.Error("epoll: cannot get fd", "player_id", c.player.ID, "err", err)
		go ep.svr.cleanupConnection(c)
		return
	}
	c.fd = fd

	ep.mu.Lock()
	ep.fds[fd] = c
	ep.mu.Unlock()

	if err := unix.EpollCtl(ep.efd, unix.EPOLL_CTL_ADD, fd, &unix.EpollEvent{
		Events: unix.EPOLLIN | unix.EPOLLRDHUP | unix.EPOLLONESHOT,
		Fd:     int32(fd),
	}); err != nil {
		slog.Error("epoll: EPOLL_CTL_ADD failed", "player_id", c.player.ID, "fd", fd, "err", err)
		ep.mu.Lock()
		delete(ep.fds, fd)
		ep.mu.Unlock()
		go ep.svr.cleanupConnection(c)
	}
}

func (ep *epollPoller) remove(c *Connection) {
	ep.mu.Lock()
	delete(ep.fds, c.fd)
	ep.mu.Unlock()

	unix.EpollCtl(ep.efd, unix.EPOLL_CTL_DEL, c.fd, nil)
}

func (ep *epollPoller) rearm(c *Connection) {
	unix.EpollCtl(ep.efd, unix.EPOLL_CTL_MOD, c.fd, &unix.EpollEvent{
		Events: unix.EPOLLIN | unix.EPOLLRDHUP | unix.EPOLLONESHOT,
		Fd:     int32(c.fd),
	})
}

func (ep *epollPoller) waitLoop() {
	events := make([]unix.EpollEvent, 256)
	for {
		n, err := unix.EpollWait(ep.efd, events, 100 )
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			slog.Error("EpollWait error", "err", err)
			return
		}
		for i := 0; i < n; i++ {
			ev := events[i]
			fd := int(ev.Fd)

			ep.mu.RLock()
			c, ok := ep.fds[fd]
			ep.mu.RUnlock()
			if !ok {
				continue
			}

			if ev.Events&(unix.EPOLLRDHUP|unix.EPOLLHUP|unix.EPOLLERR) != 0 {
				ep.remove(c)
				go ep.svr.cleanupConnection(c)
				continue
			}

			if ev.Events&unix.EPOLLIN != 0 {

				select {
				case ep.jobs <- c:
				default:
					ep.rearm(c)
				}
			}
		}
	}
}

func (ep *epollPoller) worker() {
	for c := range ep.jobs {
		ep.processRead(c)
	}
}

func (ep *epollPoller) processRead(c *Connection) {
	select {
	case <-c.ctx.Done():
		return
	default:
	}

	c.rawConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

	hdr, err := ws.ReadHeader(c.rawConn)
	if err != nil {
		if err == io.EOF || isClosedErr(err) {

		} else {
			metrics.WSReadErrors.Inc()
		}
		go ep.svr.cleanupConnection(c)
		return
	}
	if !validClientHeader(hdr) {
		metrics.WSReadErrors.Inc()
		ep.svr.logRejectedFrame(c, hdr)
		go ep.svr.cleanupConnection(c)
		return
	}

	var payload []byte
	var poolBuf *[]byte
	if hdr.Length > 0 {
		if int(hdr.Length) <= readBufPoolSize {
			poolBuf = readBufPool.Get().(*[]byte)
			payload = (*poolBuf)[:hdr.Length]
		} else {
			payload = make([]byte, hdr.Length)
		}
		if _, err := io.ReadFull(c.rawConn, payload); err != nil {
			if poolBuf != nil {
				readBufPool.Put(poolBuf)
			}
			metrics.WSReadErrors.Inc()
			go ep.svr.cleanupConnection(c)
			return
		}
	}

	ws.Cipher(payload, hdr.Mask, 0)

	atomic.StoreInt64(&c.lastActivity, clock.Now())

	switch hdr.OpCode {
	case ws.OpClose:
		if poolBuf != nil {
			readBufPool.Put(poolBuf)
		}
		go ep.svr.cleanupConnection(c)
		return

	case ws.OpPing:

		pongFrame, compErr := ws.CompileFrame(ws.NewPongFrame(payload))
		if poolBuf != nil {
			readBufPool.Put(poolBuf)
			poolBuf = nil
		}
		if compErr == nil {
			select {
			case c.writeCh <- writeJob{direct: pongFrame, timeout: directWriteTimeout}:
			default:
			}
		}

	case ws.OpPong:

	case ws.OpBinary:
		metrics.BytesReceived.Add(float64(len(payload)))

		if !c.rateLimiter.Allow() {

			metrics.MessagesRateLimited.Inc()
			if poolBuf != nil {
				readBufPool.Put(poolBuf)
				poolBuf = nil
			}
			go ep.svr.cleanupConnection(c)
			return
		} else {

			ep.svr.processMessage(c, payload)
		}

	default:

	}

	if poolBuf != nil {
		readBufPool.Put(poolBuf)
	}

	ep.rearm(c)
}

func isClosedErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "use of closed network connection") ||
		contains(s, "connection reset") ||
		contains(s, "broken pipe")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && indexStr(s, substr) >= 0)
}

func indexStr(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
