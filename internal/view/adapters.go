package view

import (
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Software telemetry and a policy assertion answer different questions from an
// application response. Keep those answers visible without upgrading evidence.
func adapterDetails(check model.Check, state model.CheckState) []string {
	policy := AdapterPolicy(check, state)
	if policy == "" {
		return nil
	}
	runtime := "unverified"
	if last := state.Last; last != nil && state.Freshness == "fresh" {
		if last.Evidence == "reported" && ((last.Stage == "adapter" || last.Stage == "adapter_api") && last.Outcome != "error" || last.Facts["mode"] != "" || last.Facts["backend_state"] != "" || last.Facts["rx_bytes"] != "" || last.Facts["ready_connections"] != "" || last.Facts["ha_connections"] != "" || last.Facts["tunnel_status"] != "" || last.Facts["record_count"] != "" || last.Facts["pool_healthy"] != "") {
			runtime = "available"
		}
	}
	return []string{"Runtime read: " + runtime, "Policy check: " + strings.ReplaceAll(policy, "_", " "), "Business reachability: unmeasured; use a separate application probe"}
}

// AdapterPolicy is what a software adapter check's declared policy says of
// its current evidence: not_configured when it declares none, unverified
// until a fresh sample decides it, then matched or violated. It is "" for
// a check that is not a software adapter.
func AdapterPolicy(check model.Check, state model.CheckState) string {
	switch check.Kind {
	case "mihomo", "tailscale", "wireguard", "cloudflared", "cloudflare":
	default:
		return ""
	}
	policy := "not_configured"
	configured := check.Kind == "mihomo" && (check.Adapter.ExpectedTUN != nil || check.Adapter.ExpectedMode != "" || len(check.Adapter.ExpectedProviders) > 0 || check.Adapter.ProviderReference != nil)
	configured = configured || check.Kind == "wireguard" && (len(check.Adapter.ExpectedAllowedIPs) > 0 || check.Adapter.ExpectedKeepaliveSeconds != nil)
	configured = configured || check.Kind == "cloudflared" && check.Adapter.Cloudflared != nil && (check.Adapter.Cloudflared.ExpectedConnections != nil || len(check.Adapter.Cloudflared.ExpectedColos) > 0)
	configured = configured || check.Kind == "cloudflare" && cloudflarePolicyConfigured(check.Adapter.Cloudflare)
	if configured {
		policy = "unverified"
	}
	if last := state.Last; last != nil && state.Freshness == "fresh" {
		if policyViolation(check, last) {
			policy = "violated"
		} else if configured && last.Outcome == "ok" && policyEvidenceComplete(check, last) {
			policy = "matched"
		}
	}
	return policy
}

// Older workers could mark samples ok despite missing TUN or tunnel evidence.
// Never treat those stored samples as verified policy.
func policyEvidenceComplete(check model.Check, last *model.Observation) bool {
	if check.Kind == "mihomo" && check.Adapter.ExpectedTUN != nil {
		actual := last.Facts["tun_enabled"]
		if actual != "true" && actual != "false" {
			return false
		}
	}
	if check.Kind == "cloudflare" && check.Adapter.Cloudflare != nil {
		options := check.Adapter.Cloudflare
		if options.ExpectedConnections != nil || len(options.ExpectedColos) > 0 {
			count := last.Facts["tunnel_connections"]
			if count == "" {
				return false
			}
			if len(options.ExpectedColos) > 0 && (count == "0" || last.Facts["tunnel_colos"] == "") {
				return false
			}
		}
	}
	return true
}

// cloudflarePolicyConfigured reports whether the API read declares any state
// assertion. Without one the adapter still reports facts, but there is no
// policy to match or violate.
func cloudflarePolicyConfigured(options *model.CloudflareOptions) bool {
	return options != nil && (options.ExpectedConnections != nil || len(options.ExpectedColos) > 0 || options.ExpectedProxied != nil || options.ExpectedContent != "" || len(options.ExpectedOrigins) > 0)
}

func policyViolation(check model.Check, last *model.Observation) bool {
	if last.Outcome == "policy" || last.Facts["allowed_ips_match"] == "false" || last.Facts["keepalive_match"] == "false" {
		return true
	}
	if check.Kind != "mihomo" {
		return false
	}
	mode := last.Facts["mode"]
	if check.Adapter.ExpectedMode != "" && mode != "" && mode != "other" && mode != check.Adapter.ExpectedMode {
		return true
	}
	if want := check.Adapter.ExpectedTUN; want != nil {
		actual := last.Facts["tun_enabled"]
		return actual == "true" && !*want || actual == "false" && *want
	}
	return false
}
