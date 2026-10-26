package view

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
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

func TestServiceRuntimeReportsDoNotEstablishReachability(t *testing.T) {
	for _, kind := range []string{"mihomo", "tailscale", "wireguard", "cloudflared", "cloudflare", "external"} {
		for _, health := range []string{"up", "degraded", "down"} {
			t.Run(kind+"/"+health, func(t *testing.T) {
				s := scopeSnapshot()
				s.Topology.Checks = s.Topology.Checks[:1]
				s.Topology.Checks[0].Kind = kind
				s.Checks = s.Checks[:1]
				s.Checks[0].Health = health
				s.Checks[0].Evidence = "reported"
				r := (View{Snapshot: s, Now: time.Now()}).Rows(Services)[0]
				a := r.Assessment
				if a.Availability != "unknown" || a.Reachability != "unknown" || len(a.Evidence) != 1 || a.Evidence[0].Scope != "reported" || a.Evidence[0].Health != health || len(a.Hints) != 0 {
					t.Fatal("runtime report became measured application or path evidence", a)
				}
				if !strings.Contains(strings.Join(r.Details, "\n"), "upstream report via WSL: "+health) {
					t.Fatal("runtime report was lost", r.Details)
				}
				// A separate application response still establishes availability.
				measured := scopeSnapshot()
				s.Topology.Checks = append(s.Topology.Checks, measured.Topology.Checks[1])
				s.Checks = append(s.Checks, measured.Checks[1])
				r = (View{Snapshot: s, Now: time.Now()}).Rows(Services)[0]
				if r.Assessment.Availability != "up" || r.Assessment.Reachability != "unknown" || Pill(r) != "UP" {
					t.Fatal("runtime report overrode application response", r.Assessment)
				}
			})
		}
	}
}

func TestServiceMihomoPolicyKeepsReportedScope(t *testing.T) {
	c := testkit.Config()
	x := c.Checks[0]
	x.Kind, x.Path, x.Adapter.ExpectedMode = "mihomo", "", "global"
	c.Checks = []model.Check{x}
	now := time.Now()
	o := testkit.Observation(c, x.ID, 1, now, "policy")
	o.Evidence, o.Stage, o.Code, o.Connectivity = "reported", "adapter", "mode_mismatch", false
	o.Facts = map[string]string{"mode": "rule"}
	s := engine.Build(c, "review", "", []model.Observation{o}, nil, now)
	r := (View{Snapshot: s, Now: now}).Rows(Services)[0]
	if r.Health != "degraded" || r.Assessment.Reachability != "unknown" || r.Assessment.Evidence[0].Scope != "reported" || Pill(r) == "ACCESS FAIL" {
		t.Fatal("Mihomo policy became an access failure", r)
	}
}

func TestLocalServiceWarningSurvivesConfirmationAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outcomes     []string
		health, pill string
	}{
		{"unconfirmed_failure", []string{"fail"}, "degraded", "HEALTH WARN"},
		{"recovering", []string{"fail", "fail", "ok"}, "degraded", "HEALTH WARN"},
		{"confirmed_failure", []string{"fail", "fail"}, "down", "LOCAL FAIL"},
		{"recovered", []string{"fail", "fail", "ok", "ok"}, "up", "UP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := model.Config{Schema: 1, Project: "lab",
				Nodes:     []model.Node{{ID: "host"}},
				Observers: []model.Observer{{ID: "local", Node: "host", Context: "service host", StaleSeconds: 90}},
				Services:  []model.Service{{ID: "web", Node: "host"}},
				Checks:    []model.Check{{ID: "health", Service: "web", Observer: "local", Kind: "http", Target: "http://127.0.0.1/health", FailureThreshold: 2, RecoveryThreshold: 2}},
			}
			config.Defaults(&c)
			if err := config.Validate(c); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			rows := make([]model.Observation, 0, len(tc.outcomes))
			for i, outcome := range tc.outcomes {
				rows = append(rows, testkit.Observation(c, "health", uint64(i+1), now.Add(time.Duration(i-len(tc.outcomes))*time.Second), outcome))
			}
			s := engine.Build(c, "review", "", rows, nil, now)
			v := View{Snapshot: s, Now: now}
			r := v.Rows(Services)[0]
			if r.Health != tc.health || r.Assessment.Availability != tc.health || r.Assessment.Reachability != "unknown" || Pill(r) != tc.pill || State(r) != tc.pill {
				t.Fatal("local check state was lost", r)
			}
			card, _ := v.Fleet().ServiceCard("web")
			if card.Assessment.Availability != tc.health {
				t.Fatal("card differs from service row", card.Assessment)
			}
			for _, mode := range []string{"cached", "stale", "unseen"} {
				v := View{Snapshot: s, Now: now}
				v.Snapshot.Checks = append([]model.CheckState(nil), s.Checks...)
				switch mode {
				case "cached":
					v.Cached = true
				case "stale":
					v.Snapshot.Checks[0].Freshness = "stale"
				case "unseen":
					v.Snapshot.Checks = nil
				}
				if a := v.Rows(Services)[0].Assessment; a.Availability != "unknown" || a.Reachability != "unknown" {
					t.Fatal(mode+" view borrowed local health", a)
				}
			}
		})
	}
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
