package tui

import (
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// A host's resources over time, which its card and evidence draw as
// charts, and a service's checks' recent samples, which its card draws as
// sparks while the Services tab shows cards. Each such check keeps its
// newest samples: what its history held when first read, then each new
// one a snapshot brings. Only samples under the newest one's contract are
// kept, so a chart never joins two disks or two sets of limits.

// trendKeep bounds the samples a check keeps.
const trendKeep = 2 * view.StripSamples

// trend is a check's samples, oldest first, cut down to what the
// charts draw; read is whether its history has been asked for.
type trend struct {
	samples []model.Observation
	read    bool
}

// usage is what a sample measured, in percent; NaN where it has no reading.
type usage struct{ cpu, memory, disk float64 }

var noUsage = usage{math.NaN(), math.NaN(), math.NaN()}

// usageOf reads a sample's percentages. Only a reported sample whose
// counters were read, ok or past a limit, has any.
func usageOf(o model.Observation) usage {
	if o.Evidence != "reported" || o.Outcome != "ok" && o.Outcome != "policy" {
		return noUsage
	}
	return usage{percentFact(o.Facts, "cpu_used_percent"), percentFact(o.Facts, "memory_used_percent"), percentFact(o.Facts, "disk_used_percent")}
}

// percentFact is a percentage fact, NaN when absent or out of range.
func percentFact(f map[string]string, key string) float64 {
	n, e := strconv.ParseFloat(f[key], 64)
	if e != nil || math.IsNaN(n) || n < 0 || n > 100 {
		return math.NaN()
	}
	return n
}

// chartFacts are the facts a trend keeps of each sample.
var chartFacts = []string{"cpu_used_percent", "memory_used_percent", "disk_used_percent"}

func (m *Model) keepTrend(check string, entries []model.Observation) *trend {
	if m.trends == nil {
		m.trends = map[string]*trend{}
	}
	t := m.trends[check]
	if t == nil {
		t = &trend{}
		m.trends[check] = t
	}
	for _, o := range entries {
		i, found := slices.BinarySearchFunc(t.samples, o.SampledAt, func(s model.Observation, at time.Time) int { return s.SampledAt.Compare(at) })
		if found || o.SampledAt.IsZero() {
			continue
		}
		kept := model.Observation{Contract: o.Contract, SampledAt: o.SampledAt, Outcome: o.Outcome, Evidence: o.Evidence, DurationMS: o.DurationMS}
		for _, k := range chartFacts {
			if v, ok := o.Facts[k]; ok {
				if kept.Facts == nil {
					kept.Facts = map[string]string{}
				}
				kept.Facts[k] = v
			}
		}
		t.samples = slices.Insert(t.samples, i, kept)
	}
	if n := len(t.samples); n > 0 {
		if c := t.samples[n-1].Contract; c != "" {
			t.samples = slices.DeleteFunc(t.samples, func(o model.Observation) bool { return o.Contract != "" && o.Contract != c })
		}
	}
	if n := len(t.samples) - trendKeep; n > 0 {
		t.samples = slices.Delete(t.samples, 0, n)
	}
	return t
}

// KeepTrend keeps what reading a check's history returned. A failed
// read is not retried: the chart fills from snapshots instead.
func (m *Model) KeepTrend(check string, entries []model.Observation, e error) {
	m.keepTrend(check, entries).read = true
}

// wantTrend names a check whose history the charts have not read: a host
// check's, then, while the Overview shows the products, one a product line
// may lead with, or while the Services tab shows cards, a shown service's.
func (m *Model) wantTrend() string {
	if m.Edit != nil {
		return ""
	}
	for _, x := range m.Snapshot.Topology.Checks {
		if t := m.trends[x.ID]; x.Kind == "host" && (t == nil || !t.read) {
			return x.ID
		}
	}
	if m.productsOverview() {
		lines := map[string]bool{}
		for _, id := range view.ProductRoots(m.Snapshot.Topology) {
			lines[id] = true
		}
		for _, x := range m.Snapshot.Topology.Checks {
			if t := m.trends[x.ID]; lines[x.Service] && x.Kind != "host" && (t == nil || !t.read) {
				return x.ID
			}
		}
	}
	if !m.serviceCards() {
		return ""
	}
	checks := map[string][]string{}
	for _, s := range m.Snapshot.Services {
		checks[s.ID] = s.Checks
	}
	for _, r := range m.Rows() {
		for _, id := range checks[r.ID] {
			if t := m.trends[id]; t == nil || !t.read {
				return id
			}
		}
	}
	return ""
}

// absorb takes the newest sample of each check that keeps samples from a
// snapshot not yet taken from.
func (m *Model) absorb() {
	if g := m.Snapshot.GeneratedAt; m.Edit != nil || !g.IsZero() && g.Equal(m.absorbed) {
		return
	}
	m.absorbed = m.Snapshot.GeneratedAt
	host := map[string]bool{}
	for _, x := range m.Snapshot.Topology.Checks {
		if x.Kind == "host" {
			host[x.ID] = true
		}
	}
	for _, c := range m.Snapshot.Checks {
		if (host[c.ID] || m.trends[c.ID] != nil) && c.Last != nil {
			m.keepTrend(c.ID, []model.Observation{*c.Last})
		}
	}
}

// trendSamples are what a host check's charts are drawn from.
func (m *Model) trendSamples(check string) []model.Observation {
	if t := m.trends[check]; t != nil {
		return t.samples
	}
	return nil
}

// usageSlots lays samples out on the strip's time axis in at most n slots,
// newest at the right, with what each slot measured.
func usageSlots(x model.Check, samples []model.Observation, now time.Time, n int) ([]view.Slot, []usage) {
	slots := view.NewStrip(x, samples, now, n).Slots
	at := make(map[int64]usage, len(samples))
	for _, o := range samples {
		at[o.SampledAt.UnixNano()] = usageOf(o)
	}
	out := make([]usage, len(slots))
	for i, sl := range slots {
		out[i] = noUsage
		if u, ok := at[sl.SampledAt.UnixNano()]; ok && !sl.SampledAt.IsZero() {
			out[i] = u
		}
	}
	return slots, out
}
