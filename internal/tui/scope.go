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
