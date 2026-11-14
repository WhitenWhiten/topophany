package tui

import (
	"strings"
	"unicode"

	"github.com/WhitenWhiten/topophany/internal/view"
)

// Lists are drawn as tables: a health glyph, the title (with its tree guide
// in the Topology hierarchy), aligned secondary columns, and a state column
// that stays empty for healthy rows so problems are the only lit words.

// stateWord is the state-column label of a row and its style; "" for rows
// that are simply up.
func stateWord(r Row) (string, Style) {
	word := view.State(r)
	if assessed(r, word) {
		return assessmentWord(*r.Assessment)
	}
	switch {
	case word == "cached", word == "affected", word == "outdated", word == "recovering":
		return word, fg(cWarn)
	case word == "paused", word == "maintenance", word == "closed":
		return word, fg(cMuted)
	case word == "DOWN":
		return word, fg(cDown).bold()
	case word == "WARN":
		return word, fg(cWarn).bold()
	case word == "":
		return "", Style{}
	}
	return word, fg(cFaint)
}

type tableLayout struct {
	titleW int
	idx    []int  // the Row.Cols shown, in order; dropped columns are absent
	cols   []int  // their widths
	right  []bool // and which are numeric, so right-aligned
	stateW int
}

const tableGap = 2

// layoutTable sizes columns from every row, not just the visible ones, so the
// table does not jitter while scrolling. A column of words that every row
// shares (all "fresh", all "incident") says nothing per row and is left
// out. When space is short the widest column gives way first; a column is
// dropped (rightmost first) only when every column is already at its
// minimum.
func layoutTable(rows []Row, heads []string, w int) tableLayout {
	l := tableLayout{}
	ncol := 0
	words := map[string]int{}
	for _, r := range rows {
		l.titleW = max(l.titleW, width(r.Tree+r.Title))
		ncol = max(ncol, len(r.Cols))
		s, _ := stateWord(r)
		words[s]++
		if s != "" {
			l.stateW = max(l.stateW, width(s))
		}
	}
	shared := make([]bool, ncol)
	for i := range shared {
		first, n := "", 0
		shared[i] = true
		for _, r := range rows {
			if r.Kind == "group" {
				continue
			}
			v := ""
			if i < len(r.Cols) {
				v = r.Cols[i]
			}
			if n == 0 {
				first = v
			}
			shared[i] = shared[i] && v == first
			n++
		}
		shared[i] = shared[i] && n >= 3 && first != "" && (first[0] < '0' || first[0] > '9')
	}
	// A state every row shares (a draft is all "unknown", a cached view all
	// "cached") says nothing per row; the header already says it.
	if len(words) == 1 && len(rows) >= 3 {
		l.stateW = 0
	}
	if len(heads) > 0 {
		l.titleW = max(l.titleW, width(heads[0]))
	}
	natTitle := l.titleW
	l.titleW = min(l.titleW, 44)
	pref := make([]int, ncol)
	natural := make([]int, ncol)
	l.right = make([]bool, ncol)
	for i := range pref {
		numeric := false
		for _, r := range rows {
			if i < len(r.Cols) && r.Cols[i] != "" {
				pref[i] = max(pref[i], width(r.Cols[i]))
				natural[i] = pref[i]
				v := r.Cols[i]
				numeric = v[0] >= '0' && v[0] <= '9' && (numeric || pref[i] == width(v))
			}
		}
		if pref[i] > 0 && i+1 < len(heads) {
			pref[i] = max(pref[i], min(width(heads[i+1]), 12))
		}
		pref[i] = min(pref[i], 28)
		l.right[i] = numeric
	}
	keep := []int{}
	for i := range pref {
		if pref[i] > 0 && !shared[i] {
			keep = append(keep, i)
		}
	}
	numeric := l.right
	title := l.titleW
	for n := len(keep); n >= 0; n-- {
		l.idx = keep[:n]
		l.cols = l.cols[:0]
		for _, i := range l.idx {
			l.cols = append(l.cols, pref[i])
		}
		l.titleW = title
		total := 2 + l.titleW
		for _, c := range l.cols {
			total += tableGap + c
		}
		if l.stateW > 0 {
			total += tableGap + l.stateW
		}
		for total > w {
			widest, at := l.titleW-14, -1
			for k, c := range l.cols {
				if c-min(9, pref[l.idx[k]]) > widest {
					widest, at = c-min(9, pref[l.idx[k]]), k
				}
			}
			if widest <= 0 {
				break
			}
			if at < 0 {
				l.titleW--
			} else {
				l.cols[at]--
			}
			total--
		}
		if total <= w || n == 0 {
			if total > w {
				l.titleW = max(4, l.titleW-(total-w))
			}
			// Spare room goes back to whatever was capped: long titles
			// first, then the rightmost columns (routes, addresses).
			spare := w - total
			grow := func(cur *int, want int) {
				if d := min(spare, want-*cur); d > 0 {
					*cur += d
					spare -= d
				}
			}
			grow(&l.titleW, natTitle)
			for k := len(l.cols) - 1; k >= 0; k-- {
				grow(&l.cols[k], natural[l.idx[k]])
			}
			break
		}
	}
	l.right = make([]bool, len(l.idx))
	for k, i := range l.idx {
		l.right[k] = numeric[i]
	}
	return l
}

// cellTone quiets the words that only say "as expected" and lights the
// ones that do not, in whichever column they appear.
var cellTone = map[string]Color{"fresh": cFaint, "stale": cWarn}

// fitHead shortens a heading such as "NODE · ENDPOINT · LINK" by whole parts
// rather than cutting it mid-word.
func fitHead(s string, w int) string {
	for width(s) > w {
		i := strings.LastIndex(s, " · ")
		if i < 0 {
			return ellipsis(s, w)
		}
		s = s[:i]
	}
	return s
}

func (m *Model) drawList(cv *Canvas, x, y, w, h int, rows []Row) {
	if h <= 0 {
		return
	}
	if len(rows) == 0 {
		// Say why the list is empty, and how to fill it where a key can.
		msg, hint := "No "+strings.ToLower(tabs[m.Tab])+" declared.", ""
		switch {
		case m.Search != "":
			msg, hint = "Nothing matches “"+m.Search+"”.", "Esc clears the filter."
		case m.Edit != nil:
			msg = "Nothing here yet — press a to add."
		case m.AttentionOnly && m.businessTab():
			msg, hint = "Nothing here needs attention.", "b Browse shows everything again."
		case m.Tab == tabIncidents:
			msg = "No incidents: none open, and none closed in the last day."
		case m.Tab == tabTopology:
			msg = "No nodes declared."
		case m.businessTab() && (m.Application != "" || m.Environment != "" || m.ServiceFilter != ""):
			msg, hint = "Nothing in this application or environment.", "b Browse picks another, or clears the filters."
		}
		cv.Text(x, y, ellipsis(msg, w), fg(cMuted).italic(), w)
		if hint != "" && h > 1 {
			cv.Text(x, y+1, ellipsis(hint, w), fg(cFaint), w)
		}
		return
	}
	rows = m.logoRows(rows)
	heads := m.tabColumns()
	lay := layoutTable(rows, heads, w)
	if len(heads) > 0 && h >= 6 {
		hs := fg(cFaint).bold()
		cx := x + 2
		cv.Text(cx, y, fitHead(heads[0], lay.titleW), hs, lay.titleW)
		cx += lay.titleW
		for i, cw := range lay.cols {
			cx += tableGap
			if c := lay.idx[i]; c+1 < len(heads) {
				head := fitHead(heads[c+1], cw)
				if lay.right[i] {
					head = strings.Repeat(" ", max(0, cw-width(head))) + head
				}
				cv.Text(cx, y, head, hs, cw)
			}
			cx += cw
		}
		y++
		h--
	}
	if m.Selected < m.Offset {
		m.Offset = m.Selected
	}
	if m.Selected >= m.Offset+h {
		m.Offset = m.Selected - h + 1
	}
	groups := m.groupColors()
	for i := 0; i < h; i++ {
		j := m.Offset + i
		if j >= len(rows) {
			break
		}
		r := rows[j]
		selected := j == m.Selected
		base := Style{}
		if selected {
			base = Style{BG: cSelBG}
			cv.Fill(x-1, y+i, w+2, 1, base)
			cv.Set(x-1, y+i, '▌', fg(cAccent).Bg(cSelBG))
		}
		cx := x
		if m.NoColor {
			mark := ' '
			if selected {
				mark = '›'
			}
			cv.Set(cx-1, y+i, mark, base)
		}
		if r.Kind == "group" {
			label := r.Tree + r.Title
			titleW := min(width(label), max(12, w/2))
			cv.Text(cx, y+i, ellipsis(label, titleW), base.Fg(cText).bold(), titleW)
			if len(r.Cols) > 0 {
				cv.Text(cx+titleW+2, y+i, ellipsis(r.Cols[0], w-titleW-2), base.Fg(cMuted), max(0, w-titleW-2))
			}
			m.hit(x-1, y+i, w+2, "row:"+itoa(j))
			continue
		}
		cv.Set(cx, y+i, healthGlyph(r.Health), base.Fg(healthColor(r.Health)).bold())
		cx += 2
		tw := lay.titleW
		if r.Tree != "" {
			n := cv.Text(cx, y+i, r.Tree, base.Fg(cFaint), tw)
			cx += n
			tw -= n
		}
		ts := base.Fg(cText)
		switch {
		case selected:
			ts = base.Fg(cGlow).bold()
		case r.Kind == "endpoint" || r.Kind == "link":
			ts = base.Fg(Mix(cText, cMuted, .55))
		case r.Kind == "node" && r.Tree == "":
			ts = ts.bold()
		}
		shown := ellipsis(r.Title, tw)
		cv.Text(cx, y+i, shown, ts, tw)
		m.highlight(cv, cx, y+i, shown, base)
		cx += tw
		for k, cw := range lay.cols {
			cx += tableGap
			if c := lay.idx[k]; c < len(r.Cols) {
				cs := base.Fg(cMuted)
				if c == 0 && r.Kind == "node" {
					if c, ok := groups[r.Cols[0]]; ok {
						cs = base.Fg(c)
					}
				}
				if r.Kind == "endpoint" || r.Kind == "link" {
					cs = base.Fg(Mix(cMuted, cFaint, .35))
				}
				v := ellipsis(r.Cols[c], cw)
				if c, ok := cellTone[v]; ok {
					cs = base.Fg(c)
				}
				if lay.right[k] {
					v = strings.Repeat(" ", max(0, cw-width(v))) + v
				}
				cv.Text(cx, y+i, v, cs, cw)
			}
			cx += cw
		}
		if word, st := stateWord(r); word != "" && lay.stateW > 0 {
			cv.Text(x+w-width(word), y+i, word, base.Merge(st), -1)
		}
		m.hit(x-1, y+i, w+2, "row:"+itoa(j))
	}
	if len(rows) > h {
		pos := " " + itoa(m.Selected+1) + "/" + itoa(len(rows)) + " "
		cv.Text(x+w-width(pos), y+h, pos, fg(cFaint), -1)
		// A thumb on the right border shows where the window sits.
		track := h
		thumb := max(1, track*h/len(rows))
		top := (track - thumb) * m.Offset / max(1, len(rows)-h)
		for k := 0; k < thumb; k++ {
			cv.Set(x+w+1, y+top+k, '┃', fg(Mix(cLine, cAccent, .45)))
		}
	}
}

// groupColors assigns each node group the hue it has on the maps.
func (m *Model) groupColors() map[string]Color {
	out := map[string]Color{}
	for _, n := range m.Snapshot.Topology.Nodes {
		if _, ok := out[n.Group]; !ok {
			out[n.Group] = groupHues[len(out)%len(groupHues)]
		}
	}
	return out
}

// highlight marks where the filter matches the title drawn at (x, y), so
// the eye finds why each row is in the list.
func (m *Model) highlight(cv *Canvas, x, y int, shown string, base Style) {
	q := []rune(strings.ToLower(m.Search))
	if len(q) == 0 || m.NoColor {
		return
	}
	rs := []rune(shown)
	for i := 0; i+len(q) <= len(rs); i++ {
		match := true
		for j := range q {
			if unicode.ToLower(rs[i+j]) != q[j] {
				match = false
				break
			}
		}
		if match {
			at := width(string(rs[:i]))
			cv.Text(x+at, y, string(rs[i:i+len(q)]), base.Fg(cGlow).Bg(Mix(cAccent, cInk, .55)).bold(), -1)
			return
		}
	}
}
