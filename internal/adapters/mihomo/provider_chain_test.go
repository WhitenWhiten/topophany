package mihomo_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func TestMihomoProviderOnlyChainAndAmbiguousNames(t *testing.T) {
	for _, tt := range []struct {
		name, mode, providers, outcome, code string
	}{
		{"provider leaf", "rule", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks"}]}}`, "ok", ""},
		{"dialer chain", "rule", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks","dialer-proxy":"DIRECT"}]}}`, "ok", ""},
		{"mode policy", "global", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks"}]}}`, "policy", "mode_mismatch"},
		{"duplicate names", "rule", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks"}]},"q":{"proxies":[{"name":"leaf","type":"VLESS"}]}}`, "unknown", "runtime_chain_unresolved"},
		{"provider cycle", "rule", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks","dialer-proxy":"leaf"}]}}`, "unknown", "runtime_chain_unresolved"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/configs":
					_, _ = w.Write([]byte(`{"mode":"` + tt.mode + `"}`))
				case "/proxies":
					_, _ = w.Write([]byte(`{"proxies":{"root":{"type":"Selector","now":"leaf","all":["leaf"]}}}`))
				case "/providers/proxies":
					reads++
					_, _ = w.Write([]byte(`{"providers":` + tt.providers + `}`))
				default:
					t.Error("unreviewed endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer s.Close()
			x := httpCheck(s.URL)
			x.Kind = "mihomo"
			x.Adapter = model.AdapterOptions{RootProxy: "root", ExpectedMode: "rule"}
			o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tt.outcome || o.Code != tt.code || reads != 1 {
				t.Fatalf("provider-only chain: %+v, reads=%d", o, reads)
			}
			if o.Outcome == "ok" && (o.Facts["reported_chain"] == "" || o.Facts["chain_resolution"] != "" || o.Connectivity || o.Evidence != "reported") {
				t.Fatalf("resolved chain lost evidence boundary: %+v", o)
			}
		})
	}
}

func TestMihomoProviderChainPreservesIndependentPolicyResults(t *testing.T) {
	for _, tc := range []struct {
		name, config, providers, outcome, code, policy string
	}{
		{"missing TUN, resolved chain", `{"mode":"rule"}`, `{"p":{"proxies":[{"name":"leaf","type":"VLESS"}]}}`, "unknown", "tun_state_unavailable", "unverified"},
		{"matching TUN, resolved chain", `{"mode":"rule","tun":{"enable":true}}`, `{"p":{"proxies":[{"name":"leaf","type":"VLESS"}]}}`, "ok", "", "matched"},
		{"TUN mismatch, resolved chain", `{"mode":"rule","tun":{"enable":false}}`, `{"p":{"proxies":[{"name":"leaf","type":"VLESS"}]}}`, "policy", "tun_mismatch", "violated"},
		{"mode mismatch, missing TUN", `{"mode":"global"}`, `{"p":{"proxies":[{"name":"leaf","type":"VLESS"}]}}`, "policy", "mode_mismatch", "violated"},
		{"TUN mismatch, unresolved chain", `{"mode":"rule","tun":{"enable":false}}`, `{}`, "policy", "tun_mismatch", "violated"},
		{"missing TUN, provider mismatch", `{"mode":"rule"}`, `{"p":{"proxies":[{"name":"leaf","type":"VLESS"}]}}`, "policy", "provider_membership_mismatch", "violated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/configs":
					_, _ = w.Write([]byte(tc.config))
				case "/proxies":
					_, _ = w.Write([]byte(`{"proxies":{"root":{"type":"Selector","now":"leaf"}}}`))
				case "/providers/proxies":
					_, _ = w.Write([]byte(`{"providers":` + tc.providers + `}`))
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer s.Close()
			wantTUN := true
			x := httpCheck(s.URL)
			x.Kind = "mihomo"
			x.Adapter = model.AdapterOptions{RootProxy: "root", ExpectedMode: "rule", ExpectedTUN: &wantTUN}
			if tc.code == "provider_membership_mismatch" {
				x.Adapter.ExpectedProviders = map[string][]string{"p": {"other"}}
			}
			o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tc.outcome || o.Code != tc.code {
				t.Fatalf("observation: %+v", o)
			}
			if policy := view.AdapterPolicy(x, model.CheckState{Freshness: "fresh", Last: &o}); policy != tc.policy {
				t.Fatalf("policy=%s observation=%+v", policy, o)
			}
			if tc.name == "missing TUN, resolved chain" && (o.Facts["tun_enabled"] != "unknown" || o.Facts["reported_chain"] != "leaf" || o.Facts["chain_resolution"] != "") {
				t.Fatalf("independent evidence lost: %+v", o)
			}
		})
	}
}
