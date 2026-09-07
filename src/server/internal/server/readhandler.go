package server

type readHandler interface {

	register(svr *Server, c *Connection)

	remove(c *Connection)
}
