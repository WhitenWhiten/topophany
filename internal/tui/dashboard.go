package tui

import (
	"sort"

	"github.com/WhitenWhiten/topophany/internal/view"
)

// The Overview answers "is anything wrong, and where?" before anything is
// read: a map of the network, a status card whose headline and big number
// carry the verdict, the list of what needs attention (selecting an item
// lights up its route on the map), a service matrix and the observers.

// bigGlyphs is a 3x5 pixel font drawn with half blocks, three rows tall.
var bigGlyphs = map[rune][5]string{
	'0': {"###", "#.#", "#.#", "#.#", "###"},
	'1': {".#.", "##.", ".#.", ".#.", "###"},
	'2': {"###", "..#", "###", "#..", "###"},
	'3': {"###", "..#", ".##", "..#", "###"},
	'4': {"#.#", "#.#", "###", "..#", "..#"},
	'5': {"###", "#..", "###", "..#", "###"},
	'6': {"###", "#..", "###", "#.#", "###"},
	'7': {"###", "..#", ".#.", ".#.", ".#."},
	'8': {"###", "#.#", "###", "#.#", "###"},
	'9': {"###", "#.#", "###", "..#", "###"},
	'/': {"..#", "..#", ".#.", "#..", "#.."},
	'-': {"...", "...", "###", "...", "..."},
}

// bigText draws s three cells tall and returns its width, lit from above:
// each of the five pixel rows a shade of st's color, from a glow at the
// top to a touch of shadow at the foot. Under NO_COLOR, or for characters
// the font lacks, it falls back to plain text.
func (m *Model) bigText(cv *Canvas, x, y int, s string, st Style) int {
	if m.NoColor {
		return cv.Text(x, y+1, s, st, -1)
	}
	c := st.FG
	shade := [5]Color{Mix(c, cGlow, .4), Mix(c, cGlow, .18), c, Mix(c, cInk, .14), Mix(c, cInk, .28)}
	cx := x
	for _, r := range s {
		g, ok := bigGlyphs[r]
		if !ok {
			cx += cv.Text(cx, y+2, string(r), st, -1)
			continue
		}
		for row := 0; row < 3; row++ {
			for col := 0; col < 3; col++ {
				top := g[row*2][col] == '#'
				bottom := row*2+1 < 5 && g[row*2+1][col] == '#'
				switch {
				case top && bottom:
					cv.Set(cx+col, y+row, '▀', st.Fg(shade[row*2]).Bg(shade[row*2+1]))
				case top:
					cv.Set(cx+col, y+row, '▀', st.Fg(shade[row*2]))
				case bottom:
					cv.Set(cx+col, y+row, '▄', st.Fg(shade[row*2+1]))
				}
			}
		}
		cx += 4
	}
	return cx - x - 1
}

// verdict is the one-line answer at the top of the Overview.
func (m *Model) verdict() (string, Color) {
	if m.Edit == nil && m.Loading != "" {
		if m.Snapshot.GeneratedAt.IsZero() && len(m.Snapshot.Topology.Nodes) == 0 {
			return m.spinner() + " CONNECTING", cAccent2
		}
		return m.spinner() + " LOADING", cAccent2
	}
	if m.waiting() {
		return "◌ OFFLINE", cWarn
	}
	v := m.view().Verdict()
	return v.Glyph + " " + v.Text, verdictColors[v.State]
}

var verdictColors = map[string]Color{"draft": cEdit, "cached": cWarn, "no-checks": cUnknown, "incidents": cDown, "down": cDown, "degraded": cWarn, "no-evidence": cUnknown, "incomplete": cWarn, "clear": cUp}

func (m *Model) statusCard(cv *Canvas, x, y, w, h int) {
	up, down, warn, unk := m.counts()
	total := up + down + warn + unk
	word, c := m.verdict()
	cv.Text(x, y, word, fg(c).bold(), w)
	when := "updated " + age(m.Now, m.Snapshot.GeneratedAt)
	switch {
	case m.Edit != nil:
		when = "no live evidence"
	case m.Loading != "":
		when = "waiting for data"
	}
	if width(word)+width(when)+2 <= w {
		cv.Text(x+w-width(when), y, when, fg(cFaint), -1)
	}
	row := y + 1
	if h >= 8 {
		// The big number is the failures when there are any, otherwise
		// what passes.
		n, label, nc := up, "checks passing", cUp
		sub := "of " + itoa(total) + " configured"
		switch {
		case total == 0 && m.Loading != "":
			n, label, nc, sub = 0, "checks known", cUnknown, m.Loading+"…"
		case total == 0 && m.Cached():
			n, label, nc, sub = 0, "checks known", cUnknown, "no snapshot received yet"
		case total == 0:
			n, label, nc, sub = 0, "checks in this view", cUnknown, "nothing is measured from here"
		case m.Cached() || m.Edit != nil:
			n, label, nc = unk, "checks without current evidence", cUnknown
		case down > 0:
			n, label, nc = down, "checks failing", cDown
		case warn > 0:
			n, label, nc = warn, "checks warning", cWarn
		}
		big := itoa(n)
		if m.Loading != "" && total == 0 {
			big = "-" // not zero: not known yet
		}
		bw := m.bigText(cv, x, row+1, big, fg(nc).bold())
		lx := x + bw + 3
		if label == "checks without current evidence" && width(label) > x+w-lx {
			label = "checks not current"
		}
		cv.Text(lx, row+1, label, fg(cText), x+w-lx)
		cv.Text(lx, row+2, sub, fg(cMuted), x+w-lx)
		if m.Snapshot.Direct == nil {
			cv.Text(x, row+4, m.view().Coverage().Line(), fg(cMuted), w)
		}
		row += 5
	}
	if row < y+h {
		if m.NoColor {
			row--
		} else {
			m.healthBar(cv, x, row, w, up, warn, down, unk, '━')
		}
		row++
	}
	if row < y+h {
		cx := x
		// The counts close up rather than lose a word when the card is
		// narrow.
		gap := "   "
		if need := 29 + width(itoa(up)+itoa(warn)+itoa(down)+itoa(unk)); need+9 > w {
			gap = map[bool]string{true: "  ", false: " "}[need+6 <= w]
		}
		count := func(g rune, gc Color, n int, word string) {
			st := fg(cFaint)
			if n > 0 {
				st = fg(gc)
			}
			cv.Set(cx, row, g, st)
			cx += 2
			ns := fg(cFaint)
			if n > 0 {
				ns = fg(cText).bold()
			}
			cx += cv.Text(cx, row, itoa(n), ns, -1)
			cx += cv.Text(cx, row, " "+word+gap, fg(cMuted), max(0, x+w-cx))
		}
		count('●', cUp, up, "up")
		count('▲', cWarn, warn, "warn")
		count('✖', cDown, down, "down")
		count('○', cUnknown, unk, "unknown")
		row++
	}
	if row < y+h {
		inc := len(m.Snapshot.Incidents)
		cx := x
		ic := cFaint
		if inc > 0 {
			ic = cDown
		}
		cv.Set(cx, row, '⚑', fg(ic))
		cx += 2
		cx += cv.Text(cx, row, plural(inc, "incident"), fg(cMuted), -1) + 4
		live, all := m.liveObservers(), len(m.Snapshot.Observers)
		oc := cAccent2
		if live < all {
			oc = cWarn
		}
		cv.Set(cx, row, '◎', fg(oc))
		cx += 2
		cv.Text(cx, row, itoa(live)+"/"+itoa(all)+" observers live", fg(cMuted), max(0, x+w-cx))
	}
}

// servicesCard shows every service, problems first: where the topology
// declares applications, a line for each application with a mark for each
// of its services; otherwise a matrix of the services' names.
func (m *Model) servicesCard(cv *Canvas, x, y, w, h int) {
	rows := m.tabRows(tabServices)
	sort.SliceStable(rows, func(i, j int) bool { return serviceRank(rows[i]) < serviceRank(rows[j]) })
	if len(rows) == 0 {
		cv.Text(x, y, m.nothing("services"), fg(cMuted).italic(), w)
		return
	}
	if apps := m.serviceApps(rows); len(apps) > 0 {
		m.applicationsCard(cv, x, y, w, h, apps)
		return
	}
	// As few columns as the height needs (no more than fit), filled evenly.
	fits := serviceCols(rows, w)
	cols := max(1, min(fits, (len(rows)+h-1)/max(1, h)))
	per := max(1, min(h, (len(rows)+cols-1)/cols))
	cellW := w / cols
	for i, r := range rows {
		cx, cy := x+(i/per)*cellW, y+i%per
		if i/per >= cols {
			break
		}
		if i%per == per-1 && i/per == cols-1 && i < len(rows)-1 {
			more := "+" + itoa(len(rows)-i) + " more"
			cv.Text(cx, cy, more, fg(cFaint), -1)
			break
		}
		g, gc := m.serviceMark(r)
		ts := fg(cMuted)
		if m.needsAttention(r) {
			ts = fg(cText)
		}
		// A name with no cell to its right may run on to the edge.
		room := cellW - 3
		if i+per >= len(rows) {
			room = x + w - cx - 2
		}
		cv.Set(cx, cy, g, fg(gc))
		n := cv.Text(cx+2, cy, ellipsis(r.Title, room), ts, room)
		m.hit(cx, cy, n+2, "goto:service:"+r.ID)
	}
}

// serviceMark is a service's mark in its state: an affected one wears the
// warning triangle, and a cached view knows no state.
func (m *Model) serviceMark(r Row) (rune, Color) {
	h, note := r.Health, r.Note
	if m.Cached() {
		h, note = "unknown", ""
	}
	if note == "affected" {
		return '▲', cWarn
	}
	return healthGlyph(h), healthColor(h)
}

// needsAttention reports whether a service is down, degraded or affected,
// as the view currently knows.
func (m *Model) needsAttention(r Row) bool {
	return !m.Cached() && (r.Health == "down" || r.Health == "degraded" || r.Note == "affected")
}

// appLine is an application on the Overview: its services, problems first.
type appLine struct {
	id, name string
	rows     []Row
}

// serviceApps groups rows, ranked problems first, by the application each
// service belongs to: applications with the most urgent service first, by
// name among equals, and the services of none last. Nil when the topology
// declares no applications.
func (m *Model) serviceApps(rows []Row) []appLine {
	c := m.Snapshot.Topology
	if len(c.Applications) == 0 {
		return nil
	}
	at := map[string]int{}
	apps := []appLine{}
	for _, r := range rows {
		id := view.EntityScope(c, "service", r.ID).Application
		i, ok := at[id]
		if !ok {
			i = len(apps)
			at[id] = i
			apps = append(apps, appLine{id: id, name: view.ApplicationName(c, id)})
		}
		apps[i].rows = append(apps[i].rows, r)
	}
	sort.SliceStable(apps, func(i, j int) bool {
		a, b := apps[i], apps[j]
		if (a.id == "") != (b.id == "") {
			return b.id == ""
		}
		if ra, rb := serviceRank(a.rows[0]), serviceRank(b.rows[0]); ra != rb {
			return ra < rb
		}
		return a.name < b.name
	})
	return apps
}

// applicationsCard draws a line for each application: the mark of its most
// urgent service, its name, a mark for each of its services and how many
// need attention. Where the card has room, an application lists its
// services under it instead, by name: those that need attention first,
// then the others while they fit. A click opens the application, or the
// service.
func (m *Model) applicationsCard(cv *Canvas, x, y, w, h int, apps []appLine) {
	nameW := 0
	for _, a := range apps {
		nameW = max(nameW, width(a.name))
	}
	nameW = min(nameW, max(8, w/3))
	attention := func(a appLine) (n, unknown int) {
		for _, r := range a.rows {
			if m.needsAttention(r) {
				n++
			} else if r.Health == "unknown" && !m.Cached() {
				unknown++
			}
		}
		return
	}
	open := make([]bool, len(apps))
	lines := len(apps)
	for _, a := range apps {
		lines += len(a.rows)
	}
	if lines <= h {
		for i := range open {
			open[i] = true
		}
	} else {
		// Those that need attention open first, then the others in turn,
		// each while its services fit.
		lines = len(apps)
		for _, first := range []bool{true, false} {
			for i, a := range apps {
				if n, _ := attention(a); !open[i] && (n > 0) == first && lines+len(a.rows) <= h {
					open[i], lines = true, lines+len(a.rows)
				}
			}
		}
	}
	cy := y
	for i, a := range apps {
		if cy >= y+h {
			break
		}
		if cy == y+h-1 && i < len(apps)-1 && !open[i] {
			cv.Text(x, cy, "+"+itoa(len(apps)-i)+" more", fg(cFaint), -1)
			break
		}
		n, unknown := attention(a)
		g, gc := m.serviceMark(a.rows[0])
		cv.Set(x, cy, g, fg(gc))
		ts := fg(cMuted)
		if n > 0 {
			ts = fg(cText).bold()
		} else if open[i] {
			ts = fg(cText)
		}
		cv.Text(x+2, cy, ellipsis(a.name, nameW), ts, nameW)
		note, nc := "", cFaint
		switch {
		case n > 0:
			note, nc = itoa(n)+" attention", cWarn
		case unknown > 0:
			note = itoa(unknown) + " unknown"
		}
		if !open[i] {
			// A mark for each service, as many as fit before the note.
			mx := x + 2 + nameW + 2
			room := x + w - mx - width(note) - 1
			shown := len(a.rows)
			if 2*shown-1 > room {
				shown = max(0, (room-3)/2)
			}
			for k := 0; k < shown; k++ {
				mg, mc := m.serviceMark(a.rows[k])
				cv.Set(mx+2*k, cy, mg, fg(mc))
			}
			if shown < len(a.rows) && room >= 2 {
				cv.Text(mx+2*shown, cy, "+"+itoa(len(a.rows)-shown), fg(cFaint), -1)
			}
		}
		if note != "" {
			cv.Text(x+w-width(note), cy, note, fg(nc), -1)
		}
		id := a.id
		if id == "" {
			id = "-" // the services of no application
		}
		m.hit(x, cy, w, "goto:application:"+id)
		cy++
		if !open[i] {
			continue
		}
		for _, r := range a.rows {
			if cy >= y+h {
				break
			}
			mg, mc := m.serviceMark(r)
			cv.Set(x+2, cy, mg, fg(mc))
			rs := fg(cMuted)
			if m.needsAttention(r) {
				rs = fg(cText)
			}
			k := cv.Text(x+4, cy, ellipsis(r.Title, w-4), rs, w-4)
			m.hit(x+2, cy, k+2, "goto:service:"+r.ID)
			cy++
		}
	}
}

// servicesTag is the SERVICES card's count: of services, and of the
// applications they are grouped in where there are any.
func (m *Model) servicesTag() string {
	t := m.Snapshot.Topology
	if len(t.Applications) == 0 {
		return "· " + itoa(len(t.Services))
	}
	return "· " + itoa(len(t.Services)) + " in " + plural(len(t.Applications), "application")
}

// servicesNeed is how many lines the SERVICES card wants at its width:
// a line an application, or the names two to a line.
func (m *Model) servicesNeed() int {
	if apps := m.serviceApps(m.tabRows(tabServices)); len(apps) > 0 {
		return len(apps)
	}
	return (len(m.Snapshot.Topology.Services) + 1) / 2
}

// nothing is what an empty card says: nothing declared, or nothing known
// yet while the first snapshot is on its way.
func (m *Model) nothing(what string) string {
	if m.Loading != "" && len(m.Snapshot.Topology.Nodes) == 0 {
		return "Waiting for the topology…"
	}
	return "No " + what + " declared."
}

// serviceCols is how many columns of service names fit in w.
func serviceCols(rows []Row, w int) int {
	cellW := 0
	for _, r := range rows {
		cellW = max(cellW, width(r.Title)+4)
	}
	return max(1, min(w/max(1, min(cellW, 26)), w/12))
}

func serviceRank(r Row) int { return view.ServiceRank(r) }

// observersCard lists each vantage point with the age of its last evidence.
func (m *Model) observersCard(cv *Canvas, x, y, w, h int) {
	rows := m.tabRows(tabObservers)
	if h <= 0 {
		return
	}
	if len(rows) == 0 {
		cv.Text(x, y, m.nothing("observers"), fg(cMuted).italic(), w)
		return
	}
	for i, r := range rows {
		if i >= h {
			break
		}
		if i == h-1 && len(rows) > h {
			cv.Text(x, y+i, "+"+itoa(len(rows)-i)+" more", fg(cFaint), -1)
			break
		}
		hl := r.Health
		if m.Cached() {
			hl = "unknown"
		}
		g, gc := healthGlyph(hl), healthColor(hl)
		if r.Note == "outdated" {
			g, gc = '▲', cWarn // alive, but measuring an outdated topology
		}
		cv.Set(x, y+i, g, fg(gc))
		last := ""
		if len(r.Cols) > 1 {
			last = r.Cols[1]
		}
		lw := width(last)
		n := cv.Text(x+2, y+i, r.Title, fg(cText), w-lw-4)
		cv.Text(x+w-lw, y+i, last, fg(cFaint), -1)
		m.hit(x, y+i, n+2, "goto:observer:"+r.ID)
	}
}

// overview lays the dashboard out for the space it has: a map beside a
// sidebar when wide, the map over the cards when medium, cards alone when
// small.
// attentionCard lists what needs attention in the panel's inner rectangle
// with list, or says why nothing does: all clear, cached, a draft, or no
// evidence yet.
func (m *Model) attentionCard(cv *Canvas, x, y, w, h int, rows []Row, list func(cv *Canvas, x, y, w, h int, rows []Row)) {
	if len(rows) > 0 || m.Search != "" {
		list(cv, x, y, w, h, rows)
		return
	}
	state, msg, sub := m.view().Calm()
	c := map[string]Color{"draft": cEdit, "cached": cWarn, "no-checks": cUnknown, "clear": cUp}[state]
	if m.Edit == nil && m.Loading != "" {
		msg, sub, c = m.spinner()+" Waiting for evidence", "Nothing current is known yet; this fills in as soon as a snapshot arrives.", cAccent2
	}
	cv.Text(x, y, msg, fg(c).bold(), w)
	for i, l := range wrap(sub, w) {
		if 1+i < h {
			cv.Text(x, y+1+i, l, fg(cMuted), w)
		}
	}
}

// mapTag is the network map panel's tag: what the topology declares.
func (m *Model) mapTag() string {
	t := m.Snapshot.Topology
	return "· " + plural(len(t.Nodes), "node") + " · " + plural(len(t.Links), "link")
}

func (m *Model) overview(cv *Canvas, x, y, w, h int, rows []Row, sel Row, tag string) {
	t := m.Snapshot.Topology
	mapTitle := "NETWORK"
	mapTag := m.mapTag() + " · m z"
	attention := func(x, y, w, h int, active bool) {
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, "NEEDS ATTENTION", tag, active)
		m.attentionCard(cv, ix, iy, iw, ih, rows, m.drawList)
	}
	nObservers := len(m.Snapshot.Observers)
	switch {
	case w >= 140 && h >= 24:
		sw := min(max(w*36/100, 46), 74)
		mw := w - sw
		// The map gives rows it does not need to what needs attention;
		// when nothing does, it keeps them.
		mh := h
		if need := m.mapRows(mw - 2); need > 0 && h-need-2 >= 9 && (len(rows) > 0 || m.Search != "") {
			mh = need + 2
		}
		ix, iy, iw, ih := m.panel(cv, x, y, mw, mh, mapTitle, mapTag, false)
		m.mapIn(cv, ix-1, iy, iw+2, ih, sel)
		sh := 11
		if h < 34 {
			sh = 6
		}
		ix, iy, iw, ih = m.panel(cv, x+mw, y, sw, sh, "STATUS", "", false)
		m.statusCard(cv, ix, iy, iw, ih)
		left := h - sh
		oh := max(1, min(nObservers, 6)) + 2
		if mh < h {
			// The map letterboxes: attention goes under it, the sidebar
			// keeps services and observers.
			attention(x, y+mh, mw, h-mh, true)
			vh := left - oh
			ix, iy, iw, ih = m.panel(cv, x+mw, y+sh, sw, vh, "SERVICES", m.servicesTag(), false)
			m.servicesCard(cv, ix, iy, iw, ih)
			ix, iy, iw, ih = m.panel(cv, x+mw, y+sh+vh, sw, oh, "OBSERVERS", "", false)
			m.observersCard(cv, ix, iy, iw, ih)
			return
		}
		vh := min(m.servicesNeed()+2, max(4, left/3))
		switch calm := len(rows) == 0 && m.Search == ""; {
		case calm && len(t.Applications) > 0:
			// Nothing to attend to: a short all-clear, every observer that
			// fits beside a line for each application, and the rest to the
			// services, which list their names where they fit.
			oh = min(nObservers+2, max(0, left-5-m.servicesNeed()-2))
			vh = left - 5 - oh
		case calm:
			// Nothing to attend to: a short all-clear, and the room goes
			// to the service matrix, as much as it can fill.
			svc := m.tabRows(tabServices)
			fits := serviceCols(svc, sw-4)
			vh = max(vh, min(left-oh-5, (len(svc)+fits-1)/fits+2))
		}
		if left-oh-vh < 5 {
			oh = 0
		}
		ah := left - vh - oh
		attention(x+mw, y+sh, sw, ah, true)
		ix, iy, iw, ih = m.panel(cv, x+mw, y+sh+ah, sw, vh, "SERVICES", m.servicesTag(), false)
		m.servicesCard(cv, ix, iy, iw, ih)
		if oh > 0 {
			ix, iy, iw, ih = m.panel(cv, x+mw, y+sh+ah+vh, sw, oh, "OBSERVERS", "", false)
			m.observersCard(cv, ix, iy, iw, ih)
		}
	case w >= 90 && h >= 22:
		mh := h * 52 / 100
		if rows := m.mapRows(w - 2); rows > 0 {
			mh = min(mh, rows+2)
		}
		ix, iy, iw, ih := m.panel(cv, x, y, w, mh, mapTitle, mapTag, false)
		m.mapIn(cv, ix-1, iy, iw+2, ih, sel)
		// Under the map: the status card, with services or observers
		// beneath it when there is room, beside what needs attention.
		sw := min(max(w*42/100, 44), 64)
		sh := min(h-mh, 11)
		ix, iy, iw, ih = m.panel(cv, x, y+mh, sw, sh, "STATUS", "", false)
		m.statusCard(cv, ix, iy, iw, ih)
		under := h - mh - sh
		if ah := 5; len(rows) == 0 && m.Search == "" && h-mh-ah >= 5 {
			attention(x+sw, y+mh, w-sw, ah, true)
			ix, iy, iw, ih = m.panel(cv, x+sw, y+mh+ah, w-sw, h-mh-ah, "SERVICES", m.servicesTag(), false)
			m.servicesCard(cv, ix, iy, iw, ih)
			if under >= 3 {
				ix, iy, iw, ih = m.panel(cv, x, y+mh+sh, sw, under, "OBSERVERS", "", false)
				m.observersCard(cv, ix, iy, iw, ih)
			}
			return
		}
		attention(x+sw, y+mh, w-sw, h-mh, true)
		if under >= 4 {
			ix, iy, iw, ih = m.panel(cv, x, y+mh+sh, sw, under, "SERVICES", m.servicesTag(), false)
			m.servicesCard(cv, ix, iy, iw, ih)
		}
	default:
		// Small: the status card with its big number when there is room,
		// the observers beside it when there is width, and what needs
		// attention under them; when nothing does, a short all-clear and
		// the service matrix.
		sh := min(6, h/2)
		if h >= 20 {
			sh = 11
		}
		sw := w
		if sh == 11 && w >= 76 && nObservers > 0 && m.Edit == nil {
			sw = min(max(w*55/100, 44), 64)
		}
		ix, iy, iw, ih := m.panel(cv, x, y, sw, sh, "STATUS", "", false)
		m.statusCard(cv, ix, iy, iw, ih)
		if sw < w {
			ix, iy, iw, ih = m.panel(cv, x+sw, y, w-sw, sh, "OBSERVERS", "", false)
			m.observersCard(cv, ix, iy, iw, ih)
		}
		if left := h - sh; len(rows) == 0 && m.Search == "" && left >= 9 {
			attention(x, y+sh, w, 4, true)
			ix, iy, iw, ih = m.panel(cv, x, y+sh+4, w, left-4, "SERVICES", m.servicesTag(), false)
			m.servicesCard(cv, ix, iy, iw, ih)
			return
		}
		attention(x, y+sh, w, h-sh, true)
	}
}
