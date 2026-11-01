package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/view"
)

func (a *App) problems(v view.View, h header, id string, asJSON bool) error {
	if v.Snapshot.Direct != nil {
		return errors.New("direct resource reads do not establish incidents; inspect topo status --source direct instead")
	}
	groups := v.Problems()
	if id != "" {
		found := false
		for _, p := range groups {
			if p.ID == id {
				groups = []view.Problem{p}
				found = true
				break
			}
		}
		if !found {
			return errors.New("problem not found; use topo problems to list current group IDs")
		}
	}
	if asJSON {
		return printJSON(a.Out, struct {
			header
			Problems []view.Problem `json:"problems"`
		}{h, groups})
	}
	t := &text{w: a.Out}
	h.print(t, v.Now)
	if len(groups) == 0 {
		t.line("No confirmed incidents in this view. Inspect topo status for warnings and missing evidence.")
	}
	for _, p := range groups {
		t.line()
		t.line(p.ID + " · " + p.Target)
		t.line(p.Relation)
		if !p.Current {
			t.line("Cached evidence; current state is not established.")
		}
		t.kv("Incidents", fmt.Sprint(len(p.Incidents)))
		t.kv("Observers", strings.Join(p.Observers, ", "))
		t.kv("Services", strings.Join(p.Services, ", "))
		if id == "" {
			t.kv("Inspect", "topo problems "+p.ID)
			continue
		}
		for _, r := range p.Incidents {
			t.line(r.Title)
			for _, line := range r.Details {
				t.line(line)
			}
		}
		t.rows("FAILED CHECK EVIDENCE", view.Checks, p.Checks)
		t.rows("INDEPENDENT COUNTER-EVIDENCE", view.Checks, p.CounterEvidence)
		for _, next := range p.Next {
			t.kv("Next", next)
		}
	}
	return t.err
}
