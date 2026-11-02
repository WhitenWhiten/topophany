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
	Cache *SnapshotCache
	// Scope identifies the exact transport (route, bind, TLS and resolve settings).
	Scope            string
	Client           func(model.Check, string) (*http.Client, func(), error)
	Proxied          bool
	ProxyCredentials bool
	// Secret resolves token_env. Nil reads the process environment; a
	// local stream supplies its stored secrets without exporting them.
	Secret func(string) (string, error)
	// LocalViewer says the observation goes only to the local stream of
	// the user who supplied the secret and never leaves the machine. A
	// secret-bearing read then keeps proxy names, sanitized as for a public
	// controller; its other redactions remain. Workers never set it.
	LocalViewer bool
}

func (d Dependencies) secret(name string) (string, error) {
	if d.Secret != nil {
		return d.Secret(name)
	}
	return secure.SecretEnv(name)
}

// A check can have several independent policy and evidence results. Keep them
// separate until collection finishes: a resolved chain cannot erase an
// unrelated unknown, and a known violation takes precedence over an unknown.
type judgment struct {
	outcome, code, detail string
}

func summarizeJudgments(results ...judgment) judgment {
	result := judgment{outcome: "ok", detail: "read-only runtime telemetry collected; not an end-to-end service check"}
	for _, candidate := range results {
		if candidate.outcome == "policy" || candidate.outcome == "unknown" && result.outcome != "policy" {
			result = candidate
		}
	}
	return result
}

func Collect(ctx context.Context, deps Dependencies, x model.Check, target string, o *model.Observation) (err error) {
	options := x.Adapter.Mihomo()
	token, e := deps.secret(options.TokenEnv)
	if e != nil {
		return adapters.Local("setup", "adapter_secret_missing")
	}
	// A reference must surround a fresh runtime read, never a reused one.
	if deps.Cache != nil && options.ProviderReference == nil && x.MaxDurationMS == 0 {
		snapshot, release, acquireErr := deps.Cache.acquire(ctx, deps.Scope+"\x00"+target+"\x00"+token)
		if acquireErr != nil {
			return adapters.Local("adapter_api", "sampling_canceled")
		}
		defer release()
		if snapshot != nil {
			ctx = context.WithValue(ctx, snapshotContextKey{}, snapshot)
			o.SampledAt = snapshot.started.UTC()
			defer func() {
				if err != nil {
					snapshot.started = time.Time{}
					snapshot.bodies = nil
					snapshot.bytes = 0
				}
			}()
		}
	}
	u, e := url.Parse(target)
	if e != nil {
		return adapters.Local("setup", "invalid_adapter_url")
	}
	// A controller sent a secret says its mode in a closed word and names
	// only what the topology declares; see withheld. For a local viewer,
	// who holds the secret, its names are kept as a public controller's.
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
		mode = closedMode
	}
	if secret && !deps.LocalViewer {
		known := declared(options)
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
		if deps.LocalViewer {
			o.Facts["peer_text"] = adapters.NamesKeptLocal
		}
	}
	o.Facts["tun_enabled"] = "unknown"
	if cfg.TUN.Enable != nil {
		o.Facts["tun_enabled"] = strconv.FormatBool(*cfg.TUN.Enable)
	}
	o.Facts["provider_parameters"] = "not_verifiable_by_names_alone"
	o.Stage = "adapter"
	var modeResult, tunResult, chainResult, providerResult judgment
	if options.ExpectedMode != "" && cfg.Mode != options.ExpectedMode {
		modeResult = judgment{"policy", "mode_mismatch", "runtime mode differs from declared policy"}
	}
	if options.ExpectedTUN != nil {
		if cfg.TUN.Enable == nil {
			tunResult = judgment{"unknown", "tun_state_unavailable", "controller does not report TUN state"}
		} else if *cfg.TUN.Enable != *options.ExpectedTUN {
			tunResult = judgment{"policy", "tun_mismatch", "runtime TUN state differs from declared policy"}
		}
	}

	var chain []string
	var chainErr error
	recordChain := func(chain []string) {
		for i := range chain {
			chain[i] = name(chain[i])
		}
		o.Facts["reported_chain"] = adapters.SafeFact(strings.Join(chain, " -> "))
	}
	if options.RootProxy != "" {
		chain, chainErr = expandProxy(proxies.Proxies, options.RootProxy)
		if chainErr != nil {
			// Reserve a fact before optional provider load times consume the
			// observation budget. A resolved provider chain replaces it.
			o.Facts["chain_resolution"] = "unresolved"
		} else {
			recordChain(chain)
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
	policyBefore := modeResult.outcome == "policy" || tunResult.outcome == "policy"
	if err := collectProviders(ctx, deps, client, x, target, token, name, o, &providers, missing, policyBefore, &providerResult); err != nil {
		return err
	}
	// Newer cores keep subscription nodes only in the provider inventory.
	// Complete a missing chain from that separate read, without claiming an
	// atomic snapshot or guessing which duplicate provider name was selected.
	if missing {
		if err := mergeProviderProxies(proxies.Proxies, providers); err != nil {
			return err
		}
		chain, chainErr = expandProxy(proxies.Proxies, options.RootProxy)
		if chainErr == nil {
			delete(o.Facts, "chain_resolution")
			recordChain(chain)
		}
	}
	if options.RootProxy != "" && chainErr != nil {
		chainResult = judgment{"unknown", "runtime_chain_unresolved", "runtime chain unresolved (missing selection, unsupported shape or cycle)"}
	}
	result := summarizeJudgments(modeResult, tunResult, chainResult, providerResult)
	o.Outcome, o.Code, o.Detail = result.outcome, result.code, result.detail
	return nil
}
