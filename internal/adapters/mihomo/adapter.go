// Package mihomo collects read-only Mihomo runtime telemetry.
package mihomo

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Dependencies are supplied by the probe, preserving its explicit route and TLS policy.
type Dependencies struct {
	Client           func(model.Check, string) (*http.Client, func(), error)
	Proxied          bool
	ProxyCredentials bool
	// Secret resolves token_env. Nil reads the process environment; a
	// local stream supplies its stored secrets without exporting them.
	Secret func(string) (string, error)
}

func (d Dependencies) secret(name string) (string, error) {
	if d.Secret != nil {
		return d.Secret(name)
	}
	return secure.SecretEnv(name)
}

func Collect(ctx context.Context, deps Dependencies, x model.Check, target string, o *model.Observation) (err error) {
	options := x.Adapter.Mihomo()
	token, e := deps.secret(options.TokenEnv)
	if e != nil {
		return adapters.Local("setup", "adapter_secret_missing")
	}
	u, e := url.Parse(target)
	if e != nil {
		return adapters.Local("setup", "invalid_adapter_url")
	}
	// A controller sent a secret says its mode in a closed word and names
	// only what the topology declares; see withheld.
	secret := token != "" || u.Scheme != "https" && deps.ProxyCredentials
	if secret {
		defer func() {
			for key := range o.Facts {
				if key == "core_delay_ms" || key == "core_last_checked" || strings.HasPrefix(key, "provider_updated_at.") {
					delete(o.Facts, key)
				}
			}
			var failure *adapters.Failure
			if errors.As(err, &failure) && strings.HasPrefix(failure.Code, "http_") {
				copy := *failure
				copy.Code = "unexpected_status"
				err = &copy
			}
		}()
	}
	mode, name := adapters.SafeFact, func(s string) string { return s }
	if secret {
		known := declared(options)
		mode = closedMode
		name = func(s string) string {
			if known[s] {
				return s
			}
			return "undeclared"
		}
	}
	client, closeClient, e := deps.Client(x, target)
	if e != nil {
		return e
	}
	defer closeClient()
	var cfg struct {
		Mode string `json:"mode"`
		TUN  struct {
			Enable *bool `json:"enable"`
		} `json:"tun"`
	}
	if e = readCore(ctx, client, target, "/configs", token, &cfg); e != nil {
		return e
	}
	if cfg.Mode == "" {
		return adapters.Local("adapter_api", "missing_mode")
	}
	var proxies struct {
		Proxies map[string]coreProxy `json:"proxies"`
	}
	if e = readCore(ctx, client, target, "/proxies", token, &proxies); e != nil {
		return e
	}
	if proxies.Proxies == nil {
		return adapters.Local("adapter_api", "missing_proxies")
	}
	o.PathEvidence = "runtime_reported_not_request_verified"
	o.Facts["sampling_atomicity"] = "separate_API_reads_not_atomic"
	o.Facts["mode"] = mode(cfg.Mode)
	if secret {
		o.Facts["peer_text"] = adapters.Withheld
	}
	o.Facts["tun_enabled"] = "unknown"
	if cfg.TUN.Enable != nil {
		o.Facts["tun_enabled"] = strconv.FormatBool(*cfg.TUN.Enable)
	}
	o.Facts["provider_parameters"] = "not_verifiable_by_names_alone"
	o.Outcome = "ok"
	o.Stage = "adapter"
	o.Detail = "read-only runtime telemetry collected; not an end-to-end service check"
	if options.ExpectedMode != "" && cfg.Mode != options.ExpectedMode {
		o.Outcome = "policy"
		o.Code = "mode_mismatch"
		o.Detail = "runtime mode differs from declared policy"
	}
	if options.ExpectedTUN != nil {
		if cfg.TUN.Enable == nil && o.Outcome != "policy" {
			o.Outcome, o.Code = "unknown", "tun_state_unavailable"
			o.Detail = "controller does not report TUN state"
		}
		if cfg.TUN.Enable != nil && *cfg.TUN.Enable != *options.ExpectedTUN {
			o.Outcome, o.Code = "policy", "tun_mismatch"
			o.Detail = "runtime TUN state differs from declared policy"
		}
	}

	var chainErr error
	if options.RootProxy != "" {
		chain, e := expandProxy(proxies.Proxies, options.RootProxy)
		chainErr = e
		if e != nil {
			o.Facts["chain_resolution"] = "unresolved"
			if o.Outcome != "policy" {
				o.Outcome = "unknown"
				o.Code = "runtime_chain_unresolved"
				o.Detail = "runtime chain unresolved (missing selection, unsupported shape or cycle)"
			}
		} else {
			for i := range chain {
				chain[i] = name(chain[i])
			}
			o.Facts["reported_chain"] = adapters.SafeFact(strings.Join(chain, " -> "))
		}
		if p, ok := proxies.Proxies[options.RootProxy]; ok {
			o.Facts["reported_selection"] = adapters.SafeFact(name(p.Now))
			if !secret {
				o.Facts["test_url"] = adapters.PublicURL(p.TestURL)
			}
			if !secret && len(p.History) > 0 {
				last := p.History[len(p.History)-1]
				o.Facts["core_last_checked"] = last.Time.UTC().Format(time.RFC3339)
				o.Facts["core_delay_ms"] = strconv.Itoa(last.Delay)
			}
		}
	}
	var providers coreProviders
	missing := errors.Is(chainErr, errMissingProxy)
	if err := collectProviders(ctx, deps, client, x, target, token, name, o, &providers, missing); err != nil {
		return err
	}
	// Newer cores keep subscription nodes only in the provider inventory.
	// Complete a missing chain from that separate read, without claiming an
	// atomic snapshot or guessing which duplicate provider name was selected.
	if missing {
		if err := mergeProviderProxies(proxies.Proxies, providers); err != nil {
			return err
		}
		if chain, err := expandProxy(proxies.Proxies, options.RootProxy); err == nil {
			for i := range chain {
				chain[i] = name(chain[i])
			}
			delete(o.Facts, "chain_resolution")
			o.Facts["reported_chain"] = adapters.SafeFact(strings.Join(chain, " -> "))
			if o.Outcome == "unknown" && o.Code == "runtime_chain_unresolved" {
				o.Outcome, o.Code = "ok", ""
				o.Detail = "read-only runtime telemetry collected; not an end-to-end service check"
			}
		}
	}
	return nil
}
