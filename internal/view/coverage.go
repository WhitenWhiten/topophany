package view

import (
	"fmt"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Coverage describes what this view can establish. It does not change health,
// promote path evidence into application availability or count unknown as down.
type Coverage struct {
	Language string        `json:"-"`
	Observer string        `json:"observer"`
	Services int           `json:"services"`
	Known    int           `json:"known"`
	Gaps     []CoverageGap `json:"gaps,omitempty"`
}
type CoverageGap struct {
	Service string `json:"service"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
}

func (c Coverage) Line() string {
	if text := coverageLine(c.Language, c); text != "" {
		return text
	}
	scope := c.Observer
	if scope == "" {
		scope = "all observers"
	}
	return fmt.Sprintf("Service availability: %d/%d known · view: %s", c.Known, c.Services, scope)
}
func (v View) Coverage() Coverage {
	return v.coverage(serviceAssessments(v.Snapshot, v.Cached || v.Draft))
}
func (v View) coverage(assessments map[string]ServiceAssessment) Coverage {
	out := Coverage{Language: v.Language, Observer: v.Snapshot.ViewObserver, Services: len(v.Snapshot.Topology.Services), Gaps: []CoverageGap{}}
	byService := map[string][]model.Check{}
	for _, c := range v.Snapshot.Topology.Checks {
		byService[c.Service] = append(byService[c.Service], c)
	}
	states := map[string]model.CheckState{}
	for _, s := range v.Snapshot.Checks {
		states[s.ID] = s
	}
	for _, service := range v.Snapshot.Topology.Services {
		if assessments[service.ID].Availability != "unknown" && !v.Cached && !v.Draft {
			out.Known++
			continue
		}
		gap := CoverageGap{Service: service.ID}
		active, inView, application, stale, sampled := 0, 0, 0, false, false
		for _, check := range byService[service.ID] {
			if check.Disabled || check.Maintenance {
				continue
			}
			active++
			if out.Observer != "" && check.Observer != out.Observer {
				continue
			}
			inView++
			if check.Kind != "http" {
				continue
			}
			application++
			state, ok := states[check.ID]
			stale = stale || ok && state.Freshness == "stale"
			sampled = sampled || ok && state.Last != nil
		}
		switch {
		case v.Cached:
			gap.Code, gap.Reason = "cached", "Only cached evidence is available. Reconnect to obtain current samples."
		case v.Draft:
			gap.Code, gap.Reason = "draft", "This topology has not supplied live evidence."
		case len(byService[service.ID]) == 0:
			gap.Code, gap.Reason = "unmonitored", "No check is configured for this service."
		case active == 0:
			gap.Code, gap.Reason = "suspended", "All configured checks are paused or in maintenance."
		case inView == 0:
			gap.Code, gap.Reason = "outside_view", "This observer has no active check for this service."
		case application == 0:
			gap.Code, gap.Reason = "no_application_check", "Configured checks do not establish application availability. Add an HTTP application check."
		case stale:
			gap.Code, gap.Reason = "stale", "Application evidence has expired. Check the worker and its delivery."
		case !sampled:
			gap.Code, gap.Reason = "no_samples", "Waiting for the first application sample in this view."
		default:
			gap.Code, gap.Reason = "application_unconfirmed", "Current checks do not establish application availability; inspect path and monitor-side evidence."
		}
		gap.Reason = Text(v.Language, gap.Reason)
		out.Gaps = append(out.Gaps, gap)
	}
	return out
}
