package app

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/WhitenWhiten/topophany/examples"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type topologyTemplate struct {
	ID       string `json:"id"`
	Task     string `json:"task"`
	Guide    string `json:"guide"`
	Boundary string `json:"evidence_boundary"`
}

var topologyTemplates = []topologyTemplate{
	{"minimal", "Basic network checks", "docs/GUIDE.md", "Connection success does not establish application availability."},
	{"homelab", "Multi-location network and proxy paths", "docs/GUIDE.md", "Declared routes are not proof of request-level paths."},
	{"host-resources", "Local CPU, memory, disk and process thresholds", "docs/REFERENCE.md", "Host resource readings do not establish network reachability."},
	{"cloudflare", "Edge, origin, Tunnel and API checks", "docs/CLOUDFLARE.md", "An edge response or active Tunnel does not prove origin application health."},
	{"beszel", "Beszel resource collection through a collector", "internal/adapters/beszel/README.md", "Resource mapping targets Beszel v0.21.0; configure runtime credentials separately."},
	{"komari", "Komari resource collection through a collector", "internal/adapters/komari/README.md", "Resource mapping targets Komari 1.6.0; disk is aggregate and does not prove application availability."},
	{"applications", "Group services by application and environment", "docs/GUIDE.md", "Unmonitored services remain unknown."},
	{"address-registry", "Shared address definitions", "docs/REFERENCE.md", "Address changes can reset affected measurement contracts."},
}

func (a *App) templates(args []string) error {
	f := a.flags("templates")
	output := f.String("output", "", "new topology file; existing files are refused")
	asJSON := f.Bool("json", false, "list template metadata as JSON")
	pos, err := parseMixed(f, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 || len(pos) == 1 && pos[0] == "list" {
		if *output != "" {
			return errors.New("choose a template before specifying --output")
		}
		if *asJSON {
			return printJSON(a.Out, topologyTemplates)
		}
		for _, t := range topologyTemplates {
			fmt.Fprintf(a.Out, "%s: %s\n  %s\n", t.ID, t.Task, t.Boundary)
		}
		return nil
	}
	if len(pos) != 1 || *asJSON {
		return errors.New("use topo templates list --json or topo templates NAME --output NEW_FILE")
	}
	var selected *topologyTemplate
	for i := range topologyTemplates {
		if topologyTemplates[i].ID == pos[0] {
			selected = &topologyTemplates[i]
			break
		}
	}
	if selected == nil {
		return errors.New("unknown template; use topo templates list")
	}
	data, err := examples.Topologies.ReadFile(selected.ID + ".json")
	if err != nil {
		return err
	}
	c, err := config.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	for i := range c.Checks {
		c.Checks[i].Disabled = true
	}
	if err := config.Validate(c); err != nil {
		return err
	}
	if *output == "" {
		return printJSON(a.Out, c)
	}
	data, err = secure.MarshalJSON(c, "  ")
	if err != nil {
		return err
	}
	if _, err = config.CommitFile(*output, config.FileVersion{}, append(data, '\n'), 0600); err != nil {
		return err
	}
	return printJSON(a.Out, struct {
		Path   string `json:"path"`
		Checks int    `json:"paused_checks"`
		Guide  string `json:"guide"`
		Next   string `json:"next"`
	}{*output, len(c.Checks), selected.Guide, "Review addresses, observer identity, assertions and required secret environment variables; enable only the checks you intend to run, then validate."})
}
