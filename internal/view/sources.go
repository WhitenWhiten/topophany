package view

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

type SourceCounts = model.SourceCounts
type SourcesCounts = model.SourcesCounts
type SourceStatus = model.SourceStatus
type SourcesStatus = model.SourcesStatus

// SourcesStatus is the shared acquisition judgment used by every direct
// current-state reader. It never infers machine reachability from Hub access.
func (v View) SourcesStatus() (SourcesStatus, time.Time) {
	out := SourcesStatus{State: "no_data", Sources: []SourceStatus{}}
	until := v.Now.Add(30 * time.Second)
	bound := func(t time.Time) {
		if t.After(v.Now) && t.Before(until) {
			until = t
		}
	}
	if !v.Snapshot.GeneratedAt.IsZero() {
		bound(v.Snapshot.GeneratedAt.Add(model.SnapshotLifetime))
	}
	if v.Snapshot.Direct == nil {
		return out, until
	}
	bySource := map[string]int{}
	allConnected := len(v.Snapshot.Direct.Sources) > 0
	allLoading := len(v.Snapshot.Direct.Sources) > 0
	for _, src := range v.Snapshot.Direct.Sources {
		src = cloneDirectSource(src)
		if src.State == "connected" && src.ContactValidUntil != nil && !v.Now.Before(*src.ContactValidUntil) {
			src.State = "retrying"
			code := "contact_expired"
			src.Code = &code
		}
		allConnected = allConnected && src.State == "connected"
		allLoading = allLoading && src.LastAttemptAt == nil
		bySource[src.ID] = len(out.Sources)
		out.Sources = append(out.Sources, SourceStatus{DirectSource: src})
		if src.ContactValidUntil != nil {
			bound(*src.ContactValidUntil)
		}
	}
	seen := map[string]bool{}
	for _, x := range v.Snapshot.Topology.Checks {
		if x.External == nil || x.External.Report != model.ReportTypeResource {
			continue
		}
		b := x.External
		index, ok := bySource[b.Source]
		if !ok {
			continue
		}
		key := b.Source + "\x00" + b.Object
		if seen[key] {
			continue
		}
		seen[key] = true
		counts := &out.Sources[index].Counts
		counts.Selected++
		// Several compatible bindings may name one object; any current binding
		// makes that object current. Retain a historical sample otherwise.
		current, had := false, false
		for _, candidate := range v.Snapshot.External {
			if candidate.Source != b.Source || candidate.Object != b.Object || candidate.ReportType != model.ReportTypeResource {
				continue
			}
			had = had || candidate.Report != nil && candidate.Report.Resource != nil && !candidate.SampledAt.IsZero()
			current = current || externalUsable(v, candidate)
			bound(candidate.DataDeadline)
			bound(candidate.ContactDeadline)
		}
		if current {
			counts.Current++
		} else if had {
			counts.Stale++
		} else {
			counts.Missing++
		}
	}
	out.Counts.Sources = len(out.Sources)
	for _, s := range out.Sources {
		out.Counts.Selected += s.Counts.Selected
		out.Counts.Current += s.Counts.Current
		out.Counts.Stale += s.Counts.Stale
		out.Counts.Missing += s.Counts.Missing
	}
	out.Counts.Attention = out.Counts.Stale + out.Counts.Missing
	switch {
	case out.Counts.Selected == 0:
		out.State = "no_data"
	case allLoading:
		out.State = "loading"
	case allConnected && out.Counts.Current == out.Counts.Selected:
		out.State = "ready"
	case out.Counts.Current > 0:
		out.State = "partial"
	case allConnected && out.Counts.Missing == out.Counts.Selected:
		out.State = "no_data"
	default:
		out.State = "unavailable"
	}
	// The source can be reachable even when none of its samples is current.
	// Keep the public state vocabulary stable; consumers may explain the reason.
	if allConnected && out.Counts.Current == 0 && out.Counts.Stale > 0 {
		out.Reason = "samples_stale"
	}
	return out, until
}
func cloneDirectSource(s model.DirectSource) model.DirectSource {
	cp := func(p *time.Time) *time.Time {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	s.LastAttemptAt = cp(s.LastAttemptAt)
	s.LastContactAt = cp(s.LastContactAt)
	s.ContactValidUntil = cp(s.ContactValidUntil)
	s.NextRetryAt = cp(s.NextRetryAt)
	if s.Code != nil {
		c := *s.Code
		s.Code = &c
	}
	return s
}
func CloneSourcesStatus(s SourcesStatus) SourcesStatus {
	out := s
	out.Sources = append([]SourceStatus{}, s.Sources...)
	for i := range out.Sources {
		out.Sources[i].DirectSource = cloneDirectSource(out.Sources[i].DirectSource)
	}
	return out
}
func (v View) ResourceOrigin(source, object string) *model.Origin {
	for _, s := range v.Snapshot.Topology.Sources {
		if s.ID == source {
			return &model.Origin{Adapter: s.Adapter, SourceID: s.ID, Name: s.Name, ObjectID: object}
		}
	}
	return nil
}
