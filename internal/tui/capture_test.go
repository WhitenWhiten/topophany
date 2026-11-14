package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// The captures in docs/assets are stills of the TUI on the preview, the
// README's in every language the preview is written in and the guide's in
// the default one, and the README's animations, which are chapters of the
// homepage's tour. `make captures` records each in the tour's format
// (<name>.<language>.json), a still as one frame, for scripts/capture.py to
// paint with the homepage's terminal renderer. Without TOPO_CAPTURES the
// test only checks that every capture renders.

type docShot struct {
	name     string
	readme   bool     // shown in each README, so captured in every language
	example  string   // the examples/ topology simulated, else the preview's own network
	scenario string   // the scenario half an hour old, else the transit outage
	tab      int      // the tab shown
	mapMode  string   // the map, where the tab has one
	products bool     // the Overview turned to the products (P)
	rows     int      // the terminal's height, else the tour's or the guide's
	selected string   // the ID of the row selected, else the first
	keys     []string // pressed once the row is selected
}

var docShots = []docShot{
	{name: "overview", readme: true, tab: tabOverview},
	// The board is a few rows a product, so a shorter terminal keeps it full.
	{name: "products", readme: true, example: "products.json", scenario: "app-origin-down", tab: tabOverview, products: true, rows: 30},
	{name: "servers", tab: tabServers, selected: "app"},
	{name: "servers-uniform", tab: tabServers, selected: "app", keys: []string{"s"}},
	{name: "server-card", tab: tabServers, selected: "app", keys: []string{"s", "enter"}},
	{name: "services-cards", tab: tabServices, selected: "vless-transit", keys: []string{"s"}},
	{name: "service-card", tab: tabServices, selected: "vless-transit", keys: []string{"s", "enter"}},
	{name: "network", tab: tabTopology, mapMode: "network", selected: "transit"},
	{name: "incident", tab: tabIncidents},
}

// docClip is an animation in each README: chapters of the tour, in order.
type docClip struct {
	name     string
	chapters []string
}

var docClips = []docClip{
	// An outage, its route and evidence, then the geographic map controls.
	{name: "outage", chapters: []string{"outage", "route", "geo"}},
	// The network from two vantage points, then the three maps.
	{name: "views", chapters: []string{"vantage", "virtual", "geo", "network"}},
}

// clip is the tour in lang cut down to the chapters of c.
func clip(t *testing.T, c docClip, lang string) tour {
	out := recordTour(t, lang)
	all := out.Chapters
	out.Chapters = nil
	for _, id := range c.chapters {
		i := slices.IndexFunc(all, func(ch tourChapter) bool { return ch.ID == id })
		if i < 0 {
			t.Fatalf("%s: the tour has no chapter %q", c.name, id)
		}
		out.Chapters = append(out.Chapters, all[i])
	}
	return out
}

// The guide's stills sit two to a row, so they keep a terminal's size at
// which their words still read; the README's are as wide as the tour.
const guideW, guideH = 140, 42

// shot is the TUI as it shows c in lang: half an hour into its scenario,
// four seconds after the last snapshot.
func shot(t *testing.T, c docShot, lang string) tour {
	var r *recorder
	if c.example != "" {
		cfg, e := config.Load(filepath.Join("..", "..", "examples", c.example))
		if e != nil {
			t.Fatal(e)
		}
		r = newRecorderOn(t, lang, cfg)
	} else {
		r = newRecorder(t, lang)
	}
	m := r.m
	if !c.readme {
		m.Width, m.Height = guideW, guideH
	}
	if c.rows > 0 {
		m.Height = c.rows
	}
	r.out.Size = [2]int{m.Width, m.Height}
	scenario := c.scenario
	if scenario == "" {
		scenario = "transit-blocked"
	}
	i := slices.Index(r.sim.ScenarioIDs(), scenario)
	if i < 0 {
		t.Fatalf("%s: the preview offers no %s scenario: %v", c.name, scenario, r.sim.ScenarioIDs())
	}
	r.sim.SetScenario(i, m.Now.Add(-30*time.Minute))
	r.fetch()
	m.Now = m.Now.Add(4 * time.Second)
	m.notice = ""
	m.setTab(c.tab)
	m.MapMode = c.mapMode
	m.Products = c.products
	if c.selected != "" {
		m.Selected = slices.IndexFunc(m.Rows(), func(row Row) bool { return row.ID == c.selected })
		if m.Selected < 0 {
			t.Fatalf("%s: no row %q", c.name, c.selected)
		}
	}
	for _, k := range c.keys {
		m.Key(k)
	}
	// As the run loop's sampler does, what is now shown reads its history.
	for id := m.wantTrend(); id != ""; id = m.wantTrend() {
		entries, e := r.sim.History(id, view.StripSamples, m.Now)
		m.KeepTrend(id, entries, e)
	}
	r.chapter(c.name)
	r.shoot(0)
	return r.out
}

func TestCaptures(t *testing.T) {
	dir := os.Getenv("TOPO_CAPTURES")
	local := time.Local
	time.Local = time.UTC
	defer func() { time.Local = local }()
	write := func(name, lang string, out tour) {
		b, e := json.Marshal(out)
		if e != nil {
			t.Fatal(e)
		}
		if dir == "" {
			return
		}
		if e := os.WriteFile(filepath.Join(dir, name+"."+lang+".json"), b, 0o644); e != nil {
			t.Fatal(e)
		}
	}
	for _, c := range docShots {
		langs := []string{preview.DefaultLanguage}
		if c.readme {
			langs = preview.Languages()
		}
		for _, lang := range langs {
			write(c.name, lang, shot(t, c, lang))
		}
	}
	for _, c := range docClips {
		for _, lang := range preview.Languages() {
			write(c.name, lang, clip(t, c, lang))
		}
	}
}
