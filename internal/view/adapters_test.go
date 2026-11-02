package view

import (
	"slices"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestAdapterStatusFacetsPreserveEvidenceLimits(t *testing.T) {
	check := model.Check{Kind: "mihomo", Adapter: model.AdapterOptions{ExpectedMode: "rule"}}
	for _, tc := range []struct{ outcome, fresh, runtime, policy string }{
		{"ok", "fresh", "available", "matched"},
		{"policy", "fresh", "available", "violated"},
		{"error", "fresh", "available", "unverified"},
		{"ok", "stale", "unverified", "unverified"},
	} {
		state := model.CheckState{Freshness: tc.fresh, Last: &model.Observation{Evidence: "reported", Outcome: tc.outcome, Stage: "adapter", Facts: map[string]string{"mode": "rule"}}}
		text := strings.Join(adapterDetails(check, state), "\n")
		for _, want := range []string{"Runtime read: " + tc.runtime, "Policy check: " + tc.policy, "Business reachability: unmeasured"} {
			if !strings.Contains(text, want) {
				t.Fatal(want, text)
			}
		}
	}
	if adapterDetails(model.Check{Kind: "http"}, model.CheckState{}) != nil {
		t.Fatal("application probes relabeled as software telemetry")
	}
}

func TestAdapterReadFailureAndKnownPolicyViolationStayDistinct(t *testing.T) {
	check := model.Check{Kind: "mihomo", Adapter: model.AdapterOptions{ExpectedMode: "rule"}}
	state := model.CheckState{Freshness: "fresh", Last: &model.Observation{Evidence: "reported", Stage: "adapter", Outcome: "error"}}
	if text := strings.Join(adapterDetails(check, state), "\n"); strings.Contains(text, "Runtime read: available") {
		t.Fatal(text)
	}
	state.Last.Stage = "provider_reference"
	state.Last.Facts = map[string]string{"mode": "global"}
	if text := strings.Join(adapterDetails(check, state), "\n"); !strings.Contains(text, "Policy check: violated") || !strings.Contains(text, "Runtime read: available") {
		t.Fatal(text)
	}
}

func TestCloudflaredAdapterDetails(t *testing.T) {
	four := 4
	check := model.Check{Kind: "cloudflared", Adapter: model.AdapterOptions{Cloudflared: &model.CloudflaredOptions{ExpectedConnections: &four}}}
	for _, tc := range []struct {
		name                 string
		outcome, code, stage string
		facts                map[string]string
		fresh                bool
		runtime, policy      string
	}{
		{name: "established", outcome: "ok", stage: "adapter", facts: map[string]string{"ready_connections": "4", "ha_connections": "4"}, fresh: true, runtime: "available", policy: "matched"},
		{name: "connections below", outcome: "policy", code: "connections_below_expected", stage: "adapter", facts: map[string]string{"ready_connections": "2"}, fresh: true, runtime: "available", policy: "violated"},
		{name: "colo outside", outcome: "policy", code: "colo_outside_expected", stage: "adapter", facts: map[string]string{"ready_connections": "4"}, fresh: true, runtime: "available", policy: "violated"},
		{name: "no session", outcome: "fail", code: "tunnel_not_ready", stage: "adapter", facts: map[string]string{"ready_connections": "0"}, fresh: true, runtime: "available", policy: "unverified"},
		{name: "metrics unavailable", outcome: "error", code: "metrics_unavailable", stage: "adapter", fresh: true, runtime: "unverified", policy: "unverified"},
		{name: "stale", outcome: "ok", stage: "adapter", facts: map[string]string{"ready_connections": "4"}, runtime: "unverified", policy: "unverified"},
		{name: "fact without stage", outcome: "ok", facts: map[string]string{"ha_connections": "4"}, fresh: true, runtime: "available", policy: "matched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := model.CheckState{Freshness: "fresh", Last: &model.Observation{Evidence: "reported", Outcome: tc.outcome, Code: tc.code, Stage: tc.stage, Facts: tc.facts}}
			if !tc.fresh {
				state.Freshness = "stale"
			}
			text := strings.Join(adapterDetails(check, state), "\n")
			for _, want := range []string{"Runtime read: " + tc.runtime, "Policy check: " + tc.policy, "Business reachability: unmeasured"} {
				if !strings.Contains(text, want) {
					t.Fatal(want, text)
				}
			}
		})
	}
	if adapterDetails(model.Check{Kind: "http"}, model.CheckState{}) != nil {
		t.Fatal("application probes relabeled as software telemetry")
	}
}

// The TUI's policy line and a stream's policy word are one decision: what
// the check declares, and what its current sample says of that.
func TestAdapterPolicy(t *testing.T) {
	yes, no, four := true, false, 4
	sample := func(freshness, outcome string, facts map[string]string) model.CheckState {
		return model.CheckState{Freshness: freshness, Last: &model.Observation{Evidence: "reported", Stage: "adapter", Outcome: outcome, Facts: facts}}
	}
	fresh := func(outcome string, facts map[string]string) model.CheckState { return sample("fresh", outcome, facts) }
	mihomo := func(a model.AdapterOptions) model.Check { return model.Check{Kind: "mihomo", Adapter: a} }
	reference := &model.MihomoProviderReference{URL: "https://publisher.test/members.json", Providers: []string{"edge"}, MaxAgeSeconds: 600}
	providers := map[string][]string{"pool": {"a", "b"}}
	for _, tc := range []struct {
		name  string
		check model.Check
		state model.CheckState
		want  string
	}{
		{"mihomo without expectations", mihomo(model.AdapterOptions{}), fresh("ok", map[string]string{"mode": "rule", "tun_enabled": "true"}), "not_configured"},
		{"mihomo without expectations, stale", mihomo(model.AdapterOptions{}), sample("stale", "ok", nil), "not_configured"},
		{"mihomo mode matches", mihomo(model.AdapterOptions{ExpectedMode: "rule"}), fresh("ok", map[string]string{"mode": "rule"}), "matched"},
		{"mihomo mode differs", mihomo(model.AdapterOptions{ExpectedMode: "rule"}), fresh("policy", map[string]string{"mode": "global"}), "violated"},
		{"mihomo mode differs in its facts", mihomo(model.AdapterOptions{ExpectedMode: "rule"}), fresh("ok", map[string]string{"mode": "global"}), "violated"},
		{"mihomo mode, stale sample", mihomo(model.AdapterOptions{ExpectedMode: "rule"}), sample("stale", "ok", map[string]string{"mode": "rule"}), "unverified"},
		{"mihomo mode, read failed", mihomo(model.AdapterOptions{ExpectedMode: "rule"}), fresh("error", nil), "unverified"},
		{"mihomo TUN off, expected on", mihomo(model.AdapterOptions{ExpectedTUN: &yes}), fresh("ok", map[string]string{"tun_enabled": "false"}), "violated"},
		{"mihomo TUN off, expected off", mihomo(model.AdapterOptions{ExpectedTUN: &no}), fresh("ok", map[string]string{"tun_enabled": "false"}), "matched"},
		{"mihomo legacy sample with unknown TUN", mihomo(model.AdapterOptions{ExpectedTUN: &no}), fresh("ok", map[string]string{"tun_enabled": "unknown", "reported_chain": "leaf"}), "unverified"},
		{"mihomo legacy sample without TUN", mihomo(model.AdapterOptions{ExpectedTUN: &no}), fresh("ok", nil), "unverified"},
		{"mihomo providers match", mihomo(model.AdapterOptions{ExpectedProviders: providers}), fresh("ok", map[string]string{"provider.pool": "a,b"}), "matched"},
		{"mihomo providers differ", mihomo(model.AdapterOptions{ExpectedProviders: providers}), fresh("policy", map[string]string{"provider.pool": "b,a"}), "violated"},
		{"mihomo provider reference matches", mihomo(model.AdapterOptions{ProviderReference: reference}), fresh("ok", nil), "matched"},
		{"mihomo provider reference differs", mihomo(model.AdapterOptions{ProviderReference: reference}), fresh("policy", nil), "violated"},
		{"mihomo provider reference changed while sampled", mihomo(model.AdapterOptions{ProviderReference: reference}), fresh("unknown", nil), "unverified"},
		{"cloudflare proxied matches", model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "dns_record", ExpectedProxied: &yes}}}, fresh("ok", map[string]string{"record_proxied": "true"}), "matched"},
		{"cloudflare proxied differs", model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "dns_record", ExpectedProxied: &yes}}}, fresh("policy", map[string]string{"record_proxied": "false"}), "violated"},
		{"cloudflare content matches", model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "dns_record", ExpectedContent: "192.0.2.1"}}}, fresh("ok", nil), "matched"},
		{"cloudflare read without expectations", model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "tunnel"}}}, fresh("ok", map[string]string{"tunnel_status": "healthy"}), "not_configured"},
		{"cloudflare legacy sample without colos", model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "tunnel", ExpectedColos: []string{"HKG"}}}}, fresh("ok", map[string]string{"tunnel_connections": "2"}), "unverified"},
		{"cloudflare legacy sample with no connections", model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "tunnel", ExpectedColos: []string{"HKG"}}}}, fresh("ok", map[string]string{"tunnel_connections": "0"}), "unverified"},
		{"cloudflared connections", model.Check{Kind: "cloudflared", Adapter: model.AdapterOptions{Cloudflared: &model.CloudflaredOptions{ExpectedConnections: &four}}}, fresh("ok", nil), "matched"},
		{"wireguard keepalive differs", model.Check{Kind: "wireguard", Adapter: model.AdapterOptions{ExpectedKeepaliveSeconds: &four}}, fresh("policy", map[string]string{"keepalive_match": "false"}), "violated"},
		{"tailscale declares none", model.Check{Kind: "tailscale"}, fresh("ok", map[string]string{"backend_state": "Running"}), "not_configured"},
		{"http is not an adapter", model.Check{Kind: "http"}, fresh("ok", nil), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AdapterPolicy(tc.check, tc.state)
			details := adapterDetails(tc.check, tc.state)
			if got != tc.want || (got == "") != (details == nil) {
				t.Fatal(got, details)
			}
			if got != "" && details[1] != "Policy check: "+strings.ReplaceAll(tc.want, "_", " ") {
				t.Fatal(details)
			}
		})
	}
	// The TUI's lines stay exactly as they were.
	want := []string{"Runtime read: available", "Policy check: not configured", "Business reachability: unmeasured; use a separate application probe"}
	if got := adapterDetails(model.Check{Kind: "mihomo"}, fresh("ok", map[string]string{"mode": "rule"})); !slices.Equal(got, want) {
		t.Fatal(got)
	}
}

func TestCloudflareStatusMissingIsUnverified(t *testing.T) {
	check := model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "tunnel", ExpectedStatus: "healthy"}}}
	for _, tc := range []struct{ actual, want string }{{"", "unverified"}, {"healthy", "matched"}, {"down", "unverified"}} {
		state := model.CheckState{Freshness: "fresh", Last: &model.Observation{Outcome: "ok", Facts: map[string]string{"tunnel_status": tc.actual}}}
		if got := AdapterPolicy(check, state); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
