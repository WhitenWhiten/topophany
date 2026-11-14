package tui

import (
	"github.com/WhitenWhiten/topophany/internal/view"
)

// A service's assessment keeps apart what its checks establish about the
// application, the path to it and the workers that measured it (see
// view.ServiceAssessment). The TUI marks its closed state the way it marks
// every state, with a glyph and a color, rather than with the color of the
// service's aggregate check health: a service whose checks all pass can
// still have an availability nobody established.

// assessmentMark is the glyph and color of an assessment's closed state.
func assessmentMark(state string) (rune, Color) {
	switch state {
	case "access_fail", "local_fail":
		return '✖', cDown
	case "path_warn", "health_warn":
		return '▲', cWarn
	case "up":
		return '●', cUp
	}
	return '○', cUnknown
}

// assessed reports whether word is r's assessment speaking: a service row
// whose state no note (cached, affected, …) has taken over.
func assessed(r Row, word string) bool {
	return r.Kind == "service" && r.Assessment != nil && word == r.Assessment.Label()
}

// assessmentWord is the state column's word for an assessed service, lit
// as DOWN and WARN are; availability established says no more than up
// does, and unknown is as quiet as any other unknown.
func assessmentWord(a view.ServiceAssessment) (string, Style) {
	switch g, c := assessmentMark(a.State()); {
	case c == cDown:
		return a.Label(), fg(cDown).bold()
	case c == cWarn:
		return a.Label(), fg(cWarn).bold()
	case g == '●':
		return "", Style{}
	}
	return "unknown", fg(cFaint)
}

// scopeMark is the glyph and color of one of an assessment's three words:
// up; degraded, or workers only partly online; down; unknown.
func scopeMark(word string) (rune, Color) {
	switch word {
	case "up":
		return '●', cUp
	case "degraded", "partial":
		return '▲', cWarn
	case "down":
		return '✖', cDown
	}
	return '○', cUnknown
}

// scopeTone is the color a word is written in: up quietly, trouble in
// trouble's color, unknown muted.
func scopeTone(word string) Color {
	switch word {
	case "up":
		return Mix(cUp, cMuted, .4)
	case "degraded", "partial":
		return cWarn
	case "down":
		return cDown
	}
	return cMuted
}

// scopeStrip says the three on one line of at most w columns, each its
// name, mark and word: "app ○ unknown   path ▲ degraded   worker ● up".
// Where that does not fit the gaps close, then the words of what is up
// give way, then every word, the marks still saying it.
func scopeStrip(a view.ServiceAssessment, w int) []span {
	parts := [3][2]string{{"app", a.Availability}, {"path", a.Reachability}, {"worker", a.WorkerOnline}}
	var out []span
	for _, words := range []int{2, 1, 0} {
		for _, gap := range []string{"   ", "  "} {
			out = []span{}
			for i, p := range parts {
				if i > 0 {
					out = append(out, span{gap, Style{}})
				}
				g, c := scopeMark(p[1])
				out = append(out, span{p[0] + " ", fg(cMuted)}, span{string(g), fg(c)})
				if words == 2 || words == 1 && p[1] != "up" {
					out = append(out, span{" " + p[1], fg(scopeTone(p[1]))})
				}
			}
			if spansWidth(out) <= w {
				return out
			}
		}
	}
	return out
}

// scopeNames are the evidence scopes as the evidence rows name them.
var scopeNames = map[string]string{"remote": "access", "local": "local health", "user_entry": "user entry", "reported": "upstream report"}

// scopeRows lays an assessment open in w columns: its three words, each
// check it rests on as its observer saw it (how far that reached, which
// check, and what was not current), then what the topology could add and
// what the evidence does not establish.
func scopeRows(a view.ServiceAssessment, w int) []cardRow {
	rows := []cardRow{{left: scopeStrip(a, w)}}
	for _, e := range a.Evidence {
		scope := scopeNames[e.Scope]
		if scope == "" {
			scope = e.Scope
		}
		right := []span{{scope, fg(cMuted)}, {" · " + e.Check, fg(cFaint)}}
		if e.Freshness != "fresh" && e.Freshness != "" {
			right = append(right, span{" · " + e.Freshness, fg(cWarn)})
		}
		if e.Worker != "up" {
			right = append(right, span{" · worker " + e.Worker, fg(cMuted)})
		}
		rows = append(rows, cardRow{
			left:  []span{{string(healthGlyph(e.Health)) + " ", fg(healthColor(e.Health))}, {e.ObserverName, fg(cText)}},
			right: right, tight: right[:1], hit: "goto:check:" + e.Check,
		})
	}
	// After the summary and a line for each check come the notes, in the
	// words topo show prints them.
	for _, note := range a.Lines()[1+len(a.Evidence):] {
		for _, l := range wrap(note, w) {
			rows = append(rows, cardRow{left: []span{{l, fg(cMuted).italic()}}})
		}
	}
	return rows
}

// rowLine is a card row as one line of w columns for the evidence panel:
// its left spans, then its right ones against the right edge, the short
// form or none where they do not fit.
func rowLine(r cardRow, w int) vline {
	right := r.right
	if spansWidth(r.left)+1+spansWidth(right) > w && r.tight != nil {
		right = r.tight
	}
	if spansWidth(r.left)+1+spansWidth(right) > w {
		right = nil
	}
	room := w
	if len(right) > 0 {
		room = w - spansWidth(right) - 1
	}
	out := vline{}
	n := 0
	for _, s := range r.left {
		t := ellipsis(s.s, room-n)
		out = append(out, span{t, s.st})
		n += width(t)
	}
	if len(right) > 0 {
		out = append(out, span{fit("", w-n-spansWidth(right)), Style{}})
		out = append(out, right...)
	}
	return out
}
