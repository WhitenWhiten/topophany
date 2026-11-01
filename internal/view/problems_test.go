package view

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
)

func TestProblemsKeepObserverIdentityAndUnavailableCounterEvidence(t *testing.T) {
	v := View{Snapshot: model.Snapshot{Topology: model.Config{Nodes: []model.Node{{ID: "target", Name: "Target"}}, Endpoints: []model.Endpoint{{ID: "ep", Node: "target"}}, Checks: []model.Check{{ID: "a", Observer: "home", Endpoint: "ep"}, {ID: "b", Observer: "cloud", Endpoint: "ep"}}}, Incidents: []model.Incident{{ID: "home/fail", Checks: []string{"a"}, CounterEvidence: []string{"outside"}}, {ID: "cloud/fail", Checks: []string{"b"}}}}}
	groups := v.Problems()
	if len(groups) != 1 || len(groups[0].Incidents) != 2 || len(groups[0].Observers) != 2 {
		t.Fatal(groups)
	}
	if len(groups[0].CounterEvidence) != 1 || groups[0].CounterEvidence[0].ID != "outside" || groups[0].CounterEvidence[0].Health != "unknown" {
		t.Fatal(groups)
	}
	v.Cached = true
	if v.Problems()[0].Current {
		t.Fatal("cached grouping claimed current evidence")
	}
}
