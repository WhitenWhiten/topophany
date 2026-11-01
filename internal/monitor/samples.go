package monitor

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// A service card shows each of its checks' recent samples, which no
// snapshot carries. Its source reads each check's history once, when a card
// first shows the check, and its SampleBook extends that from each
// snapshot's newest sample after, as the TUI's cards do.

// CardSamples is how many slots of samples a service card's check shows,
// and how many of its samples a source reads from its history.
const CardSamples = 40

// HistoryReads bounds the histories one fetch from a collector reads, so
// many service cards fill over a few fetches rather than hold one past its
// deadline.
const HistoryReads = 4

// Projections own these card slices. Suppress samples for a compact row even
// when another row requested history for the same check in the shared book.
func withoutHistory(value any) any {
	switch c := value.(type) {
	case CheckCard:
		c.Samples.Slots = nil
		return c
	case ServiceCard:
		for i := range c.SeenFrom {
			for j := range c.SeenFrom[i].Checks {
				c.SeenFrom[i].Checks[j].Samples.Slots = nil
			}
		}
		return c
	case LinkCard:
		for i := range c.Checks {
			c.Checks[i].Samples.Slots = nil
		}
		return c
	}
	return value
}

// SampleBook keeps the recent samples of the checks of the services a
// template's cards show, for Input.Samples. A source owns one; it is safe
// for one fetch at a time, and its methods do nothing on a nil book.
type SampleBook struct {
	mu       sync.Mutex
	services []string
	selected []string
	links    []string
	source   string
	checks   map[string]*sampleTrail
}

// sampleTrail is a check's samples, oldest first, cut down to what a card
// draws; read is whether its history has been asked for.
type sampleTrail struct {
	samples []model.Observation
	read    bool
}

// NewSampleBook is the book of t's service cards, nil when t has none.
func NewSampleBook(t Template) *SampleBook {
	b := &SampleBook{checks: map[string]*sampleTrail{}}
	for _, r := range t.Rows {
		if r.History != nil && !*r.History {
			continue
		}
		if r.Metric == "service.card" && !slices.Contains(b.services, r.Select.Service) {
			b.services = append(b.services, r.Select.Service)
		}
		if r.Metric == "link.card" && !slices.Contains(b.links, r.Select.Link) {
			b.links = append(b.links, r.Select.Link)
		}
		if r.Metric == "check.card" && !slices.Contains(b.selected, r.Select.Check) {
			b.selected = append(b.selected, r.Select.Check)
		}
	}
	if len(b.services) == 0 && len(b.selected) == 0 && len(b.links) == 0 {
		return nil
	}
	return b
}

// Unread names the checks of the cards' services in s whose history the
// book has not read, in the template's order. source names where s came
// from, its collector and revision; samples from another are forgotten.
func (b *SampleBook) Unread(s model.Snapshot, source string) []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.from(source)
	out := []string{}
	for _, id := range b.checksOf(s) {
		if t := b.checks[id]; t == nil || !t.read {
			out = append(out, id)
		}
	}
	return out
}

// Read keeps what reading a check's history returned. A failed read is
// kept as an empty one and not retried: the samples fill from snapshots.
func (b *SampleBook) Read(check string, entries []model.Observation) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keep(check, entries).read = true
}

// Samples takes the newest sample of each of the cards' checks from s and
// returns each check's samples, oldest first. Checks no card shows any
// longer are forgotten.
func (b *SampleBook) Samples(s model.Snapshot, source string) map[string][]model.Observation {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.from(source)
	wanted := map[string]bool{}
	for _, id := range b.checksOf(s) {
		wanted[id] = true
	}
	for _, c := range s.Checks {
		if wanted[c.ID] && c.Last != nil {
			b.keep(c.ID, []model.Observation{*c.Last})
		}
	}
	out := map[string][]model.Observation{}
	for id, t := range b.checks {
		if !wanted[id] {
			delete(b.checks, id)
		} else if len(t.samples) > 0 {
			out[id] = slices.Clone(t.samples)
		}
	}
	return out
}

// Fill reads the history of at most budget of the checks the book has not
// read (every one, when budget is negative), then returns Samples. A
// failed read is not retried; one the end of ctx cut short is.
func (b *SampleBook) Fill(ctx context.Context, s model.Snapshot, source string, budget int, read func(ctx context.Context, check string) ([]model.Observation, error)) map[string][]model.Observation {
	for i, id := range b.Unread(s, source) {
		if i == budget || ctx.Err() != nil {
			break
		}
		entries, err := read(ctx, id)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			entries = nil
		}
		b.Read(id, entries)
	}
	return b.Samples(s, source)
}

func (b *SampleBook) from(source string) {
	if source != b.source {
		b.source = source
		clear(b.checks)
	}
}

// checksOf are the checks of the cards' services in s.
func (b *SampleBook) checksOf(s model.Snapshot) []string {
	out := []string{}
	for _, id := range b.selected {
		for _, c := range s.Checks {
			if c.ID == id {
				out = append(out, id)
				break
			}
		}
	}
	for _, id := range b.links {
		for _, link := range s.Links {
			if link.ID == id {
				for _, check := range link.Checks {
					if !slices.Contains(out, check) {
						out = append(out, check)
					}
				}
				break
			}
		}
	}

	for _, id := range b.services {
		for _, sv := range s.Services {
			if sv.ID == id {
				for _, c := range sv.Checks {
					if !slices.Contains(out, c) {
						out = append(out, c)
					}
				}
				break
			}
		}
	}
	return out
}

// keep adds entries to a check's samples once each, keeping only those
// under the newest one's contract and at most twice what a card shows.
func (b *SampleBook) keep(check string, entries []model.Observation) *sampleTrail {
	t := b.checks[check]
	if t == nil {
		t = &sampleTrail{}
		b.checks[check] = t
	}
	for _, o := range entries {
		i, found := slices.BinarySearchFunc(t.samples, o.SampledAt, func(s model.Observation, at time.Time) int { return s.SampledAt.Compare(at) })
		if found || o.SampledAt.IsZero() {
			continue
		}
		t.samples = slices.Insert(t.samples, i, model.Observation{Contract: o.Contract, SampledAt: o.SampledAt, Outcome: o.Outcome, Evidence: o.Evidence, DurationMS: o.DurationMS})
	}
	if n := len(t.samples); n > 0 {
		if c := t.samples[n-1].Contract; c != "" {
			t.samples = slices.DeleteFunc(t.samples, func(o model.Observation) bool { return o.Contract != "" && o.Contract != c })
		}
	}
	if n := len(t.samples) - 2*CardSamples; n > 0 {
		t.samples = slices.Delete(t.samples, 0, n)
	}
	return t
}
