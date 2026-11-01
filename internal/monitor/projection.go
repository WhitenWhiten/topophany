package monitor

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// A projection owns the indexes shared by every structured row in one frame.
// Keep collector judgments and their original clocks together: locally aging
// only checks would leave entity and incident judgments backed by expired data.
type projection struct {
	v, all                         view.View
	checkStates                    map[string]model.CheckState
	checkDefs                      map[string]model.Check
	linkStates                     map[string]model.EntityState
	linkDefs                       map[string]model.Link
	fleet                          *view.Fleet
	nodeChecks                     map[string][]string
	serviceChecks                  map[string][]string
	dependencies, dependencyChecks map[string][]string
	observers                      []ObserverVerdict
	serverList                     []ServerListEntry
	serverListClock                evidenceClock
	resources                      map[string]view.NodeResources
	currentClocks, completeClocks  *evidenceIndex
	aged                           *model.Snapshot
	samples                        map[string][]model.Observation
}

func (p *projection) scalarSnapshot() model.Snapshot {
	if p.aged == nil {
		s := ageSnapshot(p.v.Snapshot, p.v.Now, p.evidence())
		p.aged = &s
	}
	return *p.aged
}

type evidenceClock struct{ sampled, until time.Time }

func newProjection(in Input, now time.Time) *projection {
	v := view.View{Snapshot: in.Snapshot, Now: now, Cached: view.Expired(in.Snapshot, now)}
	all := v
	if in.AllSnapshot != nil {
		all.Snapshot = *in.AllSnapshot
		all.Cached = view.Expired(all.Snapshot, now)
	}
	return &projection{v: v, all: all, samples: in.Samples}
}

// resourceSelection is computed once per frame and shared by scalar
// resource.* rows and every server card, so they use one snapshot and clock.
func (p *projection) resourceSelection() map[string]view.NodeResources {
	if p.resources == nil {
		p.resources = p.v.SelectResources()
	}
	return p.resources
}

func (p *projection) serverFleet() *view.Fleet {
	if p.fleet == nil {
		p.fleet = p.v.FleetWithResources(p.resourceSelection())
		p.nodeChecks = make(map[string][]string, len(p.v.Snapshot.Nodes))
		for _, n := range p.v.Snapshot.Nodes {
			p.nodeChecks[n.ID] = n.Checks
		}
		p.serviceChecks = make(map[string][]string, len(p.v.Snapshot.Services))
		for _, s := range p.v.Snapshot.Services {
			p.serviceChecks[s.ID] = s.Checks
		}
		p.indexDependencies()
	}
	return p.fleet
}

func (p *projection) observerValues() []ObserverVerdict {
	if p.observers == nil {
		p.observers = observerVerdicts(p.all)
	}
	return p.observers
}

func (p *projection) resolve(value Value) Value {
	s := p.v.Snapshot
	if s.GeneratedAt.IsZero() || s.GeneratedAt.After(p.v.Now.Add(model.SampleClockAhead)) {
		return missing(value, "source_expired")
	}
	if (value.Metric == "observers.verdicts" || value.Metric == "server.card") && p.all.Snapshot.GeneratedAt.After(p.v.Now.Add(model.SampleClockAhead)) {
		return missing(value, "source_expired")
	}
	value = p.richValue(value)
	if value.Value != nil && !p.v.Now.Before(value.ValidUntil) {
		value.Freshness = "stale"
		return missing(value, "sample_expired")
	}
	return value
}

func (p *projection) evidence() *evidenceIndex {
	if p.currentClocks == nil {
		p.currentClocks = indexEvidence(p.v)
	}
	return p.currentClocks
}
func (p *projection) completeEvidence() *evidenceIndex {
	if p.completeClocks == nil {
		p.completeClocks = indexEvidence(p.all)
	}
	return p.completeClocks
}

// Index the source's original clocks once, before any local aging. An expired
// fresh observation still bounds every conclusion that depended on it.
type evidenceIndex struct {
	base                      time.Time
	checks                    map[string]evidenceClock
	byObserver                map[string]evidenceClock
	checkClock, observerClock evidenceClock
}

func combineClock(a, b evidenceClock) evidenceClock {
	if b.sampled.After(a.sampled) {
		a.sampled = b.sampled
	}
	a.until = minTime(a.until, b.until)
	return a
}
func indexEvidence(v view.View) *evidenceIndex {
	s := v.Snapshot
	base := s.GeneratedAt.Add(model.SnapshotLifetime).UTC()
	e := &evidenceIndex{base: base, checks: make(map[string]evidenceClock, len(s.Checks)), byObserver: make(map[string]evidenceClock, len(s.Observers)), checkClock: evidenceClock{until: base}, observerClock: evidenceClock{until: base}}
	stale := make(map[string]int, len(s.Topology.Checks))
	for _, c := range s.Topology.Checks {
		stale[c.ID] = c.StaleSeconds
	}
	external := make(map[string]model.ExternalBindingView, len(s.External))
	for _, binding := range s.External {
		external[binding.Check] = binding
	}
	for _, c := range s.Checks {
		clock := evidenceClock{until: base}
		if binding, ok := external[c.ID]; ok {
			// External evidence has no native Last observation. Its report
			// age and source-contact clocks independently bound the judgment.
			clock.sampled = binding.SampledAt.UTC()
			if c.Freshness == "fresh" {
				if binding.Report == nil || binding.SampledAt.IsZero() || binding.DataDeadline.IsZero() || binding.ContactDeadline.IsZero() || binding.ValidUntil.IsZero() {
					clock.until = v.Now
				} else {
					clock.until = minTime(minTime(base, binding.ValidUntil), minTime(binding.DataDeadline, binding.ContactDeadline))
				}
			}
		} else if c.Last != nil {
			clock.sampled = c.Last.SampledAt.UTC()
			if c.Freshness == "fresh" {
				clock.until = minTime(base, clock.sampled.Add(time.Duration(stale[c.ID])*time.Second))
			}
		}
		// A fresh judgment needs a real evidence clock, even when the whole
		// external binding or native Last observation is absent.
		if c.Freshness == "fresh" && (clock.sampled.IsZero() || clock.sampled.After(v.Now.Add(model.SampleClockAhead))) {
			clock.until = v.Now
		}
		e.checks[c.ID] = clock
		e.checkClock = combineClock(e.checkClock, clock)
		own, ok := e.byObserver[c.Observer]
		if !ok {
			own = evidenceClock{until: base}
		}
		e.byObserver[c.Observer] = combineClock(own, clock)
	}
	stale = make(map[string]int, len(s.Topology.Observers))
	for _, o := range s.Topology.Observers {
		stale[o.ID] = o.StaleSeconds
	}
	for _, o := range s.Observers {
		clock := evidenceClock{sampled: o.LastSeen.UTC(), until: base}
		if o.Health == "up" && !o.LastSeen.IsZero() {
			clock.until = minTime(base, o.LastSeen.Add(time.Duration(stale[o.ID])*time.Second))
			if o.LastSeen.After(v.Now.Add(model.SampleClockAhead)) {
				clock.until = v.Now
			}
		}
		e.observerClock = combineClock(e.observerClock, clock)
		own, ok := e.byObserver[o.ID]
		if !ok {
			own = evidenceClock{until: base}
		}
		e.byObserver[o.ID] = combineClock(own, clock)
	}
	return e
}
func (e *evidenceIndex) times(ids map[string]bool, observers bool) (time.Time, time.Time) {
	clock := e.checkClock
	if ids != nil {
		clock = evidenceClock{until: e.base}
		for id := range ids {
			if c, ok := e.checks[id]; ok {
				clock = combineClock(clock, c)
			}
		}
	}
	if observers {
		clock = combineClock(clock, e.observerClock)
	}
	return clock.sampled, clock.until
}

// Dependencies carry both positive and negative judgments. Include transitive
// services and paths even when the current judgment is not affected.
func (p *projection) indexDependencies() {
	p.dependencies = map[string][]string{}
	p.dependencyChecks = map[string][]string{}
	for _, s := range p.v.Snapshot.Topology.Services {
		key := "service/" + s.ID
		p.dependencyChecks[key] = p.serviceChecks[s.ID]
		for _, dep := range s.DependsOn {
			p.dependencies[key] = append(p.dependencies[key], "service/"+dep)
		}
	}
	for _, path := range p.v.Snapshot.Topology.Paths {
		key := "path/" + path.ID
		for _, dep := range path.DependsOn {
			p.dependencies[key] = append(p.dependencies[key], "path/"+dep)
		}
		for _, dep := range path.Candidates {
			p.dependencies[key] = append(p.dependencies[key], "path/"+dep)
		}
	}
	for _, c := range p.v.Snapshot.Topology.Checks {
		if c.Path != "" {
			p.dependencyChecks["path/"+c.Path] = append(p.dependencyChecks["path/"+c.Path], c.ID)
			if c.Service != "" {
				p.dependencies["service/"+c.Service] = append(p.dependencies["service/"+c.Service], "path/"+c.Path)
			}
		}
	}
}
func (p *projection) serviceEvidence(id string, ids map[string]bool, seen map[string]bool) {
	p.dependencyEvidence("service/"+id, ids, seen)
}

func (p *projection) dependencyEvidence(root string, ids map[string]bool, seen map[string]bool) {
	pending := []string{root}
	for len(pending) > 0 {
		key := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[key] {
			continue
		}
		seen[key] = true
		for _, check := range p.dependencyChecks[key] {
			ids[check] = true
		}
		pending = append(pending, p.dependencies[key]...)
	}
}
