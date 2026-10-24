package view

import (
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"strings"
	"testing"
	"time"
)

func groupedFixture() View {
	c := testkit.Config()
	c.Applications = []model.Application{{ID: "shop", Name: "商城"}}
	c.Services[0].Application = "shop"
	c.Services[0].Environment = "production"
	c.Services[1].Application = "shop"
	c.Services[1].Environment = "staging"
	now := time.Now()
	return View{Snapshot: engine.Build(c, "primary", "", nil, nil, now), Now: now}
}
func TestGroupingScopeInheritanceAndSearch(t *testing.T) {
	v := groupedFixture()
	rows := v.GroupedList(Services, GroupOptions{Collapsed: true, By: "application"})
	if len(rows) != 3 {
		t.Fatalf("environments and ungrouped must stay separate: %+v", rows)
	}
	checks := v.GroupedList(Checks, GroupOptions{Collapsed: true, Application: "shop", Environment: "production", By: "service", Search: "商城"})
	if len(checks) != 3 || checks[0].Kind != "group" {
		t.Fatalf("expected one service and its two inherited checks: %+v", checks)
	}
	for _, r := range checks[1:] {
		if r.Kind != "check" || EntityScope(v.Snapshot.Topology, r.Kind, r.ID).Service != "web" {
			t.Fatal(r)
		}
	}
	missing := v.GroupedList(Services, GroupOptions{Collapsed: true, By: "flat", Application: "-"})
	if len(missing) != 1 || missing[0].ID != "notes" {
		t.Fatal(missing)
	}
	if len(v.GroupedList(Services, GroupOptions{Collapsed: true, By: "flat", Application: "shop", Environment: "missing"})) != 0 {
		t.Fatal("filter leaked rows")
	}
}
func TestGroupEvidenceDoesNotInventAvailability(t *testing.T) {
	v := groupedFixture()
	o := GroupOptions{Collapsed: true, By: "application", Application: "shop", Environment: "production"}
	rows := v.GroupedList(Services, o)
	if len(rows) != 1 || rows[0].Health != "unknown" {
		t.Fatal(rows)
	}
	v.Snapshot.Services[0].Health = "down"
	rows = v.GroupedList(Services, o)
	if rows[0].Health != "degraded" || !strings.Contains(rows[0].Cols[0], "1 attention") {
		t.Fatal(rows)
	}
	v.Cached = true
	rows = v.GroupedList(Services, o)
	if rows[0].Health != "unknown" || rows[0].Note != "cached" {
		t.Fatal(rows)
	}
}

func TestGroupFreshnessAndSharedDependencies(t *testing.T) {
	v := groupedFixture()
	c := &v.Snapshot.Topology
	c.Applications = append(c.Applications, model.Application{ID: "shared", Name: "Shared"})
	c.Services[2].Application = "shared"
	c.Services[0].DependsOn = []string{c.Services[2].ID}
	// A paused check alongside a passing one must remain visible in the summary.
	v.Snapshot.Services[0].Health = "up"
	v.Snapshot.Checks[0].Freshness = "maintenance"
	o := GroupOptions{Collapsed: true, By: "application", Application: "shop", Environment: "production"}
	rows := v.GroupedList(Services, o)
	if rows[0].Health == "up" || !strings.Contains(rows[0].Cols[0], "1 paused") {
		t.Fatal(rows)
	}
	detail := strings.Join(rows[0].Details, "\n")
	if strings.Count(detail, "Shared dependency:") != 1 || !strings.Contains(detail, "2 total") {
		t.Fatal(detail)
	}
	v.Snapshot.Checks[0].Freshness = "stale"
	rows = v.GroupedList(Checks, GroupOptions{Collapsed: true, By: "service", Service: "web"})
	if !strings.Contains(rows[0].Cols[0], "1 stale") {
		t.Fatal(rows)
	}
}

func TestGroupCheckCountsRespectObserverScope(t *testing.T) {
	v := groupedFixture()
	c := v.Snapshot.Topology
	v.Snapshot = engine.Build(c, "primary", "home", nil, nil, v.Now)
	rows := v.GroupedList(Services, GroupOptions{Collapsed: true, By: "application", Application: "shop", Environment: "production"})
	if !strings.Contains(strings.Join(rows[0].Details, "\n"), "Checks: 1 total") {
		t.Fatal(rows[0].Details)
	}
}

func TestEnvironmentGroupingRetainsMemberEvidence(t *testing.T) {
	v := groupedFixture()
	v.Snapshot.Topology.Services[1].Environment = "production"
	v.Snapshot.Topology.Services[1].Application = "another-app"
	rows := v.GroupedList(Services, GroupOptions{By: "environment", Collapsed: true})
	var production *Row
	for i := range rows {
		if rows[i].Title == "production" {
			production = &rows[i]
		}
	}
	if production == nil || !strings.Contains(production.Cols[0], "2 members") || production.Health != "unknown" {
		t.Fatal(rows)
	}
	if !strings.Contains(strings.Join(production.Details, "\n"), "not business availability") {
		t.Fatal(production.Details)
	}
}
