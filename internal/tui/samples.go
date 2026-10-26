package tui

import (
	"context"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// History reads up to limit of a check's recent samples, newest first.
type History func(ctx context.Context, check string, limit int) ([]model.Observation, error)

type snapshotHistory func(context.Context, client.HistoryView, int) ([]model.Observation, error)

func collectorHistory(c *client.Client) snapshotHistory {
	return func(ctx context.Context, v client.HistoryView, limit int) ([]model.Observation, error) {
		h, _, err := c.HistoryAt(ctx, v, limit)
		return h.Entries, err
	}
}

// samples are a check's recent samples as last read for its evidence.
type samples struct {
	check   string
	entries []model.Observation
	err     string
}

// wantSamples names the check whose samples the evidence needs read, or ""
// when none: the selected check's when none are held for it and, at a
// refresh, when reading them failed or the snapshot holds a newer sample.
func (m *Model) wantSamples(refresh bool) string {
	if m.Edit != nil || m.Tab != tabChecks {
		return ""
	}
	rows := m.Rows()
	if m.Selected < 0 || m.Selected >= len(rows) || rows[m.Selected].Kind != "check" {
		return ""
	}
	id, h := rows[m.Selected].ID, m.samples
	switch {
	case h.check != id:
		return id
	case !refresh:
		return ""
	case h.err != "":
		return id
	}
	for _, c := range m.Snapshot.Checks {
		if c.ID == id && c.Last != nil && (len(h.entries) == 0 || c.Last.SampledAt.After(h.entries[0].SampledAt)) {
			return id
		}
	}
	return ""
}

// KeepSamples keeps what reading a check's samples returned. A failed read
// keeps the samples held for the same check: they are still what was
// measured.
func (m *Model) KeepSamples(check string, entries []model.Observation, e error) {
	if e != nil {
		if m.samples.check != check {
			m.samples = samples{check: check}
		}
		m.samples.err = e.Error()
		m.diagnosticEvent("history_error", "history_read_failed", check+": "+m.samples.err)
		return
	}
	if m.samples.check == check && m.samples.err != "" {
		m.diagnosticEvent("recovered", "history_read_recovered", check)
	}
	m.samples = samples{check: check, entries: entries}
}

// sampleLines are what the evidence of a check shows of its recent samples
// in w columns: a strip of their outcomes over one of what the passing
// ones took, newest at the right, and their latency.
func (m *Model) sampleLines(r Row, w int) []vline {
	if r.Kind != "check" || m.Edit != nil {
		return nil
	}
	head := func(aside string) vline {
		l := vline{{"RECENT SAMPLES", fg(Mix(cAccent, cMuted, .35)).bold()}}
		if aside != "" {
			l = append(l, span{"  " + aside, fg(cFaint)})
		}
		return l
	}
	h := m.samples
	if h.check != r.ID {
		return []vline{head(""), {{"reading…", fg(cMuted).italic()}}, {}}
	}
	if len(h.entries) == 0 {
		say := "none in the collector's history"
		if h.err != "" {
			say = "unavailable: " + h.err
		}
		out := []vline{head("")}
		for _, l := range wrap(say, w) {
			out = append(out, vline{{l, fg(cMuted).italic()}})
		}
		return append(out, vline{})
	}
	x := model.Check{ID: r.ID}
	var day *model.Rollup
	for _, c := range m.Snapshot.Topology.Checks {
		if c.ID == r.ID {
			x = c
		}
	}
	for i := range m.Snapshot.Checks {
		if c := &m.Snapshot.Checks[i]; c.ID == r.ID {
			day = view.RollupWindow(c.Availability, "24h")
		}
	}
	n := w
	if x.Kind == "host" {
		// Room on the right for the charts' scale.
		n = max(8, w-13)
	}
	s := view.NewStrip(x, h.entries, m.Now, n)
	out := []vline{head(s.Span(m.Now) + " · " + s.Every())}
	if h.err != "" {
		for _, l := range wrap("Not refreshed: "+h.err, w) {
			out = append(out, vline{{l, fg(cWarn)}})
		}
	}
	strip := vline{}
	for _, sl := range s.Slots {
		c := cFaint
		if health := view.SlotHealth(sl.Outcome); health != "" {
			c = healthColor(health)
		}
		strip = append(strip, span{view.SlotGlyph(sl.Outcome), fg(c)})
	}
	out = append(out, strip)
	if x.Kind == "host" {
		// A host check's elapsed time is how long it watched the CPU; what
		// it measured is what to see.
		return append(append(out, vline{{view.StripLegend, fg(cMuted).italic()}}, vline{}), m.checkCharts(x, s, h.entries)...)
	}
	if s.Latency.Samples > 0 {
		out = append(out, vline{{view.Bars(s.Slots), fg(Mix(cAccent, cMuted, .5))}})
	}
	out = append(out, vline{{s.Latency.String(), fg(cText)}})
	// The last day's first byte, split by what the edge said about its
	// cache, and the colos it answered from, at most LinePops shares. Both
	// say their window, so the pair reads together, and both are wrapped to
	// the panel so a narrow terminal keeps them readable.
	if day != nil {
		if line := view.FirstByteLine(day.FirstByte); line != "" {
			for _, l := range wrapWindowed("first byte "+line, "24h", w) {
				out = append(out, vline{{l, fg(cText)}})
			}
		}
		if line := view.PopsLine(day.Pops, view.LinePops); line != "" {
			for _, l := range wrapWindowed("colos "+line, "24h", w) {
				out = append(out, vline{{l, fg(Mix(cAccent, cMuted, .35))}})
			}
		}
	}
	return append(out, vline{{view.StripLegend, fg(cMuted).italic()}}, vline{})
}

// fetchedSamples is what a read of a check's samples returned.
type fetchedSamples struct {
	check   string
	entries []model.Observation
	err     error
	trend   bool // read for a host check's charts, not the selected check's evidence
	view    client.HistoryView
}

// sampler reads the samples the evidence needs, one read at a time, and
// then the history of each host check its charts have not read.
type sampler struct {
	read     History
	readAt   snapshotHistory
	busy     bool
	results  chan fetchedSamples
	server   string
	revision string
}

func (s *sampler) request(ctx context.Context, m *Model, refresh bool) {
	if s.readAt != nil && (s.server != m.Server || s.revision != m.Snapshot.Revision) {
		// Cached history has the same attribution boundary as in-flight reads.
		m.samples, m.trends = samples{}, nil
		m.absorbed = time.Time{}
		s.server, s.revision = m.Server, m.Snapshot.Revision
	}
	if s.read == nil && s.readAt == nil || s.busy || s.readAt != nil && m.Snapshot.GeneratedAt.IsZero() {
		return
	}
	id, trend := m.wantSamples(refresh), false
	if id == "" {
		id, trend = m.wantTrend(), true
	}
	if id == "" {
		return
	}
	s.busy = true
	v := client.SnapshotHistory(m.Snapshot, m.Server, id)
	go func() {
		var entries []model.Observation
		var e error
		if s.readAt != nil {
			entries, e = s.readAt(ctx, v, view.StripSamples)
		} else {
			entries, e = s.read(ctx, id, view.StripSamples)
		}
		select {
		case s.results <- fetchedSamples{id, entries, e, trend, v}:
		case <-ctx.Done():
		}
	}()
}

func (s *sampler) keep(m *Model, got fetchedSamples) {
	s.busy = false
	if s.readAt != nil && got.view != client.SnapshotHistory(m.Snapshot, m.Server, got.check) {
		return
	}
	if got.trend {
		if got.err != nil {
			m.diagnosticEvent("history_error", "trend_read_failed", got.check+": "+got.err.Error())
		}
		m.KeepTrend(got.check, got.entries, got.err)
	} else {
		m.KeepSamples(got.check, got.entries, got.err)
	}
}
