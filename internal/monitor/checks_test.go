package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/tailscale"
	"github.com/WhitenWhiten/topophany/internal/adapters/wireguard"
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

// A diagnostic says what 1.5.2's probe policies measured and declared:
// the certificate's expiry, the elapsed time against its budget. It never
// says what a private JSON assertion expected.
func TestDiagnosticProbePolicies(t *testing.T) {
	facts := map[string]string{"tls_days_remaining": "9", "tls_not_after": "2026-11-10T07:00:00Z", "tls_version": "0x304"}
	for _, kind := range []string{"http", "tls"} {
		if f := diagnosticFacts(model.Check{Kind: kind}, facts); len(f) != 2 || f["tls_days_remaining"] != "9" || f["tls_not_after"] != "2026-11-10T07:00:00Z" {
			t.Fatal(kind, f)
		}
	}
	for _, kind := range []string{"tcp", "mihomo", "cloudflare", "wireguard"} {
		if f := diagnosticFacts(model.Check{Kind: kind}, facts); len(f) != 0 {
			t.Fatal(kind, f)
		}
	}
	if e := expectedFacts(model.Check{Kind: "http"}); len(e) != 0 {
		t.Fatal("unset policies exported", e)
	}

	now := time.Now().UTC().Truncate(time.Second)
	check := model.Check{ID: "site", Name: "Site", Observer: "o", Kind: "http", Target: "https://site.test/health", StaleSeconds: 60, MaxDurationMS: 800, TLSMinDays: 14,
		HTTP: model.HTTPOptions{JSONEquals: map[string]json.RawMessage{"/private-pointer": json.RawMessage(`"private-expected"`)}}}
	last := &model.Observation{SampledAt: now.Add(-time.Second), Outcome: "policy", Evidence: "measured", Stage: "latency_policy", Code: "latency_exceeded", DurationMS: 1430, Facts: facts}
	snap := model.Snapshot{GeneratedAt: now, Topology: model.Config{Observers: []model.Observer{{ID: "o", Name: "Observer"}}, Checks: []model.Check{check}},
		Checks: []model.CheckState{{ID: "site", Observer: "o", Health: "degraded", Freshness: "fresh", PolicyConfirmed: true, Last: last}}}
	tpl := serviceTemplate()
	tpl.Rows = append(tpl.Rows, Row{ID: "check", Metric: "check.card", Visual: "card", Select: Selector{Check: "site"}})
	p := Build(Config{Source: "client", Template: tpl}, Input{Snapshot: snap}, now)
	d := p.Rows[1].Value.(CheckCard).Diagnostic
	if d == nil || d.DurationMS == nil || *d.DurationMS != 1430 || d.Expected["max_duration_ms"] != "800" || d.Expected["tls_min_days"] != "14" || len(d.Expected) != 2 || d.Facts["tls_days_remaining"] != "9" {
		t.Fatalf("%+v", d)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"duration_ms":1430`) || strings.Contains(string(raw), "private-expected") || strings.Contains(string(raw), "private-pointer") || strings.Contains(string(raw), "json_equals") {
		t.Fatal(string(raw))
	}
	// A clone owns its duration, and a duration is telemetry, not decision state.
	*p.Clone().Rows[1].Value.(CheckCard).Diagnostic.DurationMS = 1
	if *d.DurationMS != 1430 {
		t.Fatal("clone shares the duration")
	}
	other := *d
	slower := int64(5000)
	other.DurationMS = &slower
	if fmt.Sprint(diagnosticState(&other)) != fmt.Sprint(diagnosticState(d)) {
		t.Fatal("elapsed time bypasses cadence")
	}
	// A measured zero is a duration; an impossible negative one is unknown.
	for elapsed, want := range map[int64]string{0: `"duration_ms":0`, -1: ""} {
		snap.Checks[0].Last.DurationMS = elapsed
		raw, _ := json.Marshal(Build(Config{Source: "client", Template: tpl}, Input{Snapshot: snap}, now).Rows[1].Value)
		if got := strings.Contains(string(raw), `"duration_ms"`); got != (want != "") || want != "" && !strings.Contains(string(raw), want) {
			t.Fatal(elapsed, string(raw))
		}
	}
}

// An adapter check's diagnostic says whether it declares a policy and what
// its sample says of it, in the TUI's terms; other checks say nothing.
func TestDiagnosticAdapterPolicy(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := func(check model.Check, outcome string, facts map[string]string) model.Snapshot {
		check.ID, check.Observer, check.StaleSeconds = "c", "o", 60
		return model.Snapshot{GeneratedAt: now, Topology: model.Config{Observers: []model.Observer{{ID: "o"}}, Checks: []model.Check{check}},
			Checks: []model.CheckState{{ID: "c", Observer: "o", Health: "up", Freshness: "fresh", Last: &model.Observation{SampledAt: now.Add(-time.Second), Outcome: outcome, Evidence: "reported", Stage: "adapter", Facts: facts}}}}
	}
	tpl := serviceTemplate()
	tpl.Rows = append(tpl.Rows, Row{ID: "check", Metric: "check.card", Visual: "card", Select: Selector{Check: "c"}})
	build := func(s model.Snapshot) Panel {
		return Build(Config{Source: "client", Template: tpl}, Input{Snapshot: s}, now)
	}
	controller := model.Check{Kind: "mihomo", Target: "http://127.0.0.1:9090"}
	for _, tc := range []struct {
		name    string
		adapter model.AdapterOptions
		outcome string
		mode    string
		want    string
	}{
		{"no expectations", model.AdapterOptions{}, "ok", "rule", "not_configured"},
		{"mode matches", model.AdapterOptions{ExpectedMode: "rule"}, "ok", "rule", "matched"},
		{"mode differs", model.AdapterOptions{ExpectedMode: "rule"}, "policy", "global", "violated"},
	} {
		check := controller
		check.Adapter = tc.adapter
		p := build(snapshot(check, tc.outcome, map[string]string{"mode": tc.mode}))
		d := p.Rows[1].Value.(CheckCard).Diagnostic
		if d == nil || d.Policy != tc.want {
			t.Fatal(tc.name, d)
		}
		raw, _ := json.Marshal(d)
		if !strings.Contains(string(raw), `"policy":"`+tc.want+`"`) {
			t.Fatal(string(raw))
		}
	}
	p := build(snapshot(model.Check{Kind: "http", Target: "https://site.test"}, "ok", nil))
	raw, _ := json.Marshal(p.Rows[1].Value)
	if d := p.Rows[1].Value.(CheckCard).Diagnostic; d == nil || d.Policy != "" || strings.Contains(string(raw), `"policy"`) {
		t.Fatal("an application check has an adapter policy", string(raw))
	}
	// A policy verdict is decision state.
	matched := build(snapshot(model.Check{Kind: "mihomo", Target: controller.Target, Adapter: model.AdapterOptions{ExpectedMode: "rule"}}, "ok", map[string]string{"mode": "rule"}))
	violated := build(snapshot(model.Check{Kind: "mihomo", Target: controller.Target, Adapter: model.AdapterOptions{ExpectedMode: "rule"}}, "ok", map[string]string{"mode": "global"}))
	if safetyState(matched, time.Time{}) == safetyState(violated, time.Time{}) {
		t.Fatal("a policy violation waited for cadence")
	}
}

// Tailscale and WireGuard diagnostics carry their closed runtime readings
// and nothing that can name an address, a host, a key or a peer's text.
func TestDiagnosticRuntimeAdapterFacts(t *testing.T) {
	for _, tc := range []struct {
		check              model.Check
		included, excluded map[string]string
		malformed          map[string]string
		expected           map[string]string
	}{
		{
			check: model.Check{Kind: "tailscale", Adapter: model.AdapterOptions{Peer: "nodekey:private-peer", TailscaleDiagnostic: "disco"}},
			included: map[string]string{"backend_state": "Running", "online_reported": "true", "active_reported": "false", "health_warning_count": "2", "last_seen": "2026-11-01T07:30:00Z",
				"transport_hint": "relay_preference_reported", "diagnostic_protocol": "disco", "diagnostic_response": "received", "diagnostic_route": "derp", "diagnostic_latency_ms": "41"},
			excluded: map[string]string{"relay": "sea", "peer": "host.tailnet-private.ts.net", "tailscale_ips": "100.64.0.2", "cur_addr": "198.51.100.7:41641", "dns_name": "host.tailnet-private.ts.net.", "public_key": "nodekey:private-peer"},
			// An older worker wrote the backend's own text; a timestamp or count of another shape is not one.
			malformed: map[string]string{"backend_state": "Running on 100.64.0.2", "last_seen": "host.tailnet-private.ts.net", "transport_hint": "direct 198.51.100.7:41641", "health_warning_count": "-1", "diagnostic_latency_ms": "41ms", "diagnostic_route": "DERP(sea)"},
			expected:  map[string]string{},
		},
		{
			check: model.Check{Kind: "wireguard", Adapter: model.AdapterOptions{Interface: "wg0", Peer: "private-public-key=", ExpectedAllowedIPs: []string{"10.0.0.2/32"}, ExpectedKeepaliveSeconds: ptr(25)}},
			included: map[string]string{"sampling_atomicity": "separate_command_reads_not_atomic", "last_handshake": "2026-11-01T07:37:00Z", "rx_bytes": "123456", "tx_bytes": "654321",
				"allowed_ips_match": "true", "keepalive_match": "false", "keepalive_seconds": "0"},
			excluded:  map[string]string{"allowed_ips": "10.0.0.2/32", "endpoint": "198.51.100.7:51820", "public_key": "private-public-key=", "preshared_key": "private-psk", "interface": "wg0"},
			malformed: map[string]string{"rx_bytes": "1e9", "last_handshake": "1730446620", "allowed_ips_match": "10.0.0.2/32", "keepalive_seconds": "25s"},
			expected:  map[string]string{"keepalive_seconds": "25"},
		},
	} {
		facts := maps.Clone(tc.included)
		maps.Copy(facts, tc.excluded)
		if got := diagnosticFacts(tc.check, facts); !maps.Equal(got, tc.included) {
			t.Fatalf("%s: %v", tc.check.Kind, got)
		}
		maps.Copy(facts, tc.malformed)
		got := diagnosticFacts(tc.check, facts)
		for key := range tc.malformed {
			if _, ok := got[key]; ok {
				t.Fatalf("%s: %s exported as %q", tc.check.Kind, key, got[key])
			}
		}
		if len(got) != len(tc.included)-len(tc.malformed) {
			t.Fatalf("%s: %v", tc.check.Kind, got)
		}
		if e := expectedFacts(tc.check); !maps.Equal(e, tc.expected) {
			t.Fatalf("%s: expected %v", tc.check.Kind, e)
		}
		// The same keys mean nothing for another kind.
		if got := diagnosticFacts(model.Check{Kind: "http"}, tc.included); len(got) != 0 {
			t.Fatal(got)
		}
	}
	if got := diagnosticFacts(model.Check{Kind: "tailscale"}, map[string]string{"last_seen": "not reported"}); got["last_seen"] != "not reported" {
		t.Fatal(got)
	}
}

// What the adapters write today passes their shapes whole, except the
// upstream DERP region name: the adapters' own synthetic fixtures, read
// through fake commands, cross-check the allowlist.
func TestDiagnosticRuntimeFactsMatchTheAdapters(t *testing.T) {
	fixture := func(path string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "adapters", path))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	ts := model.Check{Kind: "tailscale", Adapter: model.AdapterOptions{Peer: "100.64.0.1", TailscaleDiagnostic: "disco"}}
	o := model.Observation{Facts: map[string]string{}}
	err := tailscale.Collect(context.Background(), func(_ context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return fixture("tailscale/testdata/v1.90.0/status.json"), nil
		}
		return fixture("tailscale/testdata/v1.90.0/derp.txt"), nil
	}, ts.Adapter.Tailscale(), &o)
	if err != nil || o.Facts["relay"] == "" || o.Facts["diagnostic_route"] != "derp" {
		t.Fatal(err, o.Facts)
	}
	want := maps.Clone(o.Facts)
	delete(want, "relay")
	if got := diagnosticFacts(ts, o.Facts); !maps.Equal(got, want) {
		t.Fatalf("tailscale: %v, adapter wrote %v", got, o.Facts)
	}

	wg := model.Check{Kind: "wireguard", Adapter: model.AdapterOptions{Interface: "wg0", Peer: "PUBLIC-PEER", ExpectedAllowedIPs: []string{"10.0.0.0/24", "::/0"}, ExpectedKeepaliveSeconds: ptr(25)}}
	o = model.Observation{Facts: map[string]string{}}
	err = wireguard.Collect(context.Background(), func(_ context.Context, name string, args ...string) ([]byte, error) {
		return fixture("wireguard/testdata/v1.0.20210914/" + args[len(args)-1] + ".tsv"), nil
	}, wg.Adapter.WireGuard(), &o)
	if err != nil || len(o.Facts) != 7 {
		t.Fatal(err, o.Facts)
	}
	if got := diagnosticFacts(wg, o.Facts); !maps.Equal(got, o.Facts) {
		t.Fatalf("wireguard: %v, adapter wrote %v", got, o.Facts)
	}
}
