package probe

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflared"
	"github.com/WhitenWhiten/topophany/internal/adapters/mihomo"
	"github.com/WhitenWhiten/topophany/internal/adapters/tailscale"
	"github.com/WhitenWhiten/topophany/internal/adapters/wireguard"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// The supplied transport preserves the check's explicit route and TLS policy.
func (r Runner) mihomo(ctx context.Context, d Dialer, x model.Check, target string, o *model.Observation) error {
	deps := r.mihomoDependencies(d)
	deps.Cache = r.MihomoCache
	scope, _ := json.Marshal(struct {
		Dialer              Dialer
		Resolve, Server, CA string
	}{d, x.ResolveIP, x.ServerName, x.CAFile})
	deps.Scope = string(scope)
	return mihomo.Collect(ctx, deps, x, target, o)
}

// InspectMihomo reads a controller once, for configuring a check of it,
// through the transport and within the timeout that check's probe uses.
func (r Runner) InspectMihomo(ctx context.Context, c model.Config, x model.Check) (mihomo.Inspection, error) {
	resolved, err := config.ResolveCheck(c, x)
	if err != nil {
		return mihomo.Inspection{}, local("setup", "invalid_address_reference")
	}
	x = resolved
	if config.HTTPCredentials(c, x) != nil {
		return mihomo.Inspection{}, local("setup", "insecure_http_credentials")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(x.TimeoutSeconds)*time.Second)
	defer cancel()
	d := Dialer{Proxies: config.Path(c, x.Path).Proxies, BindIP: x.BindIP}
	return mihomo.Inspect(ctx, r.mihomoDependencies(d), x, config.Target(c, x))
}

func (r Runner) mihomoDependencies(d Dialer) mihomo.Dependencies {
	return mihomo.Dependencies{Secret: r.Secret, LocalViewer: r.LocalViewer, Client: func(x model.Check, target string) (*http.Client, func(), error) {
		u, err := url.Parse(target)
		if err != nil {
			return nil, nil, local("setup", "invalid_adapter_url")
		}
		name := u.Hostname()
		if x.ServerName != "" {
			name = x.ServerName
		}
		tc, err := TLSConfig(name, x.CAFile)
		if err != nil {
			return nil, nil, err
		}
		tr := restrictedTransport(tc, 0)
		tr.DialContext = func(ctx context.Context, n, a string) (net.Conn, error) {
			return d.DialContext(ctx, n, pinned(a, x.ResolveIP))
		}
		return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tr.CloseIdleConnections, nil
	}, Proxied: len(d.Proxies) > 0, ProxyCredentials: proxyCredentials(d.Proxies)}
}

// cloudflare reads Cloudflare's API through the check's explicit route, like
// the Mihomo controller read: the path's proxies are used if declared, but no
// environment proxy, no compression, no keep-alive and no redirect. A
// redirect would forward the bearer token to another location, so the client
// returns the 3xx to the adapter, which maps it to a local monitor error.
func (r Runner) cloudflare(ctx context.Context, d Dialer, x model.Check, target string, o *model.Observation) error {
	return cloudflare.Collect(ctx, cloudflare.Dependencies{Quota: r.CloudflareQuota, Client: func(x model.Check, target string) (*http.Client, func(), error) {
		u, err := url.Parse(target)
		if err != nil {
			return nil, nil, local("setup", "invalid_adapter_url")
		}
		name := u.Hostname()
		if x.ServerName != "" {
			name = x.ServerName
		}
		tc, err := TLSConfig(name, x.CAFile)
		if err != nil {
			return nil, nil, err
		}
		tr := restrictedTransport(tc, 0)
		tr.DialContext = func(ctx context.Context, n, a string) (net.Conn, error) {
			return d.DialContext(ctx, n, pinned(a, x.ResolveIP))
		}
		return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tr.CloseIdleConnections, nil
	}}, x, target, o)
}

// cloudflared reads the connector's own listener on this host. The target is
// a literal loopback URL by validation; the dialer refuses anything else so a
// changed address has no route and no fallback.
func (r Runner) cloudflared(ctx context.Context, x model.Check, target string, o *model.Observation) error {
	return cloudflared.Collect(ctx, cloudflared.Dependencies{Client: cloudflaredClient}, x, target, o)
}

// cloudflaredClient is the only transport the adapter gets. It reads the
// literal loopback listener directly: no proxy of the check's, no ambient
// proxy, no compression and no keep-alive. A dial that is not loopback is
// refused rather than routed elsewhere.
func cloudflaredClient(x model.Check, target string) (*http.Client, func(), error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, nil, local("setup", "invalid_adapter_url")
	}
	name := u.Hostname()
	if x.ServerName != "" {
		name = x.ServerName
	}
	tc, err := TLSConfig(name, x.CAFile)
	if err != nil {
		return nil, nil, err
	}
	tr := restrictedTransport(tc, 0)
	tr.DialContext = loopbackDial
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tr.CloseIdleConnections, nil
}

// loopbackDial accepts only a literal loopback address. The cloudflared
// listener is local; there is no direct or proxied fallback.
func loopbackDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, errors.New("cloudflared listener is not loopback")
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func (r Runner) tailscale(ctx context.Context, x model.Check, o *model.Observation) error {
	return tailscale.Collect(ctx, r.command, x.Adapter.Tailscale(), o)
}
func (r Runner) wireguard(ctx context.Context, x model.Check, o *model.Observation) error {
	return wireguard.Collect(ctx, r.command, x.Adapter.WireGuard(), o)
}
func safeFact(s string) string  { return adapters.SafeFact(s) }
func publicURL(s string) string { return adapters.PublicURL(s) }
