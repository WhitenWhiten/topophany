package view

import (
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestCloudflareAdapterDetails(t *testing.T) {
	four := 4
	check := model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "tunnel", ExpectedConnections: &four}}}
	for _, tc := range []struct {
		name            string
		outcome, code   string
		stage           string
		facts           map[string]string
		fresh           bool
		runtime, policy string
	}{
		{name: "tunnel reported", outcome: "ok", stage: "adapter_api", facts: map[string]string{"tunnel_status": "healthy", "tunnel_connections": "4"}, fresh: true, runtime: "available", policy: "matched"},
		{name: "connections below", outcome: "policy", code: "connections_below_expected", stage: "adapter_api", facts: map[string]string{"tunnel_status": "degraded", "tunnel_connections": "2"}, fresh: true, runtime: "available", policy: "violated"},
		{name: "no connections reported", outcome: "unknown", code: "connections_unreported", stage: "adapter_api", facts: map[string]string{"tunnel_status": "degraded"}, fresh: true, runtime: "available", policy: "unverified"},
		{name: "legacy sample without connections", outcome: "ok", stage: "adapter_api", facts: map[string]string{"tunnel_status": "degraded"}, fresh: true, runtime: "available", policy: "unverified"},
		{name: "authentication failed", outcome: "error", code: "authentication_failed", stage: "adapter_api", fresh: true, runtime: "unverified", policy: "unverified"},
		{name: "stale", outcome: "ok", stage: "adapter_api", facts: map[string]string{"tunnel_status": "healthy"}, runtime: "unverified", policy: "unverified"},
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
	// Without a declared assertion the report is still available, but there is
	// nothing to match or violate.
	unconfigured := model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "tunnel"}}}
	state := model.CheckState{Freshness: "fresh", Last: &model.Observation{Evidence: "reported", Outcome: "ok", Stage: "adapter_api", Facts: map[string]string{"tunnel_status": "healthy"}}}
	if text := strings.Join(adapterDetails(unconfigured, state), "\n"); !strings.Contains(text, "Runtime read: available") || !strings.Contains(text, "Policy check: not configured") {
		t.Fatal(text)
	}
	if adapterDetails(model.Check{Kind: "http"}, model.CheckState{}) != nil {
		t.Fatal("application probes relabeled as platform telemetry")
	}
}
