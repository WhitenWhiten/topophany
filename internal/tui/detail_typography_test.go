package tui

import (
	"strings"
	"testing"
)

func detailText(lines []vline) []string {
	out := []string{}
	for _, l := range lines {
		out = append(out, spansText(l))
	}
	return out
}

// Labels keep one column in a wide panel, however long the longest is up
// to thirty columns, so no value is pushed out of line.
func TestDetailLabelsKeepOneColumn(t *testing.T) {
	m := &Model{}
	r := Row{Kind: "check", ID: "c", Details: []string{"Check: c", "Failure / recovery threshold: 2 / 2", "Policy confirmation samples: 1"}}
	at := map[int]bool{}
	for _, l := range detailText(m.detailBody(r, 84)) {
		k, rest, _ := strings.Cut(l, "  ")
		at[width(k)+2+width(rest)-width(strings.TrimLeft(rest, " "))] = true
	}
	if len(at) != 1 {
		t.Fatalf("values start in %d columns: %v", len(at), at)
	}
}

// A label written as a phrase starts with a capital; an identifier used
// as one keeps its case.
func TestDetailLabelCapitalizesPhrasesOnly(t *testing.T) {
	for in, want := range map[string]string{
		"remote-response evidence":      "Remote-response evidence",
		"obs-mac/path/m-transit-exit-a": "obs-mac/path/m-transit-exit-a",
		"Checks":                        "Checks",
	} {
		if got := label(in); got != want {
			t.Errorf("label(%q) = %q, want %q", in, got, want)
		}
	}
}

// In a related problem each incident heads its own evidence in its own
// words, apart from the sections under it.
func TestRelatedIncidentHeadingsKeepTheirWords(t *testing.T) {
	m := &Model{}
	r := Row{Kind: "problem", ID: "p", Details: []string{"", "Seattle MacBook → Primary → Exit A:", "Scope: obs-mac/path/x", "", "Failed checks:", "m-exit"}}
	text := strings.Join(detailText(m.detailBody(r, 80)), "\n")
	if !strings.Contains(text, "Seattle MacBook → Primary → Exit A") || strings.Contains(text, "SEATTLE") {
		t.Fatalf("the incident's heading lost its words:\n%s", text)
	}
	if !strings.Contains(text, "FAILED CHECKS") {
		t.Fatalf("a section heading is no longer one:\n%s", text)
	}
}
