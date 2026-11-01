package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestCheckCardsDiagnosticScopeOwnershipAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	snap, err := previewSim(t, "", now).Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Source: "preview", Template: serviceTemplate("blog")}
	service := Build(cfg, Input{Snapshot: snap}, now).Rows[1].Value.(ServiceCard)
	id := service.SeenFrom[0].Checks[0].ID
	for i := range snap.Topology.Checks {
		if snap.Topology.Checks[i].ID == id {
			snap.Topology.Checks[i].Kind = "mihomo"
			snap.Topology.Checks[i].Adapter.ExpectedMode = "rule"
		}
	}
	for i := range snap.Checks {
		cs := &snap.Checks[i]
		if cs.ID == id {
			cs.Freshness, cs.Health, cs.PolicyConfirmed = "fresh", "degraded", false
			cs.Last = &model.Observation{SampledAt: now, Outcome: "policy", Evidence: "reported", Stage: "adapter", Code: "mode_mismatch", PathEvidence: "runtime_reported", Facts: map[string]string{"mode": "global", "reported_chain": "declared -> withheld_secret_request", "peer_text": "withheld_secret_request", "test_url": "private-url", "new_unknown_fact": "secret-value"}}
		}
	}
	cfg.Template.Rows = append(cfg.Template.Rows, Row{ID: "check", Metric: "check.card", Visual: "card", Select: Selector{Check: id}}, Row{ID: "checks", Metric: "checks.list", Visual: "list"})
	if err := cfg.Template.Validate("client"); err != nil {
		t.Fatal(err)
	}
	p := Build(cfg, Input{Snapshot: snap}, now)
	card := p.Rows[2].Value.(CheckCard)
	if card.Diagnostic == nil || card.Diagnostic.Outcome != "policy" || card.Diagnostic.PolicyConfirmed || card.Diagnostic.Expected["mode"] != "rule" || card.Observer.ID == "" || card.Diagnostic.Facts["mode"] != "global" {
		t.Fatal(card)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "private-url") || strings.Contains(string(b), "secret-value") {
		t.Fatal("unlisted facts exported")
	}
	cloned := p.Clone()
	cloned.Rows[2].Value.(CheckCard).Diagnostic.Facts["mode"] = "changed"
	if card.Diagnostic.Facts["mode"] != "global" {
		t.Fatal("diagnostics alias")
	}
	// Service-card diagnostics use the same evidence and nested deadlines.
	sc := p.Rows[1].Value.(ServiceCard)
	found := false
	for _, o := range sc.SeenFrom {
		for _, c := range o.Checks {
			if c.ID == id {
				found = c.Diagnostic != nil && c.Diagnostic.Code == "mode_mismatch"
			}
		}
	}
	if !found {
		t.Fatal("service omitted diagnostics")
	}
	expired := *card.ValidUntil
	p.PublisherExpiresAt = expired.Add(time.Second)
	if Current(p, expired).Rows[2].Value != nil {
		t.Fatal("lease renewed expired evidence")
	}
	// On the wire nested expiry must erase diagnostics even if the container
	// row has a later deadline (as it can for a multi-check service).
	p.Rows[1].ValidUntil = expired.Add(time.Second)
	b, _ = json.Marshal(p)
	var decoded Panel
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	aged := Current(decoded, expired).Rows[1].Value.(ServiceCard)
	for _, o := range aged.SeenFrom {
		for _, c := range o.Checks {
			if c.ID == id && c.Diagnostic != nil {
				t.Fatal("nested diagnostic outlived evidence")
			}
		}
	}
	// A configured check absent from this observer's snapshot is not available.
	for i := range snap.Checks {
		if snap.Checks[i].ID == id {
			snap.Checks = append(snap.Checks[:i], snap.Checks[i+1:]...)
			break
		}
	}
	if Build(cfg, Input{Snapshot: snap}, now).Rows[2].Value != nil {
		t.Fatal("borrowed another observer's check")
	}
}

func TestDiagnosticFactAllowlistByAdapter(t *testing.T) {
	n := 4
	for _, tc := range []struct {
		kind, key string
		adapter   model.AdapterOptions
	}{
		{"cloudflared", "ready_connections", model.AdapterOptions{Cloudflared: &model.CloudflaredOptions{ExpectedConnections: &n}}},
		{"cloudflare", "tunnel_connections", model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "tunnel", ExpectedConnections: &n}}},
	} {
		c := model.Check{Kind: tc.kind, Adapter: tc.adapter}
		f := diagnosticFacts(c, map[string]string{tc.key: "2", "connector_id": "private-uuid", "raw_response": "secret", "reported_chain": "unrelated"})
		if len(f) != 1 || f[tc.key] != "2" || expectedFacts(c)["connections"] != "4" {
			t.Fatal(tc.kind, f)
		}
	}
	tpl := serviceTemplate()
	tpl.Rows = append(tpl.Rows, Row{ID: "bad", Metric: "check.card", Visual: "card"})
	if tpl.Validate("client") == nil {
		t.Fatal("missing selector accepted")
	}
}
