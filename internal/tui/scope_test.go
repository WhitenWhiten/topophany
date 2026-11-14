package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/view"
)

// A service's pill and state column follow its assessment, not the health
// of its checks: here every check passes, and only an established
// availability may say so.
func TestServicePillAndStateFollowTheAssessment(t *testing.T) {
	for _, tc := range []struct {
		a          view.ServiceAssessment
		pill, word string
		color      Color
	}{
		{view.ServiceAssessment{Availability: "unknown", Reachability: "unknown", WorkerOnline: "up"}, "○ UNKNOWN", "unknown", cUnknown},
		{view.ServiceAssessment{Availability: "unknown", Reachability: "degraded", WorkerOnline: "up"}, "▲ PATH WARN", "PATH WARN", cWarn},
		{view.ServiceAssessment{Availability: "unknown", Reachability: "down", WorkerOnline: "up"}, "✖ ACCESS FAIL", "ACCESS FAIL", cDown},
		{view.ServiceAssessment{Availability: "down", Reachability: "up", WorkerOnline: "up"}, "✖ LOCAL FAIL", "LOCAL FAIL", cDown},
		{view.ServiceAssessment{Availability: "up", Reachability: "up", WorkerOnline: "up"}, "● UP", "", cUp},
	} {
		r := Row{Kind: "service", ID: "s", Health: "up", Assessment: &tc.a}
		pill, st := statePill(r)
		if strings.TrimSpace(pill) != tc.pill || st.FG != tc.color {
			t.Errorf("%+v: pill %q in %06x, want %q in %06x", tc.a, pill, uint32(st.FG)&0xFFFFFF, tc.pill, uint32(tc.color)&0xFFFFFF)
		}
		word, ws := stateWord(r)
		if word != tc.word {
			t.Errorf("%+v: state column %q, want %q", tc.a, word, tc.word)
		}
		if (tc.color == cDown || tc.color == cWarn) && (ws.FG != tc.color || ws.Attr&Bold == 0) {
			t.Errorf("%+v: %q is not lit as DOWN and WARN are", tc.a, word)
		}
	}
}

func spansText(ss []span) string {
	var b strings.Builder
	for _, s := range ss {
		b.WriteString(s.s)
	}
	return b.String()
}

// The scope strip closes its gaps, then drops the words of what is up,
// then every word, and the marks still say all three.
func TestScopeStripGivesWayToItsMarks(t *testing.T) {
	a := view.ServiceAssessment{Availability: "unknown", Reachability: "degraded", WorkerOnline: "up"}
	for _, tc := range []struct {
		w    int
		want string
	}{
		{80, "app ○ unknown   path ▲ degraded   worker ● up"},
		{44, "app ○ unknown  path ▲ degraded  worker ● up"},
		{40, "app ○ unknown  path ▲ degraded  worker ●"},
		{24, "app ○  path ▲  worker ●"},
	} {
		if got := spansText(scopeStrip(a, tc.w)); got != tc.want {
			t.Errorf("%d columns: %q, want %q", tc.w, got, tc.want)
		}
	}
}

// The evidence panel, the service card and the open card say what the
// assessment establishes as marks and rows, never as the sentences topo
// show prints.
func TestServiceEvidenceScopeIsLaidOutAsRows(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 200, 50
	m.setTab(tabServices)
	m.Selected = slices.IndexFunc(m.Rows(), func(r Row) bool { return r.ID == "vless-transit" })
	if m.Selected < 0 {
		t.Fatal("the preview has no vless-transit service")
	}
	sentences := []string{"path reachability:", "worker online:", "access from ", "app:"}
	for _, step := range []string{"list", "s", "enter"} {
		if step != "list" {
			m.Key(step)
		}
		out := visible(m)
		if step != "s" && (!strings.Contains(out, "EVIDENCE SCOPE") || !strings.Contains(out, "access · ")) {
			t.Fatalf("%s: no evidence scope laid out as rows:\n%s", step, out)
		}
		if !strings.Contains(out, "app ") || !strings.Contains(out, "worker ") {
			t.Fatalf("%s: no scope strip:\n%s", step, out)
		}
		for _, s := range sentences {
			if strings.Contains(out, s) {
				t.Fatalf("%s: the assessment is still written as sentences (%q):\n%s", step, s, out)
			}
		}
	}
}

// On a card too narrow for both, a service's name gives way to its state,
// however long the state's pill: " ✖ ACCESS FAIL " stays whole.
func TestCardHeadKeepsTheStateWhole(t *testing.T) {
	a := view.ServiceAssessment{Availability: "unknown", Reachability: "down", WorkerOnline: "up"}
	pill, st := statePill(Row{Kind: "service", ID: "s", Health: "up", Assessment: &a})
	cv := NewCanvas(38, 1)
	(&Model{}).drawRow(cv, 0, 0, 38, cardRow{left: []span{{"VLESS+Reality Entry (Primary)", fg(cText)}}, right: []span{{pill, st}}}, cInk)
	if got := strings.Join(cv.Lines(colorNone), ""); !strings.HasSuffix(got, pill) || !strings.Contains(got, "…") {
		t.Fatalf("card head %q", got)
	}
}
