package monitor

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

type SummaryCounts struct {
	view.Counts
	Paused      int `json:"paused"`
	Maintenance int `json:"maintenance"`
}
type SummaryVerdict struct {
	view.Verdict
	Counts     SummaryCounts `json:"counts"`
	Configured int           `json:"configured"`
	Incidents  int           `json:"incidents"`
	Observers  struct {
		Live int `json:"live"`
		All  int `json:"all"`
	} `json:"observers"`
}
type ObserverVerdict struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Node       string      `json:"node"`
	Health     string      `json:"health"`
	State      string      `json:"state"`
	Live       bool        `json:"live"`
	LastSeen   *time.Time  `json:"last_seen"`
	ValidUntil *time.Time  `json:"valid_until,omitempty"`
	Counts     view.Counts `json:"counts"`
}
type AttentionItem struct {
	Kind         string     `json:"kind"`
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Route        []string   `json:"route"`
	Observer     string     `json:"observer"`
	ObserverName string     `json:"observer_name"`
	Since        *time.Time `json:"since"`
	Health       string     `json:"health"`
	State        string     `json:"state"`
}
type AttentionList struct {
	Total int             `json:"total"`
	Items []AttentionItem `json:"items"`
}
type ServiceValue struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Health    string `json:"health"`
	Monitored bool   `json:"monitored"`
	Affected  bool   `json:"affected"`
}
type ApplicationValue struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Environment string         `json:"environment"`
	Health      string         `json:"health"`
	Services    []ServiceValue `json:"services"`
	Counts      view.Counts    `json:"counts"`
	Attention   int            `json:"attention"`
	Unmonitored int            `json:"unmonitored"`
}
type ResourceValue struct {
	Origin          *model.Origin `json:"origin,omitempty"`
	Check           string        `json:"check"`
	Observer        string        `json:"observer"`
	Health          string        `json:"health"`
	Freshness       string        `json:"freshness"`
	SampledAt       *time.Time    `json:"sampled_at"`
	ValidUntil      *time.Time    `json:"valid_until"`
	Source          string        `json:"source,omitempty"`
	Object          string        `json:"object,omitempty"`
	TimeBasis       string        `json:"time_basis,omitempty"`
	SelectionReason string        `json:"selection_reason,omitempty"`
	Selected        bool          `json:"selected,omitempty"`
	DataDeadline    time.Time     `json:"data_deadline,omitzero"`
	ContactDeadline time.Time     `json:"contact_deadline,omitzero"`
	CPU             struct {
		Percent *float64 `json:"percent"`
		Limit   *float64 `json:"limit"`
		Logical *uint64  `json:"logical"`
		Basis   string   `json:"basis,omitempty"`
	} `json:"cpu"`
	Memory struct {
		Percent *float64 `json:"percent"`
		Limit   *float64 `json:"limit"`
		Used    *uint64  `json:"used_bytes"`
		Total   *uint64  `json:"total_bytes"`
		Basis   string   `json:"basis"`
	} `json:"memory"`
	Disk struct {
		Percent *float64 `json:"percent"`
		Limit   *float64 `json:"limit"`
		Path    string   `json:"path"`
		Used    *uint64  `json:"used_bytes"`
		Total   *uint64  `json:"total_bytes"`
		Basis   string   `json:"basis,omitempty"`
	} `json:"disk"`
	Process *ProcessValue `json:"process"`
}
type ProcessValue struct {
	Name     string  `json:"name"`
	Matching *uint64 `json:"matching"`
}
type ServerListEntry struct {
	Origin   *model.Origin `json:"origin,omitempty"`
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Kind     string        `json:"kind"`
	Provider string        `json:"provider"`
	Group    string        `json:"group"`
	Where    string        `json:"where"`
	Health   string        `json:"health"`
	Note     string        `json:"note"`
}
type ServerCard struct {
	Origin    *model.Origin     `json:"origin,omitempty"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Provider  string            `json:"provider"`
	Group     string            `json:"group"`
	Where     string            `json:"where"`
	Health    string            `json:"health"`
	Note      string            `json:"note"`
	Resources []ResourceValue   `json:"resources"`
	Services  []ServiceValue    `json:"services"`
	Observes  []ObserverVerdict `json:"observes"`
}

func verdictHealth(state string) string {
	switch state {
	case "clear":
		return "up"
	case "degraded":
		return "degraded"
	case "incidents", "down":
		return "down"
	}
	return "unknown"
}
func healthRank(health string) int {
	switch health {
	case "down":
		return 0
	case "degraded":
		return 1
	case "up":
		return 3
	}
	return 2
}
func worse(a, b string) string {
	if healthRank(b) < healthRank(a) {
		return b
	}
	return a
}
func named(id, name string) string {
	if name != "" {
		return name
	}
	return id
}
func stamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}
func summary(v view.View) SummaryVerdict {
	out := SummaryVerdict{Verdict: v.Verdict(), Counts: SummaryCounts{Counts: v.Counts()}, Incidents: len(v.Snapshot.Incidents)}
	out.Configured = out.Counts.Total()
	out.Observers.Live, out.Observers.All = v.Live()
	for _, c := range v.Snapshot.Checks {
		if c.Health != "unknown" && !v.Cached {
			continue
		}
		switch c.Freshness {
		case "paused":
			out.Counts.Paused++
		case "maintenance":
			out.Counts.Maintenance++
		}
	}
	return out
}
func observerVerdicts(v view.View) []ObserverVerdict {
	defs := map[string]model.Observer{}
	for _, o := range v.Snapshot.Topology.Observers {
		defs[o.ID] = o
	}
	out := []ObserverVerdict{}
	for _, o := range v.Snapshot.Observers {
		own := v.ObserverView(o.ID)
		if v.Snapshot.ViewObserver != "" && v.Snapshot.ViewObserver != o.ID {
			// Scoped feeds retain global heartbeats, but not other observers'
			// check judgments. Keep liveness and mark only those judgments cached.
			own.Cached = true
			own.Snapshot.Checks = nil
			for _, c := range v.Snapshot.Topology.Checks {
				if c.Observer == o.ID {
					own.Snapshot.Checks = append(own.Snapshot.Checks, model.CheckState{ID: c.ID, Health: "unknown"})
				}
			}
		}
		verdict := own.Verdict()
		d := defs[o.ID]
		live := o.Health == "up" && !v.Cached
		h := verdictHealth(verdict.State)
		if !live {
			h = "unknown"
		}
		out = append(out, ObserverVerdict{ID: o.ID, Name: named(o.ID, d.Name), Node: d.Node, Health: h, State: verdict.State, Live: live, LastSeen: stamp(o.LastSeen), Counts: own.Counts()})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if healthRank(out[i].Health) != healthRank(out[j].Health) {
			return healthRank(out[i].Health) < healthRank(out[j].Health)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
func attention(v view.View, limit int) AttentionList {
	rows := v.List(view.Overview, "")
	out := AttentionList{Total: len(rows), Items: []AttentionItem{}}
	names := map[string]string{}
	for _, o := range v.Snapshot.Topology.Observers {
		names[o.ID] = named(o.ID, o.Name)
	}
	for _, r := range rows[:min(limit, len(rows))] {
		o := ""
		var since *time.Time
		if r.Kind == "incident" {
			for _, i := range v.Snapshot.Incidents {
				if i.ID == r.ID {
					o, _, _ = strings.Cut(i.Scope, "/")
					since = stamp(i.Since)
					break
				}
			}
			for _, i := range v.Snapshot.Ledger {
				if i.ID == r.ID && (i.State == "open" || i.State == "recovering") {
					o, _, _ = strings.Cut(i.Scope, "/")
					since = stamp(i.Since)
					break
				}
			}
		} else if r.Kind == "observer" {
			o = r.ID
		} else if r.Kind == "check" {
			for _, c := range v.Snapshot.Checks {
				if c.ID == r.ID {
					o = c.Observer
					break
				}
			}
		} else if r.Kind == "path" {
			for _, p := range v.Snapshot.Topology.Paths {
				if p.ID == r.ID {
					o = p.Observer
					break
				}
			}
		}
		state := strings.ToLower(view.State(r))
		if r.Kind == "incident" {
			state = strings.ToLower(view.Pill(r))
		}
		if state == "" {
			state = "up"
		}
		route := strings.Split(r.Title, " → ")
		if len(route) > 1 && route[0] == names[o] {
			route = route[1:]
		}
		out.Items = append(out.Items, AttentionItem{Kind: r.Kind, ID: r.ID, Title: r.Title, Route: route, Observer: o, ObserverName: names[o], Since: since, Health: r.Health, State: state})
	}
	return out
}
func services(v view.View) map[string]ServiceValue {
	out := map[string]ServiceValue{}
	fleet := v.Fleet()
	// Include services on an undeclared/empty node too, as the Services tab does.
	nodes := map[string]bool{}
	for _, s := range v.Snapshot.Topology.Services {
		if nodes[s.Node] {
			continue
		}
		nodes[s.Node] = true
		for _, c := range fleet.Card(s.Node).Services {
			out[c.ID] = ServiceValue{ID: c.ID, Name: named(c.ID, c.Name), Health: c.Health, Monitored: c.Monitored, Affected: c.Affected}
		}
	}
	return out
}
func applications(v view.View) []ApplicationValue {
	out := []ApplicationValue{}
	svcs := services(v)
	for _, g := range v.ApplicationGroups() {
		a := ApplicationValue{ID: g.ID, Name: g.Name, Environment: g.Environment, Health: g.Health, Counts: g.Counts, Services: []ServiceValue{}}
		for _, r := range g.Rows {
			svc := svcs[r.ID]
			if !svc.Monitored {
				a.Unmonitored++
			} else if svc.Health != "up" {
				a.Attention++
			}
			if len(a.Services) < 64 {
				a.Services = append(a.Services, svc)
			}
		}
		out = append(out, a)
		if len(out) == 64 {
			break
		}
	}
	return out
}

func percent(f map[string]string, key string) *float64 {
	n, e := strconv.ParseFloat(f[key], 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
		return nil
	}
	return &n
}
func number(f map[string]string, key string) *uint64 {
	n, e := strconv.ParseUint(f[key], 10, 64)
	if e != nil {
		return nil
	}
	return &n
}
func threshold(n float64) *float64 {
	if n <= 0 {
		return nil
	}
	return &n
}
func resource(v view.View, r view.CardResource) ResourceValue {
	out := ResourceValue{Origin: r.Origin, Check: r.Check, Observer: r.Observer.ID, Health: r.Health, Freshness: r.Freshness, SampledAt: stamp(r.SampledAt), Source: r.Source, Object: r.Object, TimeBasis: r.TimeBasis, SelectionReason: r.Reason, Selected: r.Selected, DataDeadline: r.DataDeadline, ContactDeadline: r.ContactDeadline}
	var def model.Check
	for _, c := range v.Snapshot.Topology.Checks {
		if c.ID == r.Check {
			def = c
			break
		}
	}
	if !r.ValidUntil.IsZero() {
		out.ValidUntil = stamp(r.ValidUntil)
	} else if !r.SampledAt.IsZero() {
		out.ValidUntil = stamp(minTime(r.SampledAt.Add(time.Duration(def.StaleSeconds)*time.Second), v.Snapshot.GeneratedAt.Add(model.SnapshotLifetime)))
	}
	h := view.ResourceOptions(def)
	out.Disk.Path = r.DiskPath
	if out.Disk.Path == "" {
		out.Disk.Path = h.DiskPath
	}
	if out.Disk.Path == "" {
		out.Disk.Path = "/"
	}
	if !r.External && h.ProcessName != "" {
		out.Process = &ProcessValue{Name: h.ProcessName}
	}
	// Facts are the view's authority on readable, fresh resource telemetry.
	if r.Freshness != "fresh" || r.Facts == nil || !r.Current {
		return out
	}
	f := r.Facts
	out.CPU.Percent, out.CPU.Logical, out.CPU.Limit = percent(f, "cpu_used_percent"), number(f, "cpu_logical_count"), threshold(h.MaxCPUPercent)
	out.CPU.Basis = f["cpu_basis"]
	out.Memory.Percent, out.Memory.Limit = percent(f, "memory_used_percent"), threshold(h.MaxMemoryPercent)
	out.Memory.Used, out.Memory.Total, out.Memory.Basis = number(f, "memory_used_bytes"), number(f, "memory_total_bytes"), f["memory_basis"]
	out.Disk.Percent, out.Disk.Limit = percent(f, "disk_used_percent"), threshold(h.MaxDiskPercent)
	out.Disk.Used, out.Disk.Total = number(f, "disk_used_bytes"), number(f, "disk_total_bytes")
	out.Disk.Basis = f["disk_basis"]
	if out.Process != nil {
		out.Process.Matching = number(f, "process_matching_count")
	}
	return out
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func serverEntry(fleet *view.Fleet, id string, card view.Card) ServerListEntry {
	n := fleet.Nodes[id]
	out := ServerListEntry{ID: id, Name: fleet.Names[id], Kind: n.Kind, Provider: view.ProviderName(n.Provider), Group: card.Group, Where: card.Where}
	out.Health, out.Note = fleet.NodeState(id)
	return out
}

func (p *projection) servers() ([]ServerListEntry, time.Time, time.Time) {
	if p.serverList == nil {
		fleet := p.serverFleet()
		known := make(map[string]bool, len(fleet.Nodes))
		for id := range fleet.Nodes {
			known[id] = true
		}
		p.serverList = []ServerListEntry{}
		ids := map[string]bool{}
		// The Servers tab preserves topology order and folds owned containers
		// into their host. Only listed node judgments bound this row's lease.
		for _, n := range p.v.Snapshot.Topology.Nodes {
			if !view.ServerNode(n, known) {
				continue
			}
			entry := serverEntry(fleet, n.ID, fleet.Card(n.ID))
			selected := p.resourceSelection()[n.ID].Selected
			entry.Origin = p.v.ResourceOrigin(selected.Source, selected.Object)
			p.serverList = append(p.serverList, entry)
			for _, id := range p.nodeChecks[n.ID] {
				ids[id] = true
			}
			if len(p.serverList) == 256 {
				break
			}
		}
		p.serverListClock.sampled, p.serverListClock.until = p.evidence().times(ids, false)
	}
	return p.serverList, p.serverListClock.sampled, p.serverListClock.until
}

func (p *projection) server(id string) (ServerCard, time.Time, time.Time, bool) {
	v := p.v
	fleet := p.serverFleet()
	_, ok := fleet.Nodes[id]
	if !ok {
		return ServerCard{}, time.Time{}, time.Time{}, false
	}
	card := fleet.Card(id)
	entry := serverEntry(fleet, id, card)
	selected := p.resourceSelection()[id].Selected
	entry.Origin = p.v.ResourceOrigin(selected.Source, selected.Object)
	out := ServerCard{Origin: entry.Origin, ID: entry.ID, Name: entry.Name, Kind: entry.Kind, Provider: entry.Provider, Group: entry.Group, Where: entry.Where, Health: entry.Health, Note: entry.Note, Resources: []ResourceValue{}, Services: []ServiceValue{}, Observes: []ObserverVerdict{}}
	ids := map[string]bool{}
	for _, c := range p.nodeChecks[id] {
		ids[c] = true
	}
	seen := map[string]bool{}
	for _, s := range card.Services[:min(32, len(card.Services))] {
		p.serviceEvidence(s.ID, ids, seen)
	}
	for _, r := range card.Resources {
		out.Resources = append(out.Resources, resource(v, r))
	}
	// Selection order is authoritative: an explicit primary disk or source
	// must not be reordered by a generic root-path preference.
	sampled, until := p.evidence().times(ids, false)
	for _, r := range out.Resources {
		// Resource deadlines govern only their numeric payloads.
		if r.SampledAt != nil && r.SampledAt.After(sampled) {
			sampled = *r.SampledAt
		}
	}
	for _, s := range card.Services[:min(32, len(card.Services))] {
		out.Services = append(out.Services, ServiceValue{ID: s.ID, Name: named(s.ID, s.Name), Health: s.Health, Monitored: s.Monitored, Affected: s.Affected})
	}
	onNode := map[string]bool{}
	for _, o := range card.Observers {
		onNode[o.ID] = true
	}
	for _, o := range p.observerValues() {
		if onNode[o.ID] {
			clock := p.completeEvidence().byObserver[o.ID]
			o.ValidUntil = stamp(clock.until)
			if clock.sampled.After(sampled) {
				sampled = clock.sampled
			}
			out.Observes = append(out.Observes, o)
		}
		if len(out.Observes) == 64 {
			break
		}
	}
	ageCard(&out, p.v.Now)
	return out, sampled, until, true
}

func (p *projection) richValue(value Value) Value {
	v := p.v
	value.SampledAt, value.ValidUntil = p.evidence().times(nil, true)
	value.Health = "up"
	if v.Cached {
		value.Freshness = "cached"
		value.Health = "unknown"
	}
	switch value.Metric {
	case "sources.status":
		status, until := v.SourcesStatus()
		value.Value = status
		value.Text = status.State
		value.ValidUntil = until
		value.SampledAt = time.Time{}
		value.Health = "unknown"
		if status.State == "ready" {
			value.Health = "up"
		} else if status.State == "partial" {
			value.Health = "degraded"
		}
	case "summary.verdict":
		s := summary(v)
		value.Value = s
		value.Text = s.Text
		value.Health = verdictHealth(s.State)
	case "observers.verdicts":
		observers := p.observerValues()
		problems := 0
		for _, o := range observers {
			value.Health = worse(value.Health, o.Health)
			if o.Health == "down" || o.Health == "degraded" {
				problems++
			}
		}
		value.Value = observers[:min(64, len(observers))]
		value.Text = fmt.Sprintf("%d of %d see a problem", problems, len(observers))
		value.SampledAt, value.ValidUntil = p.completeEvidence().times(nil, true)
	case "attention.list":
		limit := 5
		if value.Limit != nil {
			limit = *value.Limit
		}
		a := attention(v, limit)
		value.Value = a
		value.Text = fmt.Sprintf("%d need attention", a.Total)
		for _, item := range a.Items {
			value.Health = worse(value.Health, item.Health)
		}
	case "applications.list":
		apps := applications(v)
		value.Value = apps
		value.Text = fmt.Sprintf("%d applications", len(apps))
		for _, a := range apps {
			value.Health = worse(value.Health, a.Health)
		}
	case "server.card":
		card, sampled, until, ok := p.server(value.Select.Node)
		if !ok {
			return missing(value, "entity_missing")
		}
		value.Value = card
		value.Text = card.Name
		value.Health = card.Health
		value.SampledAt = sampled
		value.ValidUntil = until
	case "link.card":
		card, ok := p.link(value.Select.Link)
		if !ok {
			return missing(value, "entity_missing")
		}
		value.Value, value.Text, value.Health = card, card.From.Name+" → "+card.To.Name, card.Health
		if card.SampledAt != nil {
			value.SampledAt = *card.SampledAt
		} else {
			value.SampledAt = time.Time{}
		}
		if card.ValidUntil != nil {
			value.ValidUntil = *card.ValidUntil
		}
	case "links.list":
		list := []LinkSummary{}
		clock := evidenceClock{until: p.evidence().base}
		for _, def := range p.v.Snapshot.Topology.Links {
			s, ok := p.linkSummary(def.ID)
			if !ok {
				continue
			}
			list = append(list, s)
			c := evidenceClock{}
			if s.SampledAt != nil {
				c.sampled = *s.SampledAt
			}
			if s.ValidUntil != nil {
				c.until = *s.ValidUntil
			}
			clock = combineClock(clock, c)
			value.Health = worse(value.Health, s.Health)
			if len(list) == 256 {
				break
			}
		}
		value.Value, value.Text = list, fmt.Sprintf("%d links", len(list))
		value.SampledAt, value.ValidUntil = clock.sampled, clock.until
	case "check.card":
		card, ok := p.check(value.Select.Check)
		if !ok {
			return missing(value, "entity_missing")
		}
		value.Value, value.Text, value.Health = card, card.Name, card.Health
		clock := p.evidence().checks[card.ID]
		value.SampledAt, value.ValidUntil = clock.sampled, clock.until
	case "checks.list":
		p.indexChecks()
		list := []CheckListEntry{}
		ids := map[string]bool{}
		for _, def := range p.v.Snapshot.Topology.Checks {
			cs, ok := p.checkStates[def.ID]
			if !ok {
				continue
			}
			list = append(list, CheckListEntry{ID: def.ID, Name: named(def.ID, def.Name), Kind: def.Kind, Observer: def.Observer, Service: def.Service, Link: def.Link, Health: cs.Health, Freshness: cs.Freshness})
			ids[def.ID] = true
			value.Health = worse(value.Health, cs.Health)
			if len(list) == 256 {
				break
			}
		}
		value.Value, value.Text = list, fmt.Sprintf("%d checks", len(list))
		value.SampledAt, value.ValidUntil = p.evidence().times(ids, false)
	case "service.card":
		card, sampled, until, ok := p.service(value.Select.Service)
		if !ok {
			return missing(value, "entity_missing")
		}
		value.Value = card
		value.Text = card.Name
		value.Health = card.Health
		value.SampledAt, value.ValidUntil = sampled, until
	case "services.list":
		list, sampled, until := p.serviceList()
		value.Value = list
		value.Text = fmt.Sprintf("%d services", len(list))
		value.SampledAt, value.ValidUntil = sampled, until
		for _, s := range list {
			value.Health = worse(value.Health, s.Health)
		}
	case "servers.list":
		servers, sampled, until := p.servers()
		value.Value = servers
		value.Text = fmt.Sprintf("%d servers", len(servers))
		value.SampledAt, value.ValidUntil = sampled, until
		for _, server := range servers {
			value.Health = worse(value.Health, server.Health)
		}
	}
	return value
}
