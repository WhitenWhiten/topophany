package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func serviceTemplate(services ...string) Template {
	t := Template{Schema: 1, Title: "Services", Rows: []Row{{ID: "services", Metric: "services.list", Visual: "list"}}}
	t.Bar.Row = "services"
	for i, id := range services {
		t.Rows = append(t.Rows, Row{ID: fmt.Sprintf("card%d", i), Metric: "service.card", Visual: "card", Select: Selector{Service: id}})
	}
	return t
}

func previewSim(t *testing.T, scenario string, now time.Time) *preview.Sim {
	t.Helper()
	c, err := preview.Preset("en")
	if err != nil {
		t.Fatal(err)
	}
	sim := preview.New(c, now, "en")
	if scenario != "" {
		sim.SetScenario(slices.Index(sim.ScenarioIDs(), scenario), now.Add(-30*time.Minute))
	}
	return sim
}

// The list is the Services tab's, in its order, and every entry's card
// says the same of it.
func TestServiceListMatchesServicesTab(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, name := range previewSim(t, "", now).ScenarioIDs() {
		t.Run(name, func(t *testing.T) {
			snap, err := previewSim(t, name, now).Snapshot("", now)
			if err != nil {
				return // Collector outage has no snapshot to list.
			}
			v := view.View{Snapshot: snap, Now: now}
			rows := []view.Row{}
			for _, r := range v.GroupedList(view.Services, view.GroupOptions{}) {
				if r.Kind == "service" {
					rows = append(rows, r)
				}
			}
			ids := []string{}
			for _, r := range rows {
				ids = append(ids, r.ID)
			}
			p := Build(Config{Source: "preview", Template: serviceTemplate(ids...)}, Input{Snapshot: snap}, now)
			list := p.Rows[0].Value.([]ServiceListEntry)
			if len(list) != len(rows) || p.Rows[0].Text != fmt.Sprintf("%d services", len(rows)) {
				t.Fatalf("list has %d entries, Services tab has %d", len(list), len(rows))
			}
			for i, entry := range list {
				row := rows[i]
				if entry.ID != row.ID || entry.Name != row.Title || entry.Health != row.Health || entry.Note != row.Note {
					t.Fatalf("list differs from Services tab: %+v / %+v", entry, row)
				}
				card, ok := p.Rows[i+1].Value.(ServiceCard)
				if !ok {
					t.Fatal("listed service has no readable card", entry.ID, p.Rows[i+1])
				}
				if card.ID != entry.ID || card.Name != entry.Name || card.Application.Name != entry.Application || card.Health != entry.Health || card.Note != entry.Note || card.Monitored != entry.Monitored || card.Host.HostValue != entry.Host {
					t.Fatalf("list differs from card: %+v / %+v", entry, card)
				}
				if p.Rows[i+1].Health != card.Health || p.Rows[i+1].Text != card.Name {
					t.Fatal("card row's state is not the card's", p.Rows[i+1])
				}
			}
		})
	}
}

// A card says what the Services tab's card says, each check with its own
// deadline, and on the wire every reading a consumer may withhold is there
// to withhold: a colo as "" rather than absent, slots as null when topo
// holds no samples.
func TestServiceCardValue(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	sim := previewSim(t, "", now)
	snap, err := sim.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Source: "preview", Template: serviceTemplate("blog", "nope")}
	p := Build(cfg, Input{Snapshot: snap}, now)
	card := p.Rows[1].Value.(ServiceCard)
	if card.Name != "Main Site (Caddy + Blog)" || card.Application != (RefValue{"main-site", "Main site"}) || card.Health != "up" || !card.Monitored {
		t.Fatalf("%+v", card)
	}
	if card.Host.ID != "app" || card.Host.Provider != "Oracle" || card.Host.Where != "New York" || card.Host.Health != "up" {
		t.Fatalf("%+v", card.Host)
	}
	if len(card.SeenFrom) != 3 || len(card.Endpoints) != 2 || card.Answered == nil || len(card.DependsOn) != 1 || card.DependsOn[0].ID != "postgres" {
		t.Fatalf("%+v", card)
	}
	if via := card.Endpoints[0].Via; len(via) != 1 || via[0] != (ViaValue{"cf", "Cloudflare", "edge", "Cloudflare"}) {
		t.Fatalf("%+v", card.Endpoints[0])
	}
	r := p.Rows[1]
	for _, s := range card.SeenFrom {
		for _, x := range s.Checks {
			if x.ValidUntil == nil || x.ValidUntil.Before(r.ValidUntil) || x.Latency == nil || x.Up24h == nil || x.Colo == nil || x.Samples.Interval <= 0 || x.Samples.Slots != nil {
				t.Fatalf("%+v", x)
			}
		}
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	check := wire["seen_from"].([]any)[0].(map[string]any)["checks"].([]any)[0].(map[string]any)
	if _, ok := check["colo"].(string); !ok {
		t.Fatalf("colo is not a string: %s", raw)
	}
	if samples := check["samples"].(map[string]any); samples["slots"] != nil || samples["interval_seconds"] == nil {
		t.Fatalf("samples without any held: %v", samples)
	}
	if m := p.Rows[2]; m.Value != nil || m.Reason != "entity_missing" || m.Health != "unknown" {
		t.Fatal("an undeclared service has a card", m)
	}

	// With the history read, every check shows its recent samples: at
	// most a card's worth, a passing one with what it took.
	book := NewSampleBook(cfg.Template)
	in := Input{Snapshot: snap, Samples: book.Fill(context.Background(), snap, "", -1, func(_ context.Context, id string) ([]model.Observation, error) {
		return sim.History(id, CardSamples, now)
	})}
	card = Build(cfg, in, now).Rows[1].Value.(ServiceCard)
	for _, s := range card.SeenFrom {
		for _, x := range s.Checks {
			if len(x.Samples.Slots) == 0 || len(x.Samples.Slots) > CardSamples {
				t.Fatalf("%s: %d slots", x.ID, len(x.Samples.Slots))
			}
			for _, sl := range x.Samples.Slots {
				if (sl.ElapsedMS != nil) != (sl.Outcome == "ok") {
					t.Fatalf("%s: %+v", x.ID, sl)
				}
			}
		}
	}
	other := Build(cfg, in, now).Clone()
	other.Rows[1].Value.(ServiceCard).SeenFrom[0].Checks[0].Samples.Slots[0].Outcome = "edited"
	if card.SeenFrom[0].Checks[0].Samples.Slots[0].Outcome == "edited" {
		t.Fatal("clone shares a card's samples")
	}
}

// Each check's readings expire at its own deadline, which a renewed lease
// never moves; the card and the check's identity stay. A file reader,
// which decodes the card as a map, applies the same.
func TestServiceCardChecksExpireOneByOne(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snap := model.Snapshot{GeneratedAt: now, Topology: model.Config{
		Nodes:     []model.Node{{ID: "host", Kind: "host"}},
		Services:  []model.Service{{ID: "svc", Node: "host"}},
		Observers: []model.Observer{{ID: "a"}, {ID: "b"}},
	}}
	for _, spec := range []struct {
		id, observer string
		ttl          int
	}{{"short", "a", 3}, {"long", "b", 10}} {
		snap.Topology.Checks = append(snap.Topology.Checks, model.Check{ID: spec.id, Kind: "http", Observer: spec.observer, Service: "svc", IntervalSeconds: 1, StaleSeconds: spec.ttl})
		last := &model.Observation{SampledAt: now.Add(-time.Second), Outcome: "ok", DurationMS: 20, Facts: map[string]string{"edge_pop": "SJC", "edge_provider": "cloudflare"}}
		snap.Checks = append(snap.Checks, model.CheckState{ID: spec.id, Observer: spec.observer, Health: "up", Freshness: "fresh", Last: last,
			Availability: []model.Availability{{Window: "24h", Rollup: model.Rollup{Seconds: 100, Up: 100, Latency: &model.Latency{Samples: 10, P50: 20, P95: 30}}}}})
	}
	snap.Nodes = []model.EntityState{{ID: "host", Health: "up"}}
	snap.Services = []model.EntityState{{ID: "svc", Health: "up", Checks: []string{"short", "long"}}}
	cfg := Config{Source: "client", Template: serviceTemplate("svc")}
	in := Input{Snapshot: snap, Samples: map[string][]model.Observation{"short": {*snap.Checks[0].Last}, "long": {*snap.Checks[1].Last}}}
	p := Build(cfg, in, now)
	r := p.Rows[1]
	if r.Value == nil || !r.ValidUntil.Equal(now.Add(2*time.Second)) {
		t.Fatal("the row is not bound by the service's evidence", r)
	}
	// As a file older than its row deadline rule might carry it.
	p.Rows[1].ValidUntil = now.Add(time.Minute)
	p.PublisherExpiresAt = now.Add(time.Minute)
	raw, err := MarshalPanel(p)
	if err != nil {
		t.Fatal(err)
	}
	var file Panel
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, from := range []Panel{p, file} {
		card := Current(from, now.Add(5*time.Second)).Rows[1].Value.(ServiceCard)
		got := map[string]SeenCheckValue{}
		for _, s := range card.SeenFrom {
			for _, x := range s.Checks {
				got[x.ID] = x
			}
		}
		short, long := got["short"], got["long"]
		if short.Health != "unknown" || short.Freshness != "stale" || short.Latency != nil || short.Up24h != nil || short.Colo != nil || short.Samples.Slots != nil || short.SampledAt == nil || short.ValidUntil == nil || short.Samples.Interval != 1 {
			t.Fatalf("an expired check keeps its readings: %+v", short)
		}
		if long.Health != "up" || long.Latency == nil || long.Colo == nil || *long.Colo != "SJC" || len(long.Samples.Slots) == 0 {
			t.Fatalf("one check's deadline expired another: %+v", long)
		}
	}
	if r := Build(cfg, in, now.Add(5*time.Second)).Rows[1]; r.Value != nil || r.Freshness != "stale" {
		t.Fatal("the producer revived an expired judgment", r)
	}
}

func TestServiceRowsValidation(t *testing.T) {
	for _, source := range []string{"client", "collector", "preview"} {
		if err := serviceTemplate("svc").Validate(source); err != nil {
			t.Fatal(source, err)
		}
	}
	if err := serviceTemplate("svc").Validate("worker"); err == nil {
		t.Fatal("worker accepted service cards")
	}
	for _, change := range []func(*Template){
		func(t *Template) { t.Rows[1].Select.Service = "" },
		func(t *Template) { t.Rows[1].Select.Service = "Not an ID" },
		func(t *Template) { t.Rows[1].Select = Selector{Node: "svc"} },
		func(t *Template) { t.Rows[1].Visual = "list" },
		func(t *Template) { t.Rows[0].Select.Service = "svc" },
		func(t *Template) {
			t.Rows = append(t.Rows, Row{ID: "server", Metric: "server.card", Visual: "card", Select: Selector{Node: "host", Service: "svc"}})
		},
	} {
		bad := serviceTemplate("svc")
		change(&bad)
		if err := bad.Validate("client"); err == nil {
			t.Fatal("invalid service row accepted", bad.Rows)
		}
	}
}

// The book reads each card's checks' history once, extends it from each
// snapshot, keeps one measurement and a bounded tail, and forgets what
// came from another source or another card.
func TestSampleBook(t *testing.T) {
	var none *SampleBook
	if NewSampleBook(serverListTemplate()) != nil || none.Unread(model.Snapshot{}, "") != nil || none.Samples(model.Snapshot{}, "") != nil {
		t.Fatal("a template without service cards keeps samples")
	}
	none.Read("x", nil)
	now := time.Now().UTC()
	at := func(s int, contract string) model.Observation {
		return model.Observation{SampledAt: now.Add(time.Duration(s) * time.Second), Outcome: "ok", Contract: contract, DurationMS: int64(s)}
	}
	snap := model.Snapshot{Services: []model.EntityState{{ID: "svc", Checks: []string{"a", "b"}}, {ID: "other", Checks: []string{"c"}}},
		Checks: []model.CheckState{{ID: "a", Last: ptr(at(0, "k"))}, {ID: "c", Last: ptr(at(0, "k"))}}}
	b := NewSampleBook(serviceTemplate("svc", "svc"))
	if got := b.Unread(snap, "one"); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
	reads := 0
	samples := b.Fill(context.Background(), snap, "one", 1, func(_ context.Context, id string) ([]model.Observation, error) {
		reads++
		return []model.Observation{at(-2, "k"), at(-1, "k"), at(-3, "old")}, nil
	})
	if reads != 1 || len(samples["a"]) != 3 || samples["a"][0].DurationMS != -2 || samples["c"] != nil || samples["b"] != nil {
		t.Fatal("budget, order, contract or scope", reads, samples)
	}
	if got := b.Unread(snap, "one"); !slices.Equal(got, []string{"b"}) {
		t.Fatal("a read check is read again", got)
	}
	b.Fill(context.Background(), snap, "one", -1, func(context.Context, string) ([]model.Observation, error) { return nil, errors.New("down") })
	if got := b.Unread(snap, "one"); len(got) != 0 {
		t.Fatal("a failed read is retried", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := b.Fill(ctx, snap, "two", -1, func(context.Context, string) ([]model.Observation, error) { return nil, nil }); len(got["a"]) != 1 || len(b.Unread(snap, "two")) != 2 {
		t.Fatal("another source's samples are kept, or a cut-short read counts", got)
	}
	for i := 1; i <= 3*CardSamples; i++ {
		snap.Checks[0].Last = ptr(at(i, "k"))
		samples = b.Samples(snap, "two")
	}
	if a := samples["a"]; len(a) != 2*CardSamples || a[len(a)-1].DurationMS != 3*CardSamples {
		t.Fatal("tail", len(a))
	}
	snap.Checks[0].Last = ptr(at(1000, "new"))
	if a := b.Samples(snap, "two")["a"]; len(a) != 1 {
		t.Fatal("a new measurement joins the old", len(a))
	}
	snap.Services = snap.Services[1:]
	if got := b.Samples(snap, "two"); len(got) != 0 {
		t.Fatal("a service no longer in the snapshot keeps samples", got)
	}
}

func ptr[T any](v T) *T { return &v }

// Scoped to one observer, a service only others measure is elsewhere on
// its card and in the list, so a consumer can say it is not seen from here.
func TestScopedServiceIsElsewhere(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	snap, err := previewSim(t, "", now).Snapshot("obs-mac", now)
	if err != nil {
		t.Fatal(err)
	}
	p := Build(Config{Source: "preview", Template: serviceTemplate("vless-transit")}, Input{Snapshot: snap}, now)
	card := p.Rows[1].Value.(ServiceCard)
	i := slices.IndexFunc(p.Rows[0].Value.([]ServiceListEntry), func(e ServiceListEntry) bool { return e.ID == "vless-transit" })
	if card.Monitored || !card.Elsewhere || i < 0 || !p.Rows[0].Value.([]ServiceListEntry)[i].Elsewhere {
		t.Fatalf("%+v", card)
	}
}

// assessedSnapshot is a service with a passing remote path check and a
// failing local application check, each with its own deadline.
func assessedSnapshot(now time.Time) model.Snapshot {
	last := func(outcome string) *model.Observation {
		return &model.Observation{SampledAt: now.Add(-time.Second), Outcome: outcome, DurationMS: 12}
	}
	return model.Snapshot{GeneratedAt: now, Topology: model.Config{
		Nodes:     []model.Node{{ID: "s2", Kind: "host"}, {ID: "workstation", Kind: "host"}},
		Services:  []model.Service{{ID: "api", Name: "API", Node: "s2"}},
		Observers: []model.Observer{{ID: "wsl", Name: "WSL", Node: "workstation", StaleSeconds: 60}, {ID: "s2", Name: "S2", Node: "s2", StaleSeconds: 60}},
		Checks: []model.Check{
			{ID: "remote", Service: "api", Observer: "wsl", Kind: "tcp", Target: "s2.test:443", StaleSeconds: 30},
			{ID: "local", Service: "api", Observer: "s2", Kind: "http", Target: "http://127.0.0.1/health", StaleSeconds: 10},
		},
	}, Nodes: []model.EntityState{{ID: "s2", Health: "down"}, {ID: "workstation", Health: "up"}},
		Services:  []model.EntityState{{ID: "api", Health: "down", Evidence: "measured", Reason: "local health failed", Checks: []string{"remote", "local"}}},
		Checks:    []model.CheckState{{ID: "remote", Observer: "wsl", Health: "up", Freshness: "fresh", Last: last("ok")}, {ID: "local", Observer: "s2", Health: "down", Freshness: "fresh", Last: last("fail")}},
		Observers: []model.ObserverState{{ID: "wsl", Health: "up", LastSeen: now}, {ID: "s2", Health: "up", LastSeen: now}}}
}

// A card carries the Services tab's assessment in closed words, valid
// until the earliest deadline of the checks behind it, and leaves every
// existing field as it was.
func TestServiceCardAssessment(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snap := assessedSnapshot(now)
	cfg := Config{Source: "client", Template: serviceTemplate("api")}
	p := Build(cfg, Input{Snapshot: snap}, now)
	card := p.Rows[1].Value.(ServiceCard)
	a := card.Assessment
	if a == nil || a.State != "local_fail" || a.Availability != "down" || a.Reachability != "up" || a.WorkerOnline != "up" || len(a.Hints) != 0 || a.Hints == nil {
		t.Fatalf("%+v", a)
	}
	want := []AssessmentEvidence{
		{Check: "remote", Observer: RefValue{"wsl", "WSL"}, Scope: "remote", Health: "up", Freshness: "fresh", Worker: "up"},
		{Check: "local", Observer: RefValue{"s2", "S2"}, Scope: "local", Health: "down", Freshness: "fresh", Worker: "up"},
	}
	if !slices.Equal(a.Evidence, want) {
		t.Fatalf("%+v", a.Evidence)
	}
	deadline := now.Add(9 * time.Second) // the local check's: sampled 1s ago, stale after 10s
	if a.ValidUntil == nil || !a.ValidUntil.Equal(deadline) {
		t.Fatal(a.ValidUntil)
	}
	tab, _ := (view.View{Snapshot: snap, Now: now}).Fleet().ServiceCard("api")
	if card.Health != tab.Health || card.Note != tab.Note || card.Evidence != tab.Evidence || card.Reason != tab.Reason || tab.Assessment.Label() != "LOCAL FAIL" {
		t.Fatalf("existing fields changed: %+v", card)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for k := range wire["assessment"].(map[string]any) {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"availability", "evidence", "hints", "path_reachability", "state", "valid_until", "worker_online"}) {
		t.Fatal(keys)
	}
	if e := wire["assessment"].(map[string]any)["evidence"].([]any)[0].(map[string]any); e["observer"].(map[string]any)["name"] != "WSL" || e["scope"] != "remote" {
		t.Fatal(e)
	}

	// Clones own their assessment.
	other := p.Clone()
	edited := other.Rows[1].Value.(ServiceCard).Assessment
	edited.Evidence[0].Health, *edited.ValidUntil = "edited", time.Time{}
	if a.Evidence[0].Health != "up" || !a.ValidUntil.Equal(deadline) {
		t.Fatal("clone shares the assessment")
	}

	// A judgment change is safety state, written without waiting for cadence.
	changed := p.Clone()
	changed.Rows[1].Value.(ServiceCard).Assessment.State = "access_fail"
	if safetyState(changed) == safetyState(p) {
		t.Fatal("assessment change waited for cadence")
	}

	// At its deadline the whole assessment is withheld, in memory and as a
	// file reader decodes it, even while the row itself is still current.
	p.Rows[1].ValidUntil = now.Add(time.Minute)
	p.PublisherExpiresAt = now.Add(time.Minute)
	raw, err = MarshalPanel(p)
	if err != nil {
		t.Fatal(err)
	}
	var file Panel
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, from := range []Panel{p, file} {
		if got := Current(from, deadline.Add(-time.Second)).Rows[1].Value.(ServiceCard).Assessment; got.State != "local_fail" || got.Evidence[1].Health != "down" {
			t.Fatalf("withheld early: %+v", got)
		}
		got := Current(from, deadline).Rows[1].Value.(ServiceCard).Assessment
		if got.State != "unknown" || got.Availability != "unknown" || got.Reachability != "unknown" || got.WorkerOnline != "unknown" || got.ValidUntil == nil || !got.ValidUntil.Equal(deadline) {
			t.Fatalf("expired assessment kept a judgment: %+v", got)
		}
		for i, e := range got.Evidence {
			if e.Check != want[i].Check || e.Observer != want[i].Observer || e.Scope != want[i].Scope || e.Health != "unknown" || e.Freshness != "stale" || e.Worker != "unknown" {
				t.Fatalf("expired evidence kept a judgment: %+v", e)
			}
		}
	}
	if a.State != "local_fail" {
		t.Fatal("aging edited the producer's card")
	}

	// One remote check and nothing else is a configuration hint. Its
	// deadline is the snapshot's own lifetime, shorter than the check's.
	snap.Topology.Checks, snap.Checks = snap.Topology.Checks[:1], snap.Checks[:1]
	snap.Services[0].Checks = []string{"remote"}
	a = Build(cfg, Input{Snapshot: snap}, now).Rows[1].Value.(ServiceCard).Assessment
	if a.State != "unknown" || a.Reachability != "up" || !slices.Equal(a.Hints, []string{"single_remote_check"}) || !a.ValidUntil.Equal(now.Add(model.SnapshotLifetime)) {
		t.Fatalf("%+v", a)
	}
	// Without evidence there is no deadline, and nothing to withhold.
	snap.Checks = nil
	a = Build(cfg, Input{Snapshot: snap}, now).Rows[1].Value.(ServiceCard).Assessment
	if a.ValidUntil != nil || len(a.Evidence) != 0 || a.Evidence == nil || a.State != "unknown" {
		t.Fatalf("%+v", a)
	}
	ageAssessment(a, now.Add(time.Hour))
	if !slices.Equal(a.Hints, []string{"single_remote_check"}) {
		t.Fatal(a)
	}
}

// An assessment lists at most its limit of evidence, but every check
// behind it bounds its deadline.
func TestServiceAssessmentEvidenceBound(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snap := assessedSnapshot(now)
	snap.Topology.Checks, snap.Checks = nil, nil
	for i := range AssessmentEvidenceLimit + 4 {
		id := fmt.Sprintf("c%02d", i)
		stale := 60
		if i == AssessmentEvidenceLimit+3 {
			stale = 5
		}
		snap.Topology.Checks = append(snap.Topology.Checks, model.Check{ID: id, Service: "api", Observer: "wsl", Kind: "http", Target: "https://s2.test", StaleSeconds: stale})
		snap.Checks = append(snap.Checks, model.CheckState{ID: id, Observer: "wsl", Health: "up", Freshness: "fresh", Last: &model.Observation{SampledAt: now.Add(-time.Second), Outcome: "ok"}})
	}
	a := Build(Config{Source: "client", Template: serviceTemplate("api")}, Input{Snapshot: snap}, now).Rows[1].Value.(ServiceCard).Assessment
	if len(a.Evidence) != AssessmentEvidenceLimit || a.Evidence[0].Check != "c00" || a.ValidUntil == nil || !a.ValidUntil.Equal(now.Add(4*time.Second)) {
		t.Fatalf("%d %v", len(a.Evidence), a.ValidUntil)
	}
}
