package mihomo

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"
)

// SnapshotCache shares validated response bodies only within a one-second
// collection window. It is runner-owned, discarded on reload, and never stores
// errors or judgments. References bypass it to preserve publication bracketing.
// Four snapshots of at most 8 MiB bound retained private response memory.
type SnapshotCache struct {
	mu      sync.Mutex
	entries map[[32]byte]*coreSnapshot
}
type coreSnapshot struct {
	gate    chan struct{}
	started time.Time
	bodies  map[string][]byte
	bytes   int
}
type snapshotContextKey struct{}

func (c *SnapshotCache) acquire(ctx context.Context, key string) (*coreSnapshot, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256([]byte(key))
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[[32]byte]*coreSnapshot)
	}
	s := c.entries[digest]
	if s == nil && len(c.entries) < 4 {
		s = &coreSnapshot{gate: make(chan struct{}, 1)}
		c.entries[digest] = s
	}
	c.mu.Unlock()
	if s == nil {
		return nil, func() {}, nil
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	if time.Since(s.started) >= time.Second {
		s.started = time.Now()
		s.bodies = make(map[string][]byte)
		s.bytes = 0
	}
	return s, func() { <-s.gate }, nil
}

func (s *coreSnapshot) retain(path string, body []byte) {
	if s.bytes+len(body) > 8<<20 {
		return
	}
	s.bodies[path] = body
	s.bytes += len(body)
}
