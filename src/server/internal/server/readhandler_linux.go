//go:build linux

package server

// newReadHandler constructs the Linux epoll-based read handler.
func newReadHandler(svr *Server) readHandler {
	return newEpollPoller(svr)
}
