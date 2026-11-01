package direct

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
)

// Reconfigure validates the entire candidate locally before installing it.
// Only affected sources receive a new execution generation and cancellation.
func (s *Session) Reconfigure(p Profile) error {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil || p.Profile != s.profile.Profile {
		s.mu.Unlock()
		return fail("profile_conflict")
	}
	unchanged := sameProfile(s.profile, p)
	s.mu.Unlock()
	if unchanged {
		return nil
	}

	c, e := p.Config()
	if e != nil {
		return e
	}
	rt := probe.NewSourceRuntime(probe.ProductionSourceAdapters())
	if rt.Validate(c) != nil {
		return fail("profile_unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil || p.Profile != s.profile.Profile {
		return fail("profile_conflict")
	}
	next := model.NewExternalState()
	gates := map[string]chan struct{}{}
	for id, l := range s.loops {
		gates[id] = l.gate
	}
	loops := map[string]*sourceLoop{}
	for _, entry := range p.Sources {
		old, ok := findEntry(s.profile, entry.Source.ID)
		l := s.loops[entry.Source.ID]
		if ok && l != nil && sameAcquisition(old, entry) {
			loops[entry.Source.ID] = l
			next.Sources[entry.Source.ID] = s.external.Sources[entry.Source.ID]
			for _, x := range c.Checks {
				if x.External != nil && x.External.Source == entry.Source.ID {
					if state, ok := s.external.Bindings[x.ID]; ok && state.Contract == config.Contract(c, x) {
						next.Bindings[x.ID] = model.CloneBindingState(state)
					}
				}
			}
			if old.Credential != entry.Credential {
				l.renewed401 = false
			}
			if old.Credential != entry.Credential && !(l.code == "rate_limited" && l.next.After(s.opts.Now())) {
				select {
				case l.wake <- struct{}{}:
				default:
				}
			}
		}
	}
	for id, l := range s.loops {
		if loops[id] != l {
			l.cancel()
		}
	}
	s.profile = cloneProfile(p)
	s.config = c
	s.external = next
	s.plan = engine.NewPlan(c)
	s.prepared = s.plan.PrepareExternal(nil, nil, nil, next)
	s.loops = loops
	for _, src := range c.Sources {
		if s.loops[src.ID] == nil {
			s.startLoopLocked(src, newID(), c, gates[src.ID])
		}
	}
	return nil
}
