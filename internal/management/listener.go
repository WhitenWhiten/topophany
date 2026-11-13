package management

import (
	"net"
	"sync"
)

// connectionBudget also bounds unauthenticated/TLS connections. Request and
// principal budgets alone would not limit idle peers before authentication.
type connectionBudget struct {
	net.Listener
	slots chan struct{}
}

func (l *connectionBudget) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &budgetConnection{Conn: conn, release: func() { <-l.slots }}, nil
		default:
			conn.Close()
		}
	}
}

type budgetConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *budgetConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
