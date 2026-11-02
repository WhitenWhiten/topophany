package model

import "slices"

// CloudflareOptions is the software-specific view of adapter.cloudflare, the
// read-only Cloudflare API adapter. One read is selected per check; only the
// fields that read needs are meaningful, and configuration validation refuses
// the others. The token itself lives in the worker's environment and is named
// by AdapterOptions.TokenEnv, never written here.
type CloudflareOptions struct {
	// Read selects the one allowlisted API read: tunnel, tunnel_connections, dns_record or pool.
	Read string `json:"read,omitempty"`
	// ExpectedStatus asserts the platform status word; tunnel reads only.
	ExpectedStatus string `json:"expected_status,omitempty"`
	// AccountID and TunnelID name the tunnel read's account and tunnel.
	AccountID string `json:"account_id,omitempty"`
	ZoneID    string `json:"zone_id,omitempty"`
	TunnelID  string `json:"tunnel_id,omitempty"`
	// RecordName and RecordType select the DNS record the dns_record read
	// asks for; RecordType is A, AAAA or CNAME.
	RecordName string `json:"record_name,omitempty"`
	RecordType string `json:"record_type,omitempty"`
	// ExpectedProxied declares whether the record should be proxied. Nil
	// asserts nothing.
	ExpectedProxied *bool `json:"expected_proxied,omitempty"`
	// ExpectedContent is compared with the record's content in memory and
	// never stored: the observation keeps only whether it matched.
	ExpectedContent string `json:"expected_content,omitempty"`
	// PoolID names the load balancer pool the pool read asks for.
	PoolID string `json:"pool_id,omitempty"`
	// ExpectedOrigins are the origin addresses the pool health report is
	// expected to name. Only these keys appear in the observation, as
	// origin.<declared>; an undeclared origin the report carries never does.
	ExpectedOrigins []string `json:"expected_origins,omitempty"`
	// ExpectedConnections is the number of live tunnel connections the
	// deployment declares. Below it is policy, not a failed request.
	ExpectedConnections *int `json:"expected_connections,omitempty"`
	// ExpectedColos lists uppercase three-letter city codes. Numbered API
	// locations such as lax12 are compared as LAX without merging sessions.
	ExpectedColos []string `json:"expected_colos,omitempty"`
}

// CloudflareOptions returns the typed view of the nested adapter
// configuration, detached from the adapter it was read from: slices and
// pointers are copies, not aliases.
func (a AdapterOptions) CloudflareOptions() CloudflareOptions {
	if a.Cloudflare == nil {
		return CloudflareOptions{}
	}
	v := *a.Cloudflare
	v.ExpectedOrigins = slices.Clone(a.Cloudflare.ExpectedOrigins)
	v.ExpectedColos = slices.Clone(a.Cloudflare.ExpectedColos)
	if a.Cloudflare.ExpectedProxied != nil {
		proxied := *a.Cloudflare.ExpectedProxied
		v.ExpectedProxied = &proxied
	}
	if a.Cloudflare.ExpectedConnections != nil {
		n := *a.Cloudflare.ExpectedConnections
		v.ExpectedConnections = &n
	}
	return v
}
