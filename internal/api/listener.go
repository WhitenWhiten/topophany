package api

import (
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"
)

const maxConnectionLifetime = 2 * time.Minute
const maxAuthenticationLifetime = 5 * time.Second
const connectionDrainTimeout = 15 * time.Second

// LimitListener bounds connections before TLS or HTTP parsing. Each source gets
// at most a quarter of the pending capacity (and never more than 16 slots).
// New arrivals replace the oldest unverified socket when that quota is full,
// so a shared NAT or /64 cannot permanently occupy the authentication path.
// Use ConnectionContext on the HTTP server to protect authenticated sockets.
// IPv4-mapped addresses share a quota with IPv4; IPv6 addresses share a /64.
// Source identity comes only from the socket, never from forwarded HTTP headers.
func LimitListener(inner net.Listener, maximum int) net.Listener {
	if maximum < 1 {
		panic("invalid connection limit")
	}
	return newLimitedListener(inner, maximum, max(1, min(16, maximum/4)), maxConnectionLifetime)
}

func newLimitedListener(inner net.Listener, maximum, perSource int, lifetime time.Duration) *limitedListener {
	if maximum < 1 || perSource < 1 || perSource > maximum || lifetime <= 0 {
		panic("invalid connection limits")
	}
	return &limitedListener{Listener: inner, slots: make(chan struct{}, maximum),
		perSource: perSource, lifetime: lifetime, sources: make(map[string]int), principals: make(map[string]int)}
}

type limitedListener struct {
	net.Listener
	slots         chan struct{}
	mu            sync.Mutex
	perSource     int
	lifetime      time.Duration
	sources       map[string]int
	pending       []*limitedConn // oldest first; bounded by the global connection cap
	principals    map[string]int
	authenticated int
}

// removePending holds l.mu. Authenticated sockets retain their global slot,
// but no longer consume a source's unauthenticated quota.
func (l *limitedListener) removePending(c *limitedConn) {
	if i := slices.Index(l.pending, c); i >= 0 {
		l.pending = slices.Delete(l.pending, i, i+1)
	}
	if l.sources[c.source]--; l.sources[c.source] == 0 {
		delete(l.sources, c.source)
	}
}

func (l *limitedListener) evictPending(source string) *limitedConn {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.pending {
		if l.sources[source] < l.perSource || c.source == source {
			// Make selection atomic with promotion. A racing request cannot
			// promote a socket between this choice and Close outside the lock.
			c.closed = true
			return c
		}
	}
	return nil
}

func (l *limitedListener) releaseConn(c *limitedConn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.closed = true
	if c.principal == "" {
		l.removePending(c)
	} else {
		l.authenticated--
		if l.principals[c.principal]--; l.principals[c.principal] == 0 {
			delete(l.principals, c.principal)
		}
	}
	<-l.slots
}

func connectionSource(a net.Addr) string {
	if a == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return "unknown"
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	ip = ip.WithZone("").Unmap()
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().String()
	}
	return ip.String()
}

func (l *limitedListener) reserve(source string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sources[source] >= l.perSource {
		return false
	}
	select {
	case l.slots <- struct{}{}:
		l.sources[source]++
		return true
	default:
		return false
	}
}

func (l *limitedListener) release(source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	<-l.slots
	if l.sources[source]--; l.sources[source] == 0 {
		delete(l.sources, source)
	}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		source := connectionSource(c.RemoteAddr())
		reserved := l.reserve(source)
		if !reserved {
			if old := l.evictPending(source); old != nil {
				_ = old.Close()
			}
			// Authentication or Close may have freed capacity after the first
			// reservation failed, leaving no pending socket to evict.
			reserved = l.reserve(source)
		}
		if !reserved {
			_ = c.Close()
			continue
		}
		out := &limitedConn{Conn: c, listener: l, source: source, accepted: time.Now()}
		out.release = func() { l.releaseConn(out) }
		l.mu.Lock()
		l.pending = append(l.pending, out)
		l.mu.Unlock()
		// A timer, rather than a socket deadline, also bounds active keep-alive
		// and HTTP/2 connections: net/http cannot extend it with SetDeadline.
		out.mu.Lock()
		out.timer = time.AfterFunc(min(l.lifetime, maxAuthenticationLifetime), out.expire)
		out.mu.Unlock()
		return out, nil
	}
}

// expire never cuts an authenticated request off at the rotation boundary.
// Admission and response headers stop reuse; the second timer bounds draining
// even when a peer stops reading or a handler never returns. Slots stay charged.
func (c *limitedConn) expire() {
	c.listener.mu.Lock()
	authenticated := c.principal != ""
	closed := c.closed
	c.listener.mu.Unlock()
	if closed {
		return
	}
	if !authenticated {
		_ = c.Close()
		return
	}
	c.mu.Lock()
	if !c.stopped {
		c.draining = true
		c.timer.Reset(connectionDrainTimeout)
		// Reset retains expire as the callback: the next expiration closes.
		c.stopped = true
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	_ = c.Close()
}

type limitedConn struct {
	net.Conn
	once              sync.Once
	mu                sync.Mutex
	timer             *time.Timer
	draining, stopped bool // protected by mu
	release           func()
	listener          *limitedListener
	source, principal string // principal and closed are protected by listener.mu
	closed            bool
	accepted          time.Time
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.mu.Lock()
		c.stopped = true
		if c.timer != nil {
			c.timer.Stop()
		}
		c.mu.Unlock()
		c.release()
	})
	return err
}
