//go:build !js || !collector

package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// Demo runs the real viewer and synthetic source without a terminal, disk or
// collector. Its owner serializes calls, just as the native event loop does.
type Demo struct {
	m       *Model
	sim     *preview.Sim
	encoder cellEncoder
	fetched time.Time
	painted time.Time
	closed  bool
}

type DemoScenario struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

func NewDemo(lang string, now time.Time) (*Demo, error) {
	c, err := preview.Preset(lang)
	if err != nil {
		return nil, err
	}
	sim := preview.New(c, now, lang)
	m := &Model{Language: lang, Now: now, Preview: sim, Server: "synthetic", Colors: colorTrue, Cell: 2}
	m.initializeStartup(nil)
	m.NerdFont = true // Screen draws the same provider outlines without a font.
	for _, o := range c.Observers {
		if m.Observer == "" || o.ID == "obs-mac" {
			m.Observer = o.ID
		}
		if o.ID == "obs-mac" {
			break
		}
	}
	return &Demo{m: m, sim: sim}, nil
}

func (d *Demo) Scenarios() []DemoScenario {
	ids, names := d.sim.ScenarioIDs(), d.sim.Scenarios()
	out := make([]DemoScenario, len(ids))
	for i, id := range ids {
		name, detail := scenarioName(names[i])
		out[i] = DemoScenario{id, name, detail}
	}
	return out
}

func (d *Demo) Key(key string, now time.Time) {
	if d.closed {
		return
	}
	d.m.Now = now
	switch d.m.Key(key) {
	case "quit":
		d.closed = true
	case "view":
		d.m.NextObserver()
		d.fetched = time.Time{}
	case "observe": // a touch screen's menu has picked the observer
		d.fetched = time.Time{}
	case "refresh":
		d.fetched = time.Time{}
	}
}

// SetScenario uses a stable ID, shared by all language catalogs. A switch
// keeps the existing timeline so thresholds and recovery are real transitions.
func (d *Demo) SetScenario(id string, now time.Time) {
	if d.closed {
		return
	}
	if i := slices.Index(d.sim.ScenarioIDs(), id); i >= 0 {
		d.sim.SetScenario(i, now)
		d.m.modal = nil
		d.fetched = time.Time{}
	}
}

func (d *Demo) refresh(now time.Time) {
	m := d.m
	snap, err := d.sim.Snapshot(m.Observer, now)
	d.fetched = now
	if err != nil {
		m.Error = err.Error() // Keep the previous evidence, as the native viewer does.
		return
	}
	m.Snapshot, m.Error = snap, ""
	if id := m.wantSamples(true); id != "" {
		entries, err := d.sim.History(id, view.StripSamples, now)
		m.KeepSamples(id, entries, err)
	}
	for id := m.wantTrend(); id != ""; id = m.wantTrend() {
		entries, err := d.sim.History(id, view.StripSamples, now)
		m.KeepTrend(id, entries, err)
	}
}

// Frame refreshes samples once a second and paints at the native viewer's
// animation cadence. force is for input/resize; idle calls return nil.
func (d *Demo) Frame(now time.Time, cols, rows int, force bool) *DemoFrame {
	m := d.m
	cols, rows = min(240, max(30, cols)), min(90, max(10, rows))
	resized := m.Width != cols || m.Height != rows
	m.Width, m.Height, m.Now = cols, rows, now
	refreshed := !d.closed && (d.fetched.IsZero() || now.Sub(d.fetched) >= time.Second)
	if refreshed {
		d.refresh(now)
	}
	if !force && !resized && !refreshed && (d.closed || now.Sub(d.painted) < m.Animating()) {
		return nil
	}
	if id := m.wantSamples(false); id != "" {
		entries, err := d.sim.History(id, view.StripSamples, now)
		m.KeepSamples(id, entries, err)
	}
	cv := m.draw()
	// Animated colors must not grow the browser palette without bound.
	if len(d.encoder.styles) > 8192 {
		d.encoder = cellEncoder{}
	}
	f := &DemoFrame{CellFrame: d.encoder.encode(cv), Title: m.title(), Tab: m.Tab,
		Observer: m.Observer, Scenario: d.sim.ScenarioIDs()[d.sim.Scenario()],
		Searching: m.Searching, Closed: d.closed, Device: m.deviceID()}
	if force || resized || refreshed {
		f.Text = strings.Join(cv.Lines(colorNone), "\n")
	}
	d.painted = now
	return f
}

func (d *Demo) Measured(render time.Duration, bytes int, at time.Time) {
	d.m.measured(render, bytes, at)
}
