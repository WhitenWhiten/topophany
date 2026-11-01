package tui

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"strings"
	"testing"
)

func TestRelatedIncidentNavigationKeepsEvidence(t *testing.T) {
	m := &Model{Tab: tabIncidents, Width: 100, Height: 30, NoColor: true, Snapshot: model.Snapshot{Incidents: []model.Incident{{ID: "incident", Summary: "failure", Checks: []string{}}}}}
	m.Key("b")
	if !m.RelatedIncidents || len(m.Rows()) != 1 {
		t.Fatal("related list unavailable")
	}
	m.Key("enter")
	if !m.Detail {
		t.Fatal("detail did not open")
	}
	text := strings.Join(m.Render(), "\n")
	if !strings.Contains(text, "correlation") {
		t.Fatal("causality caveat absent", text)
	}
	m.Key("b")
	if m.RelatedIncidents || m.Detail {
		t.Fatal("toggle did not restore incident list")
	}
}
