package management

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

var ErrNetworkPolicy = errors.New("management_network_denied")

func (v Constraints) allowsIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	for _, raw := range v.CIDRs {
		prefix, e := netip.ParsePrefix(raw)
		if e == nil && prefix.Contains(ip) {
			return true
		}
	}
	return false
}
func targetHost(c model.Config, x model.Check) (string, []model.Proxy, error) {
	resolved, e := config.ResolveCheck(c, x)
	if e != nil {
		return "", nil, e
	}
	x = resolved
	target := config.Target(c, x)
	path := x.Path
	pin := x.ResolveIP
	switch x.Kind {
	case "host", "tailscale", "wireguard":
		return "", nil, nil
	case "external":
		if x.External == nil {
			return "", nil, ErrNetworkPolicy
		}
		s, ok := config.Source(c, x.External.Source)
		if !ok {
			return "", nil, ErrNetworkPolicy
		}
		target = s.Target
		path = s.Path
		pin = s.ResolveIP
	}
	if pin != "" && x.Kind != "icmp" {
		return pin, config.Path(c, path).Proxies, nil
	}
	if strings.Contains(target, "://") {
		u, e := url.Parse(target)
		if e != nil || u.RawQuery != "" || u.User != nil {
			return "", nil, ErrNetworkPolicy
		}
		return u.Hostname(), config.Path(c, path).Proxies, nil
	}
	if ip, e := netip.ParseAddr(target); e == nil {
		return ip.String(), config.Path(c, path).Proxies, nil
	}
	host, _, e := net.SplitHostPort(target)
	if e != nil {
		return "", nil, ErrNetworkPolicy
	}
	return host, config.Path(c, path).Proxies, nil
}
func (v Constraints) Transport(c model.Config, x model.Check) error {
	switch x.Kind {
	case "http", "tcp", "tls", "ssh-banner", "dns", "udp-echo", "icmp", "mihomo", "tailscale", "wireguard", "host", "cloudflared", "cloudflare", "external":
	default:
		return errors.New("managed_check_kind_not_supported")
	}
	host, proxies, e := targetHost(c, x)
	if e != nil {
		return e
	}
	if host == "" {
		return nil
	}
	if len(v.CIDRs) == 0 {
		return errors.New("managed_target_requires_network_scope")
	}
	if ip, e := netip.ParseAddr(host); e == nil {
		if !v.allowsIP(ip) {
			return ErrNetworkPolicy
		}
	} else if len(proxies) > 0 || x.Kind == "icmp" {
		return errors.New("managed_proxy_dns_not_supported")
	}
	for _, proxy := range proxies {
		h, _, e := net.SplitHostPort(proxy.Address)
		if e != nil {
			return ErrNetworkPolicy
		}
		ip, e := netip.ParseAddr(h)
		if e != nil || !v.allowsIP(ip) {
			return errors.New("managed_proxy_requires_authorized_literal_ip")
		}
	}
	return nil
}

// ExecutionBinding includes private credential references deliberately omitted
// from external measurement identity; preserving history must not grant secret use.
func ExecutionBinding(c model.Config, x model.Check) string {
	credential := ""
	if x.External != nil {
		if s, ok := config.Source(c, x.External.Source); ok {
			credential = s.CredentialEnv
		}
	}
	return Digest(Marshal(struct {
		Contract   string `json:"contract"`
		Credential string `json:"credential_env"`
	}{config.Contract(c, x), credential}))
}
func (a *Agent) Dial(ctx context.Context, d *net.Dialer, network, address string) (net.Conn, error) {
	if !a.Managed() {
		return d.DialContext(ctx, network, address)
	}
	v := a.constraints.Load()
	if v == nil {
		return nil, ErrNetworkPolicy
	}
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, ErrNetworkPolicy
	}
	var ips []netip.Addr
	if ip, e := netip.ParseAddr(host); e == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, e = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil {
			return nil, e
		}
	}
	if len(ips) == 0 {
		return nil, ErrNetworkPolicy
	}
	for _, ip := range ips {
		if !v.allowsIP(ip) {
			return nil, ErrNetworkPolicy
		}
	}
	var last error
	for _, ip := range ips {
		conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	return nil, last
}
