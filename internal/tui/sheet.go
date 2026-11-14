package tui

import (
	"math"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// The open card (Enter on Servers): the selected server's card laid open
// over the others, which step back behind it, whether they lie as a wall
// or a grid. It says everything the card does with nothing cut short. Its
// band names the server, its state, kind and place, identity and
// coordinates; the operator's notes follow, as what the server is for;
// then its resources charted over the samples kept, everything that runs
// on it and reaches it, and the checks its state rests on, in two columns
// where there is the width. ↑↓ scroll it; Enter, Esc or a click beside it
// closes it.

const (
	sheetMaxW = 132 // wider than this, an open card leaves the rest to the cards behind
	sheetGap  = 5   // between its columns, a rule down the middle
	sheetCols = 96  // the width inside it from which it has two columns
)

// sheetLine is a row of an open card's body: one across it, or one in
// each column.
type sheetLine struct {
	left, right cardRow
	across      bool
}

// drawSheet lays the card of r open over the cards in the panel at
// (x, y), w by h.
func (m *Model) drawSheet(cv *Canvas, x, y, w, h int, r Row) {
	if w < 20 || h < 6 {
		return
	}
	f := m.fleet()
	c := f.Card(r.ID)
	band, hue := m.sheetBand(f, r, c)
	m.drawOpenCard(cv, x, y, w, h, r, band, m.sheetBody(f, r, c, hue, sheetWidth(w)-4))
}

// sheetWidth is how wide a card lies open in a panel w wide.
func sheetWidth(w int) int {
	if w >= 72 {
		return min(w-6, sheetMaxW)
	}
	return w
}

// drawOpenCard lays a card open over the cards in the panel at (x, y), w
// by h: its band over its body, which scrolls; the card's state edges it.
func (m *Model) drawOpenCard(cv *Canvas, x, y, w, h int, r Row, band []cardRow, body []sheetLine) {
	// The cards behind step back, and a click on them closes the card.
	cv.Tint(x, y, w, h, func(s Style) Style {
		c, b := s.FG, s.BG
		if c == 0 {
			c = cText
		}
		if b == 0 {
			b = cInk
		}
		s = Style{FG: Mix(c, cInk, .72), BG: Mix(b, cInk, .62)}
		if m.Transparent && s.BG == cInk {
			s.BG = 0
		}
		return s
	})
	if m.NoColor {
		// Without color nothing dims, so the cards make way.
		cv.Fill(x, y, w, h, Style{})
	}
	for yy := y; yy < y+h; yy++ {
		m.hit(x, yy, w, "key:esc")
	}

	sw := sheetWidth(w)
	inner := sw - 4
	room := h
	if h >= 20 {
		room = h - 2
	}
	// A blank row under the body, where there is the room for it.
	sh := min(room, len(band)+len(body)+3)
	sx, sy := x+(w-sw)/2, y+(h-sh)/2
	if m.compactLayout() {
		// A phone's page reads from the top.
		sy = y
	}
	bh := sh - 2 - len(band)
	m.Offset = min(max(m.Offset, 0), max(0, len(body)-bh))

	if !m.Transparent && !m.NoColor && !m.compactLayout() {
		// A shadow, two columns to the right and one row below, lifts the
		// card off the others; a phone shows none behind it.
		shade := func(s Style) Style {
			if s.BG == 0 {
				s.BG = cv.Base
			}
			if s.FG == 0 {
				s.FG = cText
			}
			s.BG, s.FG = Mix(s.BG, Hex(0), .5), Mix(s.FG, Hex(0), .5)
			return s
		}
		if sx+sw+2 <= x+w {
			cv.Tint(sx+sw, sy+1, 2, sh-1, shade)
		}
		if sy+sh < y+h {
			cv.Tint(sx+2, sy+sh, min(sw, x+w-sx-2), 1, shade)
		}
	}

	// Over the cards the frame is paper too, so no card shows through.
	paper := cPanelBG
	border := fg(Mix(cLine, cAccent, .75)).Bg(paper)
	switch {
	case r.Health == "down":
		border = fg(Mix(cLine, cDown, .7)).Bg(paper)
	case r.Health == "degraded" || r.Note == "affected":
		border = fg(Mix(cLine, cWarn, .6)).Bg(paper)
	}
	frame := roundFrame
	if m.NoColor {
		frame = heavyFrame
	}
	frameEdge(cv, sx, sy, sw, frame, false, border)
	frameEdge(cv, sx, sy+sh-1, sw, frame, true, border)
	for i := 1; i < sh-1; i++ {
		yy := sy + i
		cv.Set(sx, yy, frame[5], border)
		cv.Set(sx+sw-1, yy, frame[5], border)
		m.hit(sx, yy, sw, "")
		var line sheetLine
		bg := paper
		switch {
		case i-1 < len(band):
			line = sheetLine{left: band[i-1], across: true}
		case m.Offset+i-1-len(band) < len(body):
			line = body[m.Offset+i-1-len(band)]
		default:
			line = sheetLine{across: true}
		}
		if line.across && line.left.bg != 0 {
			bg = line.left.bg
		}
		cv.Fill(sx+1, yy, sw-2, 1, Style{BG: bg})
		if line.across {
			if line.left.bar != 0 {
				cv.Set(sx+1, yy, '▎', fg(line.left.bar).Bg(bg))
			}
			m.drawRow(cv, sx+2, yy, inner, line.left, bg)
			continue
		}
		lw := (inner - sheetGap) / 2
		m.drawRow(cv, sx+2, yy, lw, line.left, bg)
		cv.Set(sx+2+lw+sheetGap/2, yy, '│', fg(Mix(cLine, paper, .35)).Bg(bg))
		m.drawRow(cv, sx+2+lw+sheetGap, yy, inner-lw-sheetGap, line.right, bg)
	}
	m.hit(sx, sy, sw, "")
	m.hit(sx, sy+sh-1, sw, "")
	if len(body) > bh && bh > 0 {
		scrollThumb(cv, sx+sw-1, sy+1+len(band), bh, len(body), m.Offset, border)
		if left := len(body) - bh - m.Offset; left > 0 {
			more := " ↓ " + itoa(left) + " more "
			cv.Text(sx+sw-2-width(more), sy+sh-1, more, fg(cMuted), -1)
		}
	}
}

// sheetBand is an open card's header: the card's own, and under it the
// server's identity and, where it has them, its coordinates; with the hue
// of the card.
func (m *Model) sheetBand(f fleet, r Row, c view.Card) ([]cardRow, Color) {
	rows, hue := m.cardHead(f, r, c, true)
	n := f.Nodes[r.ID]
	id := cardRow{left: []span{{"id ", fg(Mix(cFaint, cMuted, .4))}, {r.ID, fg(Mix(cMuted, cText, .3))}}, bg: rows[0].bg, bar: hue}
	if n.Location != nil {
		id.right = []span{{view.LatLon(*n.Location), fg(Mix(cFaint, cMuted, .5))}}
	}
	return append(rows, id), hue
}

// sheetBody is what an open card says under its band, inner columns wide.
func (m *Model) sheetBody(f fleet, r Row, c view.Card, hue Color, inner int) []sheetLine {
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
	sections := [][]cardRow{}
	if len(c.Resources) > 0 {
		sections = append(sections, m.sheetResources(c.Resources, cw))
	}
	sections = append(sections, m.cardSections(f, r, c, hue, 0)...)
	if ev := m.sheetEvidence(r, cw); ev != nil {
		sections = append(sections, ev)
	}
	return sheetColumns(out, sections, two)
}

// sheetColumns lays an open card's sections under what out already holds:
// in two columns where two, the sections keeping their order down the left
// and on down the right; else one under another.
func sheetColumns(out []sheetLine, sections [][]cardRow, two bool) []sheetLine {
	stack := func(ss [][]cardRow) []cardRow {
		rows := []cardRow{}
		for i, s := range ss {
			if i > 0 {
				rows = append(rows, cardRow{})
			}
			rows = append(rows, s...)
		}
		return rows
	}
	if !two || len(sections) < 2 {
		for _, row := range stack(sections) {
			out = append(out, sheetLine{left: row, across: !two})
		}
		return out
	}
	// The sections keep their order, down the left column and on down
	// the right, split where the taller column is shortest.
	best, split := math.MaxInt, 1
	for k := 1; k < len(sections); k++ {
		if t := max(len(stack(sections[:k])), len(stack(sections[k:]))); t < best {
			best, split = t, k
		}
	}
	left, right := stack(sections[:split]), stack(sections[split:])
	for i := range max(len(left), len(right)) {
		line := sheetLine{}
		if i < len(left) {
			line.left = left[i]
		}
		if i < len(right) {
			line.right = right[i]
		}
		out = append(out, line)
	}
	return out
}

// sheetResources are the resources on an open card, w wide: for each
// check that reads them, CPU, memory and disk, each its reading now, how
// much there is and its peak and limit over a chart of the samples kept,
// then what runs.
func (m *Model) sheetResources(rs []view.CardResource, w int) []cardRow {
	head := heading(resourceTitle(rs), len(rs))
	head.right = m.resourceAside(rs)
	rows := []cardRow{head}
	well := Mix(cPanelBG, cLine, .4)
	for _, r := range rs {
		x, h := m.hostCheck(r.Check)
		hit := "goto:check:" + r.Check
		if len(rs) > 1 {
			sub := cardRow{left: []span{{"◎ ", fg(healthColor(r.Health))}, {r.Observer.Name, fg(cMuted)}}, right: m.resourceAside([]view.CardResource{r}), hit: hit}
			if h.DiskPath != "" {
				sub.left = append(sub.left, span{" · " + h.DiskPath, fg(cFaint)})
			}
			rows = append(rows, sub)
		}
		current := r.Facts != nil
		now := usageOf(model.Observation{Evidence: "reported", Outcome: "ok", Facts: r.Facts})
		caps := capacities(r.Facts)
		_, use := usageSlots(x, m.trendSamples(r.Check), m.Now, w)
		for i, mt := range metrics(h) {
			vals := make([]float64, len(use))
			for k, u := range use {
				vals[k] = mt.of(u)
			}
			v := mt.of(now)
			line := cardRow{left: []span{{fit(mt.name, 8), fg(cMuted)}}, hit: hit}
			if current && !math.IsNaN(v) {
				st := fg(loadColor(v, mt.limit)).bold()
				line.left = append(line.left, span{pct(v, 1), st})
				if caps[i] != "" {
					line.left = append(line.left, span{"  " + caps[i], fg(Mix(cMuted, cText, .3))})
				}
			} else {
				line.left = append(line.left, span{"—", fg(cFaint)})
			}
			aside := []string{}
			if peak := maxOf(vals); !math.IsNaN(peak) {
				aside = append(aside, "peak "+pct(peak, 0))
			}
			if mt.limit > 0 && mt.limit < 100 {
				aside = append(aside, "limit "+pct(mt.limit, 0))
			}
			if len(aside) > 0 {
				line.right = []span{{strings.Join(aside, " · "), fg(cFaint)}}
			}
			rows = append(rows, line)
			// With nothing read, as in a draft, an empty well is enough.
			tall := 3
			if math.IsNaN(maxOf(vals)) {
				tall = 1
			}
			for _, t := range tank(padNaN(vals, w), tall, mt.limit, current, well) {
				rows = append(rows, cardRow{left: t, hit: hit})
			}
			if i < 2 {
				rows = append(rows, cardRow{})
			}
		}
		if more := readingLines(r.Facts, 8, fg(cMuted)); len(more) > 0 {
			rows = append(rows, cardRow{})
			for _, l := range more {
				rows = append(rows, cardRow{left: l, hit: hit})
			}
		}
		if current {
			named, total := processes(r.Facts, h)
			if named == nil {
				named, total = []span{{total, fg(cMuted)}}, ""
			}
			rows = append(rows, cardRow{}, cardRow{left: named, right: []span{{total, fg(cFaint)}}, hit: hit})
		}
	}
	return rows
}

// sheetEvidence is what a server's state rests on: how it was reached and
// why, over the checks, each leading to its own evidence.
func (m *Model) sheetEvidence(r Row, w int) []cardRow {
	e, ok := m.nodeState(r.ID)
	checks := m.nodeChecks(r.ID)
	if !ok && len(checks) == 0 {
		return nil
	}
	rows := []cardRow{heading("EVIDENCE", len(checks))}
	why := strings.TrimSpace(e.Evidence)
	if e.Reason != "" {
		if why != "" {
			why += " — "
		}
		why += e.Reason
	}
	for _, l := range wrap(why, w) {
		if l != "" {
			rows = append(rows, cardRow{left: []span{{l, fg(cMuted).italic()}}})
		}
	}
	// A row a check: its state and kind in columns, its name, and where
	// it runs from.
	kw := 0
	for _, x := range checks {
		kw = max(kw, width(x.Kind))
	}
	for _, x := range checks {
		left := []span{{string(healthGlyph(x.Health)) + " ", fg(healthColor(x.Health))}, {fit(x.Kind, kw+2), fg(Mix(cAccent2, cMuted, .45))}, {x.Name, fg(cText)}}
		rows = append(rows, cardRow{left: left, right: []span{{x.Observer, fg(cFaint)}}, hit: "goto:check:" + x.ID})
	}
	return rows
}

// nodeState is a server's state in the snapshot.
func (m *Model) nodeState(id string) (model.EntityState, bool) {
	for _, e := range m.Snapshot.Nodes {
		if e.ID == id {
			return e, true
		}
	}
	return model.EntityState{}, false
}

// nodeCheck is a check a server's state rests on, by name, with its kind,
// the observer it runs from and its health in this view.
type nodeCheck struct {
	ID, Name, Kind, Observer, Health string
}

// nodeChecks are the checks a server's state rests on, in its state's order.
func (m *Model) nodeChecks(id string) []nodeCheck {
	e, ok := m.nodeState(id)
	if !ok || len(e.Checks) == 0 {
		return nil
	}
	defs := map[string]model.Check{}
	for _, x := range m.Snapshot.Topology.Checks {
		defs[x.ID] = x
	}
	observers := map[string]string{}
	for _, o := range m.Snapshot.Topology.Observers {
		observers[o.ID] = o.Name
		if o.Name == "" {
			observers[o.ID] = o.ID
		}
	}
	health := map[string]string{}
	for _, cs := range m.Snapshot.Checks {
		health[cs.ID] = cs.Health
	}
	cached := m.Cached()
	out := make([]nodeCheck, 0, len(e.Checks))
	for _, cid := range e.Checks {
		d := defs[cid]
		name := d.Name
		if name == "" {
			name = cid
		}
		hh := health[cid]
		if hh == "" || cached {
			hh = "unknown"
		}
		out = append(out, nodeCheck{ID: cid, Name: name, Kind: d.Kind, Observer: observers[d.Observer], Health: hh})
	}
	return out
}
