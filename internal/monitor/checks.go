package monitor

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// CheckDiagnostic is current observation evidence, not a second health verdict.
// An error outcome is a monitor error; policy confirmation remains the engine's.
type CheckDiagnostic struct {
	Outcome         string            `json:"outcome"`
	Evidence        string            `json:"evidence"`
	PathEvidence    string            `json:"path_evidence,omitempty"`
	Stage           string            `json:"stage,omitempty"`
	Code            string            `json:"code,omitempty"`
	PolicyConfirmed bool              `json:"policy_confirmed,omitempty"`
	Policy          string            `json:"policy,omitempty"`      // adapter checks only: view.AdapterPolicy
	DurationMS      *int64            `json:"duration_ms,omitempty"` // the observation's measured elapsed time
	Facts           map[string]string `json:"facts,omitempty"`
	Expected        map[string]string `json:"expected,omitempty"`
}

type CheckCard struct {
	SeenCheckValue
	Observer RefValue `json:"observer"`
	Service  string   `json:"service,omitempty"`
	Link     string   `json:"link,omitempty"`
	Path     string   `json:"path,omitempty"`
	Reason   string   `json:"reason"`
}

type CheckListEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Observer  string `json:"observer"`
	Service   string `json:"service,omitempty"`
	Link      string `json:"link,omitempty"`
	Health    string `json:"health"`
	Freshness string `json:"freshness"`
}

func (p *projection) indexChecks() {
	if p.checkStates != nil {
		return
	}
	p.checkStates = make(map[string]model.CheckState, len(p.v.Snapshot.Checks))
	p.checkDefs = make(map[string]model.Check, len(p.v.Snapshot.Topology.Checks))
	for _, c := range p.scalarSnapshot().Checks {
		p.checkStates[c.ID] = c
	}
	for _, c := range p.v.Snapshot.Topology.Checks {
		p.checkDefs[c.ID] = c
	}
}

func (p *projection) checkDiagnostic(id string) *CheckDiagnostic {
	p.indexChecks()
	c := p.checkStates[id]
	if p.v.Cached || c.Freshness != "fresh" || c.Last == nil {
		return nil
	}
	o := c.Last
	d := &CheckDiagnostic{Outcome: o.Outcome, Evidence: o.Evidence, PathEvidence: o.PathEvidence,
		Stage: o.Stage, Code: o.Code, PolicyConfirmed: c.PolicyConfirmed, Policy: view.AdapterPolicy(p.checkDefs[id], c),
		Facts: diagnosticFacts(p.checkDefs[id], o.Facts), Expected: expectedFacts(p.checkDefs[id])}
	if o.DurationMS >= 0 {
		elapsed := o.DurationMS
		d.DurationMS = &elapsed
	}
	return d
}

func (p *projection) check(id string) (CheckCard, bool) {
	p.indexChecks()
	d, ok := p.checkDefs[id]
	if !ok {
		return CheckCard{}, false
	}
	s, ok := p.checkStates[id]
	if !ok {
		return CheckCard{}, false
	} // never borrow another observer's evidence
	x := p.serverFleet().Check(d)
	x.Health, x.Freshness = s.Health, s.Freshness
	out := CheckCard{SeenCheckValue: p.seenCheck(x, p.samples[id]), Observer: RefValue{ID: d.Observer, Name: d.Observer}, Service: d.Service, Link: d.Link, Path: d.Path, Reason: s.Reason}
	for _, o := range p.v.Snapshot.Topology.Observers {
		if o.ID == d.Observer {
			out.Observer.Name = named(o.ID, o.Name)
			break
		}
	}
	ageSeenCheck(&out.SeenCheckValue, p.v.Now)
	return out, true
}

func cloneDiagnostic(d *CheckDiagnostic) *CheckDiagnostic {
	if d == nil {
		return nil
	}
	out := *d
	out.Facts, out.Expected = maps.Clone(d.Facts), maps.Clone(d.Expected)
	out.DurationMS = copyPointer(d.DurationMS)
	return &out
}

// Export selected, already sanitized observation facts only. Never export raw
// controller objects, URLs, credentials or arbitrary new adapter fields.
func diagnosticFacts(c model.Check, facts map[string]string) map[string]string {
	keys := strings.Fields("response_proof connection_semantics edge_provider edge_pop edge_cache edge_access edge_mitigation edge_rtt_ms trace_colo trace_warp trace_gateway trace_http trace_tls")
	shapes := map[string]factShape{}
	switch c.Kind {
	case "http", "tls":
		// Written only while tls_min_days asks for the peer certificate's expiry.
		keys = append(keys, "tls_days_remaining", "tls_not_after")
	case "mihomo":
		keys = append(keys, strings.Fields("mode tun_enabled reported_chain reported_selection chain_resolution core_delay_ms core_last_checked sampling_atomicity provider_parameters provider_comparison provider_reference_published_at provider_reference_consistency provider_reference_settling provider_reference_load_pending peer_text")...)
		providers := map[string]bool{}
		for name := range c.Adapter.ExpectedProviders {
			providers[name] = true
		}
		if r := c.Adapter.ProviderReference; r != nil {
			for _, name := range r.Providers {
				providers[name] = true
			}
		}
		for _, name := range slices.Sorted(maps.Keys(providers)) {
			keys = append(keys, "provider."+name, "provider_updated_at."+name)
		}
	case "tailscale", "wireguard":
		for _, f := range runtimeFacts[c.Kind] {
			keys = append(keys, f.key)
			shapes[f.key] = f.shape
		}
	case "cloudflared":
		keys = append(keys, strings.Fields("connector_ready metrics_status ready_connections ha_connections connection_colos requests_total request_errors active_streams connector_version")...)
	case "cloudflare":
		keys = append(keys, strings.Fields("tunnel_status tunnel_connections tunnel_pending_reconnect tunnel_colos oldest_connection_at record_count record_proxied record_ttl record_content_matches pool_healthy origins_healthy origins_total regions_reporting")...)
		for _, name := range c.Adapter.CloudflareOptions().ExpectedOrigins {
			keys = append(keys, "origin."+name)
		}
	}
	out := map[string]string{}
	for _, key := range keys {
		if shape := shapes[key]; shape != nil && !shape(facts[key]) {
			continue
		}
		if value, ok := facts[key]; ok && len(key) <= 128 && len(value) <= 2048 && len(out) < 32 {
			out[key] = value
		}
	}
	return out
}

// Local runtime adapters export only facts that are closed words, booleans,
// counts or timestamps: never addresses, hostnames, keys, peer text or other
// upstream strings, such as Tailscale's DERP region name in `relay`. Each is
// exported only in its closed shape. Another value, which an older worker
// may have written under the same key, is withheld.
type factShape func(string) bool

func oneOf(words ...string) factShape {
	return func(v string) bool { return slices.Contains(words, v) }
}
func countFact(v string) bool { _, err := strconv.ParseUint(v, 10, 64); return err == nil }
func timeFact(v string) bool  { _, err := time.Parse(time.RFC3339, v); return err == nil }

var booleanFact = oneOf("true", "false")

var runtimeFacts = map[string][]struct {
	key   string
	shape factShape
}{
	"tailscale": {
		{"backend_state", oneOf("NoState", "NeedsLogin", "NeedsMachineAuth", "Stopped", "Starting", "Running", "other")},
		{"online_reported", oneOf("true", "false", "unknown")},
		{"active_reported", booleanFact},
		{"health_warning_count", countFact},
		{"last_seen", func(v string) bool { return v == "not reported" || timeFact(v) }},
		{"transport_hint", oneOf("peer_relay_reported", "direct_endpoint_reported", "relay_preference_reported", "unknown")},
		{"diagnostic_protocol", oneOf("disco", "tsmp")},
		{"diagnostic_response", oneOf("received", "timed_out")},
		{"diagnostic_route", oneOf("direct", "derp", "peer_relay", "unknown")},
		{"diagnostic_latency_ms", countFact},
	},
	"wireguard": {
		{"sampling_atomicity", oneOf("separate_command_reads_not_atomic")},
		{"last_handshake", timeFact},
		{"rx_bytes", countFact},
		{"tx_bytes", countFact},
		{"allowed_ips_match", booleanFact},
		{"keepalive_match", booleanFact},
		{"keepalive_seconds", countFact},
	},
}

// Expectations are the check's own declared policy, never its private
// assertion values (http.json_equals) or credentials.
func expectedFacts(c model.Check) map[string]string {
	out := map[string]string{}
	if c.MaxDurationMS > 0 {
		out["max_duration_ms"] = strconv.Itoa(c.MaxDurationMS)
	}
	if c.TLSMinDays > 0 {
		out["tls_min_days"] = strconv.Itoa(c.TLSMinDays)
	}
	connections := func(n *int, colos []string) {
		if n != nil {
			out["connections"] = strconv.Itoa(*n)
		}
		if len(colos) > 0 {
			out["colos"] = strings.Join(colos, ",")
		}
	}
	switch c.Kind {
	case "mihomo":
		if c.Adapter.ExpectedMode != "" {
			out["mode"] = c.Adapter.ExpectedMode
		}
		if c.Adapter.ExpectedTUN != nil {
			out["tun_enabled"] = strconv.FormatBool(*c.Adapter.ExpectedTUN)
		}
	case "wireguard":
		// Expected AllowedIPs are addresses and stay out; allowed_ips_match
		// says whether the runtime matched them.
		if n := c.Adapter.ExpectedKeepaliveSeconds; n != nil {
			out["keepalive_seconds"] = strconv.Itoa(*n)
		}
	case "cloudflared":
		a := c.Adapter.CloudflaredOptions()
		connections(a.ExpectedConnections, a.ExpectedColos)
	case "cloudflare":
		a := c.Adapter.CloudflareOptions()
		connections(a.ExpectedConnections, a.ExpectedColos)
		if a.ExpectedProxied != nil {
			out["record_proxied"] = strconv.FormatBool(*a.ExpectedProxied)
		}
	}
	return out
}

func diagnosticState(d *CheckDiagnostic) any {
	if d == nil {
		return nil
	}
	return []any{d.Outcome, d.Evidence, d.PathEvidence, d.Stage, d.Code, d.PolicyConfirmed, d.Policy}
}
