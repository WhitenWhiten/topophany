package monitor

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// Structured rows carry safety state inside their value as well as on the row.
// A resource becoming stale or one observer losing evidence must bypass cadence
// even when another member keeps the row's aggregate health unchanged.
func safetyState(p Panel) string {
	states := []any{p.Lifecycle, p.ConfigRevision}
	if r := p.CollectorRead; r != nil {
		states = append(states, r.State, r.Stage, r.Code)
	}
	observers := func(values []ObserverVerdict) any {
		out := []any{}
		for _, v := range values {
			out = append(out, []any{v.ID, v.Health, v.State, v.Live, v.Counts})
		}
		return out
	}
	services := func(values []ServiceValue) any {
		out := []any{}
		for _, v := range values {
			out = append(out, []any{v.ID, v.Health, v.Monitored, v.Affected})
		}
		return out
	}
	for _, r := range p.Rows {
		state := []any{r.ID, r.Health, r.Freshness, r.Reason}
		switch v := r.Value.(type) {
		case []ServerListEntry:
			for _, server := range v {
				state = append(state, []any{server.ID, server.Health, server.Note})
			}
		case view.SourcesStatus:
			state = append(state, v)
		case SummaryVerdict:
			state = append(state, v.State, v.Counts, v.Observers)
		case []ObserverVerdict:
			state = append(state, observers(v))
		case AttentionList:
			for _, item := range v.Items {
				state = append(state, []any{item.Kind, item.ID, item.Health, item.State})
			}
		case []ApplicationValue:
			for _, app := range v {
				state = append(state, []any{app.ID, app.Environment, app.Health, app.Counts, services(app.Services)})
			}
		case LinkCard:
			state = append(state, v.Health, v.Evidence, v.Affected, v.Reason)
			for _, c := range v.Checks {
				state = append(state, c.ID, c.Health, c.Freshness, diagnosticState(c.Diagnostic))
			}
		case []LinkSummary:
			for _, s := range v {
				state = append(state, s.ID, s.Health, s.Evidence, s.Affected, s.Reason)
			}
		case CheckCard:
			state = append(state, v.Health, v.Freshness, diagnosticState(v.Diagnostic))
		case ServiceCard:
			for _, o := range v.SeenFrom {
				for _, c := range o.Checks {
					state = append(state, c.ID, c.Health, c.Freshness, diagnosticState(c.Diagnostic))
				}
			}
			state = append(state, assessmentState(v.Assessment), v.UnknownReason)
		case []CheckListEntry:
			state = append(state, v)
		case ServerCard:
			state = append(state, v.Health, v.Note, services(v.Services), observers(v.Observes))
			for _, resource := range v.Resources {
				state = append(state, []any{resource.Check, resource.Health, resource.Freshness})
			}
		}
		states = append(states, state)
	}
	b, _ := json.Marshal(states)
	return string(b)
}

// An assessment's words and its evidence's judgments are safety state: a
// local failure turning into an access failure is written at once.
func assessmentState(a *AssessmentValue) any {
	if a == nil {
		return nil
	}
	evidence := []any{}
	for _, e := range a.Evidence {
		evidence = append(evidence, []any{e.Check, e.Health, e.Freshness, e.Worker})
	}
	return []any{a.State, a.Availability, a.Reachability, a.WorkerOnline, evidence}
}
