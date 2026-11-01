package app

import (
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// text writes plain lines. Every value from a snapshot passes through
// clean, so a hostile name cannot reach the terminal as a control sequence.
type text struct {
	language string
	w        io.Writer
	err      error
}

func clean(s string) string {
	return secure.Text(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s))
}

func (t *text) line(parts ...string) {
	if t.err != nil {
		return
	}
	_, t.err = io.WriteString(t.w, strings.TrimRight(strings.Join(parts, ""), " ")+"\n")
}

func (t *text) kv(key, value string) { t.line(view.Text(t.language, key), ": ", clean(value)) }

// table aligns cells in columns two spaces apart. A column is as wide as
// its widest cell up to 48; a longer cell runs on rather than being cut,
// and the last column is never padded.
func (t *text) table(cells [][]string) {
	if len(cells) > 0 && t.language != "" && t.language != "en" {
		cells = append([][]string(nil), cells...)
		cells[0] = append([]string(nil), cells[0]...)
		for i, key := range cells[0] {
			cells[0][i] = view.Text(t.language, key)
		}
	}
	widths := []int{}
	for _, row := range cells {
		for i, c := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], min(48, view.Width(clean(c))))
		}
	}
	for _, row := range cells {
		var b strings.Builder
		for i, c := range row {
			c = clean(c)
			b.WriteString(c)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", max(0, widths[i]-view.Width(c))+2))
			}
		}
		t.line(b.String())
	}
}

// word is a state as the CLI writes it: the TUI's state column, with
// "up" where the TUI leaves a healthy row blank.
func word(r view.Row) string {
	if s := view.State(r); s != "" {
		return s
	}
	return "up"
}

// rows writes a titled table of a tab's rows: the state, the ID, the
// title with its tree guide, and the tab's columns.
func (t *text) rows(title string, tab int, rows []view.Row) {
	t.line(view.Text(t.language, title) + " · " + strconv.Itoa(len(rows)))
	if len(rows) == 0 {
		t.line(view.Text(t.language, "none"))
		return
	}
	cells := [][]string{append([]string{"STATE", "ID"}, view.Columns(tab)...)}
	for _, r := range rows {
		cells = append(cells, append([]string{word(r), r.ID, r.Tree + r.Title}, r.Cols...))
	}
	t.table(cells)
}

// entry writes the evidence behind a row as the TUI's evidence panel has
// it: the title, kind, ID and state, what before writes, then the detail
// lines.
func (t *text) entry(r view.Row, before func()) {
	t.line(clean(r.Title))
	t.line(clean(r.Kind + " · " + r.ID + " · " + view.Pill(r)))
	t.line()
	before()
	blank := true
	for _, l := range r.Details {
		l = strings.TrimRight(clean(l), " ")
		if l == "" {
			if !blank {
				t.line()
			}
			blank = true
			continue
		}
		t.line(l)
		blank = false
	}
}

// card writes what a server's card says beside its evidence.
func (t *text) card(c view.Card, now time.Time) {
	section := func(title string, n int) {
		t.line()
		t.line(title + " · " + strconv.Itoa(n))
	}
	names := func(refs []view.Ref) string {
		out := []string{}
		for _, r := range refs {
			out = append(out, r.Name)
		}
		return strings.Join(out, " ⇢ ")
	}
	t.line()
	t.line("CARD")
	t.kv("Kind", c.Kind)
	t.kv("Group", c.Group)
	t.kv("Where", c.Where)
	if len(c.Resources) > 0 {
		section("LOCAL RESOURCES", len(c.Resources))
		for _, r := range c.Resources {
			t.kv("Observer", r.Observer.Name+" · "+r.Health+" · "+r.Freshness)
			t.kv("Resource check", r.Check)
			for _, pair := range view.ResourceValues(r) {
				t.kv(pair[0], pair[1])
			}
		}
	}
	if len(c.Services) > 0 {
		section("SERVICES", len(c.Services))
		cells := [][]string{}
		for _, s := range c.Services {
			note, aside := "", s.Runtime
			if s.Affected && s.Health != "down" {
				note = "affected"
			}
			switch {
			case len(s.Via) > 0:
				aside = "via " + names(s.Via)
			case s.Runtime == "" && !s.Monitored:
				aside = "not monitored"
			}
			cells = append(cells, []string{word(view.Row{Health: s.Health, Note: note}), s.ID, s.Name, aside})
		}
		t.table(cells)
	}
	if len(c.Fronts) > 0 {
		section("IN FRONT OF", len(c.Fronts))
		cells := [][]string{}
		for _, ep := range c.Fronts {
			cells = append(cells, []string{word(view.Row{Health: ep.Health}), ep.ID, ep.Address, "→ " + ep.Node.Name})
		}
		t.table(cells)
	}
	if len(c.Pops) > 0 {
		section("ANSWERED FROM", len(c.Pops))
		source := " (response headers)"
		if c.PopsFromRollups {
			source = " (24h shares)"
		}
		t.line(clean(strings.Join(c.Pops, " · ")) + source)
	}
	if len(c.Addresses) > 0 {
		section("ADDRESSES", len(c.Addresses))
		cells := [][]string{}
		for _, ep := range c.Addresses {
			state := "not measured"
			if ep.Health != "" {
				state = word(view.Row{Health: ep.Health})
			}
			via := ""
			if len(ep.Via) > 0 {
				via = "via " + names(ep.Via)
			}
			cells = append(cells, []string{state, ep.ID, ep.Network, ep.Address, via})
		}
		t.table(cells)
	}
	if len(c.Hosts) > 0 {
		section("HOSTS", len(c.Hosts))
		cells := [][]string{}
		for _, k := range c.Hosts {
			cells = append(cells, []string{word(view.Row{Health: k.Health}), k.ID, k.Name, strings.Join(k.Runs, ", ")})
		}
		t.table(cells)
	}
	if len(c.Observers) > 0 {
		section("OBSERVES FROM HERE", len(c.Observers))
		cells := [][]string{}
		for _, o := range c.Observers {
			cells = append(cells, []string{word(view.Row{Health: o.Health, Note: map[bool]string{true: "outdated"}[o.Outdated]}), o.ID, o.Name, "last seen " + view.Age(now, o.LastSeen)})
		}
		t.table(cells)
	}
	if len(c.Links) > 0 {
		section("LINKS", len(c.Links))
		cells := [][]string{}
		for _, l := range c.Links {
			arrow := "→"
			if l.Inbound {
				arrow = "←"
			}
			cells = append(cells, []string{arrow, l.ID, l.Other.Name, l.Transport})
		}
		t.table(cells)
	}
	if c.Memo != "" {
		t.line()
		t.line("MEMO")
		for _, l := range strings.Split(strings.ReplaceAll(c.Memo, "\r\n", "\n"), "\n") {
			t.line(clean(l))
		}
	}
}
