//go:build !nodevice

package tui

import "github.com/WhitenWhiten/topophany/internal/view"

// touchProducts is the products board as a phone lists it: each line a
// finger tall, its product's and its own name and state over what it rests
// on and how its lead fared, and under them what the lead says where the
// line asks for a look. A tap selects a line and a second opens it; open
// makes the first tap open it.
func (m *Model) touchProducts(cv *Canvas, x, y, w, h int, lines []productLine, open bool) {
	if h <= 0 || w < 8 {
		return
	}
	if len(lines) == 0 {
		m.drawProducts(cv, x, y, w, h, lines)
		return
	}
	n := max(1, h/touchRows)
	if m.touch.seen != m.Selected+1 {
		// The selection moved: bring it into view.
		if m.Selected < m.touch.top {
			m.touch.top = m.Selected
		}
		if m.Selected >= m.touch.top+n {
			m.touch.top = m.Selected - n + 1
		}
		m.touch.seen = m.Selected + 1
	}
	m.touch.top = min(max(m.touch.top, 0), max(0, len(lines)-n))
	// Each line's chain has the room its numbers leave; the chains name
	// their layers short where one line's full names do not fit.
	room := func(i int) int { return max(0, w-2-width(touchNumbers(lines[i].l))-2) }
	short := m.shortChains(lines, room)
	for i := 0; i < n && m.touch.top+i < len(lines); i++ {
		j := m.touch.top + i
		pl := lines[j]
		p, l := pl.p, pl.l
		yy := y + i*touchRows
		selected := j == m.Selected
		look := l.NeedsLook()
		base := Style{}
		if selected {
			base = Style{BG: cSelBG}
			n := 2
			if look {
				n = 3
			}
			cv.Fill(x-1, yy, w+2, n, base)
			for k := 0; k < n; k++ {
				cv.Set(x-1, yy+k, '▌', fg(cAccent).Bg(cSelBG))
			}
		}
		// The line's mark, its product's name and its own, its state word
		// at the right where it asks for a look.
		right := w
		if look {
			word, st := lookWord(l)
			right -= width(word) + 1
			cv.Text(x+w-width(word), yy, word, base.Merge(st).bold(), -1)
		}
		cv.Set(x, yy, healthGlyph(l.Health), base.Fg(healthColor(l.Health)).bold())
		ts := base.Fg(cText).bold()
		if selected {
			ts = base.Fg(cGlow).bold()
		}
		name := p.Name
		if ln := view.ProductLineName(p.Name, l.Name); ln != p.Name {
			name += " · " + ln
		}
		cv.Text(x+2, yy, ellipsis(name, max(0, right-2)), ts, max(0, right-2))
		// Under it, what it rests on, then its lead's p50 and share up.
		nums := touchNumbers(l)
		m.drawChain(cv, x+2, yy+1, room(j), l, short, base)
		cv.Text(x+w-width(nums), yy+1, nums, base.Fg(cMuted), -1)
		// Then what the lead says, or the rule between lines.
		switch {
		case look:
			cv.Text(x+2, yy+2, m.lookSays(pl, w-2), base.Fg(cMuted), w-2)
		case !selected && j+1 < len(lines) && i+1 < n:
			for k := x + 2; k < x+w; k++ {
				cv.Set(k, yy+2, '╌', fg(Mix(cLine, cInk, .35)))
			}
		}
		act := "row:" + itoa(j)
		if open {
			act = "touch:open:" + itoa(j)
		}
		m.hitArea(x-1, yy, w+2, touchRows, act)
	}
	if len(lines) > n {
		pos := " " + itoa(min(len(lines), m.touch.top+n)) + "/" + itoa(len(lines)) + " "
		cv.Text(x+w-width(pos), y+h, pos, fg(cFaint), -1)
		scrollThumb(cv, x+w+1, y, h, len(lines)*touchRows, m.touch.top*touchRows, fg(Mix(cLine, cAccent, .45)))
	}
}

// touchNumbers are what a line's second row ends with: its lead's p50 and
// share up, or that no check measures it.
func touchNumbers(l view.ProductLine) string {
	if l.Lead == nil {
		return "not monitored"
	}
	ms, fresh := p50(l.Lead.SeenCheck)
	out := latency(ms, fresh)
	if u := lineUptime(l); u != "" {
		out += "  " + u
	}
	return out
}
