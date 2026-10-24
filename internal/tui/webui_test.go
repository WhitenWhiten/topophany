//go:build webui

package tui

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
	"math"
	"testing"
	"time"
)

func TestNativeMapsUseGraphSignalsAndFocus(t *testing.T) {
	c, e := preview.Preset("en")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	sim := preview.New(c, now.Add(-time.Minute), "en")
	snap, e := sim.Snapshot("", now)
	if e != nil {
		t.Fatal(e)
	}
	nodes, _ := (view.View{Snapshot: snap, Now: now}).Graph()
	for _, mode := range []string{"virtual", "geo", "network"} {
		t.Run(mode, func(t *testing.T) {
			m := NativeMap(snap, mode, view.Row{Kind: "node", ID: c.Nodes[0].ID}, false)
			if len(m.Nodes) != len(nodes) {
				t.Fatal(len(m.Nodes), len(nodes), m.Notice)
			}
			for i, n := range m.Nodes {
				if n.Signal != view.NodeSignal(nodes[i]) || math.IsNaN(n.X) || math.IsNaN(n.Y) {
					t.Fatal(n)
				}
				if n.ID == c.Nodes[0].ID && !n.Focus {
					t.Fatal("selection lost")
				}
			}
			if mode == "geo" && len(m.Land) != 28 {
				t.Fatal("missing original Braille land")
			}
		})
	}
	cached := NativeMap(snap, "network", view.Row{}, true)
	for _, n := range cached.Nodes {
		if n.Signal != "unknown" {
			t.Fatal("cached map claims current health", n)
		}
	}
}
func TestNativeMapBoundsAndUnlocatedTopology(t *testing.T) {
	s := model.Snapshot{Topology: model.Config{Nodes: []model.Node{{ID: "one", Name: "No location"}}}}
	m := NativeMap(s, "virtual", view.Row{}, false)
	if m.Mode != "network" || m.Notice == "" {
		t.Fatal(m)
	}
	for i := 0; i < 300; i++ {
		s.Topology.Nodes = append(s.Topology.Nodes, model.Node{ID: "node"})
	}
	m = NativeMap(s, "network", view.Row{}, false)
	if m.Notice == "" || len(m.Nodes) != 0 {
		t.Fatal("unbounded map")
	}
}

func TestNativeAboutUsesOriginalGlobe(t *testing.T) {
	c, err := preview.Preset("en")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sim := preview.New(c, now.Add(-time.Minute), "en")
	snap, err := sim.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	globe := NativeMap(snap, "globe", view.Row{}, false)
	if len(globe.Globe) == 0 || globe.Width != globe.Height || len(globe.Places) == 0 {
		t.Fatal("missing native globe geometry")
	}
}
