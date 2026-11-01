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
	v.Cached = true
	for _, gap := range v.Coverage().Gaps {
		if gap.Code != "cached" {
			t.Fatal(gap)
		}
	}
}
