package app

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestTemplatesProduceValidatedPausedConfigurations(t *testing.T) {
	for _, template := range topologyTemplates {
		t.Run(template.ID, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "topology.json")
			if _, _, err := invoke("", "templates", template.ID, "--output", path); err != nil {
				t.Fatal(err)
			}
			c, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range c.Checks {
				if !check.Disabled {
					t.Fatal("template runs without review")
				}
			}
			if _, _, err := invoke("", "templates", template.ID, "--output", path); err == nil {
				t.Fatal("template overwrote a configuration")
			}
		})
	}
}

// The mihomo starter reads the core's controller over literal loopback on
// the machine that runs it, asserts reported runtime state only, and keeps
// a separate application check for availability.
func TestMihomoTemplateKeepsTheEvidenceBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihomo.json")
	if _, _, err := invoke("", "templates", "mihomo", "--output", path); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var core, app *model.Check
	for i := range c.Checks {
		switch c.Checks[i].Kind {
		case "mihomo":
			core = &c.Checks[i]
		case "http":
			app = &c.Checks[i]
		}
	}
	if core == nil || app == nil {
		t.Fatal("template lacks the runtime or the application check", c.Checks)
	}
	u, err := url.Parse(config.Target(c, *core))
	if err != nil || u.String() != "http://127.0.0.1:9090" || core.Path != "" || core.Service != "" {
		t.Fatal("controller is not a direct literal loopback read", u, core.Path, core.Service)
	}
	var observer model.Observer
	for _, o := range c.Observers {
		if o.ID == core.Observer {
			observer = o
		}
	}
	var endpoint model.Endpoint
	for _, e := range c.Endpoints {
		if e.ID == core.Endpoint {
			endpoint = e
		}
	}
	if endpoint.Network != "loopback" || observer.Node == "" || observer.Node != endpoint.Node || endpoint.Observer != core.Observer {
		t.Fatal("the observer does not run beside the core", observer, endpoint)
	}
	a := core.Adapter
	if a.TokenEnv == "" || a.RootProxy == "" || a.ExpectedMode != "rule" || a.ExpectedTUN == nil {
		t.Fatalf("%+v", a)
	}
	if !strings.Contains(core.Notes, "not proof that application traffic followed that route") || !strings.Contains(core.Notes, app.ID) {
		t.Fatal(core.Notes)
	}
	if app.Service == "" || app.Path == "" || app.Observer != core.Observer || app.AvailabilityScope != "user_entry" {
		t.Fatalf("application check: %+v", app)
	}
	for _, tpl := range topologyTemplates {
		if tpl.ID == "mihomo" && (tpl.Guide != "internal/adapters/mihomo/README.md" || !strings.Contains(tpl.Boundary, "application check")) {
			t.Fatal(tpl)
		}
	}
	out, _, err := invoke("", "templates", "list")
	if err != nil || !strings.Contains(out, "mihomo: ") {
		t.Fatal(err, out)
	}
}
