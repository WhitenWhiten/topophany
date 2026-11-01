package monitor

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

type Input struct {
	CollectorRead *CollectorRead
	// AllSnapshot supplies observer verdicts when Snapshot is an observer-scoped view.
	AllSnapshot *model.Snapshot
	IO          ioaccount.Snapshot
	Snapshot    model.Snapshot
	Delivery    []model.DeliveryStatus
	// Samples are recent samples of the checks of the services a template's
	// cards show, by check, oldest first (SampleBook).
	Samples     map[string][]model.Observation
	Unavailable bool
}
type Panel struct {
	CollectorRead      *CollectorRead `json:"collector_read,omitempty"`
	Profile            string         `json:"profile,omitempty"`
	ProfileRevision    string         `json:"profile_revision,omitempty"`
	DatasetID          string         `json:"dataset_id,omitempty"`
	RequestID          string         `json:"request_id"`
	Error              *FrameError    `json:"error,omitempty"`
	Schema             int            `json:"schema"`
	Name               string         `json:"name"`
	ConfigRevision     string         `json:"config_revision"`
	Instance           string         `json:"instance"`
	Sequence           uint64         `json:"sequence"`
	Source             string         `json:"source"`
	Project            string         `json:"project"`
	Observer           string         `json:"observer,omitempty"`
	Lifecycle          string         `json:"lifecycle"`
	GeneratedAt        time.Time      `json:"generated_at"`
	PublisherExpiresAt time.Time      `json:"publisher_expires_at"`
	Title              string         `json:"title"`
	Icon               string         `json:"icon,omitempty"`
	BarRow             string         `json:"bar_row"`
	Rows               []Value        `json:"rows"`
}
type Value struct {
	Basis          string `json:"basis,omitempty"`
	SampleWindowMS int64  `json:"sample_window_ms,omitempty"`
	Row
	Value      any       `json:"value"`
	Unit       string    `json:"unit,omitempty"`
	Text       string    `json:"text"`
	Health     string    `json:"health"`
	Freshness  string    `json:"freshness"`
	Reason     string    `json:"reason,omitempty"`
	SampledAt  time.Time `json:"sampled_at,omitzero"`
	ValidUntil time.Time `json:"valid_until,omitzero"`
	Check      string    `json:"check,omitempty"`
	Observer   string    `json:"observer,omitempty"`
	Collector  string    `json:"collector,omitempty"`
}

func Build(c Config, in Input, now time.Time) Panel {
	p := Panel{Schema: 1, Name: c.Name, ConfigRevision: c.Revision, Source: c.Source, Project: in.Snapshot.Project, Observer: in.Snapshot.ViewObserver, Lifecycle: "running", GeneratedAt: now.UTC(), Title: c.Template.Title, Icon: c.Template.Icon, BarRow: c.Template.Bar.Row, Rows: []Value{}}
	p.CollectorRead = copyPointer(in.CollectorRead)
	if c.Source == "direct" && in.Snapshot.Direct != nil {
		p.Profile = in.Snapshot.Direct.Profile
		p.ProfileRevision = in.Snapshot.Direct.ProfileRevision
		p.DatasetID = in.Snapshot.Direct.DatasetID
	}
	if p.Observer == "" {
		p.Observer = c.Observer
	}
	if p.Project == "" {
		p.Project = c.Project
	}
	projection := newProjection(in, now)
	type richKey struct {
		metric   string
		selectBy Selector
		limit    int
	}
	cache := map[richKey]Value{}
	remaining := panelValueBudget
	for _, r := range c.Template.Rows {
		m, _ := metric(r.Metric)
		var value Value
		if m.Visual == "status" || m.Visual == "list" || m.Visual == "card" {
			key := richKey{metric: r.Metric, selectBy: r.Select}
			if r.Limit != nil {
				key.limit = *r.Limit
			}
			var ok bool
			value, ok = cache[key]
			if !ok {
				value = resolve(r, in, now, projection)
				rowBudget := maxRowValueBytes
				value = boundedValue(value, &rowBudget)
			}
			value.Row = r
			value = boundedValue(value, &remaining)
			// Cache only budgeted data, so repeats cannot retain or rebuild a
			// large projection. Exhausted rich rows leave scalars available.
			cache[key] = value
		} else {
			value = resolve(r, in, now, projection)
		}
		p.Rows = append(p.Rows, value)
	}
	return clone(p)
}
func missing(v Value, reason string) Value {
	v.Value = nil
	v.Text = "unknown"
	v.Health = "unknown"
	v.Reason = reason
	if v.Freshness == "fresh" || v.Freshness == "" {
		v.Freshness = "unavailable"
	}
	return v
}
func resolve(r Row, in Input, now time.Time, projection *projection) Value {
	m, _ := metric(r.Metric)
	v := Value{Row: r, Unit: m.Unit, Text: "unknown", Health: "unknown", Freshness: "fresh"}
	if in.Unavailable {
		return missing(v, "source_unavailable")
	}
	if m.Visual == "status" || m.Visual == "list" || m.Visual == "card" {
		return projection.resolve(v)
	}
	s := projection.scalarSnapshot()
	if strings.HasPrefix(r.Metric, "delivery.") {
		return deliveryValue(v, in.Delivery, now)
	}
	if strings.HasPrefix(r.Metric, "resource.") {
		return projection.resourceMetric(v, r)
	}
	if s.GeneratedAt.IsZero() || s.GeneratedAt.After(now.Add(model.SampleClockAhead)) || view.Expired(s, now) {
		v.Freshness = "stale"
		return missing(v, "source_expired")
	}
	if strings.HasPrefix(r.Metric, "storage.") {
		switch r.Metric {
		case "storage.journal.write_bytes":
			v.Value = in.IO.Journal.Bytes
		case "storage.checkpoint.write_bytes":
			v.Value = in.IO.Checkpoint.Bytes
		case "storage.queue.operations":
			v.Value = in.IO.Queue.Count
		case "storage.queue.write_bytes":
			v.Value = in.IO.Queue.Bytes
		case "storage.sync.count":
			v.Value = in.IO.FileSync.Count + in.IO.DirectorySync.Count
		case "storage.sync.duration_ms":
			v.Value = float64(in.IO.FileSync.Nanoseconds+in.IO.DirectorySync.Nanoseconds) / 1e6
		}
		v.Text = fmt.Sprint(v.Value)
		v.Health = "up"
		v.SampledAt = s.GeneratedAt
		v.ValidUntil = s.GeneratedAt.Add(model.SnapshotLifetime)
		return v
	}
	if m.Selector == "" {
		verdict := (view.View{Snapshot: s, Now: now}).Verdict()
		v.Value = verdict.State
		v.Text = verdict.Text
		v.Health = verdictHealth(verdict.State)
		v.SampledAt, v.ValidUntil = projection.evidence().times(nil, false)
		if !now.Before(v.ValidUntil) {
			v.Freshness = "stale"
			return missing(v, "sample_expired")
		}
		return v
	}
	var matches []model.CheckState
	for _, cs := range s.Checks {
		if r.Select.Check != "" && cs.ID != r.Select.Check {
			continue
		}
		if m.Fact != "" {
			host := false
			for _, check := range s.Topology.Checks {
				if check.ID == cs.ID {
					host = check.Kind == "host"
					break
				}
			}
			if !host {
				continue
			}
		}
		matches = append(matches, cs)
	}
	if len(matches) == 0 {
		return missing(v, "entity_missing")
	}
	if len(matches) > 1 {
		return missing(v, "selector_ambiguous")
	}
	cs := matches[0]
	v.Check = cs.ID
	v.Observer = cs.Observer
	v.Health = cs.Health
	v.Freshness = cs.Freshness
	if cs.Last == nil {
		clock := projection.evidence().checks[cs.ID]
		if r.Metric != "check.status" || clock.sampled.IsZero() {
			return missing(v, "never_sampled")
		}
		v.SampledAt, v.ValidUntil = clock.sampled, clock.until
	} else {
		v.SampledAt = cs.Last.SampledAt
		for _, check := range s.Topology.Checks {
			if check.ID == cs.ID {
				v.ValidUntil = v.SampledAt.Add(time.Duration(check.StaleSeconds) * time.Second)
				break
			}
		}
	}
	if limit := s.GeneratedAt.Add(model.SnapshotLifetime); v.ValidUntil.After(limit) {
		v.ValidUntil = limit
	}
	if cs.Freshness != "fresh" {
		return missing(v, cs.Freshness)
	}
	if v.SampledAt.After(now.Add(model.SampleClockAhead)) {
		return missing(v, "clock_skew")
	}
	if v.ValidUntil.IsZero() || !now.Before(v.ValidUntil) {
		v.Freshness = "stale"
		return missing(v, "sample_expired")
	}
	if r.Metric == "check.status" {
		v.Value = cs.Health
		v.Text = cs.Health
		return v
	}
	resource, ok := (view.View{Snapshot: s, Now: now}).HostResource(cs.ID)
	if !ok || resource.Facts == nil {
		return missing(v, "sample_unreadable")
	}
	n, e := strconv.ParseFloat(resource.Facts[m.Fact], 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
		return missing(v, "invalid_value")
	}
	v.Value = n
	switch r.Metric {
	case "host.cpu.used_percent":
		v.Basis = "host_normalized_0_100"
		v.SampleWindowMS, _ = strconv.ParseInt(resource.Facts["cpu_sample_ms"], 10, 64)
	case "host.memory.used_percent":
		v.Basis = resource.Facts["memory_basis"]
	case "host.disk.used_percent":
		v.Basis = resource.Facts["disk_basis"]
	}
	v.Text = fmt.Sprintf("%.1f%%", n)
	return v
}

// resourceMetric reads the one node-selected resource, never a different
// source and never host.* semantics from a non-host check. The frame's one
// selection is shared with server.card.
func (p *projection) resourceMetric(v Value, r Row) Value {
	if r.Select.Node == "" {
		return missing(v, "selector_required")
	}
	n, ok := p.resourceSelection()[r.Select.Node]
	if !ok || n.Selected.Check == "" {
		return missing(v, "entity_missing")
	}
	card := n.Selected
	if !card.Current || card.Facts == nil {
		return missing(v, "sample_unreadable")
	}
	m, _ := metric(r.Metric)
	value, err := strconv.ParseFloat(card.Facts[m.Fact], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return missing(v, "invalid_value")
	}
	v.Value = value
	v.Text = fmt.Sprintf("%.1f%%", value)
	v.Health = card.Health
	v.Freshness = card.Freshness
	v.Check = card.Check
	v.Observer = card.Observer.ID
	v.SampledAt = card.SampledAt
	v.ValidUntil = card.ValidUntil
	switch {
	case strings.HasPrefix(r.Metric, "resource.cpu."):
		v.Basis = card.Facts["cpu_basis"]
		if v.Basis == "" {
			if card.Native {
				v.Basis = "host_normalized_0_100"
			} else {
				v.Basis = "source_reported_0_100"
			}
		}
		v.SampleWindowMS, _ = strconv.ParseInt(card.Facts["cpu_sample_ms"], 10, 64)
	case strings.HasPrefix(r.Metric, "resource.memory."):
		v.Basis = card.Facts["memory_basis"]
	case strings.HasPrefix(r.Metric, "resource.disk."):
		v.Basis = card.Facts["disk_basis"]
	}
	return v
}

func deliveryValue(v Value, reports []model.DeliveryStatus, now time.Time) Value {
	var matches []model.DeliveryStatus
	for _, r := range reports {
		if v.Select.Collector == "" || v.Select.Collector == r.Collector {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		return missing(v, "never_reported")
	}
	if len(matches) > 1 {
		return missing(v, "selector_ambiguous")
	}
	r := matches[0]
	v.Collector = r.Collector
	v.SampledAt = r.ReportedAt
	v.ValidUntil = r.ReportedAt.Add(model.SnapshotLifetime)
	if r.ReportedAt.After(now.Add(model.SampleClockAhead)) {
		return missing(v, "clock_skew")
	}
	if !now.Before(v.ValidUntil) {
		v.Freshness = "stale"
		return missing(v, "delivery_expired")
	}
	v.Health = "up"
	if r.Code != "" {
		v.Health = "degraded"
	}
	switch v.Metric {
	case "delivery.status":
		v.Value = r.Code
		if r.Code == "" {
			v.Value = "ready"
		}
		v.Text = v.Value.(string)
	case "delivery.pending_records":
		v.Value = r.PendingRecords
		v.Text = strconv.Itoa(r.PendingRecords)
	case "delivery.pending_bytes":
		v.Value = r.PendingBytes
		v.Text = strconv.FormatInt(r.PendingBytes, 10) + " B"
	}
	return v
}

// ageSnapshot preserves collector judgments while allowing check and observer
// evidence to expire between source refreshes. It never mutates its input.
func ageSnapshot(s model.Snapshot, now time.Time, clocks *evidenceIndex) model.Snapshot {
	s.Checks = slices.Clone(s.Checks)
	for i := range s.Checks {
		c := &s.Checks[i]
		clock := clocks.checks[c.ID]
		if c.Freshness == "fresh" && (clock.sampled.After(now.Add(model.SampleClockAhead)) || !now.Before(clock.until)) {
			c.Freshness = "stale"
			c.Health = "unknown"
		}
	}
	s.Observers = slices.Clone(s.Observers)
	for i := range s.Observers {
		o := &s.Observers[i]
		for _, d := range s.Topology.Observers {
			if d.ID == o.ID && o.Health == "up" && (o.LastSeen.After(now.Add(model.SampleClockAhead)) || !now.Before(o.LastSeen.Add(time.Duration(d.StaleSeconds)*time.Second))) {
				o.Health = "unknown"
			}
		}
	}
	return s
}

// FrameError describes a rejected stream request without exposing upstream details.
type FrameError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
