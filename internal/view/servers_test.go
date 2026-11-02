package view

import (
	"slices"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// tunnelView is a home box whose link to Cloudflare carries a connector
// check and, beside it, the platform API's read of the same tunnel.
func tunnelView(now time.Time, link string, connector, api map[string]string) View {
	state := func(id, freshness string, facts map[string]string) model.CheckState {
		if facts == nil {
			return model.CheckState{ID: id, Freshness: "absent"}
		}
		return model.CheckState{ID: id, Freshness: freshness, Last: &model.Observation{SampledAt: now, Facts: facts}}
	}
	return View{Now: now, Snapshot: model.Snapshot{
		GeneratedAt: now,
		Topology: model.Config{
			Nodes: []model.Node{{ID: "home", Name: "Home box", Kind: "host"}, {ID: "cf", Name: "Cloudflare", Kind: "edge"}},
			Links: []model.Link{{ID: "home-cf", From: "home", To: "cf", Transport: "cloudflared"}},
			Checks: []model.Check{
				{ID: "tunnel", Kind: "cloudflared", Link: "home-cf"},
				{ID: "api", Kind: "cloudflare", Link: "home-cf"},
				{ID: "other", Kind: "tailscale", Link: "elsewhere"},
			},
		},
		Checks: []model.CheckState{state("tunnel", "fresh", connector), state("api", "fresh", api)},
		Links:  []model.EntityState{{ID: "home-cf", Health: link}},
	}}
}

// A link's card row carries its health and what its own fresh checks report:
// the connector's sessions and colos, and beside them, led by "API", the
// platform's connections and status. Neither reading changes the health.
func TestCardLinkCarriesTheTunnelReading(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	up := map[string]string{"ready_connections": "4", "connection_colos": "hkg,sin"}
	down := map[string]string{"ready_connections": "0", "ha_connections": "0"}
	api := map[string]string{"tunnel_connections": "4", "tunnel_status": "healthy"}
	for _, tc := range []struct {
		name      string
		view      View
		health    string
		reading   []string
		inbound   bool
		onNode    string
		otherName string
	}{
		{"connector up", tunnelView(now, "up", up, nil), "up", []string{"4 sessions", "hkg,sin"}, false, "home", "Cloudflare"},
		{"connector down", tunnelView(now, "down", down, nil), "down", []string{"no ready session"}, false, "home", "Cloudflare"},
		{"one session", tunnelView(now, "up", map[string]string{"ready_connections": "1", "connection_colos": "sin"}, nil), "up", []string{"1 session", "sin"}, false, "home", "Cloudflare"},
		{"API beside the connector", tunnelView(now, "up", up, api), "up", []string{"4 sessions", "hkg,sin", "API 4 connections healthy"}, false, "home", "Cloudflare"},
		{"the two disagree", tunnelView(now, "degraded", up, map[string]string{"tunnel_connections": "1", "tunnel_status": "degraded"}), "degraded", []string{"4 sessions", "hkg,sin", "API 1 connection degraded"}, false, "home", "Cloudflare"},
		{"API alone", tunnelView(now, "up", nil, api), "up", []string{"API 4 connections healthy"}, false, "home", "Cloudflare"},
		{"API status alone", tunnelView(now, "up", up, map[string]string{"tunnel_status": "inactive"}), "up", []string{"4 sessions", "hkg,sin", "API inactive"}, false, "home", "Cloudflare"},
		{"seen from the edge", tunnelView(now, "up", up, nil), "up", []string{"4 sessions", "hkg,sin"}, true, "cf", "Home box"},
		{"neither reported", tunnelView(now, "up", nil, nil), "up", nil, false, "home", "Cloudflare"},
		{"no facts to read", tunnelView(now, "up", map[string]string{"requests_total": "9"}, map[string]string{"tunnel_pending_reconnect": "0"}), "up", nil, false, "home", "Cloudflare"},
		// What is not a bounded number, a lowercase colo token or a closed word
		// is not shown, whatever a sample carried.
		{"unbounded facts", tunnelView(now, "up", map[string]string{"ready_connections": "four", "connection_colos": "HKG, <b>,x,hkg"}, map[string]string{"tunnel_connections": "-1", "tunnel_status": "on fire"}), "up", []string{"hkg"}, false, "home", "Cloudflare"},
		{"colos are bounded", tunnelView(now, "up", map[string]string{"ready_connections": "9", "connection_colos": "aaa,bbb,ccc,ddd,eee,fff,ggg,hhh,iii"}, nil), "up", []string{"9 sessions", "aaa,bbb,ccc,ddd,eee,fff,ggg,hhh"}, false, "home", "Cloudflare"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			links := tc.view.Fleet().Card(tc.onNode).Links
			if len(links) != 1 {
				t.Fatalf("%d links on the card, want 1", len(links))
			}
			l := links[0]
			if l.ID != "home-cf" || l.Other.Name != tc.otherName || l.Inbound != tc.inbound || l.Transport != "cloudflared" || l.Health != tc.health || !slices.Equal(l.Reading, tc.reading) {
				t.Fatalf("link %+v, want health %q reading %q", l, tc.health, tc.reading)
			}
		})
	}
}

// A reading is evidence of a sample, so a sample that is stale, or a view that
// is cached, says nothing; the link's health goes unknown with the cache.
func TestCardLinkReadingNeedsFreshEvidence(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	up := map[string]string{"ready_connections": "4", "connection_colos": "hkg,sin"}
	v := tunnelView(now, "up", up, nil)
	v.Snapshot.Checks[0].Freshness = "stale"
	if l := v.Fleet().Card("home").Links[0]; l.Reading != nil || l.Health != "up" {
		t.Fatalf("a stale connector sample was read: %+v", l)
	}
	v = tunnelView(now, "up", up, map[string]string{"tunnel_connections": "4", "tunnel_status": "healthy"})
	v.Snapshot.Checks[0].Freshness = "paused"
	if l := v.Fleet().Card("home").Links[0]; !slices.Equal(l.Reading, []string{"API 4 connections healthy"}) {
		t.Fatalf("only the fresh API sample is read: %+v", l)
	}
	v = tunnelView(now, "down", up, nil)
	v.Cached = true
	if l := v.Fleet().Card("home").Links[0]; l.Reading != nil || l.Health != "unknown" {
		t.Fatalf("a cached view read a sample or a health: %+v", l)
	}
	// The newest of two connector samples decides.
	v = tunnelView(now, "up", up, nil)
	v.Snapshot.Topology.Checks = append(v.Snapshot.Topology.Checks, model.Check{ID: "tunnel-2", Kind: "cloudflared", Link: "home-cf"})
	v.Snapshot.Checks = append(v.Snapshot.Checks, model.CheckState{ID: "tunnel-2", Freshness: "fresh", Last: &model.Observation{SampledAt: now.Add(time.Second), Facts: map[string]string{"ready_connections": "2", "connection_colos": "fra"}}})
	if l := v.Fleet().Card("home").Links[0]; !slices.Equal(l.Reading, []string{"2 sessions", "fra"}) {
		t.Fatalf("the newest sample did not decide: %+v", l)
	}
}

func TestTunnelSeparateAPIReadsKeepStatusAndCount(t *testing.T) {
	now := time.Now().UTC()
	v := tunnelView(now, "up", map[string]string{"ready_connections": "4"}, map[string]string{"tunnel_status": "healthy"})
	// Add a second platform check carrying only the newer dedicated count.
	var apiCheck model.Check
	for _, x := range v.Snapshot.Topology.Checks {
		if x.Kind == "cloudflare" {
			apiCheck = x
			break
		}
	}
	apiCheck.ID = "dedicated-connections"
	v.Snapshot.Topology.Checks = append(v.Snapshot.Topology.Checks, apiCheck)
	v.Snapshot.Checks = append(v.Snapshot.Checks, model.CheckState{ID: apiCheck.ID, Freshness: "fresh", Last: &model.Observation{SampledAt: now.Add(time.Second), Facts: map[string]string{"tunnel_connections": "3"}}})
	fleet := v.Fleet()
	if !slices.Contains(fleet.linkRead["home-cf"], "API 3 connections healthy") {
		t.Fatal(fleet.linkRead)
	}
}
