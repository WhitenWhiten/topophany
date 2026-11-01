package direct

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// A full profile retains 256 report-ID hashes per object as well as the
// latest report. At 256 objects, the supported adapters' 71-byte maximum
// sample IDs alone need over 10 MiB serialized. Reserve up to 128 KiB per
// object (64 KiB report plus cursor/history), while retaining hard caps.
const MaxStateBytes = 32 << 20
const MaxSourceStateBytes = 4 << 20
const MaxBatchBytes = 2 << 20
const MaxRetryNonces = 1024

type Collector func(context.Context, model.Config, model.Source, []source.Request) ([]source.Reading, time.Time, error)
type Options struct {
	Now                 func() time.Time
	Collect             Collector
	Backoff             func(int) time.Duration
	MaxStateBytes       int
	MaxSourceStateBytes int
}
type sourceLoop struct {
	renewed401 bool
	gate       chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	generation string
	wake       chan struct{}
	attempted  bool
	failures   int
	next       time.Time
	lastRetry  time.Time
	state      string
	code       string
}

// Session is one profile's owned acquisition lifetime. Each source has one
// serial loop; at most four network collections run together. There is no
// result queue: the bounded atomic admission transaction finishes before
// that source is scheduled again. Only display frames may be coalesced.
type Session struct {
	workers    sync.WaitGroup
	mu         sync.Mutex
	profile    Profile
	config     model.Config
	store      *Store
	opts       Options
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	first      chan struct{}
	firstOnce  sync.Once
	loops      map[string]*sourceLoop
	external   *model.ExternalState
	accounted  *model.ExternalState
	sizes      report.StateSizes
	prepared   *engine.Prepared
	plan       *engine.Plan
	generation string
	retrySeen  map[string]bool
	closed     bool
	high       time.Time
	slots      chan struct{}
}

func NewSession(ctx context.Context, store *Store, p Profile, opts Options) (*Session, error) {
	c, e := p.Config()
	if e != nil {
		return nil, e
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Backoff == nil {
		opts.Backoff = desktopBackoff
	}
	if opts.MaxStateBytes == 0 {
		opts.MaxStateBytes = MaxStateBytes
	}
	if opts.MaxSourceStateBytes == 0 {
		opts.MaxSourceStateBytes = MaxSourceStateBytes
	}
	rt := probe.NewSourceRuntime(probe.ProductionSourceAdapters())
	if e = rt.Validate(c); e != nil {
		return nil, fail("profile_unavailable")
	}
	life, cancel := context.WithCancel(ctx)
	s := &Session{profile: cloneProfile(p), config: c, store: store, opts: opts, ctx: life, cancel: cancel, done: make(chan struct{}), first: make(chan struct{}), loops: map[string]*sourceLoop{}, retrySeen: map[string]bool{}, external: model.NewExternalState(), plan: engine.NewPlan(c), generation: newID(), slots: make(chan struct{}, 4)}
	s.prepared = s.plan.PrepareExternal(nil, nil, nil, s.external)
	if opts.Collect == nil {
		s.opts.Collect = s.collect
	}
	s.mu.Lock()
	for _, src := range c.Sources {
		s.startLoopLocked(src, s.generation, c, nil)
	}
	s.mu.Unlock()
	if len(c.Sources) == 0 {
		s.firstOnce.Do(func() { close(s.first) })
	}
	go func() {
		<-s.ctx.Done()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.workers.Wait()
		close(s.done)
	}()
	return s, nil
}
func desktopBackoff(failures int) time.Duration {
	base := min(5<<min(max(failures-1, 0), 4), 60)
	return time.Duration(float64(time.Duration(base)*time.Second) * (0.9 + rand.Float64()*0.1))
}
func (s *Session) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	s.mu.Unlock()
	<-s.done
}
func (s *Session) Wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-s.first:
	}
}
func (s *Session) Profile() Profile { s.mu.Lock(); defer s.mu.Unlock(); return cloneProfile(s.profile) }
func (s *Session) Retry(nonce string) error {
	if nonce == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.retrySeen[nonce] {
		return nil
	}
	if len(s.retrySeen) >= MaxRetryNonces {
		return fail("state_budget")
	}
	s.retrySeen[nonce] = true
	now := s.opts.Now()
	for _, l := range s.loops {
		if !l.lastRetry.IsZero() && now.Sub(l.lastRetry) < 5*time.Second {
			continue
		}
		if l.code == "rate_limited" && !l.next.IsZero() && now.Before(l.next) {
			continue
		}
		l.lastRetry = now
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
	return nil
}
func (s *Session) Snapshot(observer string, now time.Time) (model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if observer != "" && observer != Observer {
		return model.Snapshot{}, fail("invalid_request")
	}
	snap := s.prepared.Build("direct-"+s.generation, observer, now)
	info := &model.DirectInfo{Profile: s.profile.Profile, ProfileRevision: s.profile.Revision, DatasetID: s.profile.Dataset, Sources: []model.DirectSource{}}
	for _, e := range s.profile.Sources {
		l := s.loops[e.Source.ID]
		state := s.external.Sources[e.Source.ID]
		v := model.DirectSource{ID: e.Source.ID, Name: e.Source.Name, Adapter: e.Source.Adapter, State: l.state, LastAttemptAt: stamp(state.LastAttemptAt), LastContactAt: stamp(state.LastContact), NextRetryAt: stamp(l.next)}
		if l.code != "" {
			code := l.code
			v.Code = &code
		}
		if !state.LastContact.IsZero() {
			until := state.LastContact.Add(time.Duration(e.Source.Stale) * time.Second)
			v.ContactValidUntil = &until
		}
		info.Sources = append(info.Sources, v)
	}
	snap.Direct = info
	status, _ := (view.View{Snapshot: snap, Now: now}).SourcesStatus()
	snap.SourcesStatus = &status
	return snap, nil
}
func stamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}
func (s *Session) requests(src model.Source) ([]model.Check, []source.Request) {
	checks := []model.Check{}
	for _, x := range s.config.Checks {
		if x.Kind == "external" && x.External != nil && x.External.Source == src.ID && !x.Disabled && !x.Maintenance {
			checks = append(checks, x)
		}
	}
	return checks, source.RequestsForChecks(checks)
}
func (s *Session) startLoopLocked(src model.Source, generation string, c model.Config, gate chan struct{}) {
	if gate == nil {
		gate = make(chan struct{}, 1)
	}
	ctx, cancel := context.WithCancel(s.ctx)
	l := &sourceLoop{gate: gate, ctx: ctx, cancel: cancel, generation: generation, wake: make(chan struct{}, 1), state: "loading"}
	s.loops[src.ID] = l
	s.workers.Add(1)
	go func() { defer s.workers.Done(); s.run(src, l, c) }()
}
func requestsFor(c model.Config, src model.Source) ([]model.Check, []source.Request) {
	temp := Session{config: c}
	return temp.requests(src)
}
func (s *Session) run(src model.Source, l *sourceLoop, currentConfig model.Config) {
	_, requests := requestsFor(currentConfig, src)
	for {
		select {
		case <-l.ctx.Done():
			return
		case l.gate <- struct{}{}:
		}
		select {
		case <-l.ctx.Done():
			<-l.gate
			return
		case s.slots <- struct{}{}:
		}
		if l.ctx.Err() != nil {
			<-s.slots
			<-l.gate
			return
		}
		s.mu.Lock()
		l.next = time.Time{}
		s.mu.Unlock()
		started := s.opts.Now().UTC()
		ctx, cancel := context.WithTimeout(l.ctx, time.Duration(src.TimeoutSeconds)*time.Second)
		readings, retryAt, err := s.opts.Collect(ctx, currentConfig, src, requests)
		cancel()
		<-s.slots
		<-l.gate
		if l.ctx.Err() != nil {
			return
		}
		finished := s.opts.Now().UTC()
		if finished.Before(started) {
			finished = started
		}
		if e := s.Record(l.generation, src.ID, started, finished, readings, err); e != nil {
			err = e
			s.mu.Lock()
			if s.loops[src.ID] != l {
				s.mu.Unlock()
				return
			}
			s.external.Sources[src.ID] = report.MergeSource(s.external.Sources[src.ID], src.ID, finished, false, AsError(e).Code, 0)
			s.accounted = nil
			s.prepared = s.prepared.WithExternalChanges(map[string]model.SourceState{src.ID: s.external.Sources[src.ID]}, nil)
			s.mu.Unlock()
		}
		s.mu.Lock()
		if s.loops[src.ID] != l || l.ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		if err == nil {
			l.state = "connected"
			l.code = ""
			l.failures = 0
			l.renewed401 = false
			l.next = finished.Add(time.Duration(src.IntervalSeconds) * time.Second)
		} else {
			l.failures++
			e := AsError(err)
			l.code = e.Code
			state, retry := failurePolicy(e.Code)
			l.state = state
			if retry {
				l.next = finished.Add(s.opts.Backoff(l.failures))
				if retryAt.After(l.next) {
					l.next = retryAt
				}
			} else {
				l.next = time.Time{}
			}
		}
		l.attempted = true
		all := true
		for _, v := range s.loops {
			all = all && v.attempted
		}
		if all {
			s.firstOnce.Do(func() { close(s.first) })
		}
		next := l.next
		s.mu.Unlock()
		if next.IsZero() {
			select {
			case <-l.ctx.Done():
				return
			case <-l.wake:
			}
		} else {
			timer := time.NewTimer(max(time.Millisecond, next.Sub(s.opts.Now())))
			select {
			case <-l.ctx.Done():
				timer.Stop()
				return
			case <-l.wake:
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

// LoadingSnapshot prepares topology without waiting for or racing the first read.
func LoadingSnapshot(p Profile, observer string, now time.Time) model.Snapshot {
	c, _ := p.Config()
	snap := engine.NewPlan(c).PrepareExternal(nil, nil, nil, model.NewExternalState()).Build("direct-loading", observer, now)
	info := &model.DirectInfo{Profile: p.Profile, ProfileRevision: p.Revision, DatasetID: p.Dataset, Sources: []model.DirectSource{}}
	for _, entry := range p.Sources {
		info.Sources = append(info.Sources, model.DirectSource{ID: entry.Source.ID, Name: entry.Source.Name, Adapter: entry.Source.Adapter, State: "loading"})
	}
	snap.Direct = info
	status, _ := (view.View{Snapshot: snap, Now: now}).SourcesStatus()
	snap.SourcesStatus = &status
	return snap
}
func (s *Session) RememberNonce(nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nonce != "" {
		s.retrySeen[nonce] = true
	}
}
func (s *Session) CheckRetry(nonce string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nonce != "" && !s.retrySeen[nonce] && len(s.retrySeen) >= MaxRetryNonces {
		return fail("state_budget")
	}
	return nil
}
