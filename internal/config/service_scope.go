package config

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func validateServiceScope(x model.Check, c model.Config) error {
	switch x.AvailabilityScope {
	case "":
		return nil
	case "remote", "local", "user_entry":
	default:
		return errors.New("availability_scope must be remote, local or user_entry")
	}
	if x.Service == "" || x.Kind != "http" {
		return errors.New("availability_scope requires an HTTP application check with a service")
	}
	if x.AvailabilityScope == "local" {
		node := ""
		for _, sv := range c.Services {
			if sv.ID == x.Service {
				node = sv.Node
			}
		}
		for _, o := range c.Observers {
			if o.ID == x.Observer && o.Node == node && node != "" && x.Path == "" {
				return nil
			}
		}
		return errors.New("local availability_scope requires an observer on the service host and no remote path")
	}
	return nil
}
