package view

import (
	"math"
	"net"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Products read the topology's applications as their owner ships them: one
// product per application and environment, as the Services tab groups them.
// A product's lines are its services that nothing else in it depends on,
// such as an API and a website; each line says what it rests on in the order
// a request meets it (the edges in front, the machine or platform it runs on,
// the services it depends on), how its lead check fared and since when it is
// in trouble. Every mark is a judgment the view makes elsewhere: the group's
// health is the Services tab's, a line's the service's, a node's its own and
// a dependency's that service's. Nothing here adds them up anew.

// Bounds on what a product lists. ProductLines and ProductLayers bound a
// product's lines and a line's dependencies; the rest are counted.
const (
	ProductsMax       = 64
	ProductLines      = 8
	ProductDependents = 3
)

// ProductBoard is every product in the view, with how many there are by
// health, how many open or recovering incidents rest on them (each once,
// however many products it touches) and how many services belong to none.
type ProductBoard struct {
	Products  []Product `json:"products"`
	Counts    Counts    `json:"counts"`
	Incidents int       `json:"incidents"`
	Ungrouped int       `json:"ungrouped"`
}

// Product is an application group as its owner ships it. Its ID and
// environment identify it together. Health is the group's, as the Services
// tab says it; Incidents counts the open and recovering incidents that rest
// on its services' checks, and Since is when the earliest of them began.
// Certificate is the soonest-expiring certificate its lines' checks read.
type Product struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Kind        string        `json:"kind"`
	Environment string        `json:"environment"`
	Health      string        `json:"health"`
	Note        string        `json:"note"`
	Attention   int           `json:"attention"`
	Unmonitored int           `json:"unmonitored"`
	Incidents   int           `json:"incidents"`
	Since       time.Time     `json:"since,omitzero"`
	Lines       []ProductLine `json:"lines"`
	MoreLines   int           `json:"more_lines"`
	Certificate *Certificate  `json:"certificate"`
	Checks      []string      `json:"-"` // every check of its services, for clocks and history
}

// ProductLine is a service of a product that nothing else in it depends on.
// Health and Note are the service's, Assessment its assessment's closed
// state word (up, health_warn, local_fail, path_warn, access_fail or
// unknown); the pill reads the note first, then the assessment, as the
// Services tab's does, and Reason is the state's own words. Checks counts
// its checks by health.
type ProductLine struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Runtime    string     `json:"runtime"`
	Health     string     `json:"health"`
	Note       string     `json:"note"`
	Reason     string     `json:"reason"`
	Assessment string     `json:"assessment"`
	Monitored  bool       `json:"monitored"`
	Elsewhere  bool       `json:"elsewhere"`
	Chain      []Layer    `json:"chain"`
	MoreLayers int        `json:"more_layers"`
	Lead       *LeadCheck `json:"lead"`
	Checks     Counts     `json:"checks"`
	Row        Row        `json:"-"`
}

// Layer is one thing a line rests on, in the order a request meets it: an
// edge or proxy in front ("edge"), the machine it runs on ("host") or the
// edge or platform it is deployed to ("platform"), and a service it depends
// on ("dependency"). A node's health is its own, up when it answered and
// unknown when nothing proves it; whether it is affected is left out, since
// an edge or a machine shared by several products is affected by any of
// them. A dependency's health is that service's.
type Layer struct {
	Role     string `json:"role"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Runtime  string `json:"runtime"`
	Health   string `json:"health"`
}

// LeadCheck is the check a line shows: a failing one first, then a warning,
// then one that passes, and one with no current evidence last, since it has
// nothing to show; among equals the one closest to what users meet (a user
// entry, then a remote check, a local one, a report), an application check
// before a handshake, then the first in the topology. So it follows trouble,
// and holds still while all is well.
// Since is when its failing or policy run began, zero while it passes;
// BudgetMS its declared latency budget; Uptime its share up over each
// window its collector's rollups hold.
type LeadCheck struct {
	SeenCheck
	Observer Ref       `json:"observer"`
	Label    string    `json:"label"`
	Scope    string    `json:"scope"`
	Since    time.Time `json:"since,omitzero"`
	BudgetMS int       `json:"budget_ms,omitempty"`
	Uptime   []Uptime  `json:"uptime"`
	// Outcome, Stage and Code are its latest sample's while that is current.
	Outcome string `json:"outcome,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Code    string `json:"code,omitempty"`
}

// Uptime is a check's share of a window's measured time up, to a hundredth
// of a percent and never rounded up, so only all of it reads 100; nil when
// nothing was measured or the totals are incomplete. Measured and Seconds
// say how much of the window the share covers.
type Uptime struct {
	Window   string   `json:"window"`
	Up       *float64 `json:"up"`
	Measured int64    `json:"measured_seconds"`
	Seconds  int64    `json:"seconds"`
}

// Certificate is a certificate a check read on its latest sample: the host
// it answered for, the days left and when it expires, and the warning window
// the check declares.
type Certificate struct {
	Check    string    `json:"check"`
	Host     string    `json:"host"`
	Days     int       `json:"days_remaining"`
	NotAfter time.Time `json:"not_after,omitzero"`
	MinDays  int       `json:"min_days"`
}

// UptimeWindows are the windows a lead check's uptime is read over.
var UptimeWindows = []string{"24h", "7d", "30d"}

// Products is every product in the view, as the Services tab groups
// applications, in the same order.
func (v View) Products() ProductBoard { return v.ProductsWith(v.Fleet()) }

// ProductsWith builds the products from a fleet already built for this view.
func (v View) ProductsWith(f *Fleet) ProductBoard {
	f.indexServices()
	out := ProductBoard{Products: []Product{}}
	groups := v.ApplicationGroups()
	incidents := v.openIncidents()
	touched := map[string]bool{}
	for _, g := range groups {
		if g.ID == "" {
			out.Ungrouped += len(g.Rows)
			continue
		}
		if len(out.Products) == ProductsMax {
			continue
		}
		p := f.product(g, incidents)
		out.Products = append(out.Products, p)
		for _, c := range p.Checks {
			touched[c] = true
		}
		switch p.Health {
		case "up":
			out.Counts.Up++
		case "degraded":
			out.Counts.Warn++
		case "down":
			out.Counts.Down++
		default:
			out.Counts.Unknown++
		}
	}
	for _, i := range incidents {
		if slices.ContainsFunc(i.checks, func(c string) bool { return touched[c] }) {
			out.Incidents++
		}
	}
	return out
}

// ProductRoots are the services that are products' lines, in topology
// order: those in an application that no other service of the same
// application and environment depends on.
func ProductRoots(t model.Config) []string {
	used := map[string]bool{}
	scope := map[string]string{}
	for _, s := range t.Services {
		scope[s.ID] = s.Application + "/" + s.Environment
	}
	for _, s := range t.Services {
		for _, d := range s.DependsOn {
			if d != s.ID && scope[d] == scope[s.ID] {
				used[d] = true
			}
		}
	}
	out := []string{}
	for _, s := range t.Services {
		if s.Application != "" && !used[s.ID] {
			out = append(out, s.ID)
		}
	}
	return out
}

// incidentRef is an open or recovering incident and the checks it rests on.
type incidentRef struct {
	id     string
	since  time.Time
	checks []string
}

// openIncidents are the current incidents and the ledger's open and
// recovering records, once each. A cached view knows none.
func (v View) openIncidents() []incidentRef {
	if v.Cached || v.Draft {
		return nil
	}
	out := []incidentRef{}
	seen := map[string]bool{}
	for _, i := range v.Snapshot.Incidents {
		seen[i.ID] = true
		out = append(out, incidentRef{i.ID, i.Since, i.Checks})
	}
	for _, r := range v.Snapshot.Ledger {
		if (r.State == "open" || r.State == "recovering") && !seen[r.ID] {
			seen[r.ID] = true
			out = append(out, incidentRef{r.ID, r.Since, r.Checks})
		}
	}
	return out
}

func (f *Fleet) product(g ApplicationGroup, incidents []incidentRef) Product {
	p := Product{ID: g.ID, Name: g.Name, Environment: g.Environment, Health: g.Health, Lines: []ProductLine{}}
	for _, a := range f.topology.Applications {
		if a.ID == g.ID {
			p.Kind = a.Kind
			break
		}
	}
	if f.cached {
		p.Health, p.Note = "unknown", "cached"
	}
	members := map[string]bool{}
	for _, r := range g.Rows {
		members[r.ID] = true
		e := f.svcState[r.ID]
		switch {
		case len(e.Checks) == 0:
			p.Unmonitored++
		case f.state(e.Health) != "up":
			p.Attention++
		}
	}
	// Its checks: those of its services, in topology order.
	for _, x := range f.topology.Checks {
		if members[x.Service] {
			p.Checks = append(p.Checks, x.ID)
		}
	}
	// Its lines: the members nothing else in it depends on.
	used := map[string]bool{}
	for _, r := range g.Rows {
		for _, d := range f.serviceDefs[r.ID].DependsOn {
			if d != r.ID && members[d] {
				used[d] = true
			}
		}
	}
	for _, r := range g.Rows {
		if used[r.ID] {
			continue
		}
		if len(p.Lines) == ProductLines {
			p.MoreLines++
			continue
		}
		p.Lines = append(p.Lines, f.productLine(r))
	}
	for _, i := range incidents {
		if slices.ContainsFunc(i.checks, func(c string) bool { return slices.Contains(p.Checks, c) }) {
			p.Incidents++
			if p.Since.IsZero() || i.since.Before(p.Since) {
				p.Since = i.since
			}
		}
	}
	p.Certificate = f.certificate(p.Lines)
	return p
}

func (f *Fleet) productLine(r Row) ProductLine {
	sv := f.serviceDefs[r.ID]
	head, _ := f.ServiceHead(r.ID)
	l := ProductLine{ID: r.ID, Name: head.Name, Runtime: sv.Runtime, Health: head.Health, Note: head.Note, Reason: head.Reason, Assessment: head.Assessment.State(), Monitored: head.Monitored, Elsewhere: head.Elsewhere, Chain: []Layer{}, Row: r}
	if f.cached {
		l.Assessment = "unknown"
	}
	l.Lead, l.Checks = f.leadCheck(r.ID)
	l.Chain, l.MoreLayers = f.chain(sv, l.Lead)
	return l
}

// nodeHealth is a node's own health: up when it answered, unknown when
// nothing proves it, and unknown throughout a cached view.
func (f *Fleet) nodeHealth(id string) string {
	h := f.state(f.nodeState[id].Health)
	if h == "" {
		return "unknown"
	}
	return h
}

// chain is what a service rests on, in the order a request meets it: the
// edges and proxies in front of the address its lead check reaches (else of
// its first address with any), the node it runs on, then the services it
// depends on, nearest first, each once.
func (f *Fleet) chain(sv model.Service, lead *LeadCheck) ([]Layer, int) {
	out := []Layer{}
	var front []string
	if lead != nil {
		front = f.endpointDefs[lead.Endpoint].Via
	}
	if len(front) == 0 {
		for _, id := range sv.Endpoints {
			if via := f.endpointDefs[id].Via; len(via) > 0 {
				front = via
				break
			}
		}
	}
	node := func(role, id, runtime string) Layer {
		n := f.Nodes[id]
		return Layer{Role: role, ID: id, Name: f.Names[id], Kind: n.Kind, Provider: ProviderName(n.Provider), Runtime: runtime, Health: f.nodeHealth(id)}
	}
	for _, id := range front {
		if id != sv.Node {
			out = append(out, node("edge", id, ""))
		}
	}
	role := "host"
	if k := f.Nodes[sv.Node].Kind; k == "edge" || k == "platform" {
		role = "platform"
	}
	if sv.Node != "" {
		out = append(out, node(role, sv.Node, sv.Runtime))
	}
	more := 0
	seen := map[string]bool{sv.ID: true}
	queue := slices.Clone(sv.DependsOn)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		d, ok := f.serviceDefs[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		queue = append(queue, d.DependsOn...)
		if more > 0 || len(out) > 0 && countRole(out, "dependency") == ProductDependents {
			more++
			continue
		}
		n := f.Nodes[d.Node]
		h := f.state(f.svcState[id].Health)
		if h == "" {
			h = "unknown"
		}
		out = append(out, Layer{Role: "dependency", ID: id, Name: named(d.Name, id), Kind: n.Kind, Provider: ProviderName(n.Provider), Runtime: d.Runtime, Health: h})
	}
	return out, more
}

func countRole(layers []Layer, role string) int {
	n := 0
	for _, l := range layers {
		if l.Role == role {
			n++
		}
	}
	return n
}

// leadCheck is the check a service's line shows, with the service's checks
// counted by health; nil when no check measures it in this view. Machine
// resource checks never lead: they say how a host fares, not the service.
func (f *Fleet) leadCheck(id string) (*LeadCheck, Counts) {
	counts := Counts{}
	scopes := map[string]string{}
	for _, e := range f.assessments[id].Evidence {
		scopes[e.Check] = e.Scope
	}
	type candidate struct {
		x     SeenCheck
		scope string
		order int
	}
	list := []candidate{}
	for i, x := range f.topology.Checks {
		cs, ok := f.checkStates[x.ID]
		if x.Service != id || !ok {
			continue
		}
		seen := f.Check(x)
		switch seen.Health {
		case "up":
			counts.Up++
		case "down":
			counts.Down++
		case "degraded":
			counts.Warn++
		default:
			counts.Unknown++
		}
		if x.Kind == "host" || cs.ID == "" {
			continue
		}
		scope := scopes[x.ID]
		if scope == "" {
			scope = "remote"
		}
		list = append(list, candidate{seen, scope, i})
	}
	if len(list) == 0 {
		return nil, counts
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if leadRank(a.x.Health) != leadRank(b.x.Health) {
			return leadRank(a.x.Health) < leadRank(b.x.Health)
		}
		if scopeRank(a.scope) != scopeRank(b.scope) {
			return scopeRank(a.scope) < scopeRank(b.scope)
		}
		if kindRank(a.x.Kind) != kindRank(b.x.Kind) {
			return kindRank(a.x.Kind) < kindRank(b.x.Kind)
		}
		return a.order < b.order
	})
	c := list[0]
	def := c.x.Check
	o := f.observerDefs[def.Observer]
	lead := &LeadCheck{SeenCheck: c.x, Observer: Ref{def.Observer, named(o.Name, def.Observer)}, Label: f.observerPlace(def.Observer), Scope: c.scope, BudgetMS: def.MaxDurationMS, Uptime: []Uptime{}}
	cs := f.checkStates[def.ID]
	if !f.cached && (c.x.Health == "down" || c.x.Health == "degraded") {
		lead.Since = cs.Since
	}
	if !f.cached && cs.Freshness == "fresh" && cs.Last != nil {
		lead.Outcome, lead.Stage, lead.Code = cs.Last.Outcome, cs.Last.Stage, cs.Last.Code
	}
	lead.Uptime = Uptimes(cs.Availability)
	return lead, counts
}

// leadRank orders checks for a line to show: failures, warnings, passes,
// then those with nothing current to show, as ServiceRank orders services.
func leadRank(health string) int {
	switch health {
	case "down":
		return 0
	case "degraded":
		return 1
	case "up":
		return 2
	}
	return 3
}

// scopeRank orders where a check measures from, nearest what users meet
// first.
func scopeRank(scope string) int {
	switch scope {
	case "user_entry":
		return 0
	case "remote":
		return 1
	case "local":
		return 2
	}
	return 3
}

// kindRank puts an application's answer before a handshake.
func kindRank(kind string) int {
	switch kind {
	case "http":
		return 0
	case "tls":
		return 1
	case "tcp":
		return 2
	}
	return 3
}

// Uptimes is a check's share up over each window its rollups hold, in
// UptimeWindows' order.
func Uptimes(list []model.Availability) []Uptime {
	out := []Uptime{}
	for _, w := range UptimeWindows {
		r := RollupWindow(list, w)
		if r == nil {
			continue
		}
		u := Uptime{Window: w, Measured: r.Measured(), Seconds: r.Seconds}
		if m := r.Measured(); m > 0 && r.Unreconciled == 0 {
			share := math.Floor(10000*float64(r.Up)/float64(m)) / 100
			u.Up = &share
		}
		out = append(out, u)
	}
	return out
}

// UptimeShare writes a share up as a line shows it: "99.98%", "100%", or
// "99.71% · 3d" when the window holds less than all of it measured.
func UptimeShare(u Uptime) string {
	if u.Up == nil {
		return ""
	}
	share := strconv.FormatFloat(*u.Up, 'f', 2, 64)
	share = strings.TrimSuffix(strings.TrimSuffix(share, "0"), ".0")
	share = strings.TrimSuffix(share, ".")
	out := share + "%"
	if held := time.Duration(u.Measured) * time.Second; u.Seconds > 0 && u.Measured < u.Seconds {
		if held >= 48*time.Hour {
			out += " · " + strconv.Itoa(int(held.Hours()/24)) + "d"
		} else {
			out += " · " + heldSpan(held)
		}
	}
	return out
}

func heldSpan(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	return strconv.Itoa(int(d.Hours())) + "h"
}

// LongestUptime is the longest window a lead holds a share for, or nil.
func LongestUptime(list []Uptime) *Uptime {
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Up != nil {
			u := list[i]
			return &u
		}
	}
	return nil
}

// certificate is the soonest-expiring certificate the lines' checks read on
// their current samples; nil when none reads one. Only a check that declares
// tls_min_days records it.
func (f *Fleet) certificate(lines []ProductLine) *Certificate {
	var out *Certificate
	for _, l := range lines {
		for _, x := range f.topology.Checks {
			if x.Service != l.ID || x.Kind == "host" {
				continue
			}
			cs, ok := f.checkStates[x.ID]
			if !ok || f.cached || cs.Freshness != "fresh" || cs.Last == nil {
				continue
			}
			days, err := strconv.Atoi(cs.Last.Facts["tls_days_remaining"])
			if err != nil {
				continue
			}
			if out != nil && days >= out.Days {
				continue
			}
			c := &Certificate{Check: x.ID, Host: f.checkHost(x), Days: days, MinDays: x.TLSMinDays}
			if t, err := time.Parse(time.RFC3339, cs.Last.Facts["tls_not_after"]); err == nil {
				c.NotAfter = t.UTC()
			}
			out = c
		}
	}
	return out
}

// checkHost is the host name a check's request names: its target's, else
// its endpoint's address's. "" when neither names one.
func (f *Fleet) checkHost(x model.Check) string {
	target := x.Target
	if target == "" {
		target = f.endpointDefs[x.Endpoint].Address
	}
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		return u.Hostname()
	}
	if h, _, err := net.SplitHostPort(target); err == nil {
		return h
	}
	return ""
}

// ProductLineName is a line's name without its product's name in front:
// "API" for "Habitly API" in Habitly. A name that is only the product's, or
// that does not begin with it, is kept whole.
func ProductLineName(product, line string) string {
	if product == "" || len(line) <= len(product) || !strings.EqualFold(line[:len(product)], product) {
		return line
	}
	// The product's name is a word of the line's, not the start of one:
	// "Habit" names no part of "Habitly API".
	rest := line[len(product):]
	if r, _ := utf8.DecodeRuneInString(rest); !strings.ContainsRune(lineSeparators, r) {
		return line
	}
	if rest = strings.TrimLeft(rest, lineSeparators); rest == "" {
		return line
	}
	return rest
}

// lineSeparators are what may stand between a product's name and a line's.
const lineSeparators = " ·:-–—/|"

// NeedsLook is whether a line asks for a look: its service is not simply
// up, or its lead check fails or warns.
func (l ProductLine) NeedsLook() bool {
	return l.Health != "up" || l.Note != "" || l.Lead != nil && (l.Lead.Health == "down" || l.Lead.Health == "degraded")
}

// LineState is a product line's state word as the board says it: its
// service's, as the Services tab's state column has it ("affected",
// "cached", ACCESS FAIL…), but where that says no more than up, the line's
// own health: WARN for a degraded service whose availability is
// established. "" for a line simply up.
func LineState(l ProductLine) string {
	r := l.Row
	r.Health, r.Note = l.Health, l.Note
	w := State(r)
	if r.Assessment != nil && w == r.Assessment.Label() && r.Assessment.State() == "up" {
		w = ""
	}
	switch {
	case w != "":
		return w
	case l.Health == "down":
		return "DOWN"
	case l.Health == "degraded":
		return "WARN"
	case l.Health == "up":
		return ""
	}
	return "unknown"
}

// LookWord is the word a line that asks for a look leads with: its
// LineState, or, for a line up whose lead check fails or warns, the
// lead's health.
func LookWord(l ProductLine) string {
	if w := LineState(l); w != "" {
		return w
	}
	if l.Lead != nil {
		return strings.ToUpper(HealthWord(l.Lead.Health))
	}
	return "UP"
}

// Look is what a line that asks for a look says of itself, each part ""
// when it has none: how long its lead check has failed ("for 8m"), what the
// lead says, or why the line is not up when the lead says nothing, where the
// lead was seen from ("seen from Lisbon"), and the incidents that rest on
// its product ("⚑ 1 incident").
type Look struct {
	Since, Says, Seen, Incidents string
}

// ProductLook is what line l of product p says of itself at now.
func ProductLook(now time.Time, p Product, l ProductLine) Look {
	out := Look{}
	if x := l.Lead; x != nil && !x.Since.IsZero() && !x.Since.After(now) {
		out.Since = "for " + Duration(now.Sub(x.Since))
	}
	switch {
	case l.Lead != nil && LeadSays(*l.Lead) != "":
		out.Says = LeadSays(*l.Lead)
		if l.Lead.Label != "" {
			out.Seen = "seen from " + l.Lead.Label
		}
	case !l.Monitored:
		out.Says = "no check measures it"
	case l.Reason != "":
		out.Says = l.Reason
	}
	if p.Incidents > 0 {
		out.Incidents = "⚑ " + Plural(p.Incidents, "incident")
	}
	return out
}

// Parts are the look's parts that say something, in order.
func (k Look) Parts() []string {
	out := []string{}
	for _, s := range []string{k.Since, k.Says, k.Seen, k.Incidents} {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// LeadSays is what a lead check's latest sample said when it did not simply
// pass, in a few words: "HTTP 521 from Cloudflare LIS", "connect: timeout",
// "certificate expires in 9 days", "slower than its 800 ms budget", or what
// the edge said in the application's place. "" while it passes or says
// nothing current. Its words are the evidence's; it concludes nothing.
func LeadSays(l LeadCheck) string {
	f := l.Facts
	edge := ProviderName(f["edge_provider"])
	if pop := f["edge_pop"]; edge != "" && model.IsColo(pop) {
		edge += " " + pop
	}
	switch {
	case l.Outcome == "":
		return ""
	case f["edge_mitigation"] == "challenge":
		return "a challenge page from " + named(edge, "the edge") + "; the origin was not asked"
	case f["edge_access"] == "login_redirect":
		return "an Access login from " + named(edge, "the edge") + "; the origin was not asked"
	case l.Outcome == "ok":
		return ""
	case l.Code == "certificate_expiring":
		if d, err := strconv.Atoi(f["tls_days_remaining"]); err == nil {
			return "certificate expires in " + Plural(d, "day")
		}
		return "certificate inside its warning window"
	case l.Code == "latency_exceeded" && l.Check.MaxDurationMS > 0:
		return "slower than its " + itoa(l.Check.MaxDurationMS) + " ms budget"
	case l.Code == "pop_mismatch" && model.IsColo(f["edge_pop"]):
		return "answered from " + f["edge_pop"] + ", outside its declared colos"
	case f["http_status"] != "" && l.Outcome == "fail":
		if edge != "" {
			return "HTTP " + f["http_status"] + " from " + edge
		}
		return "HTTP " + f["http_status"]
	}
	words := strings.ReplaceAll(l.Code, "_", " ")
	if stage := strings.ReplaceAll(l.Stage, "_", " "); stage != "" && words != "" {
		words = stage + ": " + words
	}
	if l.Outcome == "error" {
		return "monitor error" + map[bool]string{true: ", " + words, false: ""}[words != ""]
	}
	return words
}

// LayerLabel names a layer as a chain says it: an edge by its provider, a
// platform by its provider and the service's runtime ("Cloudflare workers"),
// a machine and a dependency by their names.
func LayerLabel(l Layer) string {
	switch l.Role {
	case "edge":
		return named(l.Provider, l.Name)
	case "platform":
		return strings.TrimSpace(named(l.Provider, l.Name) + " " + l.Runtime)
	}
	return l.Name
}

// ProductDetails are the evidence lines of a product's line, as the Overview
// in Products mode shows them beside the board: the product, the line's state
// and why, what it rests on, and its lead check, then the service's own
// evidence as the Services tab gives it.
func ProductDetails(now time.Time, p Product, l ProductLine) []string {
	kind := ""
	if p.Kind != "" {
		kind = " · " + p.Kind
	}
	out := []string{"Product: " + ScopeLabel(p.Name, p.Environment) + kind, "Product health: " + HealthWord(p.Health) + " · " + Plural(p.Attention, "service") + " needing attention"}
	if p.Incidents > 0 {
		out = append(out, "Incidents: "+itoa(p.Incidents)+" open since "+Stamp(now, p.Since))
	}
	if c := p.Certificate; c != nil {
		line := c.Host + " · " + Plural(c.Days, "day") + " left"
		if !c.NotAfter.IsZero() {
			line += " · until " + c.NotAfter.Local().Format("2006-01-02")
		}
		if c.MinDays > 0 {
			line += " · warns within " + Plural(c.MinDays, "day")
		}
		out = append(out, "Certificate: "+line)
	}
	out = append(out, "", "Line: "+l.Name+map[bool]string{true: " · " + l.Runtime, false: ""}[l.Runtime != ""], "Reason: "+l.Reason)
	out = append(out, "", "Rests on:")
	for _, c := range l.Chain {
		parts := []string{c.Name}
		if c.Provider != "" && c.Provider != c.Name {
			parts = append(parts, c.Provider)
		}
		if c.Runtime != "" {
			parts = append(parts, c.Runtime)
		}
		out = append(out, label(c.Role)+": "+strings.Join(parts, " · ")+" · "+HealthWord(c.Health))
	}
	if l.MoreLayers > 0 {
		out = append(out, "More: "+Plural(l.MoreLayers, "dependency"))
	}
	if len(l.Chain) == 0 {
		out = append(out, "Nothing declared: no edge, host or dependency.")
	}
	if x := l.Lead; x != nil {
		out = append(out, "", "Lead check:", "Check: "+x.Name+" ("+x.ID+")", "Seen from: "+x.Observer.Name+" · "+x.Label+" · "+strings.ReplaceAll(x.Scope, "_", " "), "Health: "+x.Health, "Freshness: "+x.Freshness)
		if x.Latency != nil && x.Freshness == "fresh" {
			lat := "p50 " + ms(x.Latency.P50) + " · p95 " + ms(x.Latency.P95)
			if x.BudgetMS > 0 {
				lat += " · budget " + itoa(x.BudgetMS) + " ms"
			}
			out = append(out, "Latency: "+lat)
		}
		up := []string{}
		for _, u := range x.Uptime {
			if s := UptimeShare(u); s != "" {
				up = append(up, u.Window+" "+s)
			}
		}
		if len(up) > 0 && x.Freshness == "fresh" {
			out = append(out, "Up: "+strings.Join(up, " · "))
		}
		if !x.Since.IsZero() {
			out = append(out, "Failing since: "+Stamp(now, x.Since))
		}
		if says := LeadSays(*x); says != "" {
			out = append(out, "Says: "+says)
		}
	} else if !l.Monitored {
		out = append(out, "", "No check measures this line.")
	}
	return append(append(out, ""), l.Row.Details...)
}

// label is a role or key with a capital, as the evidence's labels read.
func label(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
