package server

import (
	"net"
	"sync"
)

// mutexConn serializes Write on a socket so parallel sub-agents can emit NDJSON safely.
type mutexConn struct {
	net.Conn
	mu sync.Mutex
}

func (c *mutexConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.Write(b)
}

func guardConn(conn net.Conn) net.Conn {
	if _, ok := conn.(*mutexConn); ok {
		return conn
	}
	return &mutexConn{Conn: conn}
}
