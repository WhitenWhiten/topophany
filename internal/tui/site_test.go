package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// The homepage (site/public) replays the real TUI: `make site` drives a preview
// model through the tour below, once in each language the preview is written
// in, and writes every frame as the cells that changed since the one before
// (tour.<language>.json), with the land mask the hero globe is drawn from.
// Without TOPO_SITE the test only records the tour, so one the preview has
// outgrown fails before it reaches the homepage.

// The tour is recorded as a wide terminal: at 200 columns the full maps
// have room for their places' cards and the world beside them. The
// README's animations and its still are cut from it at the same size.
const tourW, tourH = 200, 50

type tourFrame struct {
	D   int     `json:"d"`   // milliseconds this frame stays on screen
	Ops [][]any `json:"ops"` // [y, x, text, [style, count, ...]]
}

type tourChapter struct {
	ID     string      `json:"id"`
	Tab    int         `json:"tab"`
	Frames []tourFrame `json:"frames"`
}

type tour struct {
	Size     [2]int        `json:"size"`
	Styles   [][3]int      `json:"styles"` // fg (-1 default), bg, attributes
	Tabs     [][2]int      `json:"tabs"`   // x, width of each tab on row 1
	Names    []string      `json:"names"`  // the name of each tab
	Chapters []tourChapter `json:"chapters"`
}

type recorder struct {
	t       *testing.T
	m       *Model
	sim     *preview.Sim
	fetched time.Time
	encoder cellEncoder
	out     tour
}

func newRecorder(t *testing.T, lang string) *recorder {
	c, e := preview.Preset(lang)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	sim := preview.New(c, now, lang)
	r := &recorder{t: t, sim: sim}
	// The page draws the providers' logos itself, so the tour shows them as
	// a terminal with a Nerd Font does.
	r.m = &Model{Language: lang, Now: now, Width: tourW, Height: tourH, Preview: sim, Server: "synthetic", Colors: colorTrue, NerdFont: true}
	r.out.Size = [2]int{tourW, tourH}
	r.out.Names = tabs
	r.fetch()
	// As the run loop's sampler does after the first snapshot, the charts
	// read each host check's history.
	for id := r.m.wantTrend(); id != ""; id = r.m.wantTrend() {
		entries, e := sim.History(id, view.StripSamples, now)
		r.m.KeepTrend(id, entries, e)
	}
	r.m.say("Preview: synthetic evidence only · x switch scenario · D debug overlay", false)
	return r
}

func (r *recorder) fetch() {
	snap, e := r.sim.Snapshot(r.m.Observer, r.m.Now)
	if e != nil {
		r.t.Fatal(e)
	}
	r.m.Snapshot, r.fetched = snap, r.m.Now
}

// chapter starts a new chapter with a full frame, so the page can jump to it.
func (r *recorder) chapter(id string) {
	r.out.Chapters = append(r.out.Chapters, tourChapter{ID: id, Tab: r.m.Tab})
	r.encoder.prev = nil
}

// shoot renders the model and records the cells that changed, held for d.
func (r *recorder) shoot(d time.Duration) {
	cv := r.m.draw()
	if cv == nil {
		r.t.Fatal("tour frame too small")
	}
	if r.out.Tabs == nil {
		for _, h := range r.m.hits {
			if h.y == 1 && strings.HasPrefix(h.key, "tab:") {
				r.out.Tabs = append(r.out.Tabs, [2]int{h.x, h.w})
			}
		}
	}
	r.frame(cv, d)
}

// frame records the cells of cv that changed since the last frame, held
// for d.
func (r *recorder) frame(cv *Canvas, d time.Duration) {
	ch := &r.out.Chapters[len(r.out.Chapters)-1]
	f := r.encoder.encode(cv)
	r.out.Styles = append(r.out.Styles, f.Styles...)
	ch.Frames = append(ch.Frames, tourFrame{D: int(d / time.Millisecond), Ops: f.Ops})
}

// play runs the model for total of simulated time, a frame every step,
// each shown for show (shorter than step for a time-lapse). Snapshots
// refresh every second, as in topo preview.
func (r *recorder) play(total, step, show time.Duration) {
	for el := time.Duration(0); el < total; el += step {
		r.shoot(show)
		r.wait(step)
	}
}

func (r *recorder) wait(d time.Duration) {
	r.m.Now = r.m.Now.Add(d)
	if r.m.Now.Sub(r.fetched) >= time.Second {
		r.fetch()
	}
}

// key presses k and shows the result for hold.
func (r *recorder) key(k string, hold time.Duration) {
	switch r.m.Key(k) {
	case "view":
		r.m.NextObserver()
		r.fetch()
	}
	r.shoot(hold)
	r.wait(hold)
}

// observe switches the view to observer id, which the preview must have: a
// view of no observer would be recorded as a screen with nothing measured.
func (r *recorder) observe(id string) {
	if id != "" && !slices.ContainsFunc(r.m.Snapshot.Topology.Observers, func(o model.Observer) bool { return o.ID == id }) {
		r.t.Fatalf("the preview has no observer %q", id)
	}
	r.m.Observer = id
	r.m.Selected, r.m.Offset = 0, 0
	r.fetch()
}

const milli = time.Millisecond

// record is the tour: an outage arriving on the Overview, its route and
// evidence, the same network from two vantage points, the three maps, the
// servers as a wall, as a grid and the main one laid open, the incidents
// and the About globe.
func (r *recorder) record() {
	m := r.m
	s := time.Second

	r.chapter("outage")
	r.play(3*s, 500*milli, 500*milli)
	r.key("x", 900*milli)
	r.key("j", 1200*milli)
	m.modal = nil
	r.sim.SetScenario(1, m.Now)
	name, _ := scenarioName(r.sim.Scenarios()[1])
	m.say("Scenario: "+name+" — states change as new samples arrive", false)
	r.fetch()
	// Evidence arrives sample by sample: a 75 s time-lapse.
	r.play(75*s, s, 160*milli)
	r.play(2*s, 500*milli, 500*milli)

	r.chapter("route")
	for i := 0; i < 5; i++ {
		m.Selected = i
		r.play(1500*milli, 500*milli, 500*milli)
	}
	m.Selected = 0
	r.key("enter", 4*s)
	m.Detail = false

	r.chapter("vantage")
	r.observe("obs-mac")
	r.play(3*s, 500*milli, 500*milli)
	r.observe("obs-app")
	r.play(3*s, 500*milli, 500*milli)
	r.observe("")

	m.setTab(tabTopology)
	m.MapMode = "virtual"
	r.chapter("virtual")
	r.play(5*s, 140*milli, 140*milli)

	m.MapMode = "geo"
	r.chapter("geo")
	r.play(3*s, 140*milli, 140*milli)
	// Give the geographic map room, then show all three presentations and
	// return to the panels before moving on to the network graph.
	for _, key := range []string{"f", "p", "p", "p", "f"} {
		m.Key(key)
		r.play(3*s, 140*milli, 140*milli)
	}

	m.MapMode = "network"
	r.chapter("network")
	r.play(5*s, 140*milli, 140*milli)
	m.MapMode = ""

	m.setTab(tabServers)
	r.chapter("servers")
	for i := 0; i < 4; i++ {
		m.Selected = i
		r.play(1500*milli, 500*milli, 500*milli)
	}
	// The same cards at one size; then over to the main server, and its
	// card laid open over them.
	r.key("s", 2500*milli)
	if app := slices.IndexFunc(m.Rows(), func(row Row) bool { return row.ID == "app" }); app >= 0 {
		for m.Selected != app {
			k := "j"
			if m.Selected > app {
				k = "k"
			}
			r.key(k, 400*milli)
		}
	}
	r.key("enter", 4500*milli)
	m.Detail, m.Uniform = false, false

	m.setTab(tabIncidents)
	r.chapter("incidents")
	r.play(2*s, 500*milli, 500*milli)
	r.key("enter", 4*s)

	m.setTab(tabAbout)
	m.aboutAt = m.Now
	r.chapter("about")
	r.play(7*s, 100*milli, 100*milli)
}

// tours holds the tour in each language once recorded, for the homepage and
// the README's animations to share: recording all three takes seconds.
var tours sync.Map

// recordTour records the tour in lang, with timestamps in UTC so that the
// evidence reads the same wherever it is made.
func recordTour(t *testing.T, lang string) tour {
	if out, ok := tours.Load(lang); ok {
		return out.(tour)
	}
	out := recordTourOnce(t, lang)
	tours.Store(lang, out)
	return out
}

// recordTourOnce records without the shared cache, so a test can record the
// same tour twice: the homepage is generated from it and has to be the same
// bytes every time it is rebuilt from the same source.
func recordTourOnce(t *testing.T, lang string) tour {
	t.Helper()
	local := time.Local
	time.Local = time.UTC
	defer func() { time.Local = local }()
	r := newRecorder(t, lang)
	r.record()
	return r.out
}

// The tours are generated from the TUI and committed. Rebuilding one has to
// match the committed bytes, or the site says something the code no longer
// draws; site-check also guards the pages and wasm names for a full build.
func TestCommittedToursAreCurrent(t *testing.T) {
	dir := filepath.Join("..", "..", "site", "public", "data")
	if _, e := os.Stat(dir); e != nil {
		t.Skip("the site is not part of this checkout")
	}
	for _, lang := range preview.Languages() {
		want, e := os.ReadFile(filepath.Join(dir, "tour."+lang+".json"))
		if e != nil {
			t.Fatalf("read %s tour: %v; run make site", lang, e)
		}
		got, e := json.Marshal(recordTour(t, lang))
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s tour is stale; run make site", lang)
		}
	}
}

func TestTourIsReproducible(t *testing.T) {
	lang := preview.DefaultLanguage
	first, second := recordTourOnce(t, lang), recordTourOnce(t, lang)
	a, e := json.Marshal(first)
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(second)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("recording the same tour twice produced different bytes; the site would churn on every rebuild")
	}
}

// A tour recorded on one architecture has to match a rebuild on another.
// Each case lands within an ulp of a channel's rounding boundary, where a
// fused multiply-add (arm64 fuses one, amd64 does not) rounds the other way.
func TestMixRoundsTheSameOnEveryArchitecture(t *testing.T) {
	for _, tc := range []struct {
		a, b Color
		t    float64
		want Color
	}{
		{Hex(0x050505), Hex(0x000000), .9, Hex(0x010101)},
		{Hex(0x0f1011), Hex(0x000102), .9, Hex(0x020304)},
	} {
		if got := Mix(tc.a, tc.b, tc.t); got != tc.want {
			t.Errorf("Mix(%06x, %06x, %v) = %06x, want %06x", uint32(tc.a)&0xFFFFFF, uint32(tc.b)&0xFFFFFF, tc.t, uint32(got)&0xFFFFFF, uint32(tc.want)&0xFFFFFF)
		}
	}
}

func TestSite(t *testing.T) {
	dir := os.Getenv("TOPO_SITE")
	for _, lang := range preview.Languages() {
		out := recordTour(t, lang)
		b, e := json.Marshal(out)
		if e != nil {
			t.Fatal(e)
		}
		frames := 0
		for _, c := range out.Chapters {
			frames += len(c.Frames)
		}
		t.Logf("%s: %d chapters, %d frames, %d styles, %d bytes", lang, len(out.Chapters), frames, len(out.Styles), len(b))
		if dir == "" {
			continue
		}
		if e := os.WriteFile(filepath.Join(dir, "tour."+lang+".json"), b, 0o644); e != nil {
			t.Fatal(e)
		}
	}
	if dir == "" {
		return
	}
	if e := os.WriteFile(filepath.Join(dir, "land.bin"), landBits, 0o644); e != nil {
		t.Fatal(e)
	}
}
