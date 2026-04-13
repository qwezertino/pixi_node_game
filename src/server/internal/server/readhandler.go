package server

// readHandler abstracts the strategy for handling incoming WebSocket reads.
//
// On Linux the epoll-based implementation is used: a fixed pool of N goroutines
// serves all client connections via epoll(7).  This reduces goroutine count from
// one-per-connection (2 400 at 2 400 clients) to ~2×GOMAXPROCS (~24), which
// cuts GC STW from ~7 ms to < 0.5 ms.
//
// On non-Linux platforms the goroutine-per-connection fallback is used instead
// (identical to the old handleConnection logic).
type readHandler interface {
	// register begins servicing reads for a newly-promoted WebSocket connection.
	register(svr *Server, c *Connection)

	// remove stops watching a connection (called before rawConn.Close).
	remove(c *Connection)
}
