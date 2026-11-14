package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func (m *Model) profile() int {
	if m.NoColor {
		return colorNone
	}
	return m.Colors
}

// Frame is a full repaint; the run loop uses Render and repaints changed lines.
func (m *Model) Frame() string {
	lines := m.Render()
	if m.Width < 30 || m.Height < 10 {
		return "\x1b[H" + lines[0] + "\x1b[J"
	}
	return "\x1b[H" + strings.Join(lines, "\r\n")
}

// title names the terminal window after the verdict, so a tab bar or a
// taskbar says whether anything is wrong while the UI is out of sight.
func (m *Model) title() string {
	t := ""
	if m.Edit != nil {
		t = "✎ " + filepath.Base(m.Edit.Path)
		if m.Edit.Dirty {
			t += " ●"
		}
	} else {
		word, _ := m.verdict()
		glyph, rest, _ := strings.Cut(word, " ")
		if m.Loading != "" {
			glyph = "◌" // the spinner turns too fast for a title
		}
		t = glyph + " " + strings.ToLower(rest)
		if p := m.Snapshot.Project; p != "" {
			t += " · " + p
		}
	}
	return ellipsis(secure.Text(t), 100) + " · Topophany"
}

func (m *Model) Render() []string {
	cv := m.draw()
	if cv == nil {
		return []string{fit("Topophany: resize terminal (30x10 minimum)", max(m.Width, 1))}
	}
	return cv.Lines(m.profile())
}

// draw paints one frame, or returns nil when the terminal is too small.
func (m *Model) draw() *Canvas {
	var rows []Row
	m.frameRows = &rows
	defer func() { m.frameRows = nil }()
	w, h := m.Width, m.Height
	m.hits = m.hits[:0]
	if w < 30 || h < 10 {
		return nil
	}
	m.absorb()
	if m.Device != nil {
		return m.simulate()
	}
	cv := NewCanvas(w, h)
	cv.Hyperlinks = m.Hyperlinks
	if !m.Transparent {
		cv.Base = cInk
	}
	m.stage = nil
	m.header(cv, 0, 0, w)
	m.tabBar(cv, 0, 1, w)
	top := 3
	if m.waiting() {
		// Nothing to lay out yet: the splash says what is happening.
		m.splash(cv, 0, 2, w, h-3)
		m.footer(cv, 0, h-1, w)
		m.modalOver(cv)
		return cv
	}
	m.splashAt = time.Time{}
	if m.Tab == tabOverview && !m.hasNews() && !m.FullMap && m.Snapshot.Ingress == nil {
		// The Overview's status card says everything the status row would;
		// there the row is kept only for notices and errors, and while the
		// map alone has the card's place. The card has no room to say that
		// evidence is not arriving either, so the row returns for that.
		top = 2
	} else {
		m.statusBar(cv, 0, 2, w)
	}
	m.body(cv, 0, top, w, h-1-top)
	m.footer(cv, 0, h-1, w)
	if m.Debug {
		m.drawDebug(cv)
	}
	m.modalOver(cv)
	return cv
}

// modalOver draws the open modal, if any, over the frame. The footer is
// then the modal's own keys, the only ones that mean anything while it is
// open, so it is lit again rather than dimmed with the rest.
func (m *Model) modalOver(cv *Canvas) {
	if m.modal == nil {
		return
	}
	m.drawModalOver(cv)
	m.footer(cv, 0, cv.H-1, cv.W)
}

// drawModalOver dims the frame and draws the open modal over it.
func (m *Model) drawModalOver(cv *Canvas) {
	{
		cv.Each(func(s Style) Style {
			c, b := s.FG, s.BG
			if c == 0 {
				c = cText
			}
			if b == 0 {
				b = cInk
			}
			s = Style{FG: Mix(c, cInk, .66), BG: Mix(b, cInk, .66)}
			if m.Transparent && s.BG == cInk {
				s.BG = 0
			}
			return s
		})
		m.drawingModal = true
		m.drawModal(cv)
		m.drawingModal = false
	}
}

func (m *Model) body(cv *Canvas, x, y, w, h int) {
	rows := m.Rows()
	if m.Selected >= len(rows) {
		m.Selected = max(0, len(rows)-1)
	}
	sel := Row{}
	if len(rows) > 0 {
		sel = rows[m.Selected]
	}
	title := strings.ToUpper(tabs[m.Tab])
	tag := "· " + itoa(len(rows))
	if m.businessTab() {
		groups, members := 0, 0
		for _, r := range rows {
			if r.Kind == "group" {
				groups++
			} else {
				members++
			}
		}
		if m.serviceCards() {
			groups = len(m.serviceGroups(rows))
		}
		if groups > 0 {
			tag = "· " + plural(groups, "group") + " · " + itoa(members) + " shown"
		}
	}
	if m.Search != "" {
		tag += " matching “" + m.Search + "”"
	}
	// The cards stay where they were while one lies open over them.
	if m.businessTab() && (!m.Detail || m.serviceCards()) {
		cv.Text(x+1, y, ellipsis(m.businessStatus()+" · b Browse", w-2), fg(cMuted), w-2)
		m.hit(x+1, y, w-2, "key:b")
		y++
		h--
	}
	if m.Tab == tabAbout {
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, "ABOUT", "· the appearance of place", false)
		m.drawAbout(cv, ix-1, iy, iw+2, ih)
		return
	}
	if m.Detail && m.Tab != tabServers && !m.serviceCards() {
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, title+" · "+sel.Title, "Esc/Enter back · ↑↓ scroll", true)
		m.drawDetail(cv, ix, iy, iw, ih, sel, true)
		return
	}
	if m.FullMap && (m.Tab == tabTopology || m.Tab == tabOverview) {
		m.fullMap(cv, x, y, w, h, rows, sel)
		return
	}
	switch {
	case m.Tab == tabTopology:
		mode := mapNames[m.mapMode()]
		if w >= 120 {
			lw := min(max(w*40/100, 34), 84)
			mw := w - lw
			// In a tall panel, a geo or virtual map takes only the rows it
			// can fill and the evidence moves under it, leaving the list the
			// full height.
			mh := h
			if rows := m.mapRows(mw - 2); rows > 0 && h-rows-2 >= 10 {
				mh = rows + 2
			}
			ix, iy, iw, ih := m.panel(cv, x, y, mw, mh, title+" · "+mode, "m switch · z zoom", false)
			m.mapIn(cv, ix-1, iy, iw+2, ih, sel)
			if mh < h {
				ix, iy, iw, ih = m.panel(cv, x, y+mh, mw, h-mh, "EVIDENCE", "Enter expand", false)
				m.drawDetail(cv, ix, iy, iw, ih, sel, false)
				ix, iy, iw, ih = m.panel(cv, x+mw, y, lw, h, "NODES · LINKS", tag, true)
				m.drawList(cv, ix, iy, iw, ih, rows)
				return
			}
			lh := h
			if h >= 16 {
				lh = h * 55 / 100
			}
			ix, iy, iw, ih = m.panel(cv, x+mw, y, lw, lh, "NODES · LINKS", tag, true)
			m.drawList(cv, ix, iy, iw, ih, rows)
			if lh < h {
				ix, iy, iw, ih = m.panel(cv, x+mw, y+lh, lw, h-lh, "EVIDENCE", "", false)
				m.drawDetail(cv, ix, iy, iw, ih, sel, false)
			}
			return
		}
		if h >= 16 {
			mh := h * 55 / 100
			ix, iy, iw, ih := m.panel(cv, x, y, w, mh, title+" · "+mode, "m · z", false)
			m.mapIn(cv, ix-1, iy, iw+2, ih, sel)
			ix, iy, iw, ih = m.panel(cv, x, y+mh, w, h-mh, "NODES · LINKS", tag, true)
			m.drawList(cv, ix, iy, iw, ih, rows)
			return
		}
	case m.Tab == tabOverview:
		m.overview(cv, x, y, w, h, rows, sel, tag)
		return
	case m.Tab == tabServers:
		// The cards stay where they were while one lies open over them.
		if m.Uniform {
			title += " · uniform"
		}
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, title, tag, !m.Detail)
		m.drawServers(cv, ix, iy, iw, ih, rows)
		if m.Detail && len(rows) > 0 {
			m.drawSheet(cv, ix-1, iy, iw+2, ih, sel)
		}
		return
	case m.serviceCards():
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, title+" · cards", tag, !m.Detail)
		m.drawServiceCards(cv, ix, iy, iw, ih, rows)
		if m.Detail && len(rows) > 0 {
			m.drawServiceSheet(cv, ix-1, iy, iw+2, ih, sel)
		}
		return
	}
	if w >= 100 {
		// The list leads: it gets a little more than half, so names read
		// whole, and the evidence keeps the rest.
		lw := w / 2
		if w >= 120 {
			lw = w * 56 / 100
		}
		ix, iy, iw, ih := m.panel(cv, x, y, lw, h, title, tag, true)
		m.drawList(cv, ix, iy, iw, ih, rows)
		hint := "Enter expand"
		if sel.Kind == "group" {
			hint = "i expand · Enter fold"
		}
		ix, iy, iw, ih = m.panel(cv, x+lw, y, w-lw, h, "EVIDENCE", hint, false)
		m.drawDetail(cv, ix, iy, iw, ih, sel, false)
		return
	}
	ix, iy, iw, ih := m.panel(cv, x, y, w, h, title, tag, true)
	m.drawList(cv, ix, iy, iw, ih, rows)
}

// mapNames are the maps as panel titles name them.
var mapNames = map[string]string{"virtual": "virtual geo", "geo": "geo map", "network": "network graph"}

// fullMap gives the whole body to the map, so its cards and labels have
// the room to say everything. The selection still moves with ↑↓, and the
// panel's foot names it.
func (m *Model) fullMap(cv *Canvas, x, y, w, h int, rows []Row, sel Row) {
	title := strings.ToUpper(tabs[m.Tab]) + " · " + mapNames[m.mapMode()]
	ix, iy, iw, ih := m.panel(cv, x, y, w, h, title, "m switch · z zoom · f panels", false)
	m.mapIn(cv, ix-1, iy, iw+2, ih, sel)
	if sel.Kind == "" || w < 30 {
		return
	}
	// Along the foot: the selection and its state on the left, where it
	// stands in the list on the right.
	fy := y + h - 1
	pos := " " + itoa(m.Selected+1) + "/" + itoa(len(rows)) + " "
	pill, ps := statePill(sel)
	rx := x + w - 2 - width(pos)
	cv.Text(rx, fy, pos, fg(cFaint), -1)
	if rx -= width(pill) + 1; rx-x > 24 {
		cv.Text(rx, fy, pill, ps, -1)
	} else {
		rx += width(pill) + 1
	}
	cx := x + 2
	room := rx - 2 - cx
	sub := "  " + sel.Kind + " · " + sel.ID
	if _, scope, ok := strings.Cut(sel.ID, "/"); ok && sel.Kind == "incident" {
		sub = "  incident · " + scope
	}
	name := " " + strings.TrimSpace(sel.Title)
	if width(name)+width(sub)+1 > room {
		sub = ""
	}
	n := cv.Text(cx, fy, ellipsis(name, room-1), fg(cGlow).bold(), room-1)
	n += cv.Text(cx+n, fy, sub+" ", fg(cFaint), room-n)
	m.hit(cx, fy, n, "key:enter")
}

func (m *Model) drawModal(cv *Canvas) {
	md := m.modal
	bg := Style{BG: cPanelBG}
	switch md.kind {
	case "preferences":
		m.drawPreferences(cv)
	case "info", "confirm":
		mw := width(md.title) + 8
		for _, l := range md.lines {
			mw = max(mw, width(l)+6)
		}
		x, y, w, h := m.modalBox(cv, mw, len(md.lines)+5, md.title)
		for i, l := range md.lines {
			if i >= h-2 {
				break
			}
			st := bg.Fg(cText)
			if strings.HasPrefix(l, "    ") {
				st = bg.Fg(cAccent).bold()
			}
			cv.Text(x, y+1+i, l, st, w)
		}
		by := y + h - 1
		if md.kind == "info" {
			m.button(cv, x+w-8, by, "OK", "btn:ok", true)
		} else {
			n := m.button(cv, x+w-24, by, "y Yes", "btn:yes", true)
			m.button(cv, x+w-24+n+2, by, "Esc No", "btn:no", false)
		}
	case "menu":
		mw, hint, keyed := 60, 0, false
		for _, it := range md.items {
			mw = max(mw, width(it.label)+12)
			if it.hint != "" {
				hint = 4
			}
			keyed = keyed || it.key != ""
		}
		if hint > 0 {
			mw = max(mw, 72)
		}
		x, y, w, h := m.modalBox(cv, mw, len(md.items)+4+hint, md.title)
		if hint > 0 && md.sel < len(md.items) {
			hy := y + len(md.items) + 2
			for i, l := range wrap(md.items[md.sel].hint, w-2) {
				if hy+i >= y+h {
					break
				}
				cv.Text(x+1, hy+i, l, bg.Fg(cAccent2), w-2)
			}
		}
		for i, it := range md.items {
			st := bg.Fg(cText)
			if i == md.sel {
				st = Style{FG: cGlow, BG: cSelBG}.bold()
				cv.Fill(x-1, y+1+i, w+2, 1, st)
				cv.Set(x-1, y+1+i, '▌', fg(cAccent).Bg(cSelBG))
			}
			// Items chosen by key wear it on a keycap; a menu without keys
			// (a list of choices) starts its labels where the caps would.
			lx := x + 1
			if keyed {
				if it.key != "" {
					cv.Text(x+1, y+1+i, " "+it.key+" ", fg(cGlow).Bg(cKeyBG).bold(), -1)
				}
				lx = x + 5
			}
			cv.Text(lx, y+1+i, it.label, st, x+w-1-lx)
			m.hit(x-1, y+1+i, w+2, "menu:"+itoa(i))
		}
	case "form":
		m.drawForm(cv, md.form)
	}
}

func (m *Model) drawForm(cv *Canvas, f *Form) {
	n := len(f.fields)
	// Only the fields on show take rows; the label column is as wide as
	// their longest label, so none runs out of the dialog.
	order := []int{}
	labelW := 0
	for i, fd := range f.fields {
		if f.shown(i) {
			order = append(order, i)
			labelW = max(labelW, width(fd.label))
		}
	}
	x, y, w, h := m.modalBox(cv, 86, len(order)+8, f.title)
	bg := Style{BG: cPanelBG}
	visible := max(1, h-5)
	if at := slices.Index(order, f.focus); at >= 0 {
		if at < f.scroll {
			f.scroll = at
		}
		if at >= f.scroll+visible {
			f.scroll = at - visible + 1
		}
	}
	f.scroll = max(0, min(f.scroll, len(order)-visible))
	labelW = min(max(labelW, 8), 24, max(8, w/3)) + 1
	vx := x + labelW + 1
	vw := max(4, w-labelW-1)
	for i := 0; i < visible && f.scroll+i < len(order); i++ {
		j := order[f.scroll+i]
		fd := f.fields[j]
		ry := y + 1 + i
		focused := j == f.focus
		ls := bg.Fg(cMuted)
		if focused {
			ls = bg.Fg(cAccent).bold()
		}
		lbl := ellipsis(fd.label, labelW-1)
		cv.Text(x+labelW-1-width(lbl), ry, lbl, ls, labelW-1)
		box := Style{FG: cText, BG: Mix(cPanelBG, cKeyBG, .45)}
		if focused {
			box = Style{FG: cGlow, BG: cKeyBG}
			cv.Set(vx-1, ry, '▐', fg(cAccent).Bg(cPanelBG))
		}
		if fd.readonly {
			box = bg.Fg(cMuted)
		}
		cv.Fill(vx, ry, vw, 1, box)
		switch fd.kind {
		case fChoice:
			v := fd.value
			if v == "" {
				v = "(none)"
			}
			cv.Text(vx+1, ry, "‹ "+v+" ›", box, vw-1)
		case fBool:
			mark := "○ no"
			if fd.value == "yes" {
				mark = "● yes"
			}
			if m.NoColor {
				mark = map[bool]string{true: "[x] yes", false: "[ ] no"}[fd.value == "yes"]
			}
			cv.Text(vx+1, ry, mark, box, vw-1)
		default:
			r := []rune(fd.value)
			cur := min(max(fd.cursor, 0), len(r))
			// The cursor goes where the text before it is shown, without
			// the controls Text drops.
			shown := func(from int) int { return width(secure.Text(string(r[from:cur]))) }
			start := 0
			for shown(start) > vw-3 {
				start++
			}
			cv.Text(vx+1, ry, string(r[start:]), box, vw-1)
			if focused && !fd.readonly {
				cxp := vx + 1 + shown(start)
				ch := ' '
				if cur < len(r) {
					ch = r[cur]
				}
				if m.NoColor {
					ch = '▏'
				}
				cv.Set(cxp, ry, ch, Style{FG: cDeep, BG: cAccent})
			}
			if fd.readonly {
				cv.Text(vx+vw-8, ry, "(fixed)", bg.Fg(cFaint), -1)
			}
		}
		m.hit(x, ry, w, "field:"+itoa(j))
	}
	if len(order) > visible {
		pos := itoa(min(len(order), f.scroll+visible)) + "/" + itoa(len(order))
		cv.Text(x+w-width(pos), y, pos, bg.Fg(cFaint), -1)
	}
	hy := y + h - 3
	switch {
	case f.err != "":
		cv.Text(x, hy, "✖ "+f.err, bg.Fg(cDown).bold(), w)
	case f.note != "":
		cv.Text(x, hy, "ℹ "+f.note, bg.Fg(cAccent2), w)
	}
	if f.focus < n && f.fields[f.focus].hint != "" {
		cv.Text(x, hy+1, f.fields[f.focus].hint, bg.Fg(cMuted).italic(), w)
	}
	by := y + h - 1
	bx := x + w - 30
	bx += m.button(cv, bx, by, "Apply ^S", "btn:save", f.focus == n) + 2
	m.button(cv, bx, by, "Cancel Esc", "btn:cancel", f.focus == n+1)
}
