package config

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
)

func TestAvailabilityScopeIsValidatedAndPreservesMeasurement(t *testing.T) {
	c := model.Config{Project: "lab", Services: []model.Service{{ID: "web", Node: "s2"}}, Observers: []model.Observer{{ID: "home", Node: "home"}}, Checks: []model.Check{{ID: "http", Observer: "home", Kind: "http", Target: "https://example.test"}}}
	x := c.Checks[0]
	x.Kind = "http"
	x.Service = c.Services[0].ID
	x.Path = ""
	original := Contract(c, x)
	x.AvailabilityScope = "user_entry"
	if err := validateServiceScope(x, c); err != nil {
		t.Fatal(err)
	}
	if Contract(c, x) != original {
		t.Fatal("display scope changed sample identity")
	}
	x.AvailabilityScope = "local"
	c.Observers[0].Node = c.Services[0].Node
	x.Observer = c.Observers[0].ID
	if err := validateServiceScope(x, c); err != nil {
		t.Fatal(err)
	}
	c.Observers[0].Node = "somewhere-else"
	if validateServiceScope(x, c) == nil {
		t.Fatal("foreign local assertion accepted")
	}
	x.AvailabilityScope = "anywhere"
	if validateServiceScope(x, c) == nil {
		t.Fatal("unknown scope accepted")
	}
}
