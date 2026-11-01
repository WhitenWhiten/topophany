package app

import (
	"errors"
	"net/url"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// websiteConfig prepares a reviewed local check; setup never contacts the target.
func websiteConfig(raw string) (model.Config, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return model.Config{}, errors.New("website requires an HTTP(S) URL without credentials, query or fragment; configure private request headers separately")
	}
	c := model.Config{Schema: 1, Project: "website-monitor",
		Nodes:     []model.Node{{ID: "local", Name: "This machine", Kind: "host"}, {ID: "website", Name: u.Hostname(), Kind: "external"}},
		Observers: []model.Observer{{ID: "demo", Name: "This machine", Node: "local", Context: "local website monitoring"}},
		Endpoints: []model.Endpoint{{ID: "website", Node: "website", Address: raw, Network: "public"}},
		Services:  []model.Service{{ID: "website", Name: u.Hostname(), Node: "website", Endpoints: []string{"website"}}},
		Checks:    []model.Check{{ID: "website-http", Name: "Website response", Observer: "demo", Endpoint: "website", Service: "website", Kind: "http", AvailabilityScope: "remote", HTTP: model.HTTPOptions{Status: []int{200}}}},
	}
	config.Defaults(&c)
	return c, config.Validate(c)
}
