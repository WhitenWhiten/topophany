package tui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/view"
)

// Products mode turns the Overview to what its owner ships: a board with a
// row for each line of each product, what the line rests on and how its
// lead check fared, beside the status card and the selected line's
// evidence. Every mark on it is the view's; the board only lays them out.

// productsOverview is whether the Overview shows the products board.
func (m *Model) productsOverview() bool { return m.Tab == tabOverview && m.Products }

// productLine is one row of the board: a line and the product it belongs
// to, and whether it is the product's first shown.
type productLine struct {
	p     *view.Product
	l     view.ProductLine
	first bool
}

// productBoard is the view's products, once a frame while one is drawn.
func (m *Model) productBoard() view.ProductBoard {
	if m.frameBoard != nil {
		return *m.frameBoard
	}
	b := m.view().Products()
	if m.frameRows != nil {
		m.frameBoard = &b
	}
	return b
}

// productLines are the board's lines matching the filter, with their rows:
// a service row each, its evidence the product line's.
func (m *Model) productLines() ([]Row, []productLine) {
	b := m.productBoard()
	rows, lines := []Row{}, []productLine{}
	search := strings.ToLower(m.Search)
	for i := range b.Products {
		p := &b.Products[i]
		first := true
		for _, l := range p.Lines {
			hay := strings.ToLower(p.ID + " " + p.Name + " " + p.Kind + " " + l.ID + " " + l.Name)
			for _, c := range l.Chain {
				hay += " " + strings.ToLower(c.Name+" "+c.Provider+" "+c.Runtime)
			}
			if search != "" && !strings.Contains(hay, search) {
				continue
			}
			r := l.Row
			r.Health, r.Note = l.Health, l.Note
			r.Title = l.Name
			r.Meta = p.Name
			r.Details = view.ProductDetails(m.Now, *p, l)
			rows = append(rows, r)
			lines = append(lines, productLine{p, l, first})
			first = false
		}
	}
	return rows, lines
}

// productsTag is the board's count: its products, and the incidents that
// rest on them.
func (m *Model) productsTag() string {
	b := m.productBoard()
	tag := "· " + plural(len(b.Products), "product")
	if b.Incidents > 0 {
		tag += " · " + plural(b.Incidents, "incident")
	}
	if m.Search != "" {
		tag += " matching “" + m.Search + "”"
	}
	return tag
}

// productsLayout lays the Overview out in Products mode: the board beside
// the status card and the evidence when wide, over them when medium, under
// the status card when small.
func (m *Model) productsLayout(cv *Canvas, x, y, w, h int, sel Row) {
	_, lines := m.productLines()
	title, tag := m.ui("PRODUCTS"), m.productsTag()
	board := func(x, y, w, h int) {
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, title, tag, true)
		m.drawProducts(cv, ix, iy, iw, ih, lines)
	}
	status := func(x, y, w, h int) {
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, m.ui("STATUS"), "", false)
		m.statusCard(cv, ix, iy, iw, ih)
	}
	evidence := func(x, y, w, h int) {
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, "EVIDENCE", "Enter expand", false)
		m.drawDetail(cv, ix, iy, iw, ih, sel, false)
	}
	switch {
	case w >= 140 && h >= 24:
		sw := min(max(w*34/100, 46), 72)
		bw := w - sw
		sh := 11
		if h < 34 {
			sh = 6
		}
		board(x, y, bw, h)
		status(x+bw, y, sw, sh)
		evidence(x+bw, y+sh, sw, h-sh)
	case w >= 90 && h >= 22:
		// The board takes what its lines need and leaves the evidence the
		// rest, but never less than four rows in ten or more than all but
		// the status card's.
		bh := max(min(m.productsNeed(lines)+2, h-12), h*40/100)
		board(x, y, w, bh)
		sw := min(max(w*42/100, 44), 64)
		sh := h - bh
		status(x, y+bh, sw, sh)
		evidence(x+sw, y+bh, w-sw, sh)
	default:
		sh := min(6, h/3)
		status(x, y, w, sh)
		board(x, y+sh, w, h-sh)
	}
}

// productsNeed is how many rows the board wants inside its frame: its
// header, one per line, one more under each line that asks for a look, and
// its foot or its certificates with a gap above.
func (m *Model) productsNeed(lines []productLine) int {
	n := 1
	for _, pl := range lines {
		n++
		if pl.l.NeedsLook() {
			n++
		}
	}
	return n + 2
}

// productCols are the board's column widths for its inner width: the
// product's name and kind, the line's name, the chain, then the lead's
// p50, its recent samples and its share up, which line up down the board.
type productCols struct {
	name, kind, line, chain, p50, recent, up int
	short                                    bool // the chains name their layers short, every line alike
}

const productGap = 2

func (m *Model) layoutProducts(lines []productLine, w int) productCols {
	c := productCols{p50: 6, up: 6}
	for _, pl := range lines {
		c.name = max(c.name, width(pl.p.Name))
		c.kind = max(c.kind, width(pl.p.Kind))
		c.line = max(c.line, width(view.ProductLineName(pl.p.Name, pl.l.Name)))
		if u := lineUptime(pl.l); width(u) > c.up {
			c.up = width(u)
		}
	}
	c.name = min(max(c.name, 6), 20)
	c.kind = min(c.kind, 9)
	c.line = min(max(c.line, 4), 16)
	c.recent = min(24, max(8, w/6))
	// The chain wants room for its providers' marks and its names.
	want := 18
	for _, pl := range lines {
		want = max(want, min(m.chainNeed(pl.l, true), 44))
	}
	// What the chain does not get: the marks, the names, the numbers and
	// the gaps between them.
	fixed := func() int {
		n := 2 + c.name + productGap + 2 + c.line + productGap + productGap + c.p50 + productGap + c.recent + productGap + c.up
		if c.kind > 0 {
			n += c.kind + productGap
		}
		return n
	}
	// The samples give way to the chain's names, down to a third.
	for w-fixed() < want && c.recent > 8 {
		c.recent -= 2
	}
	// A narrow board gives way from the right of what matters least: the
	// kind, then the samples, then the names.
	for w-fixed() < 18 {
		switch {
		case c.kind > 0:
			c.kind = 0
		case c.recent > 6:
			c.recent -= 2
		case c.name > 10:
			c.name--
		case c.line > 6:
			c.line--
		default:
			c.chain = max(6, w-fixed())
			c.short = m.shortChains(lines, func(int) int { return c.chain })
			return c
		}
	}
	c.chain = w - fixed()
	c.short = m.shortChains(lines, func(int) int { return c.chain })
	return c
}

// shortChains is whether the chains name their layers short: when one
// line's full names do not fit in its room, so that every line names the
// same layer the same way.
func (m *Model) shortChains(lines []productLine, room func(i int) int) bool {
	for i, pl := range lines {
		if m.chainNeed(pl.l, false) > room(i) {
			return true
		}
	}
	return false
}

// lineUptime is a line's share up over the longest window its lead holds,
// "" without one or once its lead is not current.
func lineUptime(l view.ProductLine) string {
	if l.Lead == nil || l.Lead.Freshness != "fresh" {
		return ""
	}
	if u := view.LongestUptime(l.Lead.Uptime); u != nil {
		share := view.UptimeShare(*u)
		return strings.Replace(share, " · ", " ", 1)
	}
	return ""
}

// drawProducts draws the board: a header, a row per line with its
// product's name on its first, a row of what the lead says under each line
// that asks for a look, and a foot counting what is left out.
func (m *Model) drawProducts(cv *Canvas, x, y, w, h int, lines []productLine) {
	if h <= 0 {
		return
	}
	b := m.productBoard()
	if len(lines) == 0 {
		msg, hint := "No products: no service belongs to an application.", "Declare applications and assign each service one; topo templates products writes an example."
		switch {
		case m.Search != "":
			msg, hint = "Nothing matches “"+m.Search+"”.", "Esc clears the filter."
		case m.Loading != "" && len(m.Snapshot.Topology.Nodes) == 0:
			msg, hint = "Waiting for the topology…", ""
		}
		cv.Text(x, y, ellipsis(msg, w), fg(cMuted).italic(), w)
		for i, l := range wrap(hint, w) {
			if 1+i < h {
				cv.Text(x, y+1+i, l, fg(cFaint), w)
			}
		}
		return
	}
	c := m.layoutProducts(lines, w)
	if h >= 6 {
		hs := fg(cFaint).bold()
		cx := x + 2
		cv.Text(cx, y, fitHead("PRODUCT", c.name), hs, c.name)
		cx += c.name + productGap
		if c.kind > 0 {
			cx += c.kind + productGap
		}
		cv.Text(cx+2, y, fitHead("LINE", c.line), hs, c.line)
		cx += 2 + c.line + productGap
		cv.Text(cx, y, fitHead("RESTS ON", c.chain), hs, c.chain)
		cx += c.chain + productGap
		cv.Text(cx+c.p50-3, y, "P50", hs, -1)
		cx += c.p50 + productGap
		cv.Text(cx, y, fitHead("RECENT", c.recent), hs, c.recent)
		cx += c.recent + productGap
		cv.Text(cx+c.up-3, y, "UP", hs, -1)
		y++
		h--
	}
	foot := []string{}
	if b.Ungrouped > 0 {
		foot = append(foot, plural(b.Ungrouped, "service")+" in no product")
	}
	more := 0
	for _, p := range b.Products {
		more += p.MoreLines
	}
	if more > 0 {
		foot = append(foot, plural(more, "more line")+" in topo show products")
	}
	if len(foot) > 0 && h > 2 {
		cv.Text(x, y+h-1, ellipsis(strings.Join(foot, " · "), w), fg(cFaint), w)
		h--
	}
	rows := func(i int) int {
		if lines[i].l.NeedsLook() {
			return 2
		}
		return 1
	}
	// What breaks next, where the lines leave room for it and a gap above.
	if certs := boardCertificates(lines); len(certs) > 0 && h-linesHeight(lines, rows) >= 2 {
		m.drawCertificates(cv, x, y+h-1, w, certs)
		h--
	}
	// The selection stays in sight, its look row with it.
	if m.Selected < m.Offset {
		m.Offset = m.Selected
	}
	for {
		used := 0
		for i := m.Offset; i <= m.Selected && i < len(lines); i++ {
			used += rows(i)
		}
		if used <= h || m.Offset >= m.Selected {
			break
		}
		m.Offset++
	}
	cy := y
	for j := m.Offset; j < len(lines) && cy < y+h; j++ {
		pl := lines[j]
		selected := j == m.Selected
		base := Style{}
		n := min(rows(j), y+h-cy)
		if selected {
			base = Style{BG: cSelBG}
			cv.Fill(x-1, cy, w+2, n, base)
			for k := 0; k < n; k++ {
				cv.Set(x-1, cy+k, '▌', fg(cAccent).Bg(cSelBG))
			}
		}
		m.drawProductRow(cv, x, cy, c, pl, base, selected)
		m.hit(x-1, cy, w+2, "row:"+itoa(j))
		cy++
		if n > 1 {
			m.drawProductLook(cv, x, cy, w, c, pl, base)
			m.hit(x-1, cy, w+2, "row:"+itoa(j))
			cy++
		}
	}
	if m.Offset > 0 || linesFitting(lines, m.Offset, h, rows) < len(lines) {
		pos := " " + itoa(m.Selected+1) + "/" + itoa(len(lines)) + " "
		cv.Text(x+w-width(pos), y+h, pos, fg(cFaint), -1)
	}
}

// linesHeight is how many rows every line takes, each taking rows(i).
func linesHeight(lines []productLine, rows func(int) int) int {
	n := 0
	for i := range lines {
		n += rows(i)
	}
	return n
}

// boardCertificates are the soonest-expiring certificates of the shown
// lines' products, each host once, the soonest first.
func boardCertificates(lines []productLine) []view.Certificate {
	out := []view.Certificate{}
	for _, pl := range lines {
		c := pl.p.Certificate
		if c == nil || slices.ContainsFunc(out, func(o view.Certificate) bool { return o.Host == c.Host }) {
			continue
		}
		out = append(out, *c)
	}
	slices.SortStableFunc(out, func(a, b view.Certificate) int { return cmp.Compare(a.Days, b.Days) })
	return out
}

// drawCertificates draws the certificates row: each host and the days left
// on its certificate, soonest first, one inside its check's warning window
// in the warning color, and a count of those that do not fit.
func (m *Model) drawCertificates(cv *Canvas, x, y, w int, certs []view.Certificate) {
	cx := x + cv.Text(x, y, "CERTIFICATES  ", fg(cFaint).bold(), w)
	for i, c := range certs {
		say, st := plural(c.Days, "day"), fg(cMuted)
		switch {
		case c.Days < 0:
			say, st = "expired", fg(healthColor("down"))
		case c.InsideWindow():
			st = fg(healthColor("degraded"))
		}
		sep, rest := "", ""
		if i > 0 {
			sep = " · "
		}
		if left := len(certs) - i - 1; left > 0 {
			rest = " +" + itoa(left)
		}
		if cx+width(sep+c.Host+" "+say+rest) > x+w {
			if i > 0 {
				cv.Text(cx, y, " +"+itoa(len(certs)-i), fg(cFaint), max(0, x+w-cx))
			}
			if i > 0 || cx+width(c.Host+" "+say) > x+w {
				return
			}
		}
		cx += cv.Text(cx, y, sep, fg(cFaint), -1)
		cx += cv.Text(cx, y, c.Host+" ", fg(cMuted), -1)
		cx += cv.Text(cx, y, say, st, -1)
	}
}

// linesFitting is the index past the last line that fits from first in h
// rows, each line taking rows(i).
func linesFitting(lines []productLine, first, h int, rows func(int) int) int {
	n := first
	for used := 0; n < len(lines) && used+rows(n) <= h; n++ {
		used += rows(n)
	}
	return n
}

// drawProductRow draws a line's row: the product's mark, name and kind on
// its first, then the line's mark and name, its chain, and its lead's p50,
// recent samples and share up.
func (m *Model) drawProductRow(cv *Canvas, x, y int, c productCols, pl productLine, base Style, selected bool) {
	p, l := pl.p, pl.l
	cx := x
	if pl.first {
		cv.Set(cx, y, healthGlyph(p.Health), base.Fg(healthColor(p.Health)).bold())
		ns := base.Fg(cText).bold()
		if selected {
			ns = base.Fg(cGlow).bold()
		}
		cv.Text(cx+2, y, ellipsis(p.Name, c.name), ns, c.name)
		if c.kind > 0 && p.Kind != "" {
			cv.Text(cx+2+c.name+productGap, y, ellipsis(p.Kind, c.kind), base.Fg(cFaint), c.kind)
		}
	}
	cx += 2 + c.name + productGap
	if c.kind > 0 {
		cx += c.kind + productGap
	}
	lc := healthColor(l.Health)
	if l.Health == "up" && l.Note == "" {
		lc = Mix(lc, cInk, .35) // up is quieter than trouble
	}
	cv.Set(cx, y, healthGlyph(l.Health), base.Fg(lc))
	ls := base.Fg(cText)
	if selected {
		ls = base.Fg(cGlow).bold()
	}
	cv.Text(cx+2, y, ellipsis(view.ProductLineName(p.Name, l.Name), c.line), ls, c.line)
	cx += 2 + c.line + productGap
	m.drawChain(cv, cx, y, c.chain, l, c.short, base)
	cx += c.chain + productGap
	lead, ok := view.SeenCheck{}, false
	if l.Lead != nil {
		lead, ok = l.Lead.SeenCheck, true
	}
	ms, fresh := p50(lead)
	// The p50, lit where it is over the budget its check declares.
	v, st := latency(ms, ok && fresh), base.Fg(cMuted)
	if ok && fresh {
		st = base.Fg(cText)
		if b := l.Lead.BudgetMS; b > 0 && ms > int64(b) {
			st = base.Fg(cWarn)
		}
	}
	cv.Text(cx+c.p50-width(v), y, v, st, -1)
	cx += c.p50 + productGap
	if ok {
		well := Mix(cInk, cLine, .45)
		if selected {
			well = Mix(cSelBG, cLine, .35)
		}
		sx := cx
		for _, sp := range m.spark(lead, c.recent, well) {
			sx += cv.Text(sx, y, sp.s, sp.st, -1)
		}
	} else {
		cv.Text(cx, y, ellipsis("not monitored", c.recent), base.Fg(cFaint).italic(), c.recent)
	}
	cx += c.recent + productGap
	if u := lineUptime(l); u != "" {
		cv.Text(cx+c.up-width(u), y, u, base.Fg(cMuted), -1)
	} else {
		cv.Text(cx+c.up-1, y, "—", base.Fg(cFaint), -1)
	}
}

// drawChain draws what a line rests on in w cells: each layer's name and
// mark, joined by "›", its names short where the board's are. Where they
// do not fit, the marks alone, and a count of what is left out.
func (m *Model) drawChain(cv *Canvas, x, y, w int, l view.ProductLine, short bool, base Style) {
	if len(l.Chain) == 0 {
		cv.Text(x, y, ellipsis("nothing declared", w), base.Fg(cFaint).italic(), w)
		return
	}
	names := m.chainLabels(l, short)
	if !short && chainWidth(l, names) > w {
		names = m.chainLabels(l, true)
	}
	if chainWidth(l, names) > w {
		names = make([]string, len(l.Chain))
	}
	cx := x
	for i, layer := range l.Chain {
		if cx >= x+w {
			break
		}
		if i > 0 {
			cx += cv.Text(cx, y, " › ", base.Fg(cFaint), max(0, x+w-cx))
		}
		if s := names[i]; s != "" {
			cx += cv.Text(cx, y, s+" ", base.Fg(cMuted), max(0, x+w-cx))
		}
		mc := healthColor(layer.Health)
		if layer.Health == "up" {
			mc = Mix(mc, cInk, .35)
		}
		if cx < x+w {
			cv.Set(cx, y, healthGlyph(layer.Health), base.Fg(mc))
		}
		cx++
	}
	if l.MoreLayers > 0 && cx+2 <= x+w {
		cv.Text(cx+1, y, "+"+itoa(l.MoreLayers), base.Fg(cFaint), max(0, x+w-cx-1))
	}
}

// chainLabels names each layer of a line's chain, in full or short.
func (m *Model) chainLabels(l view.ProductLine, short bool) []string {
	out := []string{}
	for _, layer := range l.Chain {
		out = append(out, m.chainLabel(layer, short))
	}
	return out
}

// chainWidth is how many cells a chain takes with names: each name and a
// space before its mark, " › " between layers, and the count of the rest.
func chainWidth(l view.ProductLine, names []string) int {
	n := 0
	for i, s := range names {
		if i > 0 {
			n += 3
		}
		n++
		if s != "" {
			n += width(s) + 1
		}
	}
	if l.MoreLayers > 0 {
		n += 1 + width("+"+itoa(l.MoreLayers))
	}
	return n
}

// chainNeed is how many cells a line's chain takes with its full names or
// its short ones.
func (m *Model) chainNeed(l view.ProductLine, short bool) int {
	return chainWidth(l, m.chainLabels(l, short))
}

// chainLabel names a layer in a chain: in full, or short, where an edge's
// or a platform's logo or mark stands in for its provider's name ("CF
// workers"), and a machine or a dependency goes by its name's first word or
// its name cut short. A mark shaped like a state, as Vercel's ▲ is, would
// read as one beside the layers' marks, so its provider's name stands.
func (m *Model) chainLabel(l view.Layer, short bool) string {
	if !short {
		return view.LayerLabel(l)
	}
	if p, ok := view.LookupProvider(l.Provider); ok && (l.Role == "edge" || l.Role == "platform") {
		mark := p.Mark
		if g := m.glyph(l.Provider); g != 0 {
			mark = string(g)
		} else if mark == "" || strings.ContainsAny(mark, stateShapes) {
			mark = p.Name
		}
		if l.Role == "platform" && l.Runtime != "" {
			return mark + " " + l.Runtime
		}
		return mark
	}
	name := l.Name
	if l.Role == "edge" || l.Role == "platform" {
		name = view.LayerLabel(l)
	}
	if first, _, ok := strings.Cut(name, " "); ok && l.Role != "dependency" {
		name = first
	}
	return ellipsis(name, 12)
}

// stateShapes are the marks of the states, which nothing else may wear.
const stateShapes = "●▲✖○◌"

// drawProductLook draws the row under a line that asks for a look: its
// state word, then what lookSays says of it.
func (m *Model) drawProductLook(cv *Canvas, x, y, w int, c productCols, pl productLine, base Style) {
	cx := x + 2 + c.name + productGap
	if c.kind > 0 {
		cx += c.kind + productGap
	}
	cx += 2
	cx += cv.Text(cx, y, "└ ", base.Fg(cFaint), -1)
	word, st := lookWord(pl.l)
	cx += cv.Text(cx, y, word, base.Merge(st).bold(), max(0, x+w-cx))
	if room := x + w - cx; room > 3 {
		if text := m.lookSays(pl, room-3); text != "" {
			cv.Text(cx, y, " · "+text, base.Fg(cMuted), room)
		}
	}
}

// lookWord is the word a line that asks for a look leads with, as
// view.LookWord has it, lit as the Services tab lights its state word.
func lookWord(l view.ProductLine) (string, Style) {
	word := view.LookWord(l)
	r := l.Row
	r.Health, r.Note = l.Health, l.Note
	if w, st := stateWord(r); w == word {
		return w, st
	}
	h := l.Health
	if view.LineState(l) == "" && l.Lead != nil {
		h = l.Lead.Health
	}
	return word, fg(healthColor(h))
}

// lookSays is what a line that asks for a look says in room cells, as
// view.ProductLook has it. Where it does not all fit, where it was seen from
// goes first, then what the lead says is cut short or, too short to read,
// left out: how long and the incidents stay.
func (m *Model) lookSays(pl productLine, room int) string {
	k := view.ProductLook(m.Now, *pl.p, pl.l)
	text := strings.Join(k.Parts(), " · ")
	if width(text) > room {
		k.Seen = ""
		text = strings.Join(k.Parts(), " · ")
	}
	if over := width(text) - room; over > 0 && k.Says != "" {
		if left := width(k.Says) - over; left >= 8 {
			k.Says = ellipsis(k.Says, left)
		} else {
			k.Says = ""
		}
		text = strings.Join(k.Parts(), " · ")
	}
	return ellipsis(text, room)
}
