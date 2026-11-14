package config

import (
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func validateApplications(c model.Config) error {
	apps := map[string]bool{}
	for _, a := range c.Applications {
		if !ID(a.ID) || apps[a.ID] || len(a.Name) > 96 || secure.Text(a.Name) != a.Name {
			return fmt.Errorf("invalid or duplicate application %q", a.ID)
		}
		if a.Kind != "" && (len(a.Kind) > 32 || !ID(a.Kind)) {
			return fmt.Errorf("application %s: kind must be a short identifier such as ios, web or api", a.ID)
		}
		apps[a.ID] = true
	}
	scope := func(app, env string) error {
		if app != "" && !apps[app] {
			return fmt.Errorf("unknown application %q", app)
		}
		if env != "" && !ID(env) {
			return fmt.Errorf("invalid environment %q", env)
		}
		return nil
	}
	for _, s := range c.Services {
		if err := scope(s.Application, s.Environment); err != nil {
			return fmt.Errorf("service %s: %w", s.ID, err)
		}
	}
	for _, x := range c.Checks {
		if x.Service != "" && (x.Application != "" || x.Environment != "") {
			return fmt.Errorf("check %s: application and environment are inherited from its service", x.ID)
		}
		if err := scope(x.Application, x.Environment); err != nil {
			return fmt.Errorf("check %s: %w", x.ID, err)
		}
	}
	return nil
}
