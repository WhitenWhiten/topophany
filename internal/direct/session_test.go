package direct

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func resourceReading(at time.Time, sample string) source.Reading {
	n := 25.0
	return source.Reading{Object: testObject, Report: model.ExternalReport{ReportMeta: model.ReportMeta{Schema: 1, Object: testObject, SampleID: sample, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &n}, Disks: []model.DiskReport{{ID: "root", Percent: &n}}}}}
}
func pausedSession(t *testing.T, opts Options) (*Session, Profile) {
	t.Helper()
	h := fixtureHub(t)
	st := testStore(t)
	p := saveFixture(t, st, testRequest(h))
	opts.Collect = func(ctx context.Context, _ model.Config, _ model.Source, _ []source.Request) ([]source.Reading, time.Time, error) {
		<-ctx.Done()
		return nil, time.Time{}, ctx.Err()
	}
	s, e := NewSession(context.Background(), st, p, opts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s, p
}
func TestSessionMatchesStoreAndDoesNotRenewOldData(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s, p := pausedSession(t, Options{Now: func() time.Time { return now }})
	c, _ := p.Config()
	disk, e := store.Open(t.TempDir(), store.Options{Observers: []string{Observer}, Now: func() time.Time { return now }}.WithTopology(c))
	if e != nil {
		t.Fatal(e)
	}
	defer disk.Close()
	reading := resourceReading(now.Add(-time.Minute), "sample-1")
	for i := 1; i <= 3; i++ {
		attempt := now.Add(-time.Duration(3-i) * time.Second)
		if e = s.Record(s.generation, c.Sources[0].ID, attempt, attempt, []source.Reading{reading}, nil); e != nil {
			t.Fatal(e)
		}
		x := c.Checks[0]
		r := model.CloneExternalReport(reading.Report)
		o := model.Observation{ID: fmt.Sprintf("direct.boot.%d", i), Project: c.Project, Observer: Observer, Check: x.ID, Boot: "boot", Sequence: uint64(i), Contract: config.Contract(c, x), SampledAt: attempt, Outcome: "ok", Evidence: "reported", Stage: "external_source", PathEvidence: "external_source", External: &model.ExternalObservation{Source: c.Sources[0].ID, SourceGeneration: s.generation, AttemptedAt: attempt, Contact: true, Report: &r}}
		if e = disk.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{o}}); e != nil {
			t.Fatal(e)
		}
	}
	if !reflect.DeepEqual(s.external, disk.ExternalView()) {
		a, _ := json.Marshal(s.external)
		b, _ := json.Marshal(disk.ExternalView())
		t.Fatalf("session/store differ\n%s\n%s", a, b)
	}
	before := s.external.Bindings[c.Checks[0].ID]
	if before.ReportSamples != 1 || before.Duplicates != 2 || !before.LastAcceptedAt.Equal(now) {
		t.Fatal(before)
	}
	snap, e := s.Snapshot("", now)
	if e != nil || !(view.View{Snapshot: snap, Now: now}).SelectResources()[c.Nodes[0].ID].Selected.Current {
		t.Fatal(e)
	}
	// Repeated projections and contact with the same saved record cannot renew it.
	for range 10 {
		_, _ = s.Snapshot("", now.Add(time.Minute))
	}
	at := now.Add(3 * time.Minute)
	old, e := s.Snapshot("", at)
	if e != nil {
		t.Fatal(e)
	}
	if old.External[0].Freshness != "stale" || s.external.Bindings[c.Checks[0].ID].ReportSamples != 1 {
		t.Fatal("publisher renewed data")
	}
	if e = s.Record("late-generation", c.Sources[0].ID, now, now, nil, nil); AsError(e).Code != "profile_conflict" {
		t.Fatal(e)
	}
	snap.External[0].Report.Resource.CPU.Percent = new(float64)
	snap.Topology.Nodes[0].Name = "mutated"
	again, _ := s.Snapshot("", now)
	if *again.External[0].Report.Resource.CPU.Percent != 25 || again.Topology.Nodes[0].Name == "mutated" {
		t.Fatal("snapshot aliases session")
	}
}
func TestSessionFailureIsolatesAndExpires(t *testing.T) {
	h := fixtureHub(t)
	st := testStore(t)
	r := testRequest(h)
	p := saveFixture(t, st, r)
	r.BaseRevision = p.Revision
	r.Source.ID = "bs-other"
	p = saveFixture(t, st, r)
	now := h.at
	s, e := NewSession(context.Background(), st, p, Options{Now: func() time.Time { return now }, Collect: func(_ context.Context, _ model.Config, src model.Source, _ []source.Request) ([]source.Reading, time.Time, error) {
		if src.ID == "bs-other" {
			return nil, time.Time{}, fail("forbidden")
		}
		return []source.Reading{resourceReading(now, "sample-1")}, time.Time{}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Wait(ctx)
	snap, e := s.Snapshot("", now)
	if e != nil || len(snap.External) != 2 || snap.Direct.Sources[1].State != "forbidden" || snap.Direct.Sources[0].State != "connected" {
		t.Fatal(snap.Direct, e)
	}
	at := now.Add(time.Second)
	if e = s.Record(s.generation, "bs-example", at, at, nil, fail("connection_failed")); e != nil {
		t.Fatal(e)
	}
	snap, _ = s.Snapshot("", at)
	if snap.External[0].Freshness != "fresh" {
		t.Fatal("failure erased still-current evidence")
	}
	snap, _ = s.Snapshot("", now.Add(181*time.Second))
	if snap.External[0].Freshness != "stale" {
		t.Fatal("failure extended evidence")
	}
}
func TestSessionBudgetsAtomicBatchAndClose(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s, p := pausedSession(t, Options{Now: func() time.Time { return now }, MaxStateBytes: 128})
	reading := resourceReading(now, "sample-1")
	e := s.Record(s.generation, p.Sources[0].Source.ID, now, now, []source.Reading{reading}, nil)
	if AsError(e).Code != "state_budget" {
		t.Fatal(e)
	}
	if len(s.external.Bindings) != 0 {
		t.Fatal("partial budget commit")
	}
	if e = s.Record(s.generation, p.Sources[0].Source.ID, now, now, []source.Reading{reading, reading}, nil); AsError(e).Code != "upstream_incompatible" {
		t.Fatal(e)
	}
	s.Close()
	if e = s.Record(s.generation, p.Sources[0].Source.ID, now, now, nil, nil); e == nil {
		t.Fatal("record after close")
	}
}
func TestSessionScheduleRetryAndFirstFailures(t *testing.T) {
	h := fixtureHub(t)
	st := testStore(t)
	p := saveFixture(t, st, testRequest(h))
	var count, active, peak atomic.Int32
	s, e := NewSession(context.Background(), st, p, Options{Collect: func(ctx context.Context, _ model.Config, _ model.Source, _ []source.Request) ([]source.Reading, time.Time, error) {
		count.Add(1)
		n := active.Add(1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		select {
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
		return nil, time.Time{}, fail("auth_required")
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Wait(ctx)
	if count.Load() != 1 {
		t.Fatal(count.Load())
	}
	s.Retry("retry-1")
	s.Retry("retry-1")
	s.Retry("retry-2")
	time.Sleep(60 * time.Millisecond)
	if count.Load() != 2 || peak.Load() != 1 {
		t.Fatal(count.Load(), peak.Load())
	}
	s.Close()
	if active.Load() != 0 {
		t.Fatal("request survives close")
	}
}
func TestSessionRealRuntime20ObjectsAndNoTickRequests(t *testing.T) {
	h := fixtureHub(t)
	h.systems = 20
	st := testStore(t)
	r := testRequest(h)
	r.Selected = nil
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("%015d", i)
		if i == 0 {
			id = testObject
		}
		r.Selected = append(r.Selected, id)
	}
	p := saveFixture(t, st, r)
	s, e := NewSession(context.Background(), st, p, Options{Now: func() time.Time { return h.at }})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.Wait(ctx)
	snap, _ := s.Snapshot("", h.at)
	if len(snap.External) != 20 || snap.Direct.Sources[0].State != "connected" {
		t.Fatal(snap.Direct)
	}
	for range 10 {
		_, _ = s.Snapshot("", h.at)
	}
	h.mu.Lock()
	got := h.counts["GET /api/collections/system_stats/records"]
	h.mu.Unlock()
	if got != 20 {
		t.Fatal("polling driven by projection", got)
	}
}
func TestSessionConcurrentSnapshotRecordClose(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s, p := pausedSession(t, Options{Now: func() time.Time { return now }})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for range 20 {
				_, _ = s.Snapshot("", now)
				_ = s.Record(s.generation, p.Sources[0].Source.ID, now, now, []source.Reading{resourceReading(now, "sample-1")}, nil)
			}
		})
	}
	wg.Go(s.Close)
	wg.Wait()
}

func TestFirstAttemptWaitIsOverallBounded(t *testing.T) {
	h := fixtureHub(t)
	st := testStore(t)
	p := saveFixture(t, st, testRequest(h))
	s, e := NewSession(context.Background(), st, p, Options{Collect: func(ctx context.Context, _ model.Config, _ model.Source, _ []source.Request) ([]source.Reading, time.Time, error) {
		<-ctx.Done()
		return nil, time.Time{}, fail("connection_failed")
	}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	s.Wait(ctx)
	if time.Since(start) > time.Second {
		t.Fatal("overall wait unbounded")
	}
	snap, _ := s.Snapshot("", time.Now())
	if snap.Direct.Sources[0].State != "loading" {
		t.Fatal(snap.Direct)
	}
	s.Close()
}

func TestReconfigureOnlyAffectedSourceAndRejectLateResults(t *testing.T) {
	h := fixtureHub(t)
	st := testStore(t)
	r := testRequest(h)
	p := saveFixture(t, st, r)
	r.BaseRevision = p.Revision
	r.Source.ID = "bs-other"
	p = saveFixture(t, st, r)
	var mu sync.Mutex
	counts := map[string]int{}
	now := h.at
	collect := func(_ context.Context, _ model.Config, src model.Source, _ []source.Request) ([]source.Reading, time.Time, error) {
		mu.Lock()
		counts[src.ID]++
		mu.Unlock()
		return []source.Reading{resourceReading(now, "sample-1")}, time.Time{}, nil
	}
	s, e := NewSession(context.Background(), st, p, Options{Now: func() time.Time { return now }, Collect: collect})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Wait(ctx)
	oldGen := s.loops["bs-example"].generation
	p.Sources[0].Source.Name = "Renamed"
	if e = s.Reconfigure(p); e != nil {
		t.Fatal(e)
	}
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	if counts["bs-example"] != 1 || counts["bs-other"] != 1 {
		t.Fatal(counts)
	}
	mu.Unlock()
	p.Sources[0].Source.MaxAge = 300
	p.Revision = "new-revision"
	if e = s.Reconfigure(p); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := counts["bs-example"] == 2
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	if counts["bs-example"] != 2 || counts["bs-other"] != 1 {
		t.Fatal(counts)
	}
	mu.Unlock()
	if e = s.Record(oldGen, "bs-example", now, now, []source.Reading{resourceReading(now, "sample-2")}, nil); AsError(e).Code != "profile_conflict" {
		t.Fatal(e)
	}
}

func Test401RefreshOnceAnd403DoesNotRefresh(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var stats, refresh atomic.Int32
			now := time.Now().UTC()
			token := jwt(now.Add(time.Hour), true)
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "auth-refresh") {
					refresh.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "record": map[string]string{"id": testSubject, "collectionName": "users"}})
					return
				}
				n := stats.Add(1)
				if n == 1 || status == 403 {
					w.WriteHeader(status)
					return
				}
				_, _ = fmt.Fprintf(w, `{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[{"id":"sssssssssssssss","system":"aaaaaaaaaaaaaaa","type":"1m","created":%q,"stats":{"cpu":25}}]}`, now.Format(time.RFC3339Nano))
			}))
			defer hub.Close()
			st := testStore(t)
			req := Request{Profile: "desktop", Source: SourceConfig{ID: "bs-example", Name: "Synthetic", Adapter: "beszel", URL: hub.URL, Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}, Selected: []string{testObject}}
			p, _, e := st.Save(context.Background(), req, Credential{Mode: "token", Token: token, Subject: "users:" + testSubject, Renewable: true}, []System{{testObject, "Synthetic"}})
			if e != nil {
				t.Fatal(e)
			}
			s, e := NewSession(context.Background(), st, p, Options{Backoff: func(int) time.Duration { return time.Millisecond }})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			deadline := time.Now().Add(time.Second)
			state := ""
			for time.Now().Before(deadline) {
				snap, _ := s.Snapshot("", time.Now())
				state = snap.Direct.Sources[0].State
				if state == "connected" || state == "forbidden" {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if status == 401 && (state != "connected" || refresh.Load() != 1 || stats.Load() != 2) {
				t.Fatal(state, refresh.Load(), stats.Load())
			}
			if status == 403 && (state != "forbidden" || refresh.Load() != 0 || stats.Load() != 1) {
				t.Fatal(state, refresh.Load(), stats.Load())
			}
		})
	}
}
func TestRetryAfterCannotBeBypassed(t *testing.T) {
	now := time.Now().UTC()
	h := fixtureHub(t)
	st := testStore(t)
	p := saveFixture(t, st, testRequest(h))
	var count atomic.Int32
	s, e := NewSession(context.Background(), st, p, Options{Collect: func(context.Context, model.Config, model.Source, []source.Request) ([]source.Reading, time.Time, error) {
		count.Add(1)
		return nil, now.Add(time.Minute), fail("rate_limited")
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Wait(ctx)
	s.Retry("retry1")
	time.Sleep(20 * time.Millisecond)
	if count.Load() != 1 {
		t.Fatal("retry bypassed rate limit")
	}
	snap, _ := s.Snapshot("", now)
	if snap.Direct.Sources[0].NextRetryAt == nil || snap.Direct.Sources[0].NextRetryAt.Before(now.Add(time.Minute)) {
		t.Fatal(snap.Direct)
	}
}

func TestActualRulesAndBudgetFailurePreserveAcceptedEvidence(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s, p := pausedSession(t, Options{Now: func() time.Time { return now }})
	s.mu.Lock()
	s.config = config.Clone(s.config)
	threshold := 20.0
	x := &s.config.Checks[0]
	x.FailureThreshold = 5
	x.RecoveryThreshold = 4
	x.PolicyThreshold = 2
	x.External.Assertions = &model.ResourceAssertions{CPUPercentMax: &threshold}
	s.plan = engine.NewPlan(s.config)
	s.mu.Unlock()
	first := resourceReading(now.Add(-10*time.Second), "sample-1")
	if e := s.Record(s.generation, p.Sources[0].Source.ID, now, now, []source.Reading{first}, nil); e != nil {
		t.Fatal(e)
	}
	state := s.external.Bindings[s.config.Checks[0].ID]
	if state.PolicyConfirmed || state.PolicyRun != 1 || state.FailureThreshold != 5 || state.RecoveryThreshold != 4 || state.PolicyThreshold != 2 {
		t.Fatal(state)
	}
	_ = s.Record(s.generation, p.Sources[0].Source.ID, now, now, []source.Reading{first}, nil)
	if s.external.Bindings[s.config.Checks[0].ID].PolicyConfirmed {
		t.Fatal("duplicate confirmed policy")
	}
	second := resourceReading(now.Add(-9*time.Second), "sample-2")
	if e := s.Record(s.generation, p.Sources[0].Source.ID, now, now, []source.Reading{second}, nil); e != nil {
		t.Fatal(e)
	}
	if !s.external.Bindings[s.config.Checks[0].ID].PolicyConfirmed {
		t.Fatal("actual policy threshold ignored")
	}
	before, _ := json.Marshal(s.external)
	s.opts.MaxStateBytes = len(before)
	third := resourceReading(now.Add(-8*time.Second), "sample-3")
	if e := s.Record(s.generation, p.Sources[0].Source.ID, now, now, []source.Reading{third}, nil); AsError(e).Code != "state_budget" {
		t.Fatal(e)
	}
	after, _ := json.Marshal(s.external)
	if !bytes.Equal(before, after) {
		t.Fatal("budget failure mutated accepted evidence")
	}
}
func TestLiveBudgetFailureHasScopedDiagnostic(t *testing.T) {
	now := time.Now().UTC()
	h := fixtureHub(t)
	st := testStore(t)
	p := saveFixture(t, st, testRequest(h))
	s, e := NewSession(context.Background(), st, p, Options{MaxStateBytes: 128, Collect: func(context.Context, model.Config, model.Source, []source.Request) ([]source.Reading, time.Time, error) {
		return []source.Reading{resourceReading(now, "sample-1")}, time.Time{}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Wait(ctx)
	snap, _ := s.Snapshot("", now)
	src := snap.Direct.Sources[0]
	if src.State != "error" || src.Code == nil || *src.Code != "state_budget" || src.LastContactAt != nil {
		t.Fatal(src)
	}
}
func TestLocalCandidateFailureRetainsOldConfig(t *testing.T) {
	s, p := pausedSession(t, Options{})
	old := s.Profile()
	p.Sources[0].Source.CAFile = "relative-ca.pem"
	if e := s.Reconfigure(p); e == nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(old, s.Profile()) {
		t.Fatal("bad local candidate installed")
	}
}

func TestReconfigureNeverOverlapsSourcePoll(t *testing.T) {
	h := fixtureHub(t)
	st := testStore(t)
	p := saveFixture(t, st, testRequest(h))
	var calls, active, peak atomic.Int32
	first := make(chan struct{})
	second := make(chan struct{})
	collect := func(ctx context.Context, _ model.Config, _ model.Source, _ []source.Request) ([]source.Reading, time.Time, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if calls.Add(1) == 1 {
			close(first)
			<-ctx.Done()
			time.Sleep(20 * time.Millisecond)
			return nil, time.Time{}, ctx.Err()
		}
		close(second)
		return nil, time.Time{}, nil
	}
	s, e := NewSession(context.Background(), st, p, Options{Collect: collect})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("first read did not start")
	}
	p.Sources[0].Source.MaxAge = 300
	if e = s.Reconfigure(p); e != nil {
		t.Fatal(e)
	}
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("replacement read did not start")
	}
	if peak.Load() != 1 {
		t.Fatal("overlapping generations", peak.Load())
	}
}

func TestRetryReplayHistoryIsBoundedAndIdempotent(t *testing.T) {
	now := time.Now().UTC()
	s, _ := pausedSession(t, Options{Now: func() time.Time { return now }})
	for i := 0; i < MaxRetryNonces; i++ {
		if e := s.Retry(fmt.Sprintf("nonce-%d", i)); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.Retry("nonce-0"); e != nil {
		t.Fatal("old nonce lost idempotence", e)
	}
	if e := s.Retry("over-budget"); AsError(e).Code != "state_budget" {
		t.Fatal(e)
	}
}

func TestParentCancellationAndEmptyProfileReconfigurationJoin(t *testing.T) {
	h := fixtureHub(t)
	st := testStore(t)
	p := saveFixture(t, st, testRequest(h))
	var active atomic.Int32
	for range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		empty := p
		empty.Sources = []Entry{}
		s, e := NewSession(ctx, st, empty, Options{Collect: func(ctx context.Context, _ model.Config, _ model.Source, _ []source.Request) ([]source.Reading, time.Time, error) {
			active.Add(1)
			defer active.Add(-1)
			<-ctx.Done()
			return nil, time.Time{}, ctx.Err()
		}})
		if e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		wg.Go(func() { _ = s.Reconfigure(p) })
		wg.Go(cancel)
		wg.Wait()
		s.Close()
		if active.Load() != 0 {
			t.Fatal("collection survived parent cancellation")
		}
	}
}

// A CA file is a source-local runtime dependency, including when a profile is
// first opened or reconfigured. Exercise the production collector, not a stub.
func TestSessionLocalTLSFailureIsIsolated(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint("invalidPEM=", invalid), func(t *testing.T) {
			h := fixtureHub(t)
			st := testStore(t)
			r := testRequest(h)
			p := saveFixture(t, st, r)
			r.BaseRevision = p.Revision
			r.Source.ID = "bs-other"
			p = saveFixture(t, st, r)
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if invalid {
				if err := os.WriteFile(ca, []byte("not a certificate"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			p.Sources[1].Source.CAFile = ca
			s, err := NewSession(context.Background(), st, p, Options{Now: func() time.Time { return h.at }, Backoff: func(int) time.Duration { return time.Hour }})
			if err != nil {
				t.Fatal("one CA failure rejected the profile", err)
			}
			defer s.Close()
			check := func(want string) {
				t.Helper()
				deadline := time.Now().Add(2 * time.Second)
				for {
					snap, err := s.Snapshot("", h.at)
					if err != nil {
						t.Fatal(err)
					}
					if snap.Direct.Sources[0].State == "connected" && snap.Direct.Sources[1].State == want {
						if want == "retrying" && (snap.Direct.Sources[1].Code == nil || *snap.Direct.Sources[1].Code != "tls_error" || snap.SourcesStatus.Counts.Current != 1) {
							t.Fatal(snap.SourcesStatus)
						}
						if want == "connected" && snap.SourcesStatus.Counts.Current != 2 {
							t.Fatal(snap.SourcesStatus)
						}
						return
					}
					if time.Now().After(deadline) {
						t.Fatal("source states did not settle", snap.SourcesStatus)
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			check("retrying")
			s.mu.Lock()
			healthyLoop := s.loops[p.Sources[0].Source.ID]
			s.mu.Unlock()
			p.Sources[1].Source.CAFile = ""
			if err := s.Reconfigure(p); err != nil {
				t.Fatal(err)
			}
			check("connected")
			p.Sources[1].Source.CAFile = ca
			if err := s.Reconfigure(p); err != nil {
				t.Fatal("one CA failure rejected reconfiguration", err)
			}
			check("retrying")
			s.mu.Lock()
			same := s.loops[p.Sources[0].Source.ID] == healthyLoop
			s.mu.Unlock()
			if !same {
				t.Fatal("healthy source restarted")
			}
		})
	}
}
