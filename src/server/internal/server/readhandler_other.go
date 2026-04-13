//go:build !linux

package server

// newReadHandler constructs the goroutine-per-connection read handler (non-Linux).
func newReadHandler(_ *Server) readHandler {
	return newGoroutineReadHandler()
}
