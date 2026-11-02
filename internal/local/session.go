package local

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/runner"
)

// Session runs a store's checks on their own intervals, as a worker runs its
// checks, while a local stream is open. It keeps one display view of their
// observations with threshold carries and a bounded trail of recent samples
// per check; it writes no files and sends nothing anywhere.
type Session struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	view      *monitor.Local
	revision  string
	contracts map[string]string
	checks    []string
	trails    map[string][]model.Observation
	changed   chan struct{}
	stop      context.CancelFunc
	done      chan struct{}
}

// NewSession starts reading p's checks at once; Close stops them.
func NewSession(ctx context.Context, p Profile, secrets map[string]string) (*Session, error) {
	cfg, err := profileConfig(p, secrets)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	s := &Session{ctx: life, cancel: cancel, view: monitor.NewLocal(cfg, Observer), trails: map[string][]model.Observation{}, changed: make(chan struct{})}
	if err := s.start(p.Revision, cfg, secrets); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

func profileConfig(p Profile, secrets map[string]string) (model.Config, error) {
	checks := make([]Check, 0, len(p.Checks))
	for _, e := range p.Checks {
		checks = append(checks, e.Check)
	}
	return Config(checks, func(id string) bool { return secrets[id] != "" })
}

// start replaces the running scheduler with one for cfg. The previous one
// has stopped and recorded its last sample before evidence is reconciled.
func (s *Session) start(revision string, cfg model.Config, secrets map[string]string) error {
	sched, err := runner.New(cfg, Observer, max(1, len(cfg.Checks)), localRunner(cfg, secrets).Run, s.record)
	if err != nil {
		return fail("invalid_request")
	}
	s.halt()
	s.mu.Lock()
	s.view.Reload(cfg)
	s.revision = revision
	s.contracts = config.Contracts(cfg)
	s.checks = s.checks[:0]
	for _, x := range cfg.Checks {
		s.checks = append(s.checks, x.ID)
	}
	for id, trail := range s.trails {
		if len(trail) == 0 || trail[len(trail)-1].Contract != s.contracts[id] {
			delete(s.trails, id)
		}
	}
	ctx, stop := context.WithCancel(s.ctx)
	done := make(chan struct{})
	s.stop, s.done = stop, done
	s.mu.Unlock()
	go func() {
		defer close(done)
		_ = sched.Run(ctx)
	}()
	return nil
}

// halt cancels the running scheduler and waits for its probes to return.
func (s *Session) halt() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	s.mu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
}

// Reconfigure applies a reloaded store. An unchanged revision keeps the
// running reads; otherwise they restart under the new checks, keeping the
// evidence of every check whose measurement contract is unchanged.
func (s *Session) Reconfigure(p Profile, secrets map[string]string) error {
	s.mu.Lock()
	same := s.revision == p.Revision && s.ctx.Err() == nil
	s.mu.Unlock()
	if same {
		return nil
	}
	cfg, err := profileConfig(p, secrets)
	if err != nil {
		return err
	}
	return s.start(p.Revision, cfg, secrets)
}

// Revision is the store revision the session runs.
func (s *Session) Revision() string { s.mu.Lock(); defer s.mu.Unlock(); return s.revision }

func (s *Session) record(v model.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contracts[v.Check] != v.Contract {
		return nil
	}
	s.view.Record(v)
	trail := s.trails[v.Check]
	if n := len(trail); n == 0 || trail[n-1].SampledAt.Before(v.SampledAt) {
		trail = append(trail, model.Observation{Contract: v.Contract, SampledAt: v.SampledAt, Outcome: v.Outcome, Evidence: v.Evidence, DurationMS: v.DurationMS})
		if n := len(trail) - 2*monitor.CardSamples; n > 0 {
			trail = slices.Delete(trail, 0, n)
		}
		s.trails[v.Check] = trail
	}
	close(s.changed)
	s.changed = make(chan struct{})
	return nil
}

// Wait returns once every check has a sample under its current contract,
// or when ctx ends.
func (s *Session) Wait(ctx context.Context) {
	for {
		s.mu.Lock()
		ready := true
		for _, id := range s.checks {
			ready = ready && len(s.trails[id]) > 0
		}
		changed := s.changed
		s.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-s.ctx.Done():
			return
		case <-changed:
		}
	}
}

// Snapshot is the checks' current display snapshot.
func (s *Session) Snapshot(now time.Time) model.Snapshot { return s.view.Snapshot(now) }

// Samples are a check's recent samples, oldest first.
func (s *Session) Samples(id string) []model.Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.trails[id])
}

// Close stops every read and waits for in-flight probes to return.
func (s *Session) Close() {
	s.cancel()
	s.halt()
}

func localRunner(cfg model.Config, secrets map[string]string) probe.Runner {
	r := probe.NewRunner(cfg, Observer)
	r.Secret, r.LocalViewer = secretResolver(secrets), true
	return r
}
