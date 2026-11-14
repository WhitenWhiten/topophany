//go:build !nodevice

package tui

import (
	"strings"
	"time"
)

// The layouts of a device's screen. Each tab has what is looked at (the
// map, the evidence, a server's open card) and what is touched (the list
// of what needs attention, the rows, the controls); a layout decides where
// each goes from the page's shape and the crease:
//
//   - compact: a phone's page, one column. The tabs go in a sidebar rail,
//     or in iPhone Duo's clock column on its cover; a tap opens a row as a
//     page of its own, with a way back.
//   - wide: a phone on its side: the rail, then the looked-at pane beside
//     the touched one.
//   - spread: iPhone Duo open sideways. A leaf each side of the crease, the
//     crease the gutter: the looked-at on the left, the touched on the
//     right beside the tabs in the column. Lying flat the gutter stays where
//     the crease will be, so a bend reflows nothing.
//   - stacked: iPhone Duo open upright. The looked-at above the crease, the
//     touched below it with the tabs at the foot: part folded like a
//     laptop, the screen and the surface.
//   - regular: a tablet. The terminal's own layouts, with touch chrome: tabs
//     a finger can hit and a toolbar in place of the key bar.

// compactLayout reports whether cards are laid out for a phone's page.
func (m *Model) compactLayout() bool { return m.touch.layout == layoutCompact }

// drawScreen draws the device's screen: the TUI laid out for the page,
// and over it what the system draws (the status bar or the clock, the
// Dynamic Island, the home indicator).
func (m *Model) drawScreen(s display) *Canvas {
	cv := NewCanvas(s.w, s.h)
	cv.Hyperlinks = m.Hyperlinks
	if !m.Transparent {
		cv.Base = cInk
	}
	page := s.page
	m.stage = &page
	m.touch.layout = s.layout()
	m.touch.mapRect = rect{}
	if m.waiting() {
		m.splash(cv, page.x, page.y, page.w, page.h)
	} else {
		m.splashAt = time.Time{}
		switch m.touch.layout {
		case layoutCompact:
			m.compact(cv, s)
		case layoutWide:
			m.wide(cv, s)
		case layoutSpread:
			m.spread(cv, s)
		case layoutStacked:
			m.stacked(cv, s)
		default:
			m.regular(cv, s)
		}
	}
	if m.touch.drawer {
		m.drawer(cv, page)
	}
	if m.modal != nil {
		m.drawModalOver(cv)
	}
	m.system(cv, s)
	return cv
}

// selection is the current tab's rows, the selection kept in range, and
// the list panel's tag.
func (m *Model) selection() ([]Row, Row, string) {
	rows := m.Rows()
	if m.Selected >= len(rows) {
		m.Selected = max(0, len(rows)-1)
	}
	sel := Row{}
	if len(rows) > 0 {
		sel = rows[m.Selected]
	}
	tag := "· " + itoa(len(rows))
	if m.Search != "" {
		tag += " matching “" + m.Search + "”"
	}
	return rows, sel, tag
}

// mapTab reports whether the tab shows a map: the Overview does, but in
// Products mode, where the board takes the map's place.
func (m *Model) mapTab() bool { return m.Tab == tabOverview && !m.Products || m.Tab == tabTopology }

// listTitle is the touched pane's title for the current tab.
func (m *Model) listTitle() string {
	switch m.Tab {
	case tabOverview:
		if m.Products {
			return m.ui("PRODUCTS")
		}
		return "NEEDS ATTENTION"
	case tabTopology:
		return "NODES · LINKS"
	}
	return strings.ToUpper(tabs[m.Tab])
}

// --- compact ----------------------------------------------------------------

// railW is a phone's sidebar rail: a glyph and its badge, a finger wide.
const railW = 6

// columnRail puts the tabs in iPhone Duo's column, where the system draws
// its bar: under the clock (and on the cover its camera), a finger tall
// each and named.
func (m *Model) columnRail(cv *Canvas, s display) {
	c := s.column
	cv.Fill(c.x, s.clock.bottom(), c.w, c.bottom()-s.clock.bottom(), Style{BG: cInkHi})
	top := s.clock.bottom() + 1
	m.tabRail(cv, rect{c.x, top, c.w, c.bottom() - top})
}

// cornerPad is how far a bar along the top keeps from the screen's left
// corner where no inset keeps it away: the glass rounds there. A fitted
// screen is a page in a browser, square at its corners.
func cornerPad(s display) int {
	if s.page.x == 0 && s.page.y == 0 && !s.Fit {
		return 2
	}
	return 0
}

func (m *Model) compact(cv *Canvas, s display) {
	p := s.page
	content := p
	m.appBar(cv, p.x, p.y, p.w, cornerPad(s))
	if !s.column.empty() {
		m.columnRail(cv, s)
	} else {
		rail := rect{p.x, p.y + 1, railW, p.h - 1}
		m.sideRail(cv, rail)
		content = rect{p.x + railW, p.y, p.w - railW, p.h}
	}
	y := p.y + 1
	y += m.viewChips(cv, content.x+1, y, content.w-1)
	m.phone(cv, rect{content.x, y, content.w, content.bottom() - y})
}

// sideRail is a phone's sidebar: the drawer's button over the tabs.
func (m *Model) sideRail(cv *Canvas, r rect) {
	cv.Fill(r.x, r.y, r.w, r.h, Style{BG: cInkHi})
	for k := r.y; k < r.bottom(); k++ {
		cv.Set(r.right()-1, k, '▕', fg(cLine).Bg(cInkHi))
	}
	cv.Set(r.x+max(1, (r.w-4)/2), r.y+1, '≡', fg(cText).Bg(cInkHi).bold())
	m.hitArea(r.x, r.y, r.w, touchRows, "touch:drawer")
	m.tabRail(cv, rect{r.x, r.y + touchRows + 1, r.w - 1, r.h - touchRows - 1})
}

// phone lays out the current tab in a phone's column r.
func (m *Model) phone(cv *Canvas, r rect) {
	rows, sel, tag := m.selection()
	title := strings.ToUpper(tabs[m.Tab])
	switch {
	case m.Tab == tabAbout:
		kh := min(18, r.h/3)
		ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, r.h-kh, "ABOUT", "", false)
		m.drawAbout(cv, ix-1, iy, iw+2, ih)
		m.drawAboutKeys(cv, rect{r.x, r.bottom() - kh, r.w, kh})
	case m.Detail && m.Tab == tabServers && len(rows) > 0:
		m.backBar(cv, r.x+1, r.y, r.w-1, len(rows))
		m.drawSheet(cv, r.x, r.y+touchRows, r.w, r.h-touchRows, sel)
	case m.Detail && m.serviceCards() && len(rows) > 0:
		m.backBar(cv, r.x+1, r.y, r.w-1, len(rows))
		m.drawServiceSheet(cv, r.x, r.y+touchRows, r.w, r.h-touchRows, sel)
	case m.Detail:
		m.backBar(cv, r.x+1, r.y, r.w-1, len(rows))
		y := r.y + touchRows
		if m.focus(sel).any() && r.h >= 44 {
			// Where it is, before the evidence for it: the map with what
			// the row lights up, the route or the node.
			mh := min(22, r.h*2/5)
			m.mapPanel(cv, rect{r.x, y, r.w, mh}, "WHERE", sel, false)
			y += mh
		}
		ix, iy, iw, ih := m.panel(cv, r.x, y, r.w, r.bottom()-y, "EVIDENCE", "", true)
		m.drawDetail(cv, ix, iy, iw, ih, sel, true)
	case m.FullMap && m.mapTab():
		m.mapPanel(cv, r, title, sel, true)
	case m.Tab == tabOverview:
		sh := 6
		ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, sh, "STATUS", "", false)
		m.statusCard(cv, ix, iy, iw, ih)
		mh := 0
		if !m.Products { // in Products mode the board takes the map's place
			mh = (r.h - sh) * 44 / 100
			m.mapPanel(cv, rect{r.x, r.y + sh, r.w, mh}, "NETWORK", sel, true)
		}
		m.listPanel(cv, rect{r.x, r.y + sh + mh, r.w, r.h - sh - mh}, rows, tag, true)
	case m.Tab == tabTopology:
		mh := r.h * 56 / 100
		m.mapPanel(cv, rect{r.x, r.y, r.w, mh}, title, sel, true)
		m.listPanel(cv, rect{r.x, r.y + mh, r.w, r.h - mh}, rows, tag, true)
	case m.Tab == tabServers:
		ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, r.h, title, tag, true)
		m.drawCompactCards(cv, ix, iy, iw, ih, rows, true)
	case m.serviceCards():
		y := r.y + m.listTools(cv, r.x+1, r.y, r.w-1)
		ix, iy, iw, ih := m.panel(cv, r.x, y, r.w, r.bottom()-y, title, tag, true)
		m.drawCompactCards(cv, ix, iy, iw, ih, rows, true)
	default:
		y := r.y + m.listTools(cv, r.x+1, r.y, r.w-1)
		m.listPanel(cv, rect{r.x, y, r.w, r.bottom() - y}, rows, tag, true)
	}
}

// listPanel is the touched list in a panel; open makes a tap open a row.
func (m *Model) listPanel(cv *Canvas, r rect, rows []Row, tag string, open bool) {
	if m.productsOverview() {
		_, lines := m.productLines()
		ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, r.h, m.listTitle(), m.productsTag(), true)
		m.touchProducts(cv, ix, iy, iw, ih, lines, open)
		return
	}
	ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, r.h, m.listTitle(), tag, true)
	if m.Tab == tabOverview {
		m.attentionCard(cv, ix, iy, iw, ih, rows, func(cv *Canvas, x, y, w, h int, rows []Row) { m.touchList(cv, x, y, w, h, rows, open) })
		return
	}
	m.touchList(cv, ix, iy, iw, ih, rows, open)
}

// listTools are the controls over a list: the filter's field and, where
// the services and checks fold into applications, the way to browse them.
// It returns the rows they took.
func (m *Model) listTools(cv *Canvas, x, y, w int) int {
	fw := w
	if m.businessTab() && len(m.Snapshot.Topology.Applications) > 0 {
		label := "▤ Browse ▾"
		fw -= width(label) + 5
		m.chip(cv, x+fw+1, y, label, "key:b", m.Application != "" || m.Environment != "" || m.AttentionOnly)
	}
	m.field(cv, x, y, fw, "Filter "+strings.ToLower(tabs[m.Tab])+"…")
	return touchRows
}

// compactCard is a card as a phone lists it. A server's: the band naming
// it, its state, kind and place, over what runs on it, how it is reached
// and what measures it. A service's: the band naming it, its state and
// where it runs, over who sees it from where.
func (m *Model) compactCard(f fleet, r Row, w int, selected bool) []cardRow {
	if m.serviceCards() {
		c, _ := f.ServiceCard(r.ID)
		rows, _ := m.serviceHead(f, c, r, selected)
		// A list of cards need not keep the grid's height: the panel goes
		// without the rows that only pad it, a blank row over it.
		panel := m.seenPanel(c, w)
		blank := func(r cardRow) bool { return r.left == nil && r.right == nil }
		for len(panel) > 0 && blank(panel[len(panel)-1]) {
			panel = panel[:len(panel)-1]
		}
		if len(panel) > 0 && !blank(panel[0]) {
			rows = append(rows, cardRow{})
		}
		return append(rows, panel...)
	}
	c := f.Card(r.ID)
	rows, _ := m.cardHead(f, r, c, selected)
	return append(rows, m.tallies(f, r, c, w)...)
}

// drawCompactCards lists the servers' short cards one under another, as a
// phone does, scrolled so the selection shows; open makes a tap open a card.
func (m *Model) drawCompactCards(cv *Canvas, x, y, w, h int, rows []Row, open bool) {
	if len(rows) == 0 || w < 24 {
		if m.serviceCards() {
			m.drawServiceCards(cv, x, y, w, h, rows)
			return
		}
		m.drawServers(cv, x, y, w, h, rows)
		return
	}
	f := m.fleet()
	tops := make([]int, len(rows))
	cards := make([][]cardRow, len(rows))
	total := 0
	for i, r := range rows {
		cards[i] = m.compactCard(f, r, w-4, i == m.Selected)
		tops[i] = total
		total += len(cards[i]) + 3
	}
	total--
	sel := min(max(m.Selected, 0), len(rows)-1)
	if top := tops[sel]; top < m.cardsTop {
		m.cardsTop = top
	} else if bottom := top + len(cards[sel]) + 2; bottom > m.cardsTop+h {
		m.cardsTop = bottom - h
	}
	m.cardsTop = min(max(m.cardsTop, 0), max(0, total-h))
	for i, r := range rows {
		top, ch := tops[i]-m.cardsTop, len(cards[i])+2
		if top+ch <= 0 || top >= h {
			continue
		}
		first := len(m.hits)
		m.drawCard(cv, x, y+top, w, cards[i], r, i, y, y+h)
		y0, y1 := max(y, y+top), min(y+h, y+top+ch)
		if y1-y0 < touchRows {
			// A sliver of a card at the edge is no target for a finger:
			// scrolling brings the card in.
			m.hits = m.hits[:first]
			continue
		}
		if open {
			m.hitArea(x, y0, w, y1-y0, "touch:open:"+itoa(i))
		}
	}
	if total > h {
		scrollThumb(cv, x+w+1, y, h, total, m.cardsTop, fg(Mix(cLine, cAccent, .45)))
	}
}

// --- wide -------------------------------------------------------------------

func (m *Model) wide(cv *Canvas, s display) {
	p := s.page
	if all := len(tabs) * touchRows; all <= p.h {
		// The drawer comes out with a swipe from the edge; the rail fills
		// the height with the tabs alone.
		cv.Fill(p.x, p.y, railW, p.h, Style{BG: cInkHi})
		m.tabRail(cv, rect{p.x, p.y + (p.h-all)/2, railW - 1, all})
	} else {
		// Too short for them all: the drawer's button over those that fit.
		m.sideRail(cv, rect{p.x, p.y, railW, p.h})
	}
	c := rect{p.x + railW, p.y, p.w - railW, p.h}
	m.appBar(cv, c.x, c.y, c.w, 0)
	body := rect{c.x, c.y + 1, c.w, c.h - 1}
	lw := body.w * 56 / 100
	m.twoPanes(cv, rect{body.x, body.y, lw, body.h}, rect{body.x + lw, body.y, body.w - lw, body.h})
}

// --- spread and stacked -----------------------------------------------------

func (m *Model) spread(cv *Canvas, s display) {
	p := s.page
	left := rect{p.x, p.y, s.crease.x - p.x, p.h}
	right := rect{s.crease.right(), p.y, p.right() - s.crease.right(), p.h}
	m.appBar(cv, p.x, p.y, p.w, cornerPad(s))
	m.columnRail(cv, s)
	if m.FullMap && m.mapTab() {
		// The map alone, by choice: across the crease, its controls on the
		// right leaf, clear of the fold.
		m.mapPanel(cv, p.cut(1, 0), strings.ToUpper(tabs[m.Tab]), m.selected(), false)
		m.mapControls(cv, right.right()-14, p.y+2)
		return
	}
	m.twoPanes(cv, left.cut(1, 0), right.cut(1, 0))
}

func (m *Model) stacked(cv *Canvas, s display) {
	p := s.page
	upper := rect{p.x, p.y, p.w, s.crease.y - p.y}
	lower := rect{p.x, s.crease.bottom(), p.w, p.bottom() - s.crease.bottom()}
	m.appBar(cv, upper.x, upper.y, upper.w, 0)
	m.tabDock(cv, rect{lower.x, lower.bottom() - touchRows, lower.w, touchRows})
	lower.h -= touchRows
	if m.FullMap && m.mapTab() {
		m.mapPanel(cv, rect{p.x, p.y + 1, p.w, lower.bottom() - p.y - 1}, strings.ToUpper(tabs[m.Tab]), m.selected(), false)
		m.mapControls(cv, p.right()-14, lower.bottom()-touchRows)
		return
	}
	m.twoPanes(cv, upper.cut(1, 0), lower)
}

// selected is the selected row of the current tab.
func (m *Model) selected() Row {
	_, sel, _ := m.selection()
	return sel
}

// twoPanes lays the current tab out as what is looked at in look and what
// is touched in touch, the controls at the top of touch.
func (m *Model) twoPanes(cv *Canvas, look, touch rect) {
	rows, sel, tag := m.selection()
	m.lookPane(cv, look, rows, sel)
	r := touch.cut(m.paneTools(cv, touch.x+1, touch.y, touch.w-1), 0)
	switch {
	case m.Tab == tabAbout:
		m.drawAboutKeys(cv, r)
	case m.Tab == tabOverview && r.h >= 22 && r.w >= 80:
		// Room for the status card beside what needs attention: its big
		// number, and under it the observers.
		sw := min(46, r.w*46/100)
		sh := min(11, r.h-6)
		ix, iy, iw, ih := m.panel(cv, r.x, r.y, sw, sh, "STATUS", "", false)
		m.statusCard(cv, ix, iy, iw, ih)
		ix, iy, iw, ih = m.panel(cv, r.x, r.y+sh, sw, r.h-sh, "OBSERVERS", "", false)
		m.observersCard(cv, ix, iy, iw, ih)
		m.listPanel(cv, rect{r.x + sw, r.y, r.w - sw, r.h}, rows, tag, false)
	case m.Tab == tabOverview:
		sh := 6
		if r.h >= 30 {
			sh = 11
		}
		ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, sh, "STATUS", "", false)
		m.statusCard(cv, ix, iy, iw, ih)
		m.listPanel(cv, rect{r.x, r.y + sh, r.w, r.h - sh}, rows, tag, false)
	case m.Tab == tabServers || m.serviceCards():
		ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, r.h, strings.ToUpper(tabs[m.Tab]), tag, true)
		m.drawCompactCards(cv, ix, iy, iw, ih, rows, false)
	default:
		m.listPanel(cv, r, rows, tag, false)
	}
}

// paneTools are the controls at the top of the touched pane: the view and
// the scenario, the filter and, beside a map, its views and zoom. It
// returns the rows they took.
func (m *Model) paneTools(cv *Canvas, x, y, w int) int {
	if m.Tab == tabAbout {
		return m.viewChips(cv, x, y, w)
	}
	used := 0
	if m.mapTab() {
		segs, at := m.mapSegments()
		need := 0
		for _, s := range segs {
			need += width(s.label) + 3
		}
		if w >= need+1+14+28 {
			// The map's controls, clear of the map itself: on the half
			// a finger rests on.
			n := m.segmented(cv, x+w-need-1-14, y, segs, at)
			m.mapControls(cv, x+w-13, y)
			w -= n + 15
		}
		m.viewChips(cv, x, y, w)
		return touchRows
	}
	m.viewChips(cv, x, y, w)
	used += touchRows
	return used + m.listTools(cv, x, y+used, w)
}

// lookPane is what the tab shows to be looked at: the map, a server's open
// card, the evidence for the selection.
func (m *Model) lookPane(cv *Canvas, r rect, rows []Row, sel Row) {
	switch {
	case m.Tab == tabAbout:
		ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, r.h, "ABOUT", "· the appearance of place", false)
		m.drawAbout(cv, ix-1, iy, iw+2, ih)
	case m.mapTab() && !m.Detail:
		title := "NETWORK"
		if m.Tab == tabTopology {
			title = "TOPOLOGY"
		}
		m.mapPanel(cv, r, title, sel, m.touch.layout != layoutStacked)
	case m.Tab == tabServers && len(rows) > 0:
		m.drawSheet(cv, r.x, r.y, r.w, r.h, sel)
	case m.serviceCards() && len(rows) > 0:
		m.drawServiceSheet(cv, r.x, r.y, r.w, r.h, sel)
	default:
		hint := ""
		if m.mapTab() {
			hint = "tap for the map"
		}
		ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, r.h, "EVIDENCE", hint, false)
		m.drawDetail(cv, ix, iy, iw, ih, sel, m.Detail)
		if m.mapTab() {
			m.hitArea(r.x, r.y, r.w, r.h, "touch:back")
		}
	}
}

// drawAboutKeys says, where About's text stands beside the globe, how to
// get about by touch.
func (m *Model) drawAboutKeys(cv *Canvas, r rect) {
	ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, r.h, "BY TOUCH", "", true)
	lines := []vline{
		{{"Tap", fg(cAccent).bold()}, {" a row to select it, and again to open it.", fg(cText)}},
		{{"Pinch", fg(cAccent).bold()}, {" the map to magnify it; drag it to look around.", fg(cText)}},
		{{"Swipe", fg(cAccent).bold()}, {" a list to scroll it, and down at its top to refresh.", fg(cText)}},
		{{"◎ ▾", fg(cAccent).bold()}, {" picks the observer the network is viewed from.", fg(cText)}},
		{{"⋯", fg(cAccent).bold()}, {" has every action the keyboard has.", fg(cText)}},
	}
	y := iy
	for _, l := range lines {
		cx := ix
		for _, sp := range l {
			for i, part := range wrap(sp.s, ix+iw-cx) {
				if y+i >= iy+ih {
					break
				}
				if i > 0 {
					y++
					cx = ix + 2
				}
				cx += cv.Text(cx, y, part, sp.st, ix+iw-cx)
			}
		}
		y += 2
	}
}

// --- regular ----------------------------------------------------------------

func (m *Model) regular(cv *Canvas, s display) {
	p := s.page
	// The terminal's own header, the view and the scenario named in it;
	// under it the tabs a finger can hit, and the chips for the view and
	// the scenario where the tabs leave the room.
	m.header(cv, p.x, p.y, p.w)
	y := p.y + 1
	end := m.touchTabs(cv, p.x+1, y, p.w-2)
	if left := p.right() - 1 - (end + 2); left >= 5 {
		m.viewChips(cv, end+2, y, left)
	}
	y += touchRows
	m.statusBar(cv, p.x, y, p.w)
	y++
	bottom := p.bottom() - touchRows
	m.body(cv, p.x, y, p.w, bottom-y)
	m.toolbar(cv, p.x+1, bottom, p.w-2)
}

// toolbar is the key bar for fingers: a chip for each action the footer
// would offer, as many as fit, and the rest under ⋯.
func (m *Model) toolbar(cv *Canvas, x, y, w int) {
	need := func(a action) int { return width(a.label) + width(a.key) + 7 }
	all := m.touchActions()
	shown := fitActions(all, w-8, need)
	cx := x
	for _, a := range shown {
		cx += m.keyChip(cv, cx, y, a.keycap) + 1
	}
	if len(shown) < len(all) {
		m.chip(cv, x+w-5, y, "⋯", "touch:more", false)
	}
}

// keyChip is a chip for an action that names its key too, for the
// keyboard a tablet may have.
func (m *Model) keyChip(cv *Canvas, x, y int, k keycap) int {
	w := m.chip(cv, x, y, k.label+"  "+k.key, "key:"+k.action, false)
	cv.Text(x+2+width(k.label)+2, y+1, k.key, fg(Mix(cAccent, cInk, .35)), -1)
	return w
}

// touchActions are the actions a touch screen offers as controls: those
// the footer offers with something to do, but quitting, which a phone's
// system does itself, the debug overlay, and the editor's hints, as a
// device views and never edits. The uniform cards are offered where the
// wall of cards is drawn, on a tablet.
func (m *Model) touchActions() []action {
	out := []action{}
	for _, a := range m.actions() {
		switch a.action {
		case "", "q", "D":
			continue
		case "a", "e", "d":
			if m.Edit == nil {
				continue
			}
		case "s":
			if m.touch.layout != layoutRegular {
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// moreMenu offers every action the footer would, as a menu a finger can
// pick from, so no screen loses one.
func (m *Model) moreMenu() {
	as := m.touchActions()
	items := make([]menuItem, len(as))
	for i, a := range as {
		items[i] = menuItem{key: a.key, label: a.label}
	}
	m.modal = &modal{kind: "menu", title: "Actions", items: items, pick: func(i int) string {
		return m.Key(as[i].action)
	}}
}
