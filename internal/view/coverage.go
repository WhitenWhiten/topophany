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
		code, reason := v.coverageGap(byService[service.ID], states, out.Observer)
		out.Gaps = append(out.Gaps, CoverageGap{Service: service.ID, Code: code, Reason: Text(v.Language, reason)})
	}
	return out
}

// coverageGap is the one choice of why a service's availability is
// unknown in this view, from its configured checks: a closed code and its
// sentence. Every consumer of a gap reads it from Coverage's Gaps.
func (v View) coverageGap(checks []model.Check, states map[string]model.CheckState, observer string) (code, reason string) {
	active, inView, application, stale, sampled := 0, 0, 0, false, false
	for _, check := range checks {
		if check.Disabled || check.Maintenance {
			continue
		}
		active++
		if observer != "" && check.Observer != observer {
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
		return "cached", "Only cached evidence is available. Reconnect to obtain current samples."
	case v.Draft:
		return "draft", "This topology has not supplied live evidence."
	case len(checks) == 0:
		return "unmonitored", "No check is configured for this service."
	case active == 0:
		return "suspended", "All configured checks are paused or in maintenance."
	case inView == 0:
		return "outside_view", "This observer has no active check for this service."
	case application == 0:
		return "no_application_check", "Configured checks do not establish application availability. Add an HTTP application check."
	case stale:
		return "stale", "Application evidence has expired. Check the worker and its delivery."
	case !sampled:
		return "no_samples", "Waiting for the first application sample in this view."
	}
	return "application_unconfirmed", "Current checks do not establish application availability; inspect path and monitor-side evidence."
}

// Reasons counts the services whose availability is unknown by gap code,
// only the codes that occur. The counts sum to Services - Known.
func (c Coverage) Reasons() map[string]int {
	out := map[string]int{}
	for _, gap := range c.Gaps {
		out[gap.Code]++
	}
	return out
}

// UnknownReasons is each service's gap code, for the services whose
// availability is unknown; a known service has none.
func (c Coverage) UnknownReasons() map[string]string {
	out := make(map[string]string, len(c.Gaps))
	for _, gap := range c.Gaps {
		out[gap.Service] = gap.Code
	}
	return out
}
