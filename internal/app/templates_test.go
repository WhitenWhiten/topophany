package app

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"path/filepath"
	"testing"
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
