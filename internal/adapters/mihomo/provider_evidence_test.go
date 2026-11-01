package mihomo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/mihomo"
	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestMihomoLoadTimesPreserveMembershipAndAdmission(t *testing.T) {
	names := []string{strings.Repeat("a", 119), strings.Repeat("b", 108), strings.Repeat("c", 109), strings.Repeat("池", 39), "pool", "pool.updated_at"}
	loaded, expected := map[string]any{}, map[string][]string{}
	for i, name := range names {
		member := fmt.Sprintf("member-%d", i)
		expected[name] = []string{member}
		loaded[name] = map[string]any{"proxies": []map[string]string{{"name": member}}, "updatedAt": testControllerUpdated}
	}
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/configs":
			fmt.Fprint(w, `{"mode":"rule"}`)
		case "/proxies":
			fmt.Fprint(w, `{"proxies":{}}`)
		case "/providers/proxies":
			json.NewEncoder(w).Encode(map[string]any{"providers": loaded})
		default:
			t.Errorf("unexpected API read: %s", r.URL.Path)
		}
	}))
	defer controller.Close()
	c := testkit.Config()
	x := &c.Checks[0]
	x.Kind, x.Target = "mihomo", controller.URL
	x.Adapter.ExpectedProviders = expected
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	o := (probe.Runner{}).Run(context.Background(), c, *x)
	if o.Outcome != "ok" {
		t.Fatal(o)
	}
	for _, name := range names {
		if got := o.Facts["provider."+name]; got != expected[name][0] {
			t.Fatalf("membership of %q overwritten: %q", name, got)
		}
		key := "provider_updated_at." + name
		want := ""
		if len(key) <= 128 {
			want = testControllerUpdated.Format(time.RFC3339Nano)
		}
		if got := o.Facts[key]; got != want {
			t.Fatalf("load time for %d-byte name: got %q, want %q", len(name), got, want)
		}
	}
	// Test the wire parser and admission too: one invalid fact refuses the
	// entire batch, including an unrelated check with valid evidence.
	o.Boot, o.Sequence, o.ID = "boot", 1, x.Observer+".boot.1"
	b := model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{o, testkit.Observation(c, "home-ssh", 2, o.SampledAt, "ok")}}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := secure.JSONBudget(raw); err != nil {
		t.Fatal("batch fails wire budget", err)
	}
	s := api.New(c, "test", nil, api.Auth{})
	if code, status := s.ValidateBatch(b, x.Observer); code != "" {
		t.Fatalf("batch rejected: %d %s", status, code)
	}
}

func TestMihomoProviderOnlyChainFitsFactBudget(t *testing.T) {
	loaded, expected := map[string]any{}, map[string][]string{}
	for i := 0; i < 16; i++ {
		name, member := fmt.Sprintf("pool-%02d", i), fmt.Sprintf("leaf-%02d", i)
		expected[name] = []string{member}
		loaded[name] = map[string]any{"proxies": []map[string]string{{"name": member, "type": "VLESS"}}, "updatedAt": testControllerUpdated}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/configs":
			fmt.Fprint(w, `{"mode":"rule"}`)
		case "/proxies":
			fmt.Fprint(w, `{"proxies":{"root":{"type":"Selector","now":"leaf-00"}}}`)
		case "/providers/proxies":
			json.NewEncoder(w).Encode(map[string]any{"providers": loaded})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	x := httpCheck(server.URL)
	x.Kind = "mihomo"
	x.Adapter = model.AdapterOptions{RootProxy: "root", ExpectedProviders: expected}
	o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["reported_chain"] != "leaf-00" || config.ObservationBounds(o, config.MaxTook) != "" {
		t.Fatalf("provider chain exceeded fact budget or lost evidence: %+v", o)
	}
}

func TestMihomoFullReferenceEvidenceFitsFactBudget(t *testing.T) {
	t.Setenv("TOPH_BUDGET_TOKEN", "synthetic-controller-token")
	published := time.Now().UTC().Add(-20 * time.Second)
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint("pending=", pending), func(t *testing.T) {
			names, desired, loaded := []string{}, map[string][]string{}, map[string]any{}
			for i := 0; i < 16; i++ {
				name := fmt.Sprintf("pool-%02d", i)
				names = append(names, name)
				desired[name] = []string{"new"}
				at := published.Add(time.Second)
				if pending {
					at = published.Add(-time.Second)
				}
				loaded[name] = map[string]any{"proxies": []map[string]string{{"name": "old"}}, "updatedAt": at}
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/configs":
					fmt.Fprint(w, `{"mode":"rule","tun":{"enable":true}}`)
				case "/proxies":
					fmt.Fprint(w, `{"proxies":{"root":{"type":"Selector","now":"node","history":[{"time":"2026-10-17T06:00:00Z","delay":5}]},"node":{"type":"VLESS"}}}`)
				case "/providers/proxies":
					json.NewEncoder(w).Encode(map[string]any{"providers": loaded})
				case "/reference":
					json.NewEncoder(w).Encode(map[string]any{"schema": 1, "published_at": published, "providers": desired})
				default:
					t.Errorf("unexpected API read: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			x := testkit.Config().Checks[0]
			x.Kind, x.IntervalSeconds, x.FailureThreshold = "mihomo", 10, 2
			x.Adapter = model.AdapterOptions{TokenEnv: "TOPH_BUDGET_TOKEN", RootProxy: "root", ProviderReference: &model.MihomoProviderReference{URL: server.URL + "/reference", Providers: names, MaxAgeSeconds: 3600}}
			// Seed every fixed transport fact the Runner can add. The injected
			// client exercises collection over local TLS, not an actual proxy.
			o := model.Observation{SampledAt: time.Now().UTC(), Facts: map[string]string{"connection_semantics": "new_client_stream; upstream_core_multiplexing_unknown", "transport_hops": "1", "bind_ip": "127.0.0.1"}}
			deps := mihomo.Dependencies{Proxied: true, Client: func(model.Check, string) (*http.Client, func(), error) { return server.Client(), func() {}, nil }}
			if err := mihomo.Collect(context.Background(), deps, x, server.URL, &o); err != nil {
				t.Fatal(err)
			}
			if o.Outcome != "policy" || len(o.Facts) > model.MaxObservationFacts || config.ObservationBounds(o, config.MaxTook) != "" {
				t.Fatalf("full evidence cannot be admitted: %+v", o)
			}
			for _, name := range names {
				if o.Facts["provider."+name] != "undeclared" {
					t.Fatal("membership lost to optional facts", name)
				}
			}
			rows := []model.Observation{o, o}
			rows[0].SampledAt = o.SampledAt.Add(-10 * time.Second)
			if got := engine.Evaluate(x, rows, o.SampledAt); got.PolicyConfirmed == pending {
				t.Fatalf("budget changed the load evidence: %+v", got)
			}
		})
	}
}

func TestMihomoReferenceLoadEvidenceSurvivesMetadataBudget(t *testing.T) {
	published := time.Now().UTC().Add(-20 * time.Second)
	for _, tc := range []struct {
		name, pending string
		loaded        any
	}{
		{"missing", "true", nil},
		{"not yet loaded", "true", published.Add(-time.Second)},
		{"loaded", "false", published.Add(time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := strings.Repeat("p", 119)
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/configs":
					fmt.Fprint(w, `{"mode":"rule"}`)
				case "/proxies":
					fmt.Fprint(w, `{"proxies":{}}`)
				case "/providers/proxies":
					json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{name: map[string]any{"proxies": []map[string]string{{"name": "old"}}, "updatedAt": tc.loaded}}})
				case "/reference":
					json.NewEncoder(w).Encode(map[string]any{"schema": 1, "published_at": published, "providers": map[string][]string{name: {"new"}}})
				default:
					t.Errorf("unexpected API read: %s", r.URL.Path)
				}
			}))
			defer controller.Close()
			c := testkit.Config()
			x := &c.Checks[0]
			x.Kind, x.Target, x.IntervalSeconds, x.FailureThreshold = "mihomo", controller.URL, 10, 2
			x.Adapter.ProviderReference = &model.MihomoProviderReference{URL: controller.URL + "/reference", Providers: []string{name}, MaxAgeSeconds: 3600}
			if err := config.Validate(c); err != nil {
				t.Fatal(err)
			}
			o := (probe.Runner{}).Run(context.Background(), c, *x)
			if o.Outcome != "policy" || o.Facts["provider_reference_load_pending"] != tc.pending || o.Facts["provider_updated_at."+name] != "" {
				t.Fatal("load evidence depends on optional metadata", o)
			}
			if code := config.ObservationBounds(o, config.MaxTook); code != "" {
				t.Fatal(code, o)
			}
			rows := []model.Observation{o, o}
			rows[0].SampledAt = o.SampledAt.Add(-10 * time.Second)
			if got := engine.Evaluate(*x, rows, o.SampledAt); got.PolicyConfirmed != (tc.pending == "false") {
				t.Fatalf("optional load time changed confirmation: %+v", got)
			}
		})
	}
}
