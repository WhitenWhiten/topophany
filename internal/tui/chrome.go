package tui

import (
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
	viewpkg "github.com/WhitenWhiten/topophany/internal/view"
)

// The frame around every view: a header bar, the tab strip, a one-line health
// summary, and the key bar at the bottom. Each draws into the row it is
// given, w cells from x0, so a terminal spreads them across its width and
// a device's screen (device.go) fits them into the room its layout leaves.

// header is the bar naming the project, the view and how live it is.
func (m *Model) header(cv *Canvas, x0, y, w int) {
	m.headerBar(cv, x0, y, w, false)
}

// headerBar draws the header; on a touch screen (touch) the view, the
// scenario and the collector are left to chips of their own (viewChips).
func (m *Model) headerBar(cv *Canvas, x0, y, w int, touch bool) {
	on := func(c Color) Style { return fg(c).Bg(cInkHi) }
	cv.Fill(x0, y, w, 1, Style{BG: cInkHi})
	type part struct {
		s  string
		st Style
	}
	measure := func(ps []part) int {
		n := 0
		for _, p := range ps {
			n += width(secure.Text(p.s))
		}
		return n
	}
	view := m.Observer
	if view == "" {
		view = "all observers"
	}
	var left []part
	if m.Snapshot.Project != "" {
		left = append(left, part{"  ╱  ", on(cFaint)}, part{m.Snapshot.Project, on(cText).bold()})
	}
	if !touch {
		left = append(left, part{"   view: ", on(cMuted)}, part{view, on(cAccent2)})
	}
	var right []part
	add := func(s string, c Color) { right = append(right, part{s, on(c)}) }
	if m.Edit != nil {
		add("✎ LOCAL DRAFT ", cEdit)
		add(m.Edit.Path, cMuted)
		if m.Edit.Dirty {
			add("  ● unsaved", cWarn)
		} else {
			add("  ✓ saved", cUp)
		}
	} else {
		if m.Direct {
			add(m.view().DirectConnection(), cText)
			if m.Snapshot.Direct != nil {
				add(" / "+m.Snapshot.Direct.Profile, cMuted)
			}
		} else if m.Preview != nil {
			add("◆ PREVIEW ", cEdit)
			if !touch {
				add(m.scenario(), cText)
			}
		} else if !touch {
			if m.Snapshot.Instance != "" {
				add(m.Snapshot.Instance, cText)
				add(" @ ", cFaint)
			}
			add(m.Server, cMuted)
		}
		if !m.Snapshot.GeneratedAt.IsZero() {
			add("   "+age(m.Now, m.Snapshot.GeneratedAt), cFaint)
		}
		add("  ", cFaint)
		switch {
		case m.Loading != "":
			add(m.spinner()+" LOADING", cAccent2)
		case m.waiting():
			add("◌ OFFLINE", cWarn)
		case m.Cached():
			add("◌ CACHED", cWarn)
		default:
			// The live dot breathes with each repaint, never faster than data.
			add("● LIVE", Mix(cUp, cGlow, .5*float64(m.Now.Second()%2)))
		}
	}
	// Without room for both sides, the right keeps only its state, and
	// the left gives way to it, ending in an ellipsis.
	const brand = 12 // "◉ TOPOPHANY" from column 1
	if 1+brand+measure(left)+2+measure(right) > w-1 {
		right = right[len(right)-1:]
	}
	rx := x0 + w - 1 - measure(right)
	x := x0 + 1
	cv.Set(x, y, '◉', on(cAccent).bold())
	x += 2
	for i, r := range "TOPOPHANY" {
		cv.Set(x+i, y, r, on(Mix(cAccent, cAccent2, float64(i)/8)).bold())
	}
	x += 9
	for _, p := range left {
		room := rx - 2 - x
		if room <= 0 {
			break
		}
		x += cv.Text(x, y, ellipsis(p.s, room), p.st, room)
	}
	for _, p := range right {
		rx += cv.Text(rx, y, p.s, p.st, -1)
	}
}

// badge is what a tab counts beside its name: the entries in it that need a
// look, red when any is down and amber otherwise; 0 when none do. A cached
// view, a draft or a view still loading claims nothing.
func (m *Model) badge(tab int) (int, Color) {
	if m.Cached() || m.Edit != nil || m.Loading != "" {
		return 0, 0
	}
	n, down := m.view().Badge(tab)
	if down {
		return n, cDown
	}
	return n, cWarn
}

// spinner is the busy indicator as it stands at the moment drawn; the run
// loop repaints often enough to turn it while something is loading.
func (m *Model) spinner() string {
	frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	n := int64(len(frames))
	return string(frames[(m.Now.UnixMilli()/90%n+n)%n])
}

// tabBar draws numbered tabs, each with the badge of what in it needs a
// look. When they do not fit, the gaps close, then a badge only tints its
// tab's number, then inactive tabs collapse to their number. Under NO_COLOR
// the active tab is bracketed.
func (m *Model) tabBar(cv *Canvas, x0, y, w int) {
	type tab struct {
		name  string
		count int
		c     Color
	}
	ts := make([]tab, len(tabs))
	for i, name := range tabs {
		n, c := m.badge(i)
		ts[i] = tab{name, n, c}
	}
	// need is the width of the strip with or without counts and names.
	need := func(counts, short bool) int {
		total := 0
		for i, t := range ts {
			label := itoa(i + 1)
			if !short || i == m.Tab {
				label += " " + t.name
			}
			if counts && t.count > 0 {
				label += " " + itoa(t.count)
			}
			total += width(label) + 3
		}
		return total
	}
	gap, counts, short := 1, true, false
	switch room := w - 1; {
	case need(true, false) <= room:
	case need(true, false)-len(tabs) <= room:
		gap = 0
	case need(false, false) <= room:
		counts = false
	case need(false, false)-len(tabs) <= room:
		gap, counts = 0, false
	default:
		counts, short = false, true
	}
	x := x0 + 1
	for i, t := range ts {
		num, name := itoa(i+1), t.name
		if short && i != m.Tab {
			name = ""
		}
		active := i == m.Tab
		start := x
		if m.NoColor {
			text := " " + strings.TrimSpace(num+" "+name) + " "
			if active {
				text = "[" + num + " " + name + "]"
			}
			if t.count > 0 && counts {
				text = strings.TrimSuffix(text, " ") + " " + itoa(t.count) + " "
			}
			x += cv.Text(x, y, text, Style{}, -1)
		} else {
			ns, ls, pad := fg(cFaint), fg(cMuted), Style{}
			bs := fg(t.c).bold()
			if t.count > 0 && !counts {
				ns = fg(t.c).bold() // no room to count: the number says it
			}
			if active {
				pad = Style{BG: cAccent}
				ns = fg(Mix(cInk, cAccent, .5)).Bg(cAccent).bold()
				ls = fg(cInk).Bg(cAccent).bold()
				bs = fg(Mix(t.c, cInk, .45)).Bg(cAccent).bold()
			}
			x += cv.Text(x, y, " ", pad, -1)
			x += cv.Text(x, y, num, ns, -1)
			if name != "" {
				x += cv.Text(x, y, " "+name, ls, -1)
			}
			if t.count > 0 && counts {
				x += cv.Text(x, y, " ", pad, -1)
				x += cv.Text(x, y, itoa(t.count), bs, -1)
			}
			x += cv.Text(x, y, " ", pad, -1)
		}
		m.hit(start, y, x-start, "tab:"+itoa(i))
		x += gap
		if x >= x0+w {
			break
		}
	}
}

func (m *Model) counts() (up, down, warn, unk int) {
	c := m.view().Counts()
	return c.Up, c.Down, c.Warn, c.Unknown
}

func (m *Model) liveObservers() int {
	live, _ := m.view().Live()
	return live
}

// healthBar draws a proportional up/warn/down/unknown bar; every non-zero
// share keeps at least one cell so a single failure is never rounded away.
func (m *Model) healthBar(cv *Canvas, x, y, w int, up, warn, down, unk int, glyph rune) {
	total := up + warn + down + unk
	if w <= 0 {
		return
	}
	if total == 0 {
		for i := 0; i < w; i++ {
			cv.Set(x+i, y, glyph, fg(cLine))
		}
		return
	}
	segs := []struct {
		n int
		c Color
	}{{up, cUp}, {warn, cWarn}, {down, cDown}, {unk, cFaint}}
	cells := make([]int, len(segs))
	used := 0
	for i, s := range segs {
		if s.n > 0 {
			cells[i] = max(1, s.n*w/total)
			used += cells[i]
		}
	}
	// Trim or pad the largest share so the bar is exactly w cells.
	big := 0
	for i := range cells {
		if cells[i] > cells[big] {
			big = i
		}
	}
	cells[big] += w - used
	pos := 0
	for i, s := range segs {
		for k := 0; k < cells[i] && pos < w; k++ {
			cv.Set(x+pos, y, glyph, fg(s.c))
			pos++
		}
	}
}

// A notice shows for noticeFor, fading out over the last noticeFade.
const (
	noticeFor  = 8 * time.Second
	noticeFade = 1500 * time.Millisecond
)

// hasNews is a notice still on show, an error, or a request in flight.
func (m *Model) hasNews() bool {
	return m.notice != "" && m.Now.Sub(m.noticeAt) < noticeFor || m.Error != "" || m.Loading != ""
}

// statusBar is the one-line health summary under the tabs, or the notice,
// error or request in flight that has the line instead.
func (m *Model) statusBar(cv *Canvas, x0, y, w int) {
	x, end := x0+1, x0+w
	if m.notice != "" && m.Now.Sub(m.noticeAt) < noticeFor {
		c, g := cUp, "✓ "
		if m.noticeErr {
			c, g = cDown, "✖ "
		}
		if left := noticeFor - m.Now.Sub(m.noticeAt); left < noticeFade {
			c = Mix(cInk, c, float64(left)/float64(noticeFade))
		}
		cv.Text(x, y, g+m.notice, fg(c), w-2)
		return
	}
	if m.Error != "" {
		cv.Text(x, y, "⚠ CACHED / READ ERROR: "+m.Error, fg(cWarn).bold(), w-2)
		return
	}
	if m.Loading != "" {
		cv.Text(x, y, m.spinner()+" "+m.Loading+"…", fg(cAccent2), w-2)
		return
	}
	if m.Edit != nil {
		// A draft has no evidence to count; say what it declares instead.
		c := m.Edit.Config
		x += cv.Text(x, y, "DRAFT", fg(cFaint).bold(), -1) + 3
		for i, part := range []string{
			plural(len(c.Nodes), "node"), plural(len(c.Links), "link"), plural(len(c.Endpoints), "endpoint"),
			plural(len(c.Services), "service"), plural(len(c.Observers), "observer"), plural(len(c.Paths), "path"), plural(len(c.Checks), "check"),
		} {
			if x+5+width(part) > end-1 {
				break
			}
			if i > 0 {
				x += cv.Text(x, y, "  ·  ", fg(cFaint), -1)
			}
			n, word, _ := strings.Cut(part, " ")
			x += cv.Text(x, y, n, fg(cText).bold(), -1)
			x += cv.Text(x, y, " "+word, fg(cMuted), max(0, end-1-x))
		}
		return
	}
	if m.Snapshot.Direct != nil {
		status, _ := m.view().SourcesStatus()
		text := viewpkg.DirectConnection(status) + " · " + status.State + " · " + itoa(status.Counts.Current) + "/" + itoa(status.Counts.Selected) + " " + viewpkg.DirectText("counts")
		cv.Text(x, y, text, fg(cText), w-2)
		return
	}
	up, down, warn, unk := m.counts()
	x += cv.Text(x, y, "CHECKS", fg(cFaint).bold(), -1) + 2
	if bw := min(24, w/9); bw >= 6 && !m.NoColor {
		m.healthBar(cv, x, y, bw, up, warn, down, unk, '━')
		x += bw + 3
	}
	// Zero counts recede; anything that needs attention is lit and bold.
	count := func(g rune, c Color, n int, word string) {
		if x >= end-6 {
			return
		}
		gs, ns := fg(cFaint), fg(cFaint)
		if n > 0 {
			gs, ns = fg(c), fg(c).bold()
			if c == cUp || c == cUnknown {
				ns = fg(cText).bold()
			}
		}
		cv.Set(x, y, g, gs)
		x += 2
		x += cv.Text(x, y, itoa(n), ns, -1)
		x += cv.Text(x, y, " "+word+"   ", fg(cMuted), -1)
	}
	count('●', cUp, up, "up")
	count('▲', cWarn, warn, "warning")
	count('✖', cDown, down, "down")
	count('○', cUnknown, unk, "unknown")
	// Handshake failures are about delivery into the collector, not about any
	// target, so they sit beside the counts rather than among them.
	if x < end-24 && m.Snapshot.Ingress != nil && m.Snapshot.Ingress.Failures > 0 {
		x += cv.Text(x, y, "│   ", fg(cLine), -1)
		cv.Set(x, y, '⚠', fg(cWarn))
		x += 2
		x += cv.Text(x, y, itoa(m.Snapshot.Ingress.Failures), fg(cWarn).bold(), -1)
		label := " ingress /" + viewpkg.Duration(time.Duration(m.Snapshot.Ingress.WindowSeconds)*time.Second) + " TLS"
		if x+width(label) > end-1 {
			label = ""
		}
		x += cv.Text(x, y, label, fg(cMuted), end-x-1)
	}
	if x < end-16 {
		x += cv.Text(x, y, "│   ", fg(cLine), -1)
		count('⚑', cDown, len(m.Snapshot.Incidents), map[bool]string{true: "incident", false: "incidents"}[len(m.Snapshot.Incidents) == 1])
	}
	if x < end-20 {
		x += cv.Text(x, y, "│   ", fg(cLine), -1)
		live, all := m.liveObservers(), len(m.Snapshot.Observers)
		c := cAccent2
		if live < all {
			c = cWarn
		}
		cv.Set(x, y, '◎', fg(c))
		x += 2
		x += cv.Text(x, y, itoa(live)+"/"+itoa(all), fg(cText).bold(), -1)
		label := " observers live"
		if x+width(label) > end-1 {
			label = " live" // a word, not most of two
		}
		cv.Text(x, y, label, fg(cMuted), end-x-1)
	}
}

// panel draws a rounded frame with a title and returns the inner rectangle.
func (m *Model) panel(cv *Canvas, x, y, w, h int, title, tag string, active bool) (int, int, int, int) {
	border, ts := fg(cLine), fg(cMuted).bold()
	if active {
		border, ts = fg(Mix(cLine, cAccent, .35)), fg(cAccent).bold()
	}
	cv.Box(x, y, w, h, border)
	if title != "" && w > 6 {
		n := cv.Text(x+2, y, " "+title+" ", ts, w-4)
		if tag != "" && x+2+n+width(tag)+3 < x+w {
			cv.Text(x+2+n, y, tag+" ", fg(cFaint), w-4-n)
		}
	}
	return x + 2, y + 1, max(0, w-4), max(0, h-2)
}

type keycap struct{ key, label, action string }

// An action is something the viewer can do from where it stands: a key on
// the footer, a control on a touch screen. When there is no room for all
// of them the highest rank gives way first, a whole rank at a time.
type action struct {
	keycap
	rank int
}

// actions are what the viewer can do from the current tab and selection,
// in reading order, when neither a modal nor the filter has the keys; the
// footer shows them as keycaps and a touch screen as controls, so every
// screen offers the same ones. Details, Filter and Quit rank first, and
// the editor hints of a read-only client give way together.
func (m *Model) actions() []action {
	var opts []action
	add := func(rank int, key, label, act string) {
		opts = append(opts, action{keycap{key, label, act}, rank})
	}
	if m.Edit != nil {
		add(0, "a", "Add", "a")
		add(0, "e", "Edit", "e")
		add(0, "d", "Delete", "d")
		add(0, "^S", "Save", "ctrl-s")
		add(0, "u", "Undo", "u")
	}
	if m.Detail && m.Tab != tabAbout {
		add(1, "↵", "Back", "enter")
		add(1, "↑↓", "Scroll", "")
	} else {
		if r, ok := m.selectedRow(); ok && r.Kind == "group" {
			add(1, "↵", "Fold", "enter")
		} else {
			add(1, "↵", "Details", "enter")
		}
	}
	if m.Tab == tabServers && !m.Detail {
		if m.Uniform {
			add(2, "s", "Full cards", "s")
		} else {
			add(2, "s", "Uniform", "s")
		}
	}
	if m.Tab == tabServices && !m.Detail {
		if m.ServiceCards {
			add(2, "s", "List", "s")
		} else {
			add(2, "s", "Cards", "s")
		}
	}
	if m.businessTab() && !m.Detail && len(m.Snapshot.Topology.Applications) > 0 {
		add(1, "b", "Browse", "b")
		if r, ok := m.selectedRow(); ok && r.Kind == "group" {
			add(1, "Space", "Fold", " ")
			add(2, "i", "Evidence", "i")
		}
		if m.Tab == tabServices {
			add(2, "c", "Checks", "c")
		}
	}
	add(1, "/", "Filter", "/")
	add(2, "v", "View", "v")
	if m.Edit == nil {
		add(3, "r", "Refresh", "r")
	}
	if m.Preview != nil {
		add(2, "x", "Scenario", "x")
	}
	if m.mapShown() {
		add(3, "m", "Map", "m")
		add(4, "z", "Zoom", "z")
		if m.mapMode() == "geo" {
			add(2, "p", m.GeoLabels.next().name(), "p")
		}
		if m.FullMap {
			add(1, "f", "Panels", "f")
		} else {
			add(3, "f", "Full map", "f")
		}
	}
	if m.Edit == nil {
		add(5, "a", "Add", "a")
		add(5, "e", "Edit", "e")
		add(5, "d", "Delete", "d")
	}
	if m.Preview != nil {
		add(6, "D", "Debug", "D")
	}
	add(4, ",", "Settings", ",")
	add(3, "?", "About", "?")
	add(0, "q", "Quit", "q")
	return opts
}

// fitActions drops the highest rank, a whole rank at a time, until what
// is left measures no more than room with need; rank 0 always stays.
func fitActions(opts []action, room int, need func(action) int) []action {
	for {
		total, worst := 0, 0
		for _, o := range opts {
			total += need(o)
			worst = max(worst, o.rank)
		}
		if total <= room || worst == 0 {
			return opts
		}
		kept := opts[:0:0]
		for _, o := range opts {
			if o.rank != worst {
				kept = append(kept, o)
			}
		}
		opts = kept
	}
}

// footer is the key bar: the keys that mean something where the viewer
// stands, as many as fit.
func (m *Model) footer(cv *Canvas, x0, y, w int) {
	bar := Style{BG: cInkHi}
	cv.Fill(x0, y, w, 1, bar)
	x, end := x0+1, x0+w
	if m.Searching {
		x += cv.Text(x, y, "Filter ", bar.Fg(cAccent).bold(), -1)
		x += cv.Text(x, y, m.Search, bar.Fg(cGlow), end-x-20)
		cv.Text(x, y, "▏  Enter/Esc finish", bar.Fg(cMuted), -1)
		return
	}
	var keys []keycap
	if m.waiting() && m.modal == nil {
		keys = m.waitingKeys()
	} else if m.modal != nil {
		keys = m.modalKeys()
	} else {
		if m.Search != "" {
			x += cv.Text(x, y, "Filter “"+m.Search+"”  Esc clear   ", bar.Fg(cAccent), -1)
		}
		opts := fitActions(m.actions(), end-x+1, func(o action) int { return width(o.key) + width(o.label) + 5 })
		for _, o := range opts {
			keys = append(keys, o.keycap)
		}
	}
	for _, k := range keys {
		need := width(k.key) + width(k.label) + 4
		if x+need > end {
			break
		}
		start := x
		cap := " " + k.key + " "
		if m.NoColor {
			cap = "[" + k.key + "]"
		}
		x += cv.Text(x, y, cap, fg(cAccent).Bg(cKeyBG).bold(), -1)
		x += cv.Text(x, y, " "+k.label+"  ", bar.Fg(cMuted), -1)
		if k.action != "" {
			m.hit(start, y, x-start, "key:"+k.action)
		}
	}
}

// waitingKeys are the only keys that mean anything before any evidence.
func (m *Model) waitingKeys() []keycap {
	var keys []keycap
	if m.Error != "" {
		keys = append(keys, keycap{"r", "Retry now", "r"})
	}
	if m.Preview != nil {
		keys = append(keys, keycap{"x", "Scenario", "x"})
	}
	return append(keys, keycap{",", "Settings", ","}, keycap{"?", "About", "?"}, keycap{"q", "Quit", "q"})
}

// modalKeys are the keys of the open modal.
func (m *Model) modalKeys() []keycap {
	switch m.modal.kind {
	case "preferences":
		return []keycap{{"Tab/↑↓", "move", ""}, {"←→", "change", ""}, {"Enter", "select", "enter"}, {"Esc", "cancel", "esc"}}
	case "form":
		return []keycap{{"Tab/↓", "next", ""}, {"↑", "prev", ""}, {"←→", "choose", ""}, {"Space", "toggle", ""}, {"^S", "apply", "btn:save"}, {"Esc", "cancel", "btn:cancel"}}
	case "menu":
		return []keycap{{"↑↓", "select", ""}, {"Enter", "choose", ""}, {"Esc", "close", "esc"}}
	}
	return []keycap{{"Enter", "ok", "enter"}, {"Esc", "close", "esc"}}
}

// --- modals ------------------------------------------------------------------

// modalBox draws a modal's box, centred on the stage, and returns its
// inner rectangle.
func (m *Model) modalBox(cv *Canvas, w, h int, title string) (int, int, int, int) {
	s := m.stageOn(cv)
	w = min(w, s.w-2)
	h = min(h, s.h-2)
	x := s.x + (s.w-w)/2
	y := max(s.y+1, s.y+(s.h-h)/2)
	if !m.Transparent && !m.NoColor {
		// A shadow, two columns to the right and one row below, lifts
		// the panel off the dimmed view.
		shade := func(s Style) Style {
			if s.BG == 0 {
				s.BG = cv.Base
			}
			if s.FG == 0 {
				s.FG = cText
			}
			s.BG, s.FG = Mix(s.BG, Hex(0), .55), Mix(s.FG, Hex(0), .55)
			return s
		}
		cv.Tint(x+w, y+1, 2, h-1, shade)
		cv.Tint(x+2, y+h, w, 1, shade)
	}
	bg := Style{BG: cPanelBG}
	cv.Fill(x, y, w, h, bg)
	cv.Box(x, y, w, h, fg(Mix(cLine, cAccent, .6)).Bg(cPanelBG))
	cv.Text(x+2, y, " "+title+" ", fg(cInk).Bg(cAccent).bold(), w-4)
	return x + 2, y + 1, w - 4, h - 2
}

func (m *Model) button(cv *Canvas, x, y int, label, action string, focused bool) int {
	st := fg(cText).Bg(cKeyBG)
	if focused {
		st = fg(cInk).Bg(cAccent).bold()
	}
	text := "[ " + label + " ]"
	if !m.NoColor {
		text = "  " + label + "  "
	}
	n := cv.Text(x, y, text, st, -1)
	m.hit(x, y, n, action)
	return n
}
