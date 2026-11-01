package monitor

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// The service cards, as the Services tab draws them: service.card for one
// service, services.list to choose them by.

type RefValue struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type HostValue struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
}
type ServiceHostValue struct {
	HostValue
	Where  string `json:"where"`
	Health string `json:"health"`
}
type LatencyValue struct {
	P50 int64 `json:"p50_ms"`
	P95 int64 `json:"p95_ms"`
}
type SlotValue struct {
	Outcome   string `json:"outcome"`
	ElapsedMS *int64 `json:"elapsed_ms,omitempty"`
}
type SamplesValue struct {
	Interval int         `json:"interval_seconds"`
	Slots    []SlotValue `json:"slots"`
}
type SeenCheckValue struct {
	Diagnostic *CheckDiagnostic `json:"diagnostic,omitempty"`
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Kind       string           `json:"kind"`
	Endpoint   string           `json:"endpoint"`
	Health     string           `json:"health"`
	Freshness  string           `json:"freshness"`
	SampledAt  *time.Time       `json:"sampled_at"`
	ValidUntil *time.Time       `json:"valid_until"`
	Latency    *LatencyValue    `json:"latency"`
	Up24h      *float64         `json:"up_24h"`
	FirstByte  string           `json:"first_byte"`
	Colo       *string          `json:"colo"`
	Samples    SamplesValue     `json:"samples"`
}
type SeenFromValue struct {
	Observer RefValue         `json:"observer"`
	Label    string           `json:"label"`
	Health   string           `json:"health"`
	Checks   []SeenCheckValue `json:"checks"`
}
type ViaValue struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
}
type EndpointValue struct {
	ID      string     `json:"id"`
	Network string     `json:"network"`
	Address string     `json:"address"`
	Health  string     `json:"health"`
	Via     []ViaValue `json:"via"`
}
type AnsweredValue struct {
	Pops  []string `json:"pops"`
	Cache []string `json:"cache"`
}
type DependencyValue struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Health    string `json:"health"`
	Affected  bool   `json:"affected"`
	Monitored bool   `json:"monitored"`
	Host      string `json:"host"`
}
type ServiceCard struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Application RefValue          `json:"application"`
	Environment string            `json:"environment"`
	Runtime     string            `json:"runtime"`
	Health      string            `json:"health"`
	Note        string            `json:"note"`
	Evidence    string            `json:"evidence"`
	Reason      string            `json:"reason"`
	Monitored   bool              `json:"monitored"`
	Elsewhere   bool              `json:"elsewhere"`
	Host        ServiceHostValue  `json:"host"`
	SeenFrom    []SeenFromValue   `json:"seen_from"`
	Endpoints   []EndpointValue   `json:"endpoints"`
	Answered    *AnsweredValue    `json:"answered_from"`
	DependsOn   []DependencyValue `json:"depends_on"`
	UsedBy      []DependencyValue `json:"used_by"`
	Runbook     string            `json:"runbook"`
	Memo        string            `json:"memo"`
}
type ServiceListEntry struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Application string    `json:"application"`
	Health      string    `json:"health"`
	Note        string    `json:"note"`
	Monitored   bool      `json:"monitored"`
	Elsewhere   bool      `json:"elsewhere"`
	Host        HostValue `json:"host"`
}

func hostValue(h view.ServiceHost) HostValue {
	return HostValue{ID: h.ID, Name: h.Name, Kind: h.Kind, Provider: h.Provider}
}

func dependencies(refs []view.ServiceRef) []DependencyValue {
	out := []DependencyValue{}
	for _, r := range refs {
		out = append(out, DependencyValue{ID: r.ID, Name: r.Name, Health: r.Health, Affected: r.Affected, Monitored: r.Monitored, Host: r.Host})
	}
	return out
}

// service is the card of the service id with its row's clocks: the
// newest sample and the earliest deadline of the service's state, its
// host's and its dependencies'. Each check's readings keep their own.
func (p *projection) service(id string) (ServiceCard, time.Time, time.Time, bool) {
	fleet := p.serverFleet()
	c, ok := fleet.ServiceCard(id)
	if !ok {
		return ServiceCard{}, time.Time{}, time.Time{}, false
	}
	ids, seen := map[string]bool{}, map[string]bool{}
	p.serviceEvidence(id, ids, seen)
	for _, check := range p.nodeChecks[c.Host.ID] {
		ids[check] = true
	}
	for _, d := range c.DependsOn {
		p.serviceEvidence(d.ID, ids, seen)
	}
	sampled, until := p.evidence().times(ids, false)
	out := ServiceCard{ID: c.ID, Name: c.Name, Application: RefValue(c.Application), Environment: c.Environment, Runtime: c.Runtime, Health: c.Health, Note: c.Note,
		Evidence: c.Evidence, Reason: c.Reason, Monitored: c.Monitored, Elsewhere: c.Elsewhere, Host: ServiceHostValue{HostValue: hostValue(c.Host), Where: c.Host.Where, Health: c.Host.Health},
		SeenFrom: []SeenFromValue{}, Endpoints: []EndpointValue{}, DependsOn: dependencies(c.DependsOn), UsedBy: dependencies(c.UsedBy), Runbook: c.Runbook, Memo: c.Memo}
	for _, s := range c.SeenFrom {
		from := SeenFromValue{Observer: RefValue(s.Observer), Label: s.Label, Health: s.Health, Checks: []SeenCheckValue{}}
		for _, x := range s.Checks {
			from.Checks = append(from.Checks, p.seenCheck(x, p.samples[x.ID]))
		}
		out.SeenFrom = append(out.SeenFrom, from)
	}
	for _, ep := range c.Endpoints {
		e := EndpointValue{ID: ep.ID, Network: ep.Network, Address: ep.Address, Health: ep.Health, Via: []ViaValue{}}
		for _, v := range ep.Via {
			n := fleet.Nodes[v.ID]
			e.Via = append(e.Via, ViaValue{ID: v.ID, Name: named(v.ID, v.Name), Kind: n.Kind, Provider: view.ProviderName(n.Provider)})
		}
		out.Endpoints = append(out.Endpoints, e)
	}
	if a := c.Answered; a != nil {
		out.Answered = &AnsweredValue{Pops: a.Pops, Cache: a.Cache}
	}
	ageServiceCard(&out, p.v.Now)
	return out, sampled, until, true
}

// seenCheck is a check as a service card's value says it, with its own
// deadline and its recent samples laid out as the TUI's spark lays them;
// none when its source holds none.
func (p *projection) seenCheck(x view.SeenCheck, samples []model.Observation) SeenCheckValue {
	out := SeenCheckValue{ID: x.ID, Name: x.Name, Kind: x.Kind, Endpoint: x.Endpoint, Health: x.Health, Freshness: x.Freshness, SampledAt: stamp(x.SampledAt), Up24h: x.Up24h, FirstByte: x.FirstByte}
	if clock, ok := p.evidence().checks[x.ID]; ok {
		out.ValidUntil = stamp(clock.until)
	}
	if l := x.Latency; l != nil {
		out.Latency = &LatencyValue{P50: l.P50, P95: l.P95}
	}
	out.Diagnostic = p.checkDiagnostic(x.ID)
	colo := x.Colo
	out.Colo = &colo
	strip := view.NewStrip(x.Check, samples, p.v.Now, CardSamples)
	out.Samples.Interval = strip.Interval
	if len(samples) > 0 {
		out.Samples.Slots = []SlotValue{}
		for _, sl := range strip.Slots {
			slot := SlotValue{Outcome: sl.Outcome}
			if sl.Outcome == "ok" {
				ms := sl.ElapsedMS
				slot.ElapsedMS = &ms
			}
			out.Samples.Slots = append(out.Samples.Slots, slot)
		}
	}
	return out
}

// serviceList is every service in the Services tab's order, with the
// row's clocks: those of the listed services' states.
func (p *projection) serviceList() ([]ServiceListEntry, time.Time, time.Time) {
	fleet := p.serverFleet()
	out := []ServiceListEntry{}
	ids, seen := map[string]bool{}, map[string]bool{}
	for _, r := range p.v.GroupedList(view.Services, view.GroupOptions{}) {
		if r.Kind != "service" {
			continue
		}
		c, ok := fleet.ServiceHead(r.ID)
		if !ok {
			continue
		}
		out = append(out, ServiceListEntry{ID: c.ID, Name: c.Name, Application: c.Application.Name, Health: c.Health, Note: c.Note, Monitored: c.Monitored, Elsewhere: c.Elsewhere, Host: hostValue(c.Host)})
		p.serviceEvidence(c.ID, ids, seen)
		if len(out) == 256 {
			break
		}
	}
	sampled, until := p.evidence().times(ids, false)
	return out, sampled, until
}
