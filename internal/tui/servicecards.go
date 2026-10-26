package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// The Services tab's cards (s): a card for each service, cut from the
// server cards' cloth, in a grid under each application's heading. Every
// card has a uniform card's fifteen rows and says the same things in the
// same places: its name and state over where it runs; who sees it from
// where, each observer with a mark for each of its checks, their recent
// samples, median latency and the day's share up, where a server's card
// charts its CPU, memory and disk; tallies of its checks, endpoints and
// dependencies; the address it is known by, with the edge in front and
// where that answered; and the first lines of its memo. Enter lays the
// selected card open, with everything else.

// serviceCards reports whether the Services tab shows its cards.
func (m *Model) serviceCards() bool { return m.Tab == tabServices && m.ServiceCards }

// serviceHead is a service card's header, on a band in the hue of where it
// runs, the hue its server's card has: its name and state over where it
// runs and the place. Trouble takes trouble's color.
func (m *Model) serviceHead(f fleet, c view.ServiceCard, r Row, selected bool) ([]cardRow, Color) {
	n := f.Nodes[c.Host.ID]
	hue := nodeHue(n, f.groups[n.Group])
	band := Mix(hue, cInk, .84)
	switch {
	case r.Health == "down":
		band = Mix(cDown, cInk, .84)
	case r.Health == "degraded" || r.Note == "affected":
		band = Mix(cWarn, cInk, .86)
	}
	if r.Assessment == nil {
		r.Assessment = &c.Assessment
	}
	pill, pillStyle := statePill(r)
	name := fg(cText).bold()
	if selected {
		name = fg(cGlow).bold()
	}
	tone := fg(Mix(hue, cText, .35))
	sub := []span{}
	if c.Runtime != "" {
		sub = append(sub, span{c.Runtime, tone}, span{" on ", fg(cMuted)})
	} else {
		sub = append(sub, span{"on ", fg(cMuted)})
	}
	sub = append(append(sub, m.logo(n.Provider)...), span{c.Host.Name, tone})
	if c.Environment != "" {
		sub = append(sub, span{" · " + c.Environment, fg(cMuted)})
	}
	where := []span{}
	switch {
	case c.Host.Where == "anycast":
		where = []span{{"◌ anycast", fg(cMuted)}}
	case c.Host.Where != "":
		where = []span{{"◎ " + c.Host.Where, fg(cMuted)}}
	}
	return []cardRow{
		{left: []span{{r.Title, name}}, right: []span{{pill, pillStyle}}, bg: band, bar: hue},
		{left: sub, right: where, bg: band, bar: hue},
	}, hue
}

// spark is a check's recent samples in n columns, newest at the right, on
// a well: a passing one a bar as tall as it took against the slowest
// shown, at most seven eighths of the row so rows stacked on a card stay
// apart; any other its strip glyph; a slot with no sample a dot. Dim when
// the check's state is not current.
func (m *Model) spark(x view.SeenCheck, n int, well Color) []span {
	slots := view.NewStrip(x.Check, m.trendSamples(x.ID), m.Now, n).Slots
	out := []span{}
	put := func(s string, st Style) {
		if k := len(out); k > 0 && out[k-1].st == st {
			out[k-1].s += s
			return
		}
		out = append(out, span{s, st})
	}
	for range n - len(slots) {
		put(" ", Style{BG: well})
	}
	slowest := int64(0)
	for _, sl := range slots {
		if sl.Outcome == "ok" {
			slowest = max(slowest, sl.ElapsedMS)
		}
	}
	tone := Mix(Mix(cAccent, cMuted, .32), well, .12)
	if x.Freshness != "fresh" {
		tone = Mix(tone, well, .45)
	}
	for _, sl := range slots {
		switch h := view.SlotHealth(sl.Outcome); {
		case sl.Outcome == "ok":
			i := 0
			if slowest > 0 {
				i = int(min(6, max(0, (sl.ElapsedMS*12+slowest)/(2*slowest))))
			}
			put(string(sparks[i]), fg(tone).Bg(well))
		case h == "":
			put("·", fg(cFaint).Bg(well))
		default:
			put(view.SlotGlyph(sl.Outcome), fg(healthColor(h)).Bg(well))
		}
	}
	return out
}

// latency writes a latency as "231 ms", or "—" without one.
func latency(l int64, ok bool) string {
	if !ok {
		return "—"
	}
	return itoa(int(l)) + " ms"
}

// upShare writes the day's share up, "97.6%", or "—" without one.
func upShare(v *float64) string {
	switch {
	case v == nil:
		return "—"
	case *v >= 100:
		return "100%"
	}
	return pct(*v, 1)
}

// p50 is a check's median latency over the day while its state is current.
func p50(x view.SeenCheck) (int64, bool) {
	if x.Freshness != "fresh" || x.Latency == nil {
		return 0, false
	}
	return x.Latency.P50, true
}

// seenPanel is the four rows under a service card's header, where a server
// card charts its resources: who sees the service from where, the worst
// first, and how it fared there lately and over the day.
func (m *Model) seenPanel(c view.ServiceCard, w int) []cardRow {
	if len(c.SeenFrom) == 0 {
		if c.Elsewhere {
			return quietPanel("others check it", quiet("not seen from here"))
		}
		return quietPanel("no check measures it", quiet("not monitored"))
	}
	const place, num, up = 9, 6, 5
	marks := 1
	for _, s := range c.SeenFrom[:min(3, len(c.SeenFrom))] {
		marks = max(marks, min(3, len(s.Checks)))
	}
	n := max(4, w-(marks+1+place+1+1+num+2+up))
	rows := []cardRow{{left: []span{{"SEEN FROM", fg(cMuted)}, {" " + itoa(len(c.SeenFrom)), fg(cText)}},
		right: []span{{padLeft("p50", num), fg(cFaint)}, {"  ", Style{}}, {padLeft("24h", up), fg(cFaint)}}}}
	well := Mix(cInk, cLine, .35)
	for _, s := range c.SeenFrom[:min(3, len(c.SeenFrom))] {
		x := s.Checks[0]
		left := []span{}
		for i, k := range s.Checks[:min(marks, len(s.Checks))] {
			g, gc := string(healthGlyph(k.Health)), healthColor(k.Health)
			if i == marks-1 && len(s.Checks) > marks {
				g, gc = "+", cFaint
			}
			left = append(left, span{g, fg(gc)})
		}
		left = append(left, span{strings.Repeat(" ", marks-min(marks, len(s.Checks))+1), Style{}}, span{fit(s.Label, place), fg(cText)}, span{" ", Style{}})
		left = append(left, m.spark(x, n, well)...)
		l, ok := p50(x)
		lst := fg(cText)
		if !ok {
			lst = fg(cFaint)
		}
		left = append(left, span{" ", Style{}}, span{padLeft(latency(l, ok), num), lst}, span{"  ", Style{}}, span{padLeft(upShare(x.Up24h), up), fg(cMuted)})
		rows = append(rows, cardRow{left: left, hit: "goto:check:" + x.ID})
	}
	for len(rows) < 4 {
		rows = append(rows, cardRow{})
	}
	return rows
}

// scopeName names the observer the view is scoped to.
func (m *Model) scopeName() string {
	here := m.Snapshot.ViewObserver
	if i := slices.IndexFunc(m.Snapshot.Topology.Observers, func(o model.Observer) bool { return o.ID == here }); i >= 0 && m.Snapshot.Topology.Observers[i].Name != "" {
		return m.Snapshot.Topology.Observers[i].Name
	}
	return here
}

// padLeft pads s on the left to n columns.
func padLeft(s string, n int) string {
	if w := width(s); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}

// serviceTallies are the server card's three panels for a service: the
// checks its state rests on, how it is reached and what it depends on.
func (m *Model) serviceTallies(c view.ServiceCard, w int) []cardRow {
	good := Mix(cUp, cMuted, .4)
	checks := tally{label: "CHECKS"}
	down, warn, unknown := 0, 0, 0
	for _, s := range c.SeenFrom {
		for _, x := range s.Checks {
			checks.marks = append(checks.marks, mark{healthGlyph(x.Health), healthColor(x.Health)})
			switch x.Health {
			case "up":
			case "down":
				down++
			case "degraded":
				warn++
			default:
				unknown++
			}
		}
	}
	slices.SortStableFunc(checks.marks, func(a, b mark) int { return markRank(a) - markRank(b) })
	checks.say = counted([]part{{down, "failing", cDown}, {warn, "degraded", cWarn}, {unknown, "unknown", cMuted}}, "all passing", good)
	if len(checks.marks) == 0 {
		checks.say = []span{{"unmeasured", fg(cFaint)}}
		if c.Elsewhere {
			checks.say = []span{{"none here", fg(cFaint)}}
		}
	}

	eps := tally{label: "ENDPOINTS"}
	nets := []string{}
	for _, ep := range c.Endpoints {
		g, gc := endpointGlyph(ep.Health)
		eps.marks = append(eps.marks, mark{g, gc})
		if ep.Network != "" && !slices.Contains(nets, ep.Network) {
			nets = append(nets, ep.Network)
		}
	}
	for i, n := range nets {
		if i > 0 {
			eps.say = append(eps.say, span{" ", Style{}})
		}
		eps.say = append(eps.say, span{n, fg(Mix(netColor(n), cInk, .2))})
	}
	if len(c.Endpoints) == 0 {
		eps.say = []span{{"none declared", fg(cFaint)}}
	}

	deps := tally{label: "DEPENDS ON"}
	dd, da, du, dn := 0, 0, 0, 0
	for _, d := range c.DependsOn {
		mk := mark{healthGlyph(d.Health), healthColor(d.Health)}
		switch {
		case d.Health == "down":
			dd++
		case d.Affected || d.Health == "degraded":
			mk = mark{'▲', cWarn}
			da++
		case !d.Monitored:
			mk.color = cFaint
			dn++
		case d.Health != "up":
			du++
		}
		deps.marks = append(deps.marks, mk)
	}
	slices.SortStableFunc(deps.marks, func(a, b mark) int { return markRank(a) - markRank(b) })
	deps.say = counted([]part{{dd, "down", cDown}, {da, "affected", cWarn}, {du, "unknown", cMuted}, {dn, "unchecked", cFaint}}, "all up", good)
	switch {
	case len(c.DependsOn) > 0:
	case len(c.UsedBy) > 0:
		deps.say = []span{{"used by " + itoa(len(c.UsedBy)), fg(cMuted)}}
	default:
		deps.say = []span{{"none declared", fg(cFaint)}}
	}
	return tallyRows([3]tally{checks, eps, deps}, w)
}

// serviceKnownAt is the address a service is known by, its first public
// one, with what answers there: the edge in front and where it answered
// from, or for a service an edge or a platform runs, where that answered;
// then how many more addresses it has. The longest of these that fits: the
// address cut to its host before the colos give way, and the colos before
// the edge's name.
func (m *Model) serviceKnownAt(f fleet, c view.ServiceCard, w int) cardRow {
	if len(c.Endpoints) == 0 {
		return cardRow{left: []span{{"no endpoint declared", fg(cFaint).italic()}}}
	}
	ep := c.Endpoints[max(0, slices.IndexFunc(c.Endpoints, func(ep view.CardEndpoint) bool { return ep.Network == "public" }))]
	g, gc := endpointGlyph(ep.Health)
	end := []span{{string(g), fg(gc)}}
	if len(c.Endpoints) > 1 {
		end = append(end, span{" +" + itoa(len(c.Endpoints)-1), fg(cFaint)})
	}
	rights := [][]span{}
	var edge model.Node
	var lead []span
	name := ""
	if len(ep.Via) > 0 {
		edge, name = f.Nodes[ep.Via[0].ID], ep.Via[0].Name
	} else if n := f.Nodes[c.Host.ID]; hosted(n.Kind) {
		// What runs it answers there; the band already names it.
		edge = n
	}
	if edge.ID != "" {
		st := fg(nodeHue(edge, cMuted))
		if name != "" {
			lead = []span{{"⇠ ", st}}
		}
		mark := m.standIn(edge)
		if mark != nil {
			mark = append(mark, span{" ", Style{}})
		}
		form := func(words string) []span {
			return joinSpans(lead, mark, []span{{words, st}, {" ", Style{}}}, end)
		}
		colos := []string{}
		for _, s := range c.SeenFrom {
			for _, x := range s.Checks {
				if x.Endpoint == ep.ID && x.Colo != "" && !slices.Contains(colos, x.Colo) {
					colos = append(colos, x.Colo)
				}
			}
		}
		if len(colos) > 1 {
			rights = append(rights, form(strings.Join(colos, " · ")))
		}
		if len(colos) > 0 {
			rights = append(rights, form(colos[0]))
		}
		if name != "" {
			rights = append(rights, form(name))
		}
		if mark != nil {
			rights = append(rights, joinSpans(lead, mark, end))
		}
	}
	rights = append(rights, end)
	net := span{fit(ep.Network, 9), fg(netColor(ep.Network))}
	for _, right := range rights {
		for _, addr := range []string{ep.Address, hostOf(ep.Address)} {
			if 9+width(addr)+1+spansWidth(right) <= w {
				return cardRow{left: []span{net, {addr, fg(cText)}}, right: right, hit: "select:endpoint:" + ep.ID}
			}
		}
	}
	return cardRow{left: []span{net, {hostOf(ep.Address), fg(cText)}}, right: end, hit: "select:endpoint:" + ep.ID}
}

func joinSpans(parts ...[]span) []span {
	out := []span{}
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func spansWidth(ss []span) int {
	n := 0
	for _, s := range ss {
		n += width(s.s)
	}
	return n
}

// serviceTile builds the rows of a service's card for content width w:
// the uniform card's fifteen.
func (m *Model) serviceTile(f fleet, r Row, w int, selected bool) []cardRow {
	c, _ := f.ServiceCard(r.ID)
	rows, _ := m.serviceHead(f, c, r, selected)
	rows = append(rows, cardRow{left: []span{{"app:" + c.Assessment.Availability + " path:" + c.Assessment.Reachability + " worker:" + c.Assessment.WorkerOnline, fg(cMuted)}}})
	rows = append(rows, m.seenPanel(c, w)...)
	rows = append(rows, cardRow{})
	rows = append(rows, m.serviceTallies(c, w)...)
	rows = append(rows, cardRow{}, m.serviceKnownAt(f, c, w))
	return append(rows, memoRows(c.Memo, w, 2)...)
}

// serviceGroups are the headings the cards lie under, in the list's order,
// each with the index of its first service in rows: the application (or
// what the list is browsed by) and what its members need.
type serviceGroup struct {
	row   Row
	first int
}

func (m *Model) serviceGroups(rows []Row) []serviceGroup {
	if m.groupMode() == "flat" {
		return nil
	}
	index := map[string]int{}
	for i, r := range rows {
		index[r.ID] = i
	}
	out := []serviceGroup{}
	var head *Row
	for _, r := range m.groupedRows(true) {
		if r.Kind == "group" {
			g := r
			head = &g
			continue
		}
		if i, ok := index[r.ID]; ok && head != nil {
			out = append(out, serviceGroup{*head, i})
			head = nil
		}
	}
	return out
}

// groupHeading heads the cards of a group: its mark and title, and what of
// its members needs a look, over a hairline to the edge.
func groupHeading(g Row) cardRow {
	left := []span{{string(healthGlyph(g.Health)) + " ", fg(healthColor(g.Health))}, {strings.TrimSpace(g.Title), fg(cText).bold()}}
	if len(g.Cols) > 0 {
		parts := strings.Split(g.Cols[0], " · ")
		left = append(left, span{"  " + parts[0], fg(cMuted)})
		for _, p := range parts[1:] {
			c := cMuted
			if strings.HasSuffix(p, "attention") {
				c = cWarn
			}
			left = append(left, span{" · ", fg(cFaint)}, span{p, fg(c)})
		}
	}
	return cardRow{left: left, rule: true}
}

// drawServiceCards lays the cards out as a grid of as many columns as fit,
// under each group's heading, scrolled so the selection shows whole.
func (m *Model) drawServiceCards(cv *Canvas, x, y, w, h int, rows []Row) {
	if h <= 0 || w <= 0 {
		return
	}
	if len(rows) == 0 {
		msg := "No services declared."
		switch {
		case m.Search != "" || m.AttentionOnly || m.Application != "" || m.Environment != "":
			msg = "No matching services."
		case m.Edit != nil:
			msg = "No services yet — press a to add one."
		case m.Loading != "" && len(m.Snapshot.Topology.Services) == 0:
			msg = "Waiting for the topology…"
		}
		cv.Text(x, y, msg, fg(cMuted).italic(), w)
		return
	}
	cols := max(1, (w+cardGap)/(cardMinW+cardGap))
	cw := min(cardMaxW, (w-cardGap*(cols-1))/cols)
	border := x + w + 1
	gw := cw*cols + cardGap*(cols-1)
	x += (w - gw) / 2
	f := m.fleet()
	// Each group's cards from its first to the next's, under its heading;
	// a flat list is one run without one.
	groups := m.serviceGroups(rows)
	runs := [][2]int{{0, len(rows)}}
	if len(groups) > 0 {
		runs = runs[:0]
		for k, g := range groups {
			from, to := g.first, len(rows)
			if k == 0 {
				from = 0
			}
			if k+1 < len(groups) {
				to = groups[k+1].first
			}
			runs = append(runs, [2]int{from, to})
		}
	}
	type placed struct{ col, top, run int }
	pos := make([]placed, len(rows))
	heads := make([]int, len(groups))
	yy := 0
	for k, run := range runs {
		if len(groups) > 0 {
			if k > 0 {
				yy++
			}
			heads[k] = yy
			yy++
		}
		for i := run[0]; i < run[1]; i++ {
			pos[i] = placed{(i - run[0]) % cols, yy + (i-run[0])/cols*(tileRows+3), k}
		}
		yy += (run[1]-run[0]+cols-1)/cols*(tileRows+3) - 1
	}
	total := yy
	sel := min(max(m.Selected, 0), len(rows)-1)
	p := pos[sel]
	top, bottom := p.top, p.top+tileRows+2
	// In its group's first row of cards, the selection brings the heading.
	if len(groups) > 0 && p.top == pos[runs[p.run][0]].top && bottom-heads[p.run] <= h {
		top = heads[p.run]
	}
	if top < m.cardsTop {
		m.cardsTop = top
	} else if bottom > m.cardsTop+h {
		m.cardsTop = min(top, bottom-h)
	}
	m.cardsTop = min(max(m.cardsTop, 0), max(0, total-h))
	for k, hy := range heads {
		if ry := y + hy - m.cardsTop; ry >= y && ry < y+h {
			m.drawRow(cv, x, ry, gw, groupHeading(groups[k].row), cInk)
		}
	}
	for i, r := range rows {
		p := pos[i]
		if p.top+tileRows+2 <= m.cardsTop || p.top >= m.cardsTop+h {
			continue
		}
		m.drawCard(cv, x+p.col*(cw+cardGap), y+p.top-m.cardsTop, cw, m.serviceTile(f, r, cw-4, i == m.Selected), r, i, y, y+h)
	}
	if total > h {
		scrollThumb(cv, border, y, h, total, m.cardsTop, fg(Mix(cLine, cAccent, .45)))
	}
}

// edgeHue is the hue of the edge a service is answered from, the first
// one in front of its endpoints or the platform it runs on, in which its
// colos are written.
func edgeHue(f fleet, c view.ServiceCard) Color {
	for _, ep := range c.Endpoints {
		if len(ep.Via) > 0 {
			return nodeHue(f.Nodes[ep.Via[0].ID], cMuted)
		}
	}
	if n := f.Nodes[c.Host.ID]; hosted(n.Kind) {
		return nodeHue(n, cMuted)
	}
	return cMuted
}

// The open card: everything a service's card says, nothing cut short.

// serviceSheetBand is an open service card's header: the card's own, and
// under it the service's identity and its application.
func (m *Model) serviceSheetBand(f fleet, c view.ServiceCard, r Row) ([]cardRow, Color) {
	rows, hue := m.serviceHead(f, c, r, true)
	id := cardRow{left: []span{{"id ", fg(Mix(cFaint, cMuted, .4))}, {r.ID, fg(Mix(cMuted, cText, .3))}}, bg: rows[0].bg, bar: hue}
	if c.Application.Name != "" {
		id.right = []span{{c.Application.Name, fg(Mix(cFaint, cMuted, .5))}}
	}
	return append(rows, id), hue
}

// serviceSheetSeen is who sees a service from where, laid open: each
// observer's checks with their samples across the column, their latency,
// first byte and the colo the edge answered from.
func (m *Model) serviceSheetSeen(f fleet, c view.ServiceCard, w int) []cardRow {
	n := max(8, w-2)
	aside := []span{}
	interval := 0
	for _, s := range c.SeenFrom {
		interval = max(interval, s.Checks[0].Interval)
	}
	if interval > 0 {
		aside = []span{{"last " + view.Duration(time.Duration(n*interval)*time.Second) + " · a slot per " + view.Duration(time.Duration(interval)*time.Second), fg(cFaint)}}
	}
	head := heading("SEEN FROM", len(c.SeenFrom))
	head.right = aside
	rows := []cardRow{head}
	checks := 0
	for _, s := range c.SeenFrom {
		checks += len(s.Checks)
	}
	said := "no check measures it"
	if checks > 0 {
		said = strings.TrimSpace(c.Evidence + " from " + plural(checks, "check"))
	}
	if c.Elsewhere && checks == 0 {
		// The view's reason, unmonitored, is of this observer alone.
		said = "no check of " + m.scopeName() + " measures it; other observers' do"
	} else if c.Reason != "" {
		said += " · " + c.Reason
	}
	for _, l := range wrap(said, w) {
		rows = append(rows, cardRow{left: []span{{l, fg(cMuted).italic()}}})
	}
	well := Mix(cPanelBG, cLine, .4)
	colo := fg(Mix(edgeHue(f, c), cText, .25))
	for _, s := range c.SeenFrom {
		rows = append(rows, cardRow{})
		// An observer with more than one check heads them, and a guide
		// down their side keeps them together.
		many := len(s.Checks) > 1
		var guide []span
		if many {
			rows = append(rows, cardRow{left: []span{{string(healthGlyph(s.Health)) + " ", fg(healthColor(s.Health))}, {s.Observer.Name, fg(cText).bold()}, {"  " + plural(len(s.Checks), "check"), fg(cFaint)}}, hit: "goto:observer:" + s.Observer.ID})
			guide = []span{{"│ ", fg(Mix(cLine, cMuted, .3))}}
		}
		under := append(slices.Clone(guide), span{"  ", Style{}})
		room := w - spansWidth(under)
		for i, x := range s.Checks {
			if i > 0 {
				rows = append(rows, cardRow{left: guide})
			}
			hit := "goto:check:" + x.ID
			title := append(slices.Clone(guide), span{string(healthGlyph(x.Health)) + " ", fg(healthColor(x.Health))}, span{s.Observer.Name, fg(cText).bold()}, span{"  " + x.Kind, fg(cFaint)})
			if many {
				title[len(title)-2] = span{x.Name, fg(cText)}
			}
			rows = append(rows, cardRow{left: title, right: []span{{upShare(x.Up24h) + " up", fg(cText)}, {" 24h", fg(cFaint)}}, hit: hit})
			rows = append(rows, cardRow{left: append(slices.Clone(under), m.spark(x, room, well)...), hit: hit})
			l, ok := p50(x)
			if !ok {
				rows = append(rows, cardRow{left: append(slices.Clone(under), span{"no current sample", fg(cFaint).italic()}), hit: hit})
				continue
			}
			line := cardRow{left: append(slices.Clone(under), span{"p50 " + latency(l, true) + " · p95 " + latency(x.Latency.P95, true), fg(cText)}), hit: hit}
			if x.Colo != "" {
				line.right = []span{{x.Colo, colo}}
			}
			rows = append(rows, line)
			if x.FirstByte != "" {
				for k, l := range wrap(x.FirstByte, room-len("first byte ")) {
					lead := "first byte "
					if k > 0 {
						lead = strings.Repeat(" ", len(lead))
					}
					rows = append(rows, cardRow{left: append(slices.Clone(under), span{lead, fg(cFaint)}, span{l, fg(cMuted)}), hit: hit})
				}
			}
		}
	}
	return rows
}

// serviceSheetSections are what an open service card says beside who sees
// it: how it is reached, where the edge answered from, where it runs, what
// it depends on and what depends on it, and its runbook.
func (m *Model) serviceSheetSections(f fleet, c view.ServiceCard) [][]cardRow {
	evidenceRows := []cardRow{}
	for _, line := range c.Assessment.Lines() {
		evidenceRows = append(evidenceRows, cardRow{left: []span{{line, fg(cMuted)}}})
	}
	out := [][]cardRow{append([]cardRow{heading("EVIDENCE SCOPE", 0)}, evidenceRows...)}
	if len(c.Endpoints) > 0 {
		rows := []cardRow{heading("REACHED AT", len(c.Endpoints))}
		for _, ep := range c.Endpoints {
			right, tight := []span{}, []span(nil)
			if len(ep.Via) > 0 {
				right, tight = m.via(f, "⇠ ", ep.Via)
				right = append(right, span{" ", Style{}})
				if tight != nil {
					tight = append(tight, span{" ", Style{}})
				}
			}
			g, gc := endpointGlyph(ep.Health)
			right = append(right, span{string(g), fg(gc)})
			if tight != nil {
				tight = append(tight, span{string(g), fg(gc)})
			}
			rows = append(rows, cardRow{left: []span{{fit(ep.Network, 9), fg(netColor(ep.Network))}, {ep.Address, fg(cText)}}, right: right, tight: tight, hit: "select:endpoint:" + ep.ID})
		}
		out = append(out, rows)
	}
	if a := c.Answered; a != nil {
		head := heading("ANSWERED FROM", 0)
		head.right = []span{{"24h shares", fg(cFaint)}}
		rows := []cardRow{head, {left: []span{{strings.Join(a.Pops, " · "), fg(Mix(edgeHue(f, c), cText, .25))}}}}
		if len(a.Cache) > 0 {
			rows = append(rows, cardRow{left: []span{{"cache ", fg(cFaint)}, {strings.Join(a.Cache, " · "), fg(cMuted)}}})
		}
		out = append(out, rows)
	}
	n := f.Nodes[c.Host.ID]
	kind := strings.Join(slices.DeleteFunc([]string{c.Host.Kind, c.Host.Provider}, func(s string) bool { return s == "" }), " · ")
	out = append(out, []cardRow{heading("RUNS ON", 0), {
		left:  append(m.logo(n.Provider), span{c.Host.Name, fg(cText)}),
		right: []span{{kind, fg(cMuted)}, {"  ", Style{}}, {string(healthGlyph(c.Host.Health)), fg(healthColor(c.Host.Health))}},
		hit:   "goto:server:" + c.Host.ID,
	}})
	list := func(title, arrow string, refs []view.ServiceRef) {
		if len(refs) == 0 {
			return
		}
		rows := []cardRow{heading(title, len(refs))}
		for _, d := range refs {
			g, gc := healthGlyph(d.Health), healthColor(d.Health)
			if d.Affected && d.Health != "down" {
				g, gc = '▲', cWarn
			}
			right := []span{{d.Host, fg(cMuted)}}
			if !d.Monitored {
				right = []span{{"not monitored", fg(cFaint)}}
			}
			rows = append(rows, cardRow{left: []span{{arrow, fg(cAccent)}, {string(g) + " ", fg(gc)}, {d.Name, fg(cText)}}, right: right, hit: "select:service:" + d.ID})
		}
		out = append(out, rows)
	}
	list("DEPENDS ON", "→ ", c.DependsOn)
	list("USED BY", "← ", c.UsedBy)
	if c.Runbook != "" {
		out = append(out, []cardRow{heading("RUNBOOK", 0), {left: []span{{"↗ ", fg(cAccent2)}, {c.Runbook, fg(cAccent2)}}, right: []span{{"not run by topo", fg(cFaint)}}}})
	}
	return out
}

// drawServiceSheet lays the card of r open over the cards in the panel at
// (x, y), w by h.
func (m *Model) drawServiceSheet(cv *Canvas, x, y, w, h int, r Row) {
	if w < 20 || h < 6 {
		return
	}
	inner := sheetWidth(w) - 4
	f := m.fleet()
	c, _ := f.ServiceCard(r.ID)
	band, _ := m.serviceSheetBand(f, c, r)
	out := []sheetLine{{across: true}}
	if c.Memo != "" {
		for _, row := range memoRows(c.Memo, inner, 0) {
			out = append(out, sheetLine{left: row, across: true})
		}
		out = append(out, sheetLine{across: true})
	}
	two := inner >= sheetCols
	cw := inner
	if two {
		cw = (inner - sheetGap) / 2
	}
	sections := append([][]cardRow{m.serviceSheetSeen(f, c, cw)}, m.serviceSheetSections(f, c)...)
	m.drawOpenCard(cv, x, y, w, h, r, band, sheetColumns(out, sections, two))
}
