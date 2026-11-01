package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestLinkCardKeepsConnectorJudgmentAndExpiresAPIIndependently(t *testing.T) {
	now := time.Now().UTC()
	c := testkit.Config()
	c.Links = append(c.Links, model.Link{ID: "tunnel", From: "home", To: "dmit", Transport: "cloudflared"})
	c.Checks = append(c.Checks,
		model.Check{ID: "connector", Observer: "home", Link: "tunnel", Kind: "cloudflared", Target: "http://127.0.0.1:20241", IntervalSeconds: 10, TimeoutSeconds: 1, StaleSeconds: 60, FailureThreshold: 1, RecoveryThreshold: 1},
		model.Check{ID: "api", Observer: "home", Link: "tunnel", Kind: "cloudflare", IntervalSeconds: 1, TimeoutSeconds: 1, StaleSeconds: 3, FailureThreshold: 1, RecoveryThreshold: 1})
	connector := testkit.Observation(c, "connector", 1, now.Add(-2*time.Second), "ok")
	connector.Evidence, connector.PathEvidence = "reported", model.LocalConnectorPathEvidence
	connector.Facts = map[string]string{"ready_connections": "4"}
	api := testkit.Observation(c, "api", 2, now.Add(-2*time.Second), "policy")
	api.Evidence, api.PathEvidence = "reported", model.RemotePlatformPathEvidence
	api.Facts = map[string]string{"tunnel_connections": "1", "tunnel_status": "degraded"}
	snap := engine.Build(c, "collector", "", []model.Observation{connector, api}, nil, now)
	tpl := Template{Schema: 1, Title: "Links", Rows: []Row{{ID: "card", Metric: "link.card", Visual: "card", Select: Selector{Link: "tunnel"}}, {ID: "list", Metric: "links.list", Visual: "list"}}}
	tpl.Bar.Row = "card"
	if err := tpl.Validate("client"); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Source: "client", Template: tpl}
	p := Build(cfg, Input{Snapshot: snap}, now)
	card := p.Rows[0].Value.(LinkCard)
	if card.Health != "up" || card.Evidence != "reported" || !strings.Contains(card.Reason, "service behind it is unproven") || len(card.Checks) != 2 {
		t.Fatal(card)
	}
	if !p.Rows[0].ValidUntil.After(now.Add(2 * time.Second)) {
		t.Fatal("API shortened connector judgment")
	}
	p.PublisherExpiresAt = now.Add(30 * time.Second)
	cloned := p.Clone()
	cloned.Rows[0].Value.(LinkCard).Checks[0].Diagnostic.Facts["ready_connections"] = "changed"
	if card.Checks[0].Diagnostic.Facts["ready_connections"] != "4" {
		t.Fatal("aliased link diagnostics")
	}
	b, _ := json.Marshal(p)
	var decoded Panel
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, aged := range []Panel{Current(decoded, now.Add(2*time.Second)), Build(cfg, Input{Snapshot: snap}, now.Add(2*time.Second))} {
		v := aged.Rows[0].Value.(LinkCard)
		if v.Health != "up" || v.Checks[0].Diagnostic == nil || v.Checks[1].Diagnostic != nil || v.Checks[1].Health != "unknown" {
			t.Fatal("API expiry changed link or retained old facts", v)
		}
	}
	// A remote API report on its own does not prove a link is available.
	snap = engine.Build(c, "collector", "", []model.Observation{api}, nil, now)
	if v := Build(cfg, Input{Snapshot: snap}, now).Rows[0].Value.(LinkCard); v.Health != "unknown" {
		t.Fatal("API proved link", v)
	}
	// Link selections cannot leak into unrelated metrics, or omit their ID.
	tpl.Rows[0].Select.Link = ""
	if tpl.Validate("client") == nil {
		t.Fatal("missing link accepted")
	}
	tpl.Rows[0].Metric, tpl.Rows[0].Visual, tpl.Rows[0].Select.Link = "checks.list", "list", "tunnel"
	if tpl.Validate("client") == nil {
		t.Fatal("foreign selector accepted")
	}
}

func TestLinkAffectedPathBoundsCard(t *testing.T) {
	now := time.Now().UTC()
	c := testkit.Config()
	snap := engine.Build(c, "collector", "home", []model.Observation{testkit.Observation(c, "home-http", 1, now, "ok")}, nil, now)
	for i := range snap.Topology.Checks {
		if snap.Topology.Checks[i].ID == "home-http" {
			snap.Topology.Checks[i].StaleSeconds = 2
		}
	}
	cfg := Config{Source: "client", Template: Template{Rows: []Row{{ID: "link", Metric: "link.card", Visual: "card", Select: Selector{Link: "home-dmit-link"}}}}}
	p := Build(cfg, Input{Snapshot: snap}, now)
	if !p.Rows[0].ValidUntil.Equal(now.Add(2 * time.Second)) {
		t.Fatal("path evidence did not bound link", p.Rows[0].ValidUntil)
	}
}
