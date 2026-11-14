package tui

import (
	"strings"
	"testing"
)

// The status card says how many services' availability the view knows as
// the third line beside its big number, leaving which view it is to the
// header, and keeps the row above the health bar clear.
func TestStatusCardSaysCoverageBesideTheBigNumber(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 200, 50
	lines := strings.Split(visible(m), "\n")
	at := -1
	for i, l := range lines {
		if strings.Contains(l, "Service availability") {
			at = i
		}
	}
	if at < 1 {
		t.Fatalf("no coverage line:\n%s", strings.Join(lines, "\n"))
	}
	if l := lines[at]; !strings.Contains(l, "/23 known") || strings.Contains(l, "view:") {
		t.Fatalf("coverage line %q", l)
	}
	if !strings.Contains(lines[at-1], "configured") {
		t.Fatalf("the coverage line is not under the big number's words:\n%s\n%s", lines[at-1], lines[at])
	}
	// The card's own columns, as wide as the overview makes the sidebar.
	sw := min(max(m.Width*36/100, 46), 74)
	card := func(l string) string { r := []rune(l); return string(r[len(r)-sw:]) }
	if strings.TrimSpace(strings.Trim(card(lines[at+1]), "│")) != "" || !strings.Contains(lines[at+2], "━") {
		t.Fatalf("the row above the health bar is not clear:\n%s\n%s", lines[at+1], lines[at+2])
	}

	// Where the words do not fit, the count keeps its noun.
	cv := NewCanvas(24, 1)
	m.coverageLine(cv, 0, 0, 24)
	if got := strings.TrimSpace(strings.Join(cv.Lines(colorNone), "")); !strings.HasSuffix(got, "services known") {
		t.Fatalf("narrow coverage line %q", got)
	}
}
