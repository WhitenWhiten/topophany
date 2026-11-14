package tui

import (
	"strings"
	"testing"
)

// What a business list is browsed by sits on its panel's top edge, not on
// a row of its own over the panel, and a click on it browses.
func TestBrowseScopeSitsOnThePanelEdge(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 200, 40
	m.setTab(tabServices)
	lines := strings.Split(visible(m), "\n")
	edge := lines[3]
	if !strings.HasPrefix(edge, "╭─ SERVICES") || !strings.Contains(edge, "by application") {
		t.Fatalf("the browse scope is not on the panel's edge:\n%s\n%s", lines[2], edge)
	}
	if strings.Contains(strings.Join(lines, "\n"), "b Browse") {
		t.Fatal("a row over the panel still says b Browse")
	}
	x := strings.Index(edge, "by application")
	if got := m.hitAt(len([]rune(edge[:x])), 3); got != "key:b" {
		t.Fatalf("a click on the scope does %q", got)
	}
}

// Where the whole scope does not fit, what is left at everything gives
// way, and what was chosen stays.
func TestBrowseScopeKeepsWhatWasChosen(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 80, 30
	m.setTab(tabServices)
	m.AttentionOnly = true
	edge := strings.Split(visible(m), "\n")[3]
	if !strings.Contains(edge, "attention only") {
		t.Fatalf("the chosen filter gave way:\n%s", edge)
	}
}
