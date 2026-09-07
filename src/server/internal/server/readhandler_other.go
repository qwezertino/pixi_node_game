//go:build !linux

package server

func newReadHandler(_ *Server) readHandler {
	return newGoroutineReadHandler()
}
