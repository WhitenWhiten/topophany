package tui

import (
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// The Servers tab: a card for each server, laid out as a wall of columns.
// A card says what the server is and where, what runs on it and how it is
// reached, what it hosts and who observes from it, and ends with the
// operator's notes as a memo. Edges and platforms get cards too, saying
// what runs there, what they stand in front of and where they answered.
// With s the cards line up as a grid at one size (tiles.go), and Enter
// lays the selected one open over either (sheet.go).

const (
	cardMinW = 42 // narrower than this, the wall drops a column
	cardMaxW = 64
	cardGap  = 2 // columns between cards
)

// cardRow is one line inside a card: spans from the left, spans pushed to
// the right edge, what those say in fewer columns where they do not fit, an
// optional tint and a click target.
type cardRow struct {
	left, right []span
	tight       []span
	bg          Color
	bar         Color // a stripe at the card's inner left edge
	rule        bool  // a hairline runs on from the left spans to the edge
	hit         string
}

// fleet is what the cards are built from, indexed once per frame, with the
// hue of each node group.
type fleet struct {
	*view.Fleet
	groups map[string]Color
}

func (m *Model) fleet() fleet { return fleet{m.view().Fleet(), m.groupColors()} }

// netColors tint the network of an address so a card shows at a glance
// what is public and what only travels the tailnet or the LAN.
var netColors = map[string]Color{"public": cAccent2, "tailnet": cEdit, "lan": Hex(0x86EFAC), "loopback": cMuted, "external": cFaint}

// card builds the rows of a server's card for content width w, as the
// wall shows it: everything it says, long lists cut short.
func (m *Model) card(f fleet, r Row, w int, selected bool) []cardRow {
	c := f.Card(r.ID)
	rows, hue := m.cardHead(f, r, c, selected)
	if len(c.Resources) > 0 {
		head := heading(resourceTitle(c.Resources), len(c.Resources))
		head.right = m.resourceAside(c.Resources)
		rows = append(rows, cardRow{}, head)
		rows = append(rows, m.resourceRows(c.Resources, w)...)
	}
	for _, s := range m.cardSections(f, r, c, hue, 8) {
		rows = append(append(rows, cardRow{}), s...)
	}
	if c.Memo != "" {
		rows = append(append(rows, cardRow{}), memoRows(c.Memo, w, 0)...)
	}
	return rows
}

// cardHead is a card's header, on a band in its group's hue: its name and
// state over its kind and where it is. A server with a problem takes the
// problem's color, so it stands out on the wall. hue is the group's, or
// the provider's for what a platform hosts.
func (m *Model) cardHead(f fleet, r Row, c view.Card, selected bool) ([]cardRow, Color) {
	n := f.Nodes[r.ID]
	hue := nodeHue(n, f.groups[n.Group])
	band := Mix(hue, cInk, .84)
	switch {
	case r.Health == "down":
		band = Mix(cDown, cInk, .84)
	case r.Health == "degraded" || r.Note == "affected":
		band = Mix(cWarn, cInk, .86)
	}
	pill, pillStyle := statePill(r)
	name := fg(cText).bold()
	if selected {
		name = fg(cGlow).bold()
	}
	sub := []span{{c.Kind, fg(Mix(hue, cText, .35))}}
	if c.Group != "" {
		sub = append(sub, span{" · " + c.Group, fg(cMuted)})
	}
	where := []span{}
	switch {
	case n.Location != nil:
		where = []span{{"◎ " + c.Where, fg(cMuted)}}
	case n.Kind == "edge":
		where = []span{{"◌ " + c.Where, fg(cMuted)}}
	case c.Where != "":
		where = []span{{c.Where, fg(cMuted)}}
	}
	return []cardRow{
		{left: append(m.logo(n.Provider), span{r.Title, name}), right: []span{{pill, pillStyle}}, bg: band, bar: hue},
		{left: sub, right: where, bg: band, bar: hue},
	}, hue
}

// heading opens a section of a card with a quiet label and a hairline, so
// the name and what needs attention stay the loudest things on it.
func heading(title string, count int) cardRow {
	head := []span{{title, fg(Mix(cMuted, cAccent, .3)).bold()}}
	if count > 1 {
		head = append(head, span{" " + itoa(count), fg(cFaint)})
	}
	return cardRow{left: head, rule: true}
}

// cardSections are what a card says after its resources, each a heading
// over its rows: what runs here, what an edge stands in front of and where
// it answered, how it is reached, what it hosts, who observes from it and
// its links. A list longer than most keeps its first entries and counts
// the rest, which Enter shows; with most 0 nothing is cut.
func (m *Model) cardSections(f fleet, r Row, c view.Card, hue Color, most int) [][]cardRow {
	out := [][]cardRow{}
	var rows []cardRow
	add := func(cr cardRow) { rows = append(rows, cr) }
	open := func(title string, count int) { rows = []cardRow{heading(title, count)} }
	done := func() { out = append(out, rows) }
	// cut reports whether the entry at i is where a list of n stops, and
	// then counts what is left.
	cut := func(i, n, most int) bool {
		if most > 0 && n > most && i == most-1 {
			add(cardRow{left: []span{{"+" + itoa(n-most+1) + " more", fg(cFaint)}}})
			return true
		}
		return false
	}

	// What runs here, healthiest last so trouble reads first.
	if len(c.Services) > 0 {
		title := "SERVICES"
		if hosted(f.Nodes[r.ID].Kind) {
			title = "RUNS HERE"
		}
		open(title, len(c.Services))
		for i, v := range c.Services {
			if cut(i, len(c.Services), most) {
				break
			}
			g, gc := healthGlyph(v.Health), healthColor(v.Health)
			if v.Affected && v.Health != "down" {
				g, gc = '▲', cWarn
			}
			right, tight := []span{}, []span(nil)
			switch {
			case len(v.Via) > 0:
				right, tight = m.via(f, "via ", v.Via)
			case v.Runtime != "":
				right = append(right, span{v.Runtime, fg(Mix(hue, cText, .2))})
			case !v.Monitored:
				right = append(right, span{"not monitored", fg(cFaint)})
			}
			add(cardRow{left: []span{{string(g) + " ", fg(gc)}, {v.Name, fg(cText)}}, right: right, tight: tight, hit: "goto:service:" + v.ID})
		}
		done()
	}

	// An edge: the addresses it answers for, and where it answered from.
	if len(c.Fronts) > 0 {
		open("IN FRONT OF", len(c.Fronts))
		for i, ep := range c.Fronts {
			if cut(i, len(c.Fronts), most) {
				break
			}
			add(cardRow{left: []span{{string(healthGlyph(ep.Health)) + " ", fg(healthColor(ep.Health))}, {hostOf(ep.Address), fg(cText)}}, right: []span{{"→ " + ep.Node.Name, fg(cMuted)}}, hit: "select:endpoint:" + ep.ID})
		}
		done()
	}
	if len(c.Pops) > 0 {
		open("ANSWERED FROM", 0)
		// Shares come from the collector's day-long rollups; bare codes are
		// the freshest response headers when no rollups are kept yet. The
		// card's flag says which, so neither claims to be the other.
		note := "reported colos"
		if c.PopsFromRollups {
			note = "24h shares"
		}
		add(cardRow{left: []span{{strings.Join(c.Pops, " · "), fg(Mix(hue, cText, .25))}}, right: []span{{note, fg(cFaint)}}})
		done()
	}

	if len(c.Regions) > 0 {
		open("REPORTED EDGE REGIONS", 0)
		add(cardRow{left: []span{{strings.Join(c.Regions, " · "), fg(Mix(hue, cText, .25))}}, right: []span{{"response headers", fg(cFaint)}}})
		done()
	}

	// How it is reached.
	if len(c.Addresses) > 0 {
		open("ADDRESSES", len(c.Addresses))
		for i, ep := range c.Addresses {
			if cut(i, len(c.Addresses), most) {
				break
			}
			right, tight := []span{}, []span(nil)
			if len(ep.Via) > 0 {
				right, tight = m.via(f, "⇠ ", ep.Via[:1])
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
			add(cardRow{left: []span{{fit(ep.Network, 9), fg(netColor(ep.Network))}, {ep.Address, fg(cText)}}, right: right, tight: tight, hit: "select:endpoint:" + ep.ID})
		}
		done()
	}

	// What it hosts.
	if len(c.Hosts) > 0 {
		open("HOSTS", len(c.Hosts))
		for i, k := range c.Hosts {
			if cut(i, len(c.Hosts), most) {
				break
			}
			tree := "├ "
			if i == len(c.Hosts)-1 {
				tree = "└ "
			}
			add(cardRow{left: []span{{tree, fg(cFaint)}, {string(healthGlyph(k.Health)) + " ", fg(healthColor(k.Health))}, {k.Name, fg(cText)}}, right: []span{{strings.Join(k.Runs, ", "), fg(cMuted)}}, hit: "select:node:" + k.ID})
		}
		done()
	}

	// Who observes from here.
	if len(c.Observers) > 0 {
		open("OBSERVES FROM HERE", len(c.Observers))
		for _, o := range c.Observers {
			oc := healthColor(o.Health)
			if o.Outdated {
				oc = cWarn
			}
			add(cardRow{left: []span{{"◎ ", fg(oc)}, {o.Name, fg(cText)}}, right: []span{{age(m.Now, o.LastSeen), fg(cFaint)}}, hit: "goto:observer:" + o.ID})
		}
		done()
	}

	// Its declared connections, of which a wall card has room for fewer.
	if len(c.Links) > 0 {
		open("LINKS", len(c.Links))
		few := most
		if few > 0 {
			few = min(few, 5)
		}
		for i, l := range c.Links {
			if cut(i, len(c.Links), few) {
				break
			}
			arrow := "→ "
			if l.Inbound {
				arrow = "← "
			}
			left := []span{{arrow, fg(cAccent)}, {l.Other.Name, fg(cText)}}
			if l.Health == "down" || l.Health == "degraded" {
				left = append([]span{{string(healthGlyph(l.Health)) + " ", fg(healthColor(l.Health))}}, left...)
			}
			right, tight := linkReading(l)
			add(cardRow{left: left, right: right, tight: tight, hit: "select:link:" + l.ID})
		}
		done()
	}
	return out
}

// linkReading is what a link's row says on the right: its transport and
// then what its own checks report, as "cloudflared · 4 sessions · hkg,sin ·
// API 4 connections healthy". Each word after the transport carries its own
// separator, so a row too narrow for all of it is cut inside the last word
// it has room for (a colo list ends in "…"), never at a bare separator. The
// shorter form drops the transport, which the reading already implies, and
// is nil when no check reported.
func linkReading(l view.CardLink) (long, tight []span) {
	long = []span{}
	if l.Transport != "" {
		long = append(long, span{l.Transport, fg(cFaint)})
	}
	if len(l.Reading) == 0 {
		return long, nil
	}
	for i, word := range l.Reading {
		if i > 0 || len(long) > 0 {
			word = " · " + word
		}
		long = append(long, span{word, fg(cMuted)})
	}
	for i, word := range l.Reading {
		if i > 0 {
			word = " · " + word
		}
		tight = append(tight, span{word, fg(cMuted)})
	}
	return long, tight
}

// via names the edges and proxies in front of something after lead, each
// led by its logo, in the color of the first; and, where every one has a
// logo or a mark to stand in for it, that shorter form, else nil.
func (m *Model) via(f fleet, lead string, refs []view.Ref) (long, tight []span) {
	st := fg(nodeHue(f.Nodes[refs[0].ID], cMuted))
	long, tight = []span{{lead, st}}, []span{{lead, st}}
	for i, ref := range refs {
		n := f.Nodes[ref.ID]
		if i > 0 {
			long = append(long, span{" ⇢ ", st})
			tight = append(tight, span{" ", st})
		}
		long = append(append(long, m.logo(n.Provider)...), span{ref.Name, st})
		in := m.standIn(n)
		if in == nil || tight == nil {
			tight = nil
			continue
		}
		tight = append(tight, in...)
	}
	return long, tight
}

// netColor tints the network of an address.
func netColor(network string) Color {
	if c, ok := netColors[network]; ok {
		return c
	}
	return cMuted
}

// endpointGlyph marks an address by its health, or with a quiet dot when
// no check targets it.
func endpointGlyph(health string) (rune, Color) {
	if health == "" {
		return '·', cFaint
	}
	return healthGlyph(health), healthColor(health)
}

// memoRows are the operator's notes as a memo, on paper: all of them, or
// with lines > 0 that many rows exactly, the last cut short with "…" when
// there is more and the paper left blank when there are no notes.
func memoRows(memo string, w, lines int) []cardRow {
	paper := Mix(cInk, cCity, .07)
	pen := Mix(cCity, cInk, .2)
	text := memoLines(memo, w-2)
	if lines > 0 {
		if len(text) > lines {
			text = append(text[:lines-1], strings.TrimRight(fit(text[lines-1], w-3), " ")+"…")
		}
		for len(text) < lines {
			text = append(text, "")
		}
	}
	rows := make([]cardRow, 0, len(text))
	for i, line := range text {
		lead := "  "
		if i == 0 {
			lead = "✎ "
		}
		st := fg(Mix(cText, cCity, .18)).italic()
		if memo == "" && i == 0 {
			line, st = "no notes", fg(Mix(pen, paper, .55)).italic()
		}
		rows = append(rows, cardRow{left: []span{{lead, fg(pen)}, {line, st}}, bg: paper, bar: Mix(cCity, cInk, .35)})
	}
	return rows
}

// memoLines keeps the line breaks an operator wrote and wraps the rest.
func memoLines(s string, w int) []string {
	out := []string{}
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		para = strings.TrimSpace(secure.Text(para))
		if para == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrap(para, max(1, w))...)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// hostOf shortens an address to its host, which is what an edge answers
// for: "https://www.example.invalid/x" → "www.example.invalid".
func hostOf(addr string) string {
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	if i := strings.IndexAny(addr, "/?#"); i >= 0 {
		addr = addr[:i]
	}
	return addr
}

// drawServers lays the cards out in as many columns as fit: as a wall,
// each card dropped into the shortest column, or as a grid of uniform
// cards in reading order; scrolled so the selection shows.
func (m *Model) drawServers(cv *Canvas, x, y, w, h int, rows []Row) {
	if h <= 0 || w <= 0 {
		return
	}
	if len(rows) == 0 {
		msg := "No servers declared."
		switch {
		case m.Search != "":
			msg = "No matching servers."
		case m.Edit != nil:
			msg = "No servers yet — press a to add a node."
		case m.Loading != "" && len(m.Snapshot.Topology.Nodes) == 0:
			msg = "Waiting for the topology…"
		}
		cv.Text(x, y, msg, fg(cMuted).italic(), w)
		return
	}
	cols := max(1, min(len(rows), (w+cardGap)/(cardMinW+cardGap)))
	cw := min(cardMaxW, (w-cardGap*(cols-1))/cols)
	// Cards narrower than the room left over sit centred.
	border := x + w + 1
	x += (w - (cw*cols + cardGap*(cols-1))) / 2
	f := m.fleet()
	type placed struct{ col, top, h int }
	pos := make([]placed, len(rows))
	cards := make([][]cardRow, len(rows))
	total := 0
	if m.Uniform {
		for i, r := range rows {
			cards[i] = m.tile(f, r, cw-4, i == m.Selected)
			pos[i] = placed{i % cols, i / cols * (tileRows + 3), tileRows + 2}
		}
		total = (len(rows)+cols-1)/cols*(tileRows+3) - 1
	} else {
		heights := make([]int, cols)
		for i, r := range rows {
			cards[i] = m.card(f, r, cw-4, i == m.Selected)
			c := 0
			for k := range heights {
				if heights[k] < heights[c] {
					c = k
				}
			}
			pos[i] = placed{c, heights[c], len(cards[i]) + 2}
			heights[c] += len(cards[i]) + 3
		}
		for _, hh := range heights {
			total = max(total, hh-1)
		}
	}
	if sel := pos[min(max(m.Selected, 0), len(pos)-1)]; sel.top < m.cardsTop {
		m.cardsTop = sel.top
	} else if sel.top+sel.h > m.cardsTop+h {
		m.cardsTop = min(sel.top, sel.top+sel.h-h)
	}
	m.cardsTop = min(max(m.cardsTop, 0), max(0, total-h))
	for i, r := range rows {
		p := pos[i]
		if p.top+p.h <= m.cardsTop || p.top >= m.cardsTop+h {
			continue
		}
		m.drawCard(cv, x+p.col*(cw+cardGap), y+p.top-m.cardsTop, cw, cards[i], r, i, y, y+h)
	}
	if total > h {
		// Where the window sits on the wall, on the panel's right border.
		scrollThumb(cv, border, y, h, total, m.cardsTop, fg(Mix(cLine, cAccent, .45)))
	}
}

// scrollThumb marks on a border at x, h rows from y, which of total rows
// show from top on.
func scrollThumb(cv *Canvas, x, y, h, total, top int, st Style) {
	thumb := max(1, h*h/total)
	at := (h - thumb) * top / max(1, total-h)
	for k := 0; k < thumb; k++ {
		cv.Set(x, y+at+k, '┃', st)
	}
}

// drawCard draws one card with its top-left corner at (x, y), clipped to
// the rows from clipTop up to clipBottom.
func (m *Model) drawCard(cv *Canvas, x, y, w int, rows []cardRow, r Row, index int, clipTop, clipBottom int) {
	if w < 8 {
		return
	}
	selected := index == m.Selected
	paper := Mix(cInk, cPanelBG, .9)
	border := fg(cLine)
	switch {
	case selected:
		paper = Mix(cPanelBG, cSelBG, .45)
		border = fg(Mix(cLine, cAccent, .75))
	case r.Health == "down":
		border = fg(Mix(cLine, cDown, .55))
	case r.Health == "degraded" || r.Note == "affected":
		border = fg(Mix(cLine, cWarn, .45))
	}
	// Without color the selection is the heavier frame.
	frame := roundFrame
	if selected && m.NoColor {
		frame = heavyFrame
	}
	visible := func(yy int) bool { return yy >= clipTop && yy < clipBottom }
	for i := 0; i < len(rows)+2; i++ {
		yy := y + i
		if !visible(yy) {
			continue
		}
		m.hit(x, yy, w, "row:"+itoa(index))
		// The frame stands on the ground and only its inside is paper, so
		// the line is the card's edge.
		switch i {
		case 0, len(rows) + 1:
			frameEdge(cv, x, yy, w, frame, i > 0, border)
			continue
		}
		row := rows[i-1]
		bg := paper
		if row.bg != 0 {
			bg = row.bg
			if selected {
				bg = Mix(row.bg, cSelBG, .35)
			}
		}
		cv.Set(x, yy, frame[5], border)
		cv.Fill(x+1, yy, w-2, 1, Style{BG: bg})
		cv.Set(x+w-1, yy, frame[5], border)
		if row.bar != 0 {
			cv.Set(x+1, yy, '▎', fg(row.bar).Bg(bg))
		}
		m.drawRow(cv, x+2, yy, w-4, row, bg)
	}
}

// A card's frame: its corners, top-left, top-right, bottom-left and
// bottom-right, then its rules across and down.
var (
	roundFrame = [6]rune{'╭', '╮', '╰', '╯', '─', '│'}
	heavyFrame = [6]rune{'┏', '┓', '┗', '┛', '━', '┃'}
)

// frameEdge draws a card's top edge at y, or its bottom one.
func frameEdge(cv *Canvas, x, y, w int, frame [6]rune, bottom bool, st Style) {
	l, r := frame[0], frame[1]
	if bottom {
		l, r = frame[2], frame[3]
	}
	cv.Set(x, y, l, st)
	for k := 1; k < w-1; k++ {
		cv.Set(x+k, y, frame[4], st)
	}
	cv.Set(x+w-1, y, r, st)
}

// drawRow writes a card row into the w columns from x, on bg: what it
// names from the left, what it says pushed to the right, and a hairline
// between when it opens a section.
func (m *Model) drawRow(cv *Canvas, x, y, w int, row cardRow, bg Color) {
	// What a row names keeps its width first; what it says on the right
	// stays whole when short (an age, a state pill: a span with its own
	// background, as long as " ▲ HEALTH WARN ") and otherwise gets what is
	// left, but never less than a third.
	spans := func(ss []span) int {
		n := 0
		for _, s := range ss {
			n += width(s.s)
		}
		return n
	}
	lw0, rw := spans(row.left), spans(row.right)
	if row.tight != nil && lw0+1+rw > w {
		// Where it does not fit, the shorter form, whole.
		row.right, rw = row.tight, spans(row.tight)
	}
	short := 12
	if len(row.right) == 1 && row.right[0].st.BG != 0 {
		short = 16
	}
	if rw > short && lw0+1+rw > w {
		rw = max(min(rw, w-lw0-1), min(rw, short, w/3))
	}
	lw := w
	if rw > 0 {
		lw = w - rw - 1
	}
	// Spans keep a background of their own, such as the state pill.
	on := func(st Style) Style {
		if st.BG == 0 {
			st.BG = bg
		}
		return st
	}
	cx := x
	for _, s := range row.left {
		if cx >= x+lw {
			break
		}
		cx += cv.Text(cx, y, ellipsis(s.s, x+lw-cx), on(s.st), x+lw-cx)
	}
	if row.rule {
		end := x + w
		if rw > 0 {
			end -= rw + 1
		}
		for k := cx + 1; k < end; k++ {
			cv.Set(k, y, '─', fg(cLine).Bg(bg))
		}
	}
	if rw > 0 {
		rx := x + w - rw
		for _, s := range row.right {
			if rx >= x+w {
				break
			}
			rx += cv.Text(rx, y, ellipsis(s.s, x+w-rx), on(s.st), x+w-rx)
		}
	}
	if row.hit != "" {
		m.hit(x-1, y, w+2, row.hit)
	}
}
