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
