package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
)

// A form widens for its longest label where the terminal has the room, so
// labels that differ only at their end ("Cloudflared expected connections",
// "Cloudflared expected colos") never read alike.
func TestFormLabelsReadWhole(t *testing.T) {
	c, e := config.Load(filepath.Join("..", "..", "examples", "cloudflare-diagnostics.json"))
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		id    string
		label []string
	}{
		{"tunnel-connector", []string{"Cloudflared expected connections", "Cloudflared expected colos"}},
		{"api-tunnel-connections", []string{"Cloudflare expected connections", "Cloudflare expected content"}},
	} {
		m := &Model{Edit: &Editor{Path: "topology.json", Config: c}, Width: 200, Height: 60, NoColor: true, Now: time.Now()}
		m.rebuild()
		m.openForm("check", tc.id)
		out := visible(m)
		for _, l := range tc.label {
			if !strings.Contains(out, l) {
				t.Fatalf("%s: the label %q is cut:\n%s", tc.id, l, out)
			}
		}
	}
}
