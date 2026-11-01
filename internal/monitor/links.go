package monitor

import (
	"slices"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type LinkSummary struct {
	ID         string     `json:"id"`
	From       RefValue   `json:"from"`
	To         RefValue   `json:"to"`
	Transport  string     `json:"transport"`
	Health     string     `json:"health"`
	Evidence   string     `json:"evidence"`
	Reason     string     `json:"reason"`
	Affected   bool       `json:"affected"`
	SampledAt  *time.Time `json:"sampled_at"`
	ValidUntil *time.Time `json:"valid_until"`
}

type LinkCard struct {
	LinkSummary
	Checks []CheckCard `json:"checks"`
}

func (p *projection) indexLinks() {
	if p.linkStates != nil {
		return
	}
	p.linkStates = make(map[string]model.EntityState, len(p.v.Snapshot.Links))
	p.linkDefs = make(map[string]model.Link, len(p.v.Snapshot.Topology.Links))
	for _, s := range p.v.Snapshot.Links {
		p.linkStates[s.ID] = s
	}
	for _, d := range p.v.Snapshot.Topology.Links {
		p.linkDefs[d.ID] = d
	}
}

func (p *projection) linkSummary(id string) (LinkSummary, bool) {
	p.indexLinks()
	d, exists := p.linkDefs[id]
	s, known := p.linkStates[id]
	if !exists || !known {
		return LinkSummary{}, false
	}
	fleet := p.serverFleet()
	states := []model.CheckState{}
	wanted := map[string]bool{}
	for _, id := range s.Checks {
		wanted[id] = true
	}
	for _, c := range p.v.Snapshot.Checks {
		if wanted[c.ID] {
			states = append(states, c)
		}
	}
	ids := map[string]bool{}
	for _, id := range engine.LinkJudgmentChecks(states) {
		ids[id] = true
	}
	// A link's affected flag also depends on end-to-end paths that contain
	// it, including their candidate/dependency paths. Preserve those clocks.
	seen := map[string]bool{}
	for _, path := range p.v.Snapshot.Topology.Paths {
		if slices.Contains(path.Links, id) && (p.v.Snapshot.ViewObserver == "" || path.Observer == p.v.Snapshot.ViewObserver) {
			p.dependencyEvidence("path/"+path.ID, ids, seen)
		}
	}
	sampled, until := p.evidence().times(ids, false)
	return LinkSummary{ID: id, From: RefValue{ID: d.From, Name: named(d.From, fleet.Names[d.From])}, To: RefValue{ID: d.To, Name: named(d.To, fleet.Names[d.To])}, Transport: d.Transport,
		Health: s.Health, Evidence: s.Evidence, Reason: s.Reason, Affected: s.Affected, SampledAt: stamp(sampled), ValidUntil: stamp(until)}, true
}

func (p *projection) link(id string) (LinkCard, bool) {
	s, ok := p.linkSummary(id)
	if !ok {
		return LinkCard{}, false
	}
	out := LinkCard{LinkSummary: s, Checks: []CheckCard{}}
	for _, id := range p.linkStates[id].Checks {
		if c, ok := p.check(id); ok {
			out.Checks = append(out.Checks, c)
		}
	}
	return out, true
}

func cloneLinkSummary(s LinkSummary) LinkSummary {
	s.SampledAt, s.ValidUntil = copyPointer(s.SampledAt), copyPointer(s.ValidUntil)
	return s
}

func cloneLinkCard(c LinkCard) LinkCard {
	c.LinkSummary = cloneLinkSummary(c.LinkSummary)
	c.Checks = slices.Clone(c.Checks)
	for i := range c.Checks {
		c.Checks[i].SeenCheckValue = cloneSeenCheck(c.Checks[i].SeenCheckValue)
	}
	return c
}
