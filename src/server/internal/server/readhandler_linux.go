//go:build linux

package server

func newReadHandler(svr *Server) readHandler {
	return newEpollPoller(svr)
}
