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

// The Servers tab has a card for each server. A card says what the server
// is and where, what runs on it and how it is reached, what it hosts and
// who observes from it, and ends with the operator's notes as a memo. Edges
// and platforms get cards too, saying what runs there, what they stand in
// front of and where they answered.

// cardPops bounds the colos a card lists; the rest stay in the JSON's
// rollup counts, not in the display.
const cardPops = 6

// ServerNode reports whether a node gets a card of its own: anything but an
// external party or a container, which appears on its host's card.
func ServerNode(n model.Node, known map[string]bool) bool {
	return n.Kind != "external" && !(n.Kind == "container" && known[n.Parent])
}

// Ref names an entity: its ID, and its name where it has one.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Card is what a server's card says beside its state and name. Healths
// are those of the view: unknown throughout a cached one.
type Card struct {
	Origin    *model.Origin  `json:"origin,omitempty"`
	Resources []CardResource `json:"resources,omitempty"`
	Kind      string         `json:"kind"`            // with its provider, e.g. "edge · Cloudflare"
	Group     string         `json:"group,omitempty"` //
	Where     string         `json:"where,omitempty"` // its place, "anycast", or "on" its host
	Services  []CardService  `json:"services"`        // what runs here, trouble first
	Fronts    []CardEndpoint `json:"in_front_of"`     // the addresses it answers first for
	Regions   []string       `json:"reported_edge_regions,omitempty"`
	Pops      []string       `json:"answered_from"` // where it said it answered: "LAX 92%", or a bare colo from fresh facts
	// PopsFromRollups says whether Pops are day shares from the collector's
	// rollups or the freshest reported colo codes. The two must not be read
	// as each other.
	PopsFromRollups bool           `json:"pops_from_rollups,omitempty"`
	Addresses       []CardEndpoint `json:"addresses"`      // how it is reached
	Hosts           []CardHost     `json:"hosts"`          // the containers and VMs on it
	Observers       []CardObserver `json:"observers"`      // who observes from here
	Links           []CardLink     `json:"links"`          // its declared connections
	Memo            string         `json:"memo,omitempty"` // the operator's notes
}

// CardService is a service that runs on a server. Via are the edges and
// proxies in front of it; Monitored is false when no check measures it.
type CardService struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Health    string `json:"health"`
	Affected  bool   `json:"affected"`
	Via       []Ref  `json:"via"`
	Runtime   string `json:"runtime,omitempty"`
	Monitored bool   `json:"monitored"`
}

// CardEndpoint is an address. Its health aggregates the checks that target
// it, and is "" when none does.
type CardEndpoint struct {
	ID      string `json:"id"`
	Network string `json:"network"`
	Address string `json:"address"`
	Health  string `json:"health"`
	Node    Ref    `json:"node"`
	Via     []Ref  `json:"via"`
}

// CardHost is a node a server hosts, with the services running on it.
type CardHost struct {
	Ref
	Health string   `json:"health"`
	Runs   []string `json:"runs"`
}

// CardObserver is an observer on a server and when it last reported;
// Outdated when it is alive but measures an outdated topology.
type CardObserver struct {
	Ref
	Health   string    `json:"health"`
	LastSeen time.Time `json:"last_seen"`
	Outdated bool      `json:"outdated"`
}

// CardLink is a declared link from or, when Inbound, to a server. Health is
// the link's, unknown throughout a cached view. Reading is what its own fresh
// checks report of it, each a few words in the order the checks speak: a
// connector's "4 sessions" and the colos it holds, then the platform API's
// "API 4 connections healthy". It is reported evidence beside the health,
// never a reason for it, and empty when no check of the link reported.
type CardLink struct {
	ID        string   `json:"id"`
	Other     Ref      `json:"other"`
	Transport string   `json:"transport"`
	Inbound   bool     `json:"inbound"`
	Health    string   `json:"health,omitempty"`
	Reading   []string `json:"reading,omitempty"`
}

// Fleet is what the cards are built from, indexed once.
type Fleet struct {
	assessments map[string]ServiceAssessment
	resources   map[string][]CardResource
	Names       map[string]string // node names; the ID of a node without one
	Nodes       map[string]model.Node
	serviceVia  map[string][]string
	cached      bool
	nodeState   map[string]model.EntityState
	services    map[string][]model.Service // by node
	svcState    map[string]model.EntityState
	endpoints   map[string][]model.Endpoint // by node
	fronted     map[string][]model.Endpoint // by the edge or proxy in front
	epHealth    map[string]string
	children    map[string][]model.Node
	observers   map[string][]model.Observer // by node
	obsState    map[string]model.ObserverState
	links       map[string][]model.Link // by either end
	regions     map[string][]string
	pops        map[string][]string         // where each edge answered, freshly
	popCounts   map[string]map[string]int64 // 24h colo counts by edge
	linkState   map[string]string           // link health, unknown throughout a cached view
	linkRead    map[string][]string         // what each link's fresh checks report of it
	topology    model.Config
	scope       string // the observer the view is scoped to, "" for all
	checkStates map[string]model.CheckState

	// Indexed for service cards, the first time one is asked for.
	serviceDefs  map[string]model.Service
	dependents   map[string][]string
	appNames     map[string]string
	checkDefs    map[string]model.Check
	observerDefs map[string]model.Observer
	endpointDefs map[string]model.Endpoint
}

func (v View) Fleet() *Fleet { return v.FleetWithResources(nil) }

// FleetWithResources builds the same fleet from a resource selection already
// computed for this frame. A nil selection is computed here. The selection is
// read-only; callers that share it must not mutate the returned cards.
func (v View) FleetWithResources(selection map[string]NodeResources) *Fleet {
	s := v.Snapshot
	t := s.Topology
	f := &Fleet{assessments: serviceAssessments(s, v.Cached), Names: map[string]string{}, Nodes: map[string]model.Node{}, serviceVia: indexServiceVia(t), cached: v.Cached, nodeState: map[string]model.EntityState{}, services: map[string][]model.Service{}, svcState: map[string]model.EntityState{}, endpoints: map[string][]model.Endpoint{}, fronted: map[string][]model.Endpoint{}, epHealth: map[string]string{}, children: map[string][]model.Node{}, observers: map[string][]model.Observer{}, obsState: map[string]model.ObserverState{}, links: map[string][]model.Link{}, pops: map[string][]string{}, regions: map[string][]string{}, popCounts: map[string]map[string]int64{}, linkState: map[string]string{}, linkRead: map[string][]string{}, topology: t, scope: s.ViewObserver, checkStates: make(map[string]model.CheckState, len(s.Checks))}
	for _, n := range t.Nodes {
		f.Nodes[n.ID] = n
		f.Names[n.ID] = n.Name
		if n.Name == "" {
			f.Names[n.ID] = n.ID
		}
		if n.Parent != "" && n.Parent != n.ID {
			f.children[n.Parent] = append(f.children[n.Parent], n)
		}
	}
	for _, e := range s.Nodes {
		f.nodeState[e.ID] = e
	}
	for _, sv := range t.Services {
		f.services[sv.Node] = append(f.services[sv.Node], sv)
	}
	for _, e := range s.Services {
		f.svcState[e.ID] = e
	}
	endpoints := map[string]model.Endpoint{}
	for _, ep := range t.Endpoints {
		endpoints[ep.ID] = ep
		f.endpoints[ep.Node] = append(f.endpoints[ep.Node], ep)
		for _, id := range ep.Via {
			f.fronted[id] = append(f.fronted[id], ep)
		}
	}
	for _, o := range t.Observers {
		f.observers[o.Node] = append(f.observers[o.Node], o)
	}
	for _, o := range s.Observers {
		f.obsState[o.ID] = o
	}
	for _, l := range t.Links {
		f.links[l.From] = append(f.links[l.From], l)
		if l.To != l.From {
			f.links[l.To] = append(f.links[l.To], l)
		}
		f.linkState[l.ID] = "unknown"
	}
	if !v.Cached {
		for _, e := range s.Links {
			f.linkState[e.ID] = e.Health
		}
		f.linkReadings(s, t)
	}
	target := map[string]string{}
	for _, x := range t.Checks {
		target[x.ID] = x.Endpoint
	}
	byEdge := map[string][]model.CheckState{}
	for _, cs := range s.Checks {
		f.checkStates[cs.ID] = cs
		ep, ok := endpoints[target[cs.ID]]
		if !ok {
			continue
		}
		at := ep.Node
		if len(ep.Via) > 0 {
			at = ep.Via[0]
		}
		// The day's shares come from the check's rollups, fresh or not:
		// they describe what was seen, and a stale view still holds them.
		byEdge[at] = append(byEdge[at], cs)
		// Where an edge said it answered from, freshly, is the fallback for
		// a collector that keeps no rollups yet.
		if cs.Freshness != "fresh" || cs.Last == nil || v.Cached {
			continue
		}
		if r := cs.Last.Facts["vercel_edge_region"]; cs.Last.Facts["edge_provider"] == "vercel" && r != "" && !slices.Contains(f.regions[at], r) {
			f.regions[at] = append(f.regions[at], r)
		}
		if pop := cs.Last.Facts["edge_pop"]; cs.Last.Facts["edge_provider"] != "vercel" && pop != "" && !slices.Contains(f.pops[at], pop) {
			f.pops[at] = append(f.pops[at], pop)
		}
	}
	for at, checks := range byEdge {
		if pops := sumDayPops(checks, nil); pops != nil {
			f.popCounts[at] = pops
		}
	}
	// An endpoint some check in the view targets has a health.
	for _, e := range s.Endpoints {
		if len(e.Checks) > 0 {
			f.epHealth[e.ID] = map[bool]string{true: "unknown", false: e.Health}[v.Cached]
		}
	}
	for at := range f.regions {
		sort.Strings(f.regions[at])
	}
	for at := range f.pops {
		sort.Strings(f.pops[at])
	}
	if selection == nil {
		selection = v.SelectResources()
	}
	f.resources = resourceCards(selection)
	return f
}

// NodeState is the node's health and presentation note, shared with topology
// rows without constructing their endpoint lists and explanatory details.
func (f *Fleet) NodeState(id string) (health, note string) {
	if f.cached {
		return "unknown", "cached"
	}
	e, ok := f.nodeState[id]
	if !ok {
		return "unknown", ""
	}
	return entityPresentation(e)
}

// Card is the card of the server with the given ID.
func (f *Fleet) Card(id string) Card {
	n := f.Nodes[id]
	state := func(h string) string {
		if f.cached {
			return "unknown"
		}
		return h
	}
	refs := func(ids []string) []Ref {
		out := []Ref{}
		for _, id := range ids {
			out = append(out, Ref{id, f.Names[id]})
		}
		return out
	}
	endpoint := func(ep model.Endpoint) CardEndpoint {
		return CardEndpoint{ID: ep.ID, Network: ep.Network, Address: ep.Address, Health: f.epHealth[ep.ID], Node: Ref{ep.Node, f.Names[ep.Node]}, Via: refs(ep.Via)}
	}
	c := Card{Kind: KindLabel(n), Group: n.Group, Memo: strings.TrimSpace(n.Notes), Services: []CardService{}, Fronts: []CardEndpoint{}, Pops: []string{}, Addresses: []CardEndpoint{}, Hosts: []CardHost{}, Observers: []CardObserver{}, Links: []CardLink{}}
	c.Resources = f.resources[n.ID]
	for _, r := range c.Resources {
		if r.Selected {
			c.Origin = r.Origin
			break
		}
	}
	if c.Kind == "" {
		c.Kind = "node"
	}
	switch {
	case n.Location != nil && n.Location.Label != "":
		c.Where = n.Location.Label
	case n.Location != nil:
		c.Where = LatLon(*n.Location)
	case n.Kind == "edge":
		c.Where = "anycast"
	case n.Parent != "":
		c.Where = "on " + f.Names[n.Parent]
	}

	// What runs here, healthiest last so trouble reads first.
	for _, sv := range f.services[n.ID] {
		e := f.svcState[sv.ID]
		c.Services = append(c.Services, CardService{ID: sv.ID, Name: sv.Name, Health: state(e.Health), Affected: e.Affected && !f.cached, Via: refs(f.serviceVia[sv.ID]), Runtime: sv.Runtime, Monitored: len(e.Checks) > 0})
	}
	rank := func(sv CardService) int {
		note := ""
		if sv.Affected && sv.Health != "down" {
			note = "affected"
		}
		return ServiceRank(Row{Health: sv.Health, Note: note})
	}
	sort.SliceStable(c.Services, func(i, j int) bool { return rank(c.Services[i]) < rank(c.Services[j]) })

	// An edge: the addresses it answers for, and where it answered from.
	for _, ep := range f.fronted[n.ID] {
		c.Fronts = append(c.Fronts, endpoint(ep))
	}
	// The day's shares first, at most six, largest first; a collector with
	// no rollups yet falls back to the freshly reported colos, ordered by
	// code as before. The flag keeps the two sources apart for text, JSON
	// and the TUI.
	if shares := PopShares(f.popCounts[n.ID], cardPops); len(shares) > 0 {
		c.Pops = append(c.Pops, shares...)
		c.PopsFromRollups = true
	} else {
		c.Pops = append(c.Pops, f.pops[n.ID]...)
	}
	c.Regions = append(c.Regions, f.regions[n.ID]...)

	// How it is reached, what it hosts and who observes from here.
	for _, ep := range f.endpoints[n.ID] {
		c.Addresses = append(c.Addresses, endpoint(ep))
	}
	for _, k := range f.children[n.ID] {
		runs := []string{}
		for _, sv := range f.services[k.ID] {
			runs = append(runs, sv.Name)
		}
		c.Hosts = append(c.Hosts, CardHost{Ref: Ref{k.ID, f.Names[k.ID]}, Health: state(f.nodeState[k.ID].Health), Runs: runs})
	}
	for _, o := range f.observers[n.ID] {
		st := f.obsState[o.ID]
		c.Observers = append(c.Observers, CardObserver{Ref: Ref{o.ID, o.Name}, Health: state(st.Health), LastSeen: st.LastSeen, Outdated: !f.cached && st.Health == "up" && st.Contracts == "outdated"})
	}

	// Its declared connections.
	for _, l := range f.links[n.ID] {
		inbound, other := false, l.To
		if l.To == n.ID && l.From != n.ID {
			inbound, other = true, l.From
		}
		c.Links = append(c.Links, CardLink{ID: l.ID, Other: Ref{other, f.Names[other]}, Transport: l.Transport, Inbound: inbound, Health: f.linkState[l.ID], Reading: f.linkRead[l.ID]})
	}
	return c
}

// LatLon writes a place as "35.7°N 139.7°E".
func LatLon(l model.Location) string {
	ns, ew := "N", "E"
	if l.Lat < 0 {
		ns = "S"
	}
	if l.Lon < 0 {
		ew = "W"
	}
	return strconv.FormatFloat(math.Abs(l.Lat), 'f', 1, 64) + "°" + ns + " " + strconv.FormatFloat(math.Abs(l.Lon), 'f', 1, 64) + "°" + ew
}
