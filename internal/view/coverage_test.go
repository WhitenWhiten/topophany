package view

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
)

func TestCoverageSeparatesScopeAndMissingApplicationEvidence(t *testing.T) {
	v := View{Snapshot: model.Snapshot{ViewObserver: "home", Topology: model.Config{Services: []model.Service{{ID: "empty"}, {ID: "remote"}, {ID: "socket"}, {ID: "paused"}, {ID: "stale"}}, Checks: []model.Check{
		{ID: "remote", Service: "remote", Observer: "other", Kind: "http"},
		{ID: "socket", Service: "socket", Observer: "home", Kind: "tcp"},
		{ID: "paused", Service: "paused", Observer: "home", Kind: "http", Disabled: true},
		{ID: "stale", Service: "stale", Observer: "home", Kind: "http"},
	}}, Checks: []model.CheckState{{ID: "stale", Freshness: "stale", Last: &model.Observation{}}}}}
	got := v.Coverage()
	if got.Known != 0 || got.Services != 5 || len(got.Gaps) != 5 {
		t.Fatal(got)
	}
	want := []string{"unmonitored", "outside_view", "no_application_check", "suspended", "stale"}
	for i, gap := range got.Gaps {
		if gap.Code != want[i] {
			t.Fatal(gap)
		}
	}
	// The counts and the per-service codes are read from the same gaps.
	reasons, total := got.Reasons(), 0
	for _, n := range reasons {
		total += n
	}
	unknown := got.UnknownReasons()
	if len(reasons) != 5 || total != got.Services-got.Known || len(unknown) != 5 || unknown["remote"] != "outside_view" || unknown["socket"] != "no_application_check" {
		t.Fatal(reasons, unknown)
	}
	v.Cached = true
	for _, gap := range v.Coverage().Gaps {
		if gap.Code != "cached" {
			t.Fatal(gap)
		}
	}
}

// A known service has no reason, and the counts cover only the unknown.
func TestCoverageReasonsOmitKnownServices(t *testing.T) {
	s := model.Snapshot{Topology: model.Config{Services: []model.Service{{ID: "known", Node: "n"}, {ID: "empty"}, {ID: "other-empty"}}, Observers: []model.Observer{{ID: "o", Node: "n"}},
		Checks: []model.Check{{ID: "local", Service: "known", Observer: "o", Kind: "http", Target: "http://127.0.0.1/health"}}},
		Checks: []model.CheckState{{ID: "local", Observer: "o", Health: "up", Freshness: "fresh", Last: &model.Observation{}}}}
	got := (View{Snapshot: s}).Coverage()
	if got.Known != 1 || got.Reasons()["unmonitored"] != 2 || len(got.Reasons()) != 1 {
		t.Fatal(got)
	}
	if _, ok := got.UnknownReasons()["known"]; ok {
		t.Fatal("a known service has a reason")
	}
	if len((Coverage{}).Reasons()) != 0 || len((Coverage{}).UnknownReasons()) != 0 {
		t.Fatal("reasons without gaps")
	}
}
