package management

import (
	"net"
	"sync"
)

// connectionBudget also bounds unauthenticated/TLS connections. Request and
// principal budgets alone would not limit idle peers before authentication.
type connectionBudget struct {
	net.Listener
	slots   chan struct{}
	perPeer int
	mu      sync.Mutex
	peers   map[string]int
}

func (l *connectionBudget) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		l.mu.Lock()
		if l.peers == nil {
			l.peers = map[string]int{}
		}
		if l.perPeer > 0 && l.peers[host] >= l.perPeer {
			l.mu.Unlock()
			conn.Close()
			continue
		}
		l.peers[host]++
		l.mu.Unlock()
		releasePeer := sync.OnceFunc(func() {
			l.mu.Lock()
			l.peers[host]--
			if l.peers[host] == 0 {
				delete(l.peers, host)
			}
			l.mu.Unlock()
		})
		select {
		case l.slots <- struct{}{}:
			return &budgetConnection{Conn: conn, authenticate: releasePeer, release: func() { <-l.slots; releasePeer() }}, nil
		default:
			releasePeer()
			conn.Close()
		}
	}
}

type budgetConnection struct {
	net.Conn
	once         sync.Once
	release      func()
	authenticate func()
}

func (c *budgetConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
