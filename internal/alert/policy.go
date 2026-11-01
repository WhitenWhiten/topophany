package alert

import (
	"errors"
	"slices"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type QuietWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func validatePolicy(w Webhook, c model.Config) error {
	if w.PrimaryInstance != "" && !config.ID(w.PrimaryInstance) {
		return errors.New("primary_instance must be a collector instance ID")
	}
	if w.QuietUntil.After(time.Now().Add(24 * time.Hour)) {
		return errors.New("quiet_until must be within 24 hours")
	}
	if len(w.Windows) > 32 || len(w.Services) > 128 {
		return errors.New("notification policy exceeds its bounded window/service limit")
	}
	windows := slices.Clone(w.Windows)
	slices.SortFunc(windows, func(a, b QuietWindow) int { return a.Start.Compare(b.Start) })
	for i, window := range windows {
		if window.Start.IsZero() || !window.End.After(window.Start) || window.End.Sub(window.Start) > 24*time.Hour || i > 0 && window.Start.Before(windows[i-1].End) {
			return errors.New("notification windows must be non-overlapping, positive and at most 24 hours")
		}
	}
	known := map[string]bool{}
	for _, service := range c.Services {
		known[service.ID] = true
	}
	for i, id := range w.Services {
		if !known[id] || slices.Contains(w.Services[:i], id) {
			return errors.New("notification services must be known and unique")
		}
	}
	return nil
}

// BindServices resolves public service references to check IDs already stored
// in incident events. Replay therefore needs no new queue schema or secret data.
func BindServices(a Config, c model.Config) Config {
	a.Webhooks = slices.Clone(a.Webhooks)
	for i := range a.Webhooks {
		w := &a.Webhooks[i]
		w.serviceChecks = nil
		for _, check := range c.Checks {
			if slices.Contains(w.Services, check.Service) {
				w.serviceChecks = append(w.serviceChecks, check.ID)
			}
		}
		slices.Sort(w.serviceChecks)
	}
	return a
}
func (w Webhook) wantsEvent(e Event) bool {
	if !w.wants(e.group(), e.Observer) {
		return false
	}
	if len(w.Services) == 0 {
		return true
	}
	if e.Incident == nil {
		return false
	}
	for _, id := range e.Incident.Checks {
		if slices.Contains(w.serviceChecks, id) {
			return true
		}
	}
	return false
}
func (w Webhook) quietDeadline(now time.Time) time.Time {
	end := w.QuietUntil
	for _, window := range w.Windows {
		if !now.Before(window.Start) && now.Before(window.End) && window.End.After(end) {
			end = window.End
		}
	}
	if !end.After(now) {
		return time.Time{}
	}
	return end
}
