package view

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// The Services tab can show each service as a card, cut from the server
// cards' cloth. A service's card says where it runs and what state it is
// in, who sees it from where and how it fared there, how it is reached and
// through what, where the edge in front answered from, what it depends on
// and what depends on it, its runbook and the operator's notes.

// ServiceCard is a service's card. Healths are those of the view: unknown
// throughout a cached one, which also withholds every current reading.
// Elsewhere is whether, in a view scoped to one observer, another's checks
// measure the service: then nothing seen here means not seen from here,
// not unmonitored.
type ServiceCard struct {
	Assessment  ServiceAssessment `json:"assessment"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Application Ref               `json:"application"`
	Environment string            `json:"environment"`
	Runtime     string            `json:"runtime"`
	Health      string            `json:"health"`
	Note        string            `json:"note"` // affected, cached or ""
	Evidence    string            `json:"evidence"`
	Reason      string            `json:"reason"`
	Monitored   bool              `json:"monitored"`
	Elsewhere   bool              `json:"elsewhere"`
	Host        ServiceHost       `json:"host"`
	SeenFrom    []SeenFrom        `json:"seen_from"`
	Endpoints   []CardEndpoint    `json:"endpoints"`
	Answered    *AnsweredFrom     `json:"answered_from"`
	DependsOn   []ServiceRef      `json:"depends_on"`
	UsedBy      []ServiceRef      `json:"used_by"`
	Runbook     string            `json:"runbook"`
	Memo        string            `json:"memo"`
}

// ServiceHost is the node a service runs on. Where is its place: its
// location's label, its parent's where it has none, or anycast for an edge.
type ServiceHost struct {
	Ref
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Where    string `json:"where"`
	Health   string `json:"health"`
}

// SeenFrom is an observer and the checks of a service it runs, worst
// first. Label is its place, or where two observers of the card share a
// place, its name without it; Health is its worst check's.
type SeenFrom struct {
	Observer Ref         `json:"observer"`
	Label    string      `json:"label"`
	Health   string      `json:"health"`
	Checks   []SeenCheck `json:"checks"`
}

// SeenCheck is one check of a service as its card shows it. Latency and
// Up24h come from its day-long rollup and stay in a cached view, as the
// shares do; Colo is what the edge in front named in its newest response,
// only while that is current.
type SeenCheck struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Endpoint  string            `json:"endpoint"`
	Health    string            `json:"health"`
	Freshness string            `json:"freshness"`
	SampledAt time.Time         `json:"sampled_at,omitzero"`
	Latency   *model.Latency    `json:"latency"`
	Up24h     *float64          `json:"up_24h"`
	FirstByte string            `json:"first_byte"`
	Colo      string            `json:"colo"`
	Interval  int               `json:"interval_seconds"`
	Check     model.Check       `json:"-"`
	Facts     map[string]string `json:"-"`
}

// AnsweredFrom is where the edge in front of a service answered its
// checks over the day, and what it said of its cache: shares of the
// checks' rollups added up, largest first.
type AnsweredFrom struct {
	Pops  []string `json:"pops"`
	Cache []string `json:"cache"`
}

// ServiceRef is a service another depends on, or one that depends on it.
type ServiceRef struct {
	Ref
	Health    string `json:"health"`
	Affected  bool   `json:"affected"`
	Monitored bool   `json:"monitored"`
	Host      string `json:"host"`
}

// cardSeen and cardRefs bound what a card lists; cardChecks bounds an
// observer's checks.
const (
	cardSeen   = 8
	cardChecks = 4
	cardRefs   = 16
)

// HealthRank orders healths worst first: down, degraded, unknown, up.
func HealthRank(h string) int {
	switch h {
	case "down":
		return 0
	case "degraded":
		return 1
	case "up":
		return 3
	}
	return 2
}

// ServiceHead is the head of the card of the service with the given ID,
// what a list of services says of each: its identity, state and host,
// with nothing listed. ok is whether the topology declares it.
func (f *Fleet) ServiceHead(id string) (c ServiceCard, ok bool) {
	f.indexServices()
	sv, ok := f.serviceDefs[id]
	if !ok {
		return ServiceCard{}, false
	}
	e := f.svcState[id]
	c = ServiceCard{Assessment: f.assessments[id], ID: id, Name: named(sv.Name, id), Application: Ref{sv.Application, f.appNames[sv.Application]}, Environment: sv.Environment, Runtime: sv.Runtime,
		Evidence: e.Evidence, Reason: e.Reason, Monitored: len(e.Checks) > 0, Runbook: strings.TrimSpace(sv.Runbook), Memo: strings.TrimSpace(sv.Notes),
		SeenFrom: []SeenFrom{}, Endpoints: []CardEndpoint{}, DependsOn: []ServiceRef{}, UsedBy: []ServiceRef{}}
	if c.Application.ID != "" && c.Application.Name == "" {
		c.Application.Name = c.Application.ID
	}
	c.Elsewhere = f.scope != "" && slices.ContainsFunc(f.topology.Checks, func(x model.Check) bool { return x.Service == id && x.Observer != f.scope })
	c.Health, c.Note = entityPresentation(e)
	if f.cached {
		c.Health, c.Note, c.Reason = "unknown", "cached", ""
	}
	n := f.Nodes[sv.Node]
	c.Host = ServiceHost{Ref: Ref{sv.Node, f.Names[sv.Node]}, Kind: n.Kind, Provider: ProviderName(n.Provider), Where: f.place(n), Health: f.state(f.nodeState[sv.Node].Health)}
	if c.Host.Name == "" {
		c.Host.Name = sv.Node
	}
	return c, true
}

// state is a health as the view shows it: unknown throughout a cached one.
func (f *Fleet) state(h string) string {
	if f.cached {
		return "unknown"
	}
	return h
}

// ServiceCard is the card of the service with the given ID, and whether
// the topology declares it.
func (f *Fleet) ServiceCard(id string) (ServiceCard, bool) {
	c, ok := f.ServiceHead(id)
	if !ok {
		return c, false
	}
	sv, e := f.serviceDefs[id], f.svcState[id]

	// Who sees it from where: its checks by observer, the observer with the
	// worst check first, each observer's checks worst first.
	by := map[string]*SeenFrom{}
	order := []string{}
	for _, cid := range e.Checks {
		x, known := f.checkDefs[cid]
		if !known {
			continue
		}
		s, ok := by[x.Observer]
		if !ok {
			o := f.observerDefs[x.Observer]
			s = &SeenFrom{Observer: Ref{x.Observer, named(o.Name, x.Observer)}, Label: f.observerPlace(x.Observer)}
			by[x.Observer] = s
			order = append(order, x.Observer)
		}
		s.Checks = append(s.Checks, f.seenCheck(x))
	}
	for _, id := range order {
		s := by[id]
		sort.SliceStable(s.Checks, func(i, j int) bool { return HealthRank(s.Checks[i].Health) < HealthRank(s.Checks[j].Health) })
		s.Health = s.Checks[0].Health
		s.Checks = s.Checks[:min(len(s.Checks), cardChecks)]
		c.SeenFrom = append(c.SeenFrom, *s)
	}
	sort.SliceStable(c.SeenFrom, func(i, j int) bool { return HealthRank(c.SeenFrom[i].Health) < HealthRank(c.SeenFrom[j].Health) })
	c.SeenFrom = c.SeenFrom[:min(len(c.SeenFrom), cardSeen)]
	// Two observers in one place go by their names, less the place.
	places := map[string]int{}
	for _, s := range c.SeenFrom {
		places[s.Label]++
	}
	for i, s := range c.SeenFrom {
		if places[s.Label] > 1 {
			if rest := strings.TrimSpace(strings.Replace(s.Observer.Name, s.Label, "", 1)); rest != "" {
				c.SeenFrom[i].Label = rest
			} else {
				c.SeenFrom[i].Label = s.Observer.Name
			}
		}
	}

	// How it is reached, and where the edge in front answered from.
	for _, eid := range sv.Endpoints {
		ep, ok := f.endpointDefs[eid]
		if !ok {
			continue
		}
		c.Endpoints = append(c.Endpoints, f.cardEndpoint(ep))
	}
	pops, cache := map[string]int64{}, map[string]int64{}
	for _, cid := range e.Checks {
		if r := RollupWindow(f.checkStates[cid].Availability, "24h"); r != nil {
			for k, v := range r.Pops {
				pops[k] += v
			}
			for k, v := range r.Cache {
				cache[k] += v
			}
		}
	}
	if shares := PopShares(pops, cardPops); len(shares) > 0 {
		c.Answered = &AnsweredFrom{Pops: shares, Cache: cacheShares(cache)}
	}

	// What it depends on, and what depends on it.
	ref := func(other string) ServiceRef {
		o := f.serviceDefs[other]
		st := f.svcState[other]
		return ServiceRef{Ref: Ref{other, named(o.Name, other)}, Health: f.state(st.Health), Affected: st.Affected && !f.cached, Monitored: len(st.Checks) > 0, Host: f.Names[o.Node]}
	}
	for _, d := range sv.DependsOn {
		if _, ok := f.serviceDefs[d]; ok && len(c.DependsOn) < cardRefs {
			c.DependsOn = append(c.DependsOn, ref(d))
		}
	}
	for _, d := range f.dependents[id] {
		if len(c.UsedBy) < cardRefs {
			c.UsedBy = append(c.UsedBy, ref(d))
		}
	}
	return c, true
}

// seenCheck is a check as a service's card shows it.
func (f *Fleet) seenCheck(x model.Check) SeenCheck {
	cs := f.checkStates[x.ID]
	out := SeenCheck{ID: x.ID, Name: named(x.Name, x.ID), Kind: x.Kind, Endpoint: x.Endpoint, Health: cs.Health, Freshness: cs.Freshness, Interval: x.IntervalSeconds, Check: x}
	if out.Health == "" {
		out.Health = "unknown"
	}
	if out.Freshness == "" {
		out.Freshness = "never"
	}
	if cs.Last != nil {
		out.SampledAt = cs.Last.SampledAt
	}
	if f.cached {
		out.Health, out.Freshness = "unknown", "stale"
	}
	if r := RollupWindow(cs.Availability, "24h"); r != nil {
		if r.Latency != nil && r.Latency.Samples > 0 {
			l := *r.Latency
			out.Latency = &l
		}
		if m := r.Measured(); m > 0 && r.Unreconciled == 0 {
			up := math.Floor(1000*float64(r.Up)/float64(m)) / 10
			out.Up24h = &up
		}
		out.FirstByte = FirstByteLine(r.FirstByte)
	}
	if out.Freshness == "fresh" && cs.Last != nil && !f.cached {
		out.Facts = cs.Last.Facts
		if pop := cs.Last.Facts["edge_pop"]; cs.Last.Facts["edge_provider"] != "vercel" && model.IsColo(pop) {
			out.Colo = pop
		}
	}
	return out
}

// cacheShares orders the cache statuses a rollup counted by their share
// of those the edge named, never rounded up; other is left out.
func cacheShares(cache map[string]int64) []string {
	total := int64(0)
	keys := []string{}
	for k, n := range cache {
		if k != "other" && n > 0 {
			total += n
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if cache[keys[i]] != cache[keys[j]] {
			return cache[keys[i]] > cache[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := []string{}
	for _, k := range keys[:min(len(keys), cardPops)] {
		pct := int(math.Floor(100 * float64(cache[k]) / float64(total)))
		share := strconv.Itoa(pct) + "%"
		if pct < 1 {
			share = "<1%"
		}
		out = append(out, k+" "+share)
	}
	return out
}

// place is a node's place: its location's label, its parent's where it
// has none, anycast for an edge, else "".
func (f *Fleet) place(n model.Node) string {
	switch {
	case n.Location != nil && n.Location.Label != "":
		return n.Location.Label
	case n.Location != nil:
		return LatLon(*n.Location)
	}
	if p, ok := f.Nodes[n.Parent]; ok && n.Parent != n.ID && p.Location != nil {
		return f.place(p)
	}
	if n.Kind == "edge" {
		return "anycast"
	}
	return ""
}

// observerPlace is where an observer is: its node's place, else its name.
func (f *Fleet) observerPlace(id string) string {
	o := f.observerDefs[id]
	if p := f.place(f.Nodes[o.Node]); p != "" && p != "anycast" {
		return p
	}
	return named(o.Name, id)
}

// cardEndpoint is an address as a card shows it.
func (f *Fleet) cardEndpoint(ep model.Endpoint) CardEndpoint {
	via := []Ref{}
	for _, id := range ep.Via {
		via = append(via, Ref{id, f.Names[id]})
	}
	return CardEndpoint{ID: ep.ID, Network: ep.Network, Address: ep.Address, Health: f.epHealth[ep.ID], Node: Ref{ep.Node, f.Names[ep.Node]}, Via: via}
}

// indexServices indexes what service cards need beyond the server cards,
// once, the first time a service card is asked for.
func (f *Fleet) indexServices() {
	if f.serviceDefs != nil {
		return
	}
	t := f.topology
	f.serviceDefs = make(map[string]model.Service, len(t.Services))
	f.dependents = map[string][]string{}
	for _, s := range t.Services {
		f.serviceDefs[s.ID] = s
	}
	for _, s := range t.Services {
		for _, d := range s.DependsOn {
			if _, ok := f.serviceDefs[d]; ok && !slices.Contains(f.dependents[d], s.ID) {
				f.dependents[d] = append(f.dependents[d], s.ID)
			}
		}
	}
	f.appNames = make(map[string]string, len(t.Applications))
	for _, a := range t.Applications {
		f.appNames[a.ID] = a.Name
	}
	f.checkDefs = make(map[string]model.Check, len(t.Checks))
	for _, x := range t.Checks {
		f.checkDefs[x.ID] = x
	}
	f.observerDefs = make(map[string]model.Observer, len(t.Observers))
	for _, o := range t.Observers {
		f.observerDefs[o.ID] = o
	}
	f.endpointDefs = make(map[string]model.Endpoint, len(t.Endpoints))
	for _, ep := range t.Endpoints {
		f.endpointDefs[ep.ID] = ep
	}
}

func named(name, id string) string {
	if name != "" {
		return name
	}
	return id
}
