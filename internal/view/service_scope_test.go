package view

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func scopeSnapshot() model.Snapshot {
	return model.Snapshot{GeneratedAt: time.Now(), Topology: model.Config{
		Services:  []model.Service{{ID: "api", Name: "API", Node: "s2"}},
		Observers: []model.Observer{{ID: "wsl", Name: "WSL", Node: "workstation"}, {ID: "s2", Name: "S2", Node: "s2"}},
		Checks:    []model.Check{{ID: "remote", Service: "api", Observer: "wsl", Kind: "http", Target: "https://s2.test"}, {ID: "local", Service: "api", Observer: "s2", Kind: "http", Target: "http://127.0.0.1/health"}},
	}, Services: []model.EntityState{{ID: "api", Health: "down", Checks: []string{"remote", "local"}}},
		Checks:    []model.CheckState{{ID: "remote", Observer: "wsl", Health: "down", Freshness: "fresh"}, {ID: "local", Observer: "s2", Health: "up", Freshness: "fresh"}},
		Observers: []model.ObserverState{{ID: "wsl", Health: "up"}, {ID: "s2", Health: "up"}}}
}
func TestServiceSeparatesAvailabilityPathAndWorker(t *testing.T) {
	s := scopeSnapshot()
	v := View{Snapshot: s, Now: time.Now()}
	r := v.Rows(Services)[0]
	a := r.Assessment
	if a.Availability != "up" || a.Reachability != "down" || a.WorkerOnline != "up" || Pill(r) != "ACCESS FAIL" || State(r) != "ACCESS FAIL" {
		t.Fatal(r)
	}
	details := strings.Join(r.Details, "\n")
	for _, want := range []string{"access from WSL: down", "S2 local health: up", "does not establish an application outage"} {
		if !strings.Contains(details, want) {
			t.Fatal(details)
		}
	}
	card, _ := v.Fleet().ServiceCard("api")
	if card.Assessment.Availability != a.Availability || card.Assessment.Reachability != a.Reachability {
		t.Fatal(card)
	}
	v.Cached = true
	r = v.List(Services, "")[0]
	if r.Assessment.Availability != "unknown" || r.Assessment.Reachability != "unknown" || State(r) != "cached" {
		t.Fatal(r)
	}
}
func TestSingleRemoteCheckWarnsAndDoesNotConfirmOutage(t *testing.T) {
	s := scopeSnapshot()
	s.Topology.Checks = s.Topology.Checks[:1]
	s.Checks = s.Checks[:1]
	r := (View{Snapshot: s, Now: time.Now()}).Rows(Services)[0]
	if r.Assessment.Availability != "unknown" || len(r.Assessment.Hints) != 1 || State(r) == "DOWN" {
		t.Fatal(r)
	}
	// Merely viewing WSL cannot borrow a local check's passing evidence.
	s = scopeSnapshot()
	s.ViewObserver = "wsl"
	s.Checks = s.Checks[:1]
	r = (View{Snapshot: s, Now: time.Now()}).Rows(Services)[0]
	if r.Assessment.Availability != "unknown" {
		t.Fatal(r)
	}
}
