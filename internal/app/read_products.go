package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/view"
)

// showProducts prints Products mode's board: a row for each line of each
// product, with what it rests on and how its lead check fared, then what
// each line in trouble says, the certificates soonest to expire, and what
// the board leaves out. --json gives the view's board as it is.
func (a *App) showProducts(v view.View, h header, asJSON bool) error {
	b := v.Products()
	if asJSON {
		return printJSON(a.Out, struct {
			header
			view.ProductBoard
		}{h, b})
	}
	t := &text{w: a.Out, language: v.Language}
	h.print(t, v.Now)
	t.line()
	head := "PRODUCTS · " + view.Plural(len(b.Products), "product")
	if b.Incidents > 0 {
		head += " · " + view.Plural(b.Incidents, "incident")
	}
	t.line(head)
	t.line()
	if len(b.Products) == 0 {
		t.line("No products: no service belongs to an application. topo templates products writes an example.")
		return t.err
	}
	cells := [][]string{{"STATE", "PRODUCT", "KIND", "LINE", "RESTS ON", "P50", "UP", "LEAD"}}
	looks := []string{}
	certs := []view.Certificate{}
	more := 0
	for _, p := range b.Products {
		more += p.MoreLines
		if p.Certificate != nil {
			certs = append(certs, *p.Certificate)
		}
		for i, l := range p.Lines {
			name, kind := p.Name, p.Kind
			if i > 0 {
				name, kind = "", ""
			}
			state := view.LineState(l)
			if state == "" {
				state = "up"
			}
			p50, up, lead := "—", "—", "not monitored"
			if x := l.Lead; x != nil {
				lead = x.ID
				if x.Freshness == "fresh" && x.Latency != nil {
					p50 = fmt.Sprintf("%d ms", x.Latency.P50)
				}
				if u := view.LongestUptime(x.Uptime); u != nil && x.Freshness == "fresh" {
					up = view.UptimeShare(*u)
				}
			}
			cells = append(cells, []string{state, name, kind, view.ProductLineName(p.Name, l.Name), chainText(l), p50, up, lead})
			if l.NeedsLook() {
				looks = append(looks, l.Name+": "+strings.Join(append([]string{view.LookWord(l)}, view.ProductLook(v.Now, p, l).Parts()...), " · "))
			}
		}
	}
	t.table(cells)
	if len(looks) > 0 {
		t.line()
		t.line("NEEDS A LOOK · " + fmt.Sprint(len(looks)))
		for _, l := range looks {
			t.line(l)
		}
	}
	if len(certs) > 0 {
		slices.SortStableFunc(certs, func(a, b view.Certificate) int { return cmp.Compare(a.Days, b.Days) })
		t.line()
		cells := [][]string{{"CERTIFICATE", "DAYS LEFT", "NOT AFTER", "WARNS WITHIN", "CHECK"}}
		for _, c := range certs {
			after, window := "—", "—"
			if !c.NotAfter.IsZero() {
				after = c.NotAfter.Format("2006-01-02")
			}
			if c.MinDays > 0 {
				window = view.Plural(c.MinDays, "day")
			}
			cells = append(cells, []string{c.Host, fmt.Sprint(c.Days), after, window, c.Check})
		}
		t.table(cells)
	}
	foot := []string{}
	if b.Ungrouped > 0 {
		foot = append(foot, view.Plural(b.Ungrouped, "service")+" in no product")
	}
	if more > 0 {
		foot = append(foot, view.Plural(more, "more line")+" past the board's bound; topo show products --json lists the board")
	}
	if len(foot) > 0 {
		t.line()
		t.line(strings.Join(foot, " · "))
	}
	return t.err
}

// chainText is what a line rests on, each layer by its name and, unless
// it is up, its own state: "Cloudflare › Hetzner Falkenstein (unknown) ›
// Snapmeal DB (unknown)".
func chainText(l view.ProductLine) string {
	if len(l.Chain) == 0 {
		return "nothing declared"
	}
	parts := []string{}
	for _, c := range l.Chain {
		part := view.LayerLabel(c)
		if c.Health != "up" {
			part += " (" + view.HealthWord(c.Health) + ")"
		}
		parts = append(parts, part)
	}
	out := strings.Join(parts, " › ")
	if l.MoreLayers > 0 {
		out += fmt.Sprintf(" +%d", l.MoreLayers)
	}
	return out
}
