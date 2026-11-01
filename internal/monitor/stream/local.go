package stream

import (
	"context"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/local"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
)

// LocalManager owns at most one local session: this machine's own checks.
// Projection handles borrow it. Each request reloads the store and
// reconfigures the same session, so a template change keeps its evidence and
// a saved change is picked up without restarting the stream. The last handle
// to close stops every read.
type LocalManager struct {
	mu      sync.Mutex
	root    string
	now     func() time.Time
	session *local.Session
	refs    int
	closed  bool
}

func NewLocalManager(root string, now func() time.Time) *LocalManager {
	if now == nil {
		now = time.Now
	}
	return &LocalManager{root: root, now: now}
}

func (m *LocalManager) Close() {
	m.mu.Lock()
	m.closed = true
	s := m.session
	m.session, m.refs = nil, 0
	m.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

func (m *LocalManager) Open(ctx context.Context, r Request) (Source, *Error) {
	if r.Source != "local" {
		return openSource(ctx, r, m.now)
	}
	// Read and apply in the same order, so concurrent opens cannot replace a
	// newer session configuration with an older snapshot.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Source{}, failure("profile_unavailable", "Local reader is closed.")
	}
	st, e := local.NewStore(m.root)
	if e != nil {
		return Source{}, localFailure(e)
	}
	p, secrets, e := st.Snapshot(ctx, r.ProfileRevision)
	if e != nil {
		if local.AsError(e).Code == "profile_conflict" {
			return Source{}, failure("profile_conflict", "Local checks changed; list them before retrying.")
		}
		return Source{}, localFailure(e)
	}
	if m.session == nil {
		s, e := local.NewSession(ctx, p, secrets)
		if e != nil {
			return Source{}, localFailure(e)
		}
		m.session = s
	} else if e = m.session.Reconfigure(p, secrets); e != nil {
		return Source{}, localFailure(e)
	}
	s := m.session
	m.refs++
	project := func(ctx context.Context) monitor.Input {
		snap := s.Snapshot(m.now())
		in := monitor.Input{Snapshot: snap}
		// The session holds every sample since the stream opened, so a
		// fresh book reads each card's whole trail on every fetch.
		in.Samples = monitor.NewSampleBook(r.Template).Fill(ctx, snap, "", -1, func(_ context.Context, id string) ([]model.Observation, error) {
			return s.Samples(id), nil
		})
		return in
	}
	fetch := func(ctx context.Context) (monitor.Input, error) {
		if e := ctx.Err(); e != nil {
			return monitor.Input{}, e
		}
		// The first fetch publishes the first read as soon as it lands,
		// within the fetch deadline, rather than a poll interval later.
		s.Wait(ctx)
		return project(ctx), nil
	}
	initial := project(ctx)
	var once sync.Once
	closeHandle := func() {
		once.Do(func() {
			m.mu.Lock()
			if m.session != s {
				m.mu.Unlock()
				return
			}
			m.refs--
			last := m.refs <= 0
			if last {
				m.session = nil
			}
			m.mu.Unlock()
			if last {
				s.Close()
			}
		})
	}
	return Source{Project: local.Project, Initial: initial, Fetch: fetch, Close: closeHandle}, nil
}

func localFailure(err error) *Error { e := local.AsError(err); return failure(e.Code, e.Message) }
