package local

import (
	"context"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestSessionReadsLikeAWorkerWithLocalThresholds(t *testing.T) {
	check := syntheticCheck(closedURL(t))
	p := Profile{Schema: 1, Revision: "r1", Checks: []Entry{{Check: check, AuthMode: "none"}}}
	s, err := NewSession(context.Background(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	s.Wait(ctx)
	cancel()
	trail := s.Samples(check.ID)
	if len(trail) != 1 || trail[0].Outcome != "fail" {
		t.Fatal("a stopped engine is not a failed read", trail)
	}
	state := func(at time.Time) model.CheckState {
		snap := s.Snapshot(at)
		if snap.Project != Project || len(snap.Checks) != 1 {
			t.Fatal(snap)
		}
		return snap.Checks[0]
	}
	first := trail[0].SampledAt
	if st := state(first); st.Health != "degraded" || st.Last.Stage != "adapter_api" {
		t.Fatal(st)
	}
	cfg, _ := Config([]Check{check}, nil)
	sample := func(outcome string, at time.Time) {
		_ = s.record(model.Observation{ID: outcome + at.String(), Check: check.ID, Observer: Observer, Project: Project, Contract: config.Contract(cfg, cfg.Checks[0]), SampledAt: at, Outcome: outcome, Evidence: "reported"})
	}
	sample("fail", first.Add(30*time.Second))
	if st := state(first.Add(30 * time.Second)); st.Health != "down" {
		t.Fatalf("second failure did not confirm: %+v", st)
	}
	sample("ok", first.Add(60*time.Second))
	if st := state(first.Add(60 * time.Second)); st.Health != "up" {
		t.Fatalf("one good read did not recover: %+v", st)
	}
	if n := len(s.Samples(check.ID)); n != 3 {
		t.Fatal(n)
	}
	// An unchanged revision keeps the running reads and their evidence.
	if err := s.Reconfigure(p, nil); err != nil || len(s.Samples(check.ID)) != 3 {
		t.Fatal(err)
	}
	// A changed measurement contract starts the check's evidence afresh.
	check.ExpectedMode = "global"
	p = Profile{Schema: 1, Revision: "r2", Checks: []Entry{{Check: check, AuthMode: "none"}}}
	if err := s.Reconfigure(p, nil); err != nil || s.Revision() != "r2" {
		t.Fatal(err)
	}
	for _, o := range s.Samples(check.ID) {
		if !o.SampledAt.After(first.Add(60 * time.Second)) {
			t.Fatal("old contract's samples kept", o)
		}
	}
}
