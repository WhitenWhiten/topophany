package tui

import (
	"github.com/WhitenWhiten/topophany/internal/view"
	"strings"
	"time"
)

// Before the first snapshot there is nothing to lay out: every view would
// stand empty, counting zeros. The splash takes the body instead: the globe
// turning beside the wordmark while the TUI connects, saying to whom; and
// when nobody answers, the same scene gone still, saying which collector
// did not answer and why, what happens next and what to check.

// waiting reports whether the viewer has no evidence yet, connecting or
// turned away: no snapshot has come, and no topology to lay views out from.
func (m *Model) waiting() bool {
	return m.Edit == nil && m.Snapshot.GeneratedAt.IsZero() && len(m.Snapshot.Topology.Nodes) == 0 && (m.Loading != "" || m.Error != "")
}

// retryEvery is how often the run loop asks again while nobody answers.
const retryEvery = 3 * time.Second

func (m *Model) splash(cv *Canvas, x, y, w, h int) {
	if m.splashAt.IsZero() || m.Now.Before(m.splashAt) {
		m.splashAt = m.Now
	}
	t := m.Now.Sub(m.splashAt).Seconds()
	offline := m.Loading == "" && m.Error != ""
	if offline {
		t = 3 // the scene holds still: nothing is on its way
	}
	lines := m.splashLines(offline)
	tw := 0
	for _, l := range lines {
		lw := 0
		for _, s := range l {
			lw += width(s.s)
		}
		tw = max(tw, lw)
	}
	tw = min(max(tw, 53), w-4)
	mark := 4 // the wordmark's rows
	if w < 57 {
		mark = 1
	}
	th := mark + 2 + len(lines)
	// The globe beside the text where there is room for both, otherwise
	// the text alone, centred.
	// The globe keeps its size whatever the text says, so nothing moves
	// when connecting turns into an answer or into no answer.
	cell := m.cellAspect()
	gh := min(h-2, max(11, h*3/5))
	gw := int(float64(gh)*cell) + 2
	globe := w >= tw+gw+10 && gh >= 9
	total := tw
	if globe {
		total += gw + 6
	}
	gx := x + max(1, (w-total)/2)
	tx := gx
	if globe {
		tx = gx + gw + 6
		gy := y + (h-gh)/2
		m.drawGlobeQuiet(cv, gx, gy, gw, gh, t)
		if offline {
			cv.Tint(gx, gy, gw, gh, func(s Style) Style {
				if s.FG != 0 {
					s.FG = Mix(s.FG, cInk, .5)
				}
				if s.BG != 0 {
					s.BG = Mix(s.BG, cInk, .5)
				}
				return s
			})
		}
	}
	ty := y + max(0, (h-th)/2)
	if mark == 4 {
		m.drawWordmark(cv, tx, ty, t)
		if offline {
			cv.Tint(tx, ty, 54, 4, func(s Style) Style {
				if s.FG != 0 {
					s.FG = Mix(s.FG, cInk, .35)
				}
				if s.BG != 0 && s.BG != cInk {
					s.BG = Mix(s.BG, cInk, .35)
				}
				return s
			})
		}
	} else {
		cv.Text(tx, ty, "T O P O P H A N Y", fg(cAccent).bold(), w-2)
	}
	ty += mark + 2
	for i, l := range lines {
		if ty+i >= y+h {
			break
		}
		// The lines come up one after another as the scene opens.
		k := clamp(t*2.2-float64(i)*.18, 0, 1)
		cx := tx
		for _, s := range l {
			st := s.st
			if st.FG != 0 {
				st.FG = Mix(cInk, st.FG, k)
			}
			cx += cv.Text(cx, ty+i, s.s, st, tx+tw-cx)
		}
	}
}

// splashLines is what the splash says under the wordmark.
func (m *Model) splashLines(offline bool) []vline {
	hosts := []string{}
	for _, u := range m.Collectors {
		hosts = append(hosts, hostOf(u))
	}
	about := ""
	if m.Project != "" {
		about = "project " + m.Project
	}
	if m.Observer != "" {
		about = strings.TrimPrefix(about+" · seen from "+m.Observer, " · ")
	}
	out := []vline{}
	if !offline {
		switch {
		case m.Direct:
			out = append(out, vline{{m.spinner() + "  ", fg(cAccent2)}, {view.DirectText("opening"), fg(cText).bold()}})
		case m.Preview != nil:
			out = append(out, vline{{m.spinner() + "  ", fg(cAccent2)}, {m.ui("Starting the preview"), fg(cText).bold()}},
				vline{{"   synthetic evidence; no collector is dialled", fg(cMuted)}})
		case len(hosts) == 0:
			out = append(out, vline{{m.spinner() + "  ", fg(cAccent2)}, {m.ui("Connecting to the collector"), fg(cText).bold()}})
		default:
			out = append(out, vline{{m.spinner() + "  ", fg(cAccent2)}, {m.ui("Connecting to "), fg(cText)}, {hosts[0], fg(cGlow).bold()}})
			for _, h := range hosts[1:] {
				out = append(out, vline{{"   or ", fg(cMuted)}, {h, fg(cText)}})
			}
		}
		if about != "" {
			out = append(out, vline{{"   " + about, fg(cMuted)}})
		}
		out = append(out, vline{}, vline{{"   The views fill in as soon as the first snapshot arrives.", fg(cFaint)}})
		return out
	}
	head, why, hint := trouble(m.Error)
	head, hint = m.ui(head), m.ui(hint)
	if m.ReconnectHint != "" {
		hint = m.ReconnectHint
	}
	out = append(out, vline{{"◌  ", fg(cWarn)}, {head, fg(cWarn).bold()}})
	for _, w := range why {
		host, reason, ok := strings.Cut(w, ": ")
		if !ok {
			for _, l := range wrap(w, 64) {
				out = append(out, vline{{"   " + l, fg(cMuted)}})
			}
			continue
		}
		fact, next, _ := strings.Cut(reason, " — ")
		out = append(out, vline{{"   " + host, fg(cText).bold()}})
		if next != "" {
			out = append(out, vline{{"   " + fact, fg(cMuted)}, {" — " + next, fg(cAccent2)}})
		} else {
			out = append(out, vline{{"   " + fact, fg(cMuted)}})
		}
	}
	if about != "" {
		out = append(out, vline{{"   " + about, fg(cFaint)}})
	}
	out = append(out, vline{})
	if m.Fetching {
		out = append(out, vline{{m.spinner() + "  ", fg(cAccent2)}, {m.ui("Trying again…"), fg(cAccent2)}})
	} else {
		out = append(out, vline{{"↻  ", fg(cAccent2)}, {"Trying again every " + itoa(int(retryEvery/time.Second)) + " s", fg(cText)}, {" · r now", fg(cMuted)}})
	}
	if hint != "" {
		for _, l := range wrap(hint, 60) {
			out = append(out, vline{{"   " + l, fg(cMuted)}})
		}
	}
	return out
}

// trouble turns the error of a read into a headline, the reasons under it
// ("host: why" where a collector is named) and what to check.
func trouble(err string) (head string, why []string, hint string) {
	switch {
	case strings.Contains(err, "no configured collector answered"):
		head = "No collector answered"
		// The client lists why each did not, in parentheses at the end.
		if i := strings.Index(err, " ("); i > 0 && strings.HasSuffix(err, ")") {
			why = strings.Split(err[i+2:len(err)-1], "; ")
		}
		if !strings.Contains(err, " — ") {
			hint = "Check the address with topo login, or see the whole UI on synthetic evidence with topo preview."
		}
	case strings.Contains(err, "authentication/authorization rejected"):
		head, hint = "The collector refused this client's token", "Save a valid viewer token with topo login."
	case strings.Contains(err, "project identity mismatch"):
		head, hint = "The collector serves another project", "Check the project given to topo login."
	case strings.Contains(err, "simulated collector outage"):
		head, why = "No collector answered", []string{"preview: a simulated outage; x picks another scenario"}
	default:
		head, why = "Could not read from the collector", []string{err}
	}
	return
}

// drawGlobeQuiet is the About globe without place names: on the splash the
// rings mark places of the world, not of a network not yet known.
func (m *Model) drawGlobeQuiet(cv *Canvas, x, y, w, h int, t float64) {
	d := NewDots(w, h)
	var p plotter = d
	if m.profile() == color256 {
		p = dotsOnly{d}
	}
	m.paintGlobe(p, w*2, h*4, m.cellAspect()/2, t, 0)
	d.Blit(cv, x, y, 0)
}
