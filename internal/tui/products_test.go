package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// productsModel is the TUI in Products mode over the products example, half
// an hour into scenario ("" for none), with every product line's history
// read as the run loop's sampler reads it.
func productsModel(t *testing.T, scenario string, w, h int) *Model {
	t.Helper()
	c, e := config.Load(filepath.Join("..", "..", "examples", "products.json"))
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 18, 13, 30, 0, 0, time.UTC)
	sim := preview.New(c, now, "en")
	if scenario != "" {
		i := slices.Index(sim.ScenarioIDs(), scenario)
		if i < 0 {
			t.Fatalf("the products example offers no %s scenario: %v", scenario, sim.ScenarioIDs())
		}
		sim.SetScenario(i, now.Add(-30*time.Minute))
	}
	snap, e := sim.Snapshot("", now)
	if e != nil {
		t.Fatal(e)
	}
	m := &Model{Snapshot: snap, Now: now, Width: w, Height: h, Preview: sim, Server: "synthetic", Products: true}
	for id := m.wantTrend(); id != "" && m.trends[id] == nil; id = m.wantTrend() {
		entries, e := sim.History(id, view.StripSamples, now)
		m.KeepTrend(id, entries, e)
	}
	return m
}

func TestProductsOverviewShowsEveryLine(t *testing.T) {
	m := productsModel(t, "", 160, 48)
	out := visible(m)
	if os.Getenv("TOPO_PRINT") != "" {
		t.Log("\n" + out)
	}
	for _, want := range []string{"PRODUCTS", "· 4 products", "Habitly", "Snapmeal", "Driftwood", "Notebook",
		"RESTS ON", "P50", "RECENT", "STATUS", "EVIDENCE", "P  Network", "CERTIFICATES"} {
		if !strings.Contains(out, want) {
			t.Errorf("the products Overview lacks %q:\n%s", want, out)
		}
	}
	rows, lines := m.productLines()
	if len(rows) != len(lines) || len(lines) != 6 {
		t.Fatalf("the example's six lines are not the Overview's rows: %d rows, %d lines", len(rows), len(lines))
	}
	for i, r := range m.Rows() {
		if r.ID != lines[i].l.ID || r.Meta != lines[i].p.Name {
			t.Fatalf("row %d is %s of %s, the board's %s of %s", i, r.ID, r.Meta, lines[i].l.ID, lines[i].p.Name)
		}
	}
	// Calm is quiet: nothing asks for a look, and no map is drawn.
	if strings.Contains(out, "└ ") {
		t.Errorf("a calm board opens a look row:\n%s", out)
	}
	if m.mapShown() {
		t.Error("the products Overview still draws the map")
	}
}

func TestProductsOverviewOpensTheLineInTrouble(t *testing.T) {
	m := productsModel(t, "app-origin-down", 160, 48)
	out := visible(m)
	if os.Getenv("TOPO_PRINT") != "" {
		t.Log("\n" + out)
	}
	_, lines := m.productLines()
	i := slices.IndexFunc(lines, func(pl productLine) bool { return pl.l.ID == "snapmeal-api" })
	if i < 0 || !lines[i].l.NeedsLook() {
		t.Fatalf("Snapmeal's API does not ask for a look: %+v", lines)
	}
	look := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "└ ") {
			look = l
		}
	}
	// Where it all fits, the look row says where the lead was seen from;
	// where it does not, that goes first and the incidents stay.
	for _, want := range []string{"ACCESS FAIL", "for ", "HTTP 521 via Cloudflare LIS", "1 incident"} {
		if !strings.Contains(look, want) {
			t.Errorf("the look row lacks %q: %q", want, look)
		}
	}
	// Selecting the line shows its evidence beside the board.
	m.Selected = i
	out = visible(m)
	for _, want := range []string{"Snapmeal API", "Snapmeal · ios", "RESTS ON", "LEAD CHECK", "snapmeal-api-home"} {
		if !strings.Contains(out, want) {
			t.Errorf("the selected line's evidence lacks %q:\n%s", want, out)
		}
	}
}

func TestProductsOverviewFitsEverySize(t *testing.T) {
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {139, 40}, {160, 48}, {220, 60}} {
		for _, scenario := range []string{"", "app-origin-down", "certificate-expiring"} {
			m := productsModel(t, scenario, size[0], size[1])
			lines := m.Render()
			if len(lines) != size[1] {
				t.Fatalf("%dx%d %s: %d lines", size[0], size[1], scenario, len(lines))
			}
			for k, l := range lines {
				if w := width(ansi.ReplaceAllString(l, "")); w > size[0] {
					t.Fatalf("%dx%d %s: line %d is %d cells wide: %q", size[0], size[1], scenario, k, w, l)
				}
			}
			out := visible(m)
			if !strings.Contains(out, "PRODUCTS") || !strings.Contains(out, "Habitly") {
				t.Errorf("%dx%d %s: the board is not shown:\n%s", size[0], size[1], scenario, out)
			}
			if scenario == "app-origin-down" && size[0] >= 90 && !strings.Contains(out, "⚑ 1 incident") {
				t.Errorf("%dx%d: a look row lost its incident:\n%s", size[0], size[1], out)
			}
			if scenario == "app-origin-down" && size[0] >= 220 && !strings.Contains(out, "seen from Lisbon") {
				t.Errorf("%dx%d: a wide look row does not say where it was seen from:\n%s", size[0], size[1], out)
			}
			if os.Getenv("TOPO_PRINT") != "" && scenario == "app-origin-down" {
				t.Logf("%dx%d\n%s", size[0], size[1], out)
			}
		}
	}
}

func TestProductsKeyTogglesTheOverview(t *testing.T) {
	m := productsModel(t, "", 160, 48)
	m.Products = false
	m.setTab(tabServices)
	m.Key("P")
	if !m.Products || m.Tab != tabOverview {
		t.Fatalf("P does not open the products Overview: products %v, tab %d", m.Products, m.Tab)
	}
	if out := visible(m); !strings.Contains(out, "PRODUCTS") {
		t.Fatalf("the Overview does not show the board:\n%s", out)
	}
	m.Key("P")
	if m.Products {
		t.Fatal("P does not switch back to the network")
	}
	if out := visible(m); strings.Contains(out, "PRODUCTS") || strings.Contains(out, "P  Network") {
		t.Fatalf("the network Overview still shows the board:\n%s", out)
	}
}

func TestProductsFilterNarrowsTheBoard(t *testing.T) {
	m := productsModel(t, "", 160, 48)
	m.Search = "supabase"
	_, lines := m.productLines()
	for _, pl := range lines {
		if pl.p.ID != "habitly" && pl.p.ID != "driftwood" {
			t.Errorf("%s of %s matches supabase", pl.l.ID, pl.p.ID)
		}
	}
	if len(lines) == 0 {
		t.Fatal("no line rests on Supabase")
	}
	m.Search = "nothing-like-this"
	if out := visible(m); !strings.Contains(out, "Nothing matches") {
		t.Fatalf("an empty filter says nothing:\n%s", out)
	}
}
