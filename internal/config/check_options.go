package config

import (
	"maps"
	"slices"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Clone and public projection deliberately visit all option blocks, including
// inactive ones: the editor preserves them across kind switches, and inactive
// credentials must never become public. Measurement selects only active blocks
// through the kind catalog without changing the wire configuration.
func cloneCheckOptions(x model.Check) model.Check {
	if x.Host != nil {
		h := *x.Host
		x.Host = &h
	}
	if x.External != nil {
		b := *x.External
		if x.External.Assertions != nil {
			a := *x.External.Assertions
			b.Assertions = &a
		}
		x.External = &b
	}
	x.HTTP.JSONEquals = maps.Clone(x.HTTP.JSONEquals)
	for key, value := range x.HTTP.JSONEquals {
		x.HTTP.JSONEquals[key] = slices.Clone(value)
	}
	x.HTTP.Status = slices.Clone(x.HTTP.Status)
	x.HTTP.ExpectedExit = slices.Clone(x.HTTP.ExpectedExit)
	x.HTTP.ExpectedPops = slices.Clone(x.HTTP.ExpectedPops)
	x.HTTP.Headers, x.HTTP.RequestHeaders = maps.Clone(x.HTTP.Headers), maps.Clone(x.HTTP.RequestHeaders)
	x.HTTP.RequestHeaderEnvs = maps.Clone(x.HTTP.RequestHeaderEnvs)
	if trace := x.HTTP.ExpectedTrace; trace != nil {
		copy := *trace
		copy.Colos = slices.Clone(trace.Colos)
		x.HTTP.ExpectedTrace = &copy
	}
	x.DNS.ExpectedIPs = slices.Clone(x.DNS.ExpectedIPs)
	if ref := x.Adapter.ProviderReference; ref != nil {
		v := *ref
		v.Providers = slices.Clone(ref.Providers)
		if ref.SettleSeconds != nil {
			settle := *ref.SettleSeconds
			v.SettleSeconds = &settle
		}
		x.Adapter.ProviderReference = &v
	}
	x.Adapter.ProviderMatchModes = maps.Clone(x.Adapter.ProviderMatchModes)
	if value := x.Adapter.ExpectedTUN; value != nil {
		copy := *value
		x.Adapter.ExpectedTUN = &copy
	}
	x.Adapter.ExpectedAllowedIPs = slices.Clone(x.Adapter.ExpectedAllowedIPs)
	if value := x.Adapter.ExpectedKeepaliveSeconds; value != nil {
		copy := *value
		x.Adapter.ExpectedKeepaliveSeconds = &copy
	}
	if cloud := x.Adapter.Cloudflared; cloud != nil {
		v := *cloud
		v.ExpectedColos = slices.Clone(cloud.ExpectedColos)
		if cloud.ExpectedConnections != nil {
			n := *cloud.ExpectedConnections
			v.ExpectedConnections = &n
		}
		x.Adapter.Cloudflared = &v
	}
	if api := x.Adapter.Cloudflare; api != nil {
		v := *api
		v.ExpectedOrigins = slices.Clone(api.ExpectedOrigins)
		v.ExpectedColos = slices.Clone(api.ExpectedColos)
		if api.ExpectedProxied != nil {
			proxied := *api.ExpectedProxied
			v.ExpectedProxied = &proxied
		}
		if api.ExpectedConnections != nil {
			n := *api.ExpectedConnections
			v.ExpectedConnections = &n
		}
		x.Adapter.Cloudflare = &v
	}
	x.Adapter.ExpectedProviders = maps.Clone(x.Adapter.ExpectedProviders)
	for k, names := range x.Adapter.ExpectedProviders {
		x.Adapter.ExpectedProviders[k] = slices.Clone(names)
	}
	return x
}

func publicCheckOptions(x model.Check) model.Check {
	x.Target = PublicURL(x.Target)
	x.HTTP.JSONEquals = nil
	x.HTTP.BearerEnv = ""
	x.Adapter.TokenEnv = ""
	if ref := x.Adapter.ProviderReference; ref != nil {
		ref.URL, ref.TokenEnv, ref.CAFile = PublicURL(ref.URL), "", ""
	}
	x.HTTP.RequestHeaders = nil
	x.HTTP.RequestHeaderEnvs = nil
	x.CAFile = ""
	return x
}
