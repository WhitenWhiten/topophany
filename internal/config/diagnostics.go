package config

import (
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type Diagnostic struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
	Next    string `json:"next_action"`
}

// Diagnostics describes coverage choices in a valid topology. These are hints,
// not invented health judgments, and they neither contact nor change targets.
func Diagnostics(c model.Config) []Diagnostic {
	out := []Diagnostic{}
	counts, applications := map[string]int{}, map[string]int{}
	for i, x := range c.Checks {
		if x.Disabled || x.Maintenance {
			continue
		}
		counts[x.Service]++
		if x.Kind == "http" {
			applications[x.Service]++
		}
		if x.Kind == "http" && x.Service != "" && x.AvailabilityScope == "" {
			out = append(out, Diagnostic{"availability_scope_inferred", fmt.Sprintf("checks[%d].availability_scope", i), "Application scope is inferred from placement and target.", "Declare local, remote or user_entry when the inferred scope does not express the intended evidence."})
		}
	}
	for i, service := range c.Services {
		field := fmt.Sprintf("services[%d]", i)
		if counts[service.ID] == 0 {
			out = append(out, Diagnostic{"service_unmonitored", field, "This service has no active checks.", "Add or enable an intentional check; registered services remain unknown."})
		} else if applications[service.ID] == 0 {
			out = append(out, Diagnostic{"application_check_missing", field, "The configured checks do not establish application availability.", "Add an HTTP application health check when application availability is required."})
		}
	}
	return out
}
