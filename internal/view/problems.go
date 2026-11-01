package view

import (
	"sort"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

type Problem struct {
	ID              string   `json:"id"`
	Target          string   `json:"target"`
	Current         bool     `json:"current"`
	Relation        string   `json:"relation"`
	Incidents       []Row    `json:"incidents"`
	Checks          []Row    `json:"checks"`
	CounterEvidence []Row    `json:"counter_evidence"`
	Observers       []string `json:"observers"`
	Services        []string `json:"services"`
	Next            []string `json:"next_actions"`
}

// Problems groups only identical declared targets. It never merges incident
// identities or attributes a cause; independent observer evidence stays intact.
func (v View) Problems() []Problem {
	checks := map[string]model.Check{}
	nodes := map[string]string{}
	endpoints := map[string]string{}
	for _, c := range v.Snapshot.Topology.Checks {
		checks[c.ID] = c
	}
	for _, n := range v.Snapshot.Topology.Nodes {
		nodes[n.ID] = named(n.Name, n.ID)
	}
	for _, e := range v.Snapshot.Topology.Endpoints {
		endpoints[e.ID] = e.Node
	}
	rows := map[string]Row{}
	for _, r := range v.Rows(Checks) {
		rows[r.ID] = r
	}
	rb := newRowBuilder(v, Incidents)
	groups := map[string]*Problem{}
	for _, incident := range v.Snapshot.Incidents {
		targets := map[string]bool{}
		for _, id := range incident.Checks {
			c := checks[id]
			target := endpoints[c.Endpoint]
			if target != "" {
				targets[target] = true
			}
		}
		key, title := "incident:"+incident.ID, incident.Summary
		if len(targets) == 1 {
			for target := range targets {
				key, title = "node:"+target, nodes[target]
			}
		}
		p := groups[key]
		if p == nil {
			p = &Problem{ID: key, Target: title, Current: !v.Cached && !v.Draft, Relation: "Grouped by the same declared target; correlation does not establish a shared root cause.", Incidents: []Row{}, Checks: []Row{}, CounterEvidence: []Row{}, Observers: []string{}, Services: []string{}, Next: []string{"Compare failed checks with the independent counter-evidence and their sample times.", "Inspect each affected path and local application check before attributing the failure to a server.", "Open the service runbook with topo show services SERVICE; commands are never auto-executed."}}
			groups[key] = p
		}
		p.Incidents = append(p.Incidents, rb.incidentRow(incident))
		for _, id := range incident.Checks {
			c := checks[id]
			p.Observers = append(p.Observers, c.Observer)
			if c.Service != "" {
				p.Services = append(p.Services, c.Service)
			}
			if r, ok := rows[id]; ok {
				p.Checks = append(p.Checks, r)
			}
		}
		for _, id := range incident.CounterEvidence {
			if r, ok := rows[id]; ok {
				p.CounterEvidence = append(p.CounterEvidence, r)
			} else {
				p.CounterEvidence = append(p.CounterEvidence, Row{Kind: "check", ID: id, Title: id, Health: "unknown", Details: []string{"Counter-evidence is referenced by the incident but its sample is unavailable in this view."}})
			}
		}
	}
	unique := func(values []string) []string {
		sort.Strings(values)
		out := []string{}
		for _, s := range values {
			if s != "" && (len(out) == 0 || out[len(out)-1] != s) {
				out = append(out, s)
			}
		}
		return out
	}
	uniqueRows := func(rows []Row) []Row {
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		out := []Row{}
		for _, r := range rows {
			if len(out) == 0 || out[len(out)-1].ID != r.ID {
				out = append(out, r)
			}
		}
		return out
	}
	out := []Problem{}
	for _, p := range groups {
		p.Observers = unique(p.Observers)
		p.Services = unique(p.Services)
		p.Checks = uniqueRows(p.Checks)
		p.CounterEvidence = uniqueRows(p.CounterEvidence)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Incidents) != len(out[j].Incidents) {
			return len(out[i].Incidents) > len(out[j].Incidents)
		}
		return strings.Compare(out[i].ID, out[j].ID) < 0
	})
	return out
}

// ProblemRows adapts the shared investigation to the existing scrollable detail
// sheet. No additional polling or mutable health state is introduced.
func (v View) ProblemRows(search string) []Row {
	out := []Row{}
	for _, p := range v.Problems() {
		if search != "" && !strings.Contains(strings.ToLower(p.ID+" "+p.Target+" "+strings.Join(p.Observers, " ")), strings.ToLower(search)) {
			continue
		}
		health, note := "down", "related incidents"
		if !p.Current {
			health, note = "unknown", "cached"
		}
		details := []string{p.Relation, "Observers: " + strings.Join(p.Observers, ", "), "Services: " + strings.Join(p.Services, ", ")}
		for _, r := range p.Incidents {
			details = append(details, "", r.Title+":")
			details = append(details, r.Details...)
		}
		details = append(details, "", "Independent counter-evidence:")
		for _, r := range p.CounterEvidence {
			details = append(details, r.ID+": "+r.Health)
			details = append(details, r.Details...)
		}
		details = append(details, "", "Next actions:")
		details = append(details, p.Next...)
		out = append(out, Row{Kind: "problem", ID: p.ID, Title: p.Target, Health: health, Note: note, Cols: []string{Plural(len(p.Incidents), "incident"), Plural(len(p.Observers), "observer")}, Details: details})
	}
	return out
}
