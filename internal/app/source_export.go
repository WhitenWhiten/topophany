package app

import (
	"errors"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func (a *App) exportDirect(args []string) error {
	f := a.flags("sources export")
	root := f.String("direct-root", "", "private direct configuration root")
	profile := f.String("profile", "", "direct profile to migrate")
	output := f.String("output", "", "new collector topology path")
	observer := f.String("observer", "source-worker", "worker that will read the sources")
	if err := parsed(f, args); err != nil {
		return err
	}
	if *profile == "" || *output == "" {
		return errors.New("use sources export --profile NAME --output NEW_FILE [--observer ID]")
	}
	store, err := direct.NewStore(*root)
	if err != nil {
		return err
	}
	p, err := store.Load(*profile)
	if err != nil {
		return err
	}
	c, err := p.CollectorConfig(*observer)
	if err != nil {
		return err
	}
	data, err := secure.MarshalJSON(c, "  ")
	if err != nil {
		return err
	}
	if _, err = config.CommitFile(*output, config.FileVersion{}, append(data, '\n'), 0600); err != nil {
		return err
	}
	type credentialRequirement struct {
		Source      string `json:"source"`
		Environment string `json:"environment"`
	}
	requirements := []credentialRequirement{}
	for _, s := range c.Sources {
		if s.CredentialEnv != "" {
			requirements = append(requirements, credentialRequirement{s.ID, s.CredentialEnv})
		}
	}
	return printJSON(a.Out, struct {
		Project     string                  `json:"project"`
		Observer    string                  `json:"observer"`
		Nodes       int                     `json:"preserved_nodes"`
		Checks      int                     `json:"paused_checks"`
		Credentials []credentialRequirement `json:"required_credentials"`
		Next        []string                `json:"next_actions"`
	}{c.Project, *observer, len(c.Nodes), len(c.Checks), requirements, []string{
		"Provision source tokens in the named worker environment variables; credentials were not copied.",
		"Review and enable intended checks, validate the topology and prepare collector/worker deployment bundles with topo setup.",
		"Wait for current resource samples with topo doctor. In the menu bar select the collector profile; unchanged node IDs preserve pinned-server references.",
		"Direct mode has no stored history to transfer. Keep the original profile until the collector has been verified.",
	}})
}
