package mihomo_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/adapters/mihomo"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestInspectListsRootGroupsInConfigurationOrderWithoutMembers(t *testing.T) {
	var mu sync.Mutex
	calls := []string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-local-secret" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/configs":
			_, _ = io.WriteString(w, `{"mode":"Rule","tun":{"enable":false}}`)
		case "/version":
			_, _ = io.WriteString(w, `{"meta":true,"version":"v1.19.99"}`)
		case "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{
				"GLOBAL":{"type":"Selector","now":"Proxy","all":["DIRECT","node-a","Proxy","Auto","Chain","Balance"]},
				"Proxy":{"type":"Selector","now":"node-a","all":["Auto","node-a","DIRECT"]},
				"Auto":{"type":"URLTest","now":"node-a","all":["node-a"]},
				"Chain":{"type":"Relay","all":["node-a"]},
				"Balance":{"type":"LoadBalance","all":["node-a"]},
				"Later":{"type":"Fallback","now":"node-a","all":["node-a"]},
				"bad‮name":{"type":"Selector","now":"node-a","all":["node-a"]},
				"node-a":{"type":"Shadowsocks"},
				"DIRECT":{"type":"Direct"}}}`)
		default:
			t.Error("unexpected read", r.URL.Path)
			http.Error(w, "no", 500)
		}
	}))
	defer s.Close()
	x := httpCheck(s.URL)
	x.Kind = "mihomo"
	x.Adapter = model.AdapterOptions{TokenEnv: "SYNTHETIC_LOCAL"}
	resolver := func(name string) (string, error) {
		if name == "SYNTHETIC_LOCAL" {
			return "synthetic-local-secret", nil
		}
		return "", errors.New("unknown")
	}
	got, err := (probe.Runner{Secret: resolver}).InspectMihomo(context.Background(), testkit.Config(), x)
	if err != nil {
		t.Fatal(err)
	}
	want := []mihomo.Group{{"Proxy", "Selector"}, {"Auto", "URLTest"}, {"Balance", "LoadBalance"}, {"Later", "Fallback"}, {"GLOBAL", "Selector"}}
	if got.Version != "v1.19.99" || got.Mode != "rule" || got.TUN == nil || *got.TUN || !reflect.DeepEqual(got.Groups, want) || got.Omitted != 1 {
		t.Fatalf("%+v", got)
	}
	if b, _ := json.Marshal(got); strings.Contains(string(b), "node-a") {
		t.Fatal("inspection named a member or selection", string(b))
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"GET /configs", "GET /version", "GET /proxies"}) {
		t.Fatal(calls)
	}
}

func TestInspectWithoutVersionOrSecretAndRefusedAuth(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/configs":
			if r.Header.Get("Authorization") != "" {
				http.Error(w, "unauthorized", 401)
				return
			}
			_, _ = io.WriteString(w, `{"mode":"global"}`)
		case "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	x := httpCheck(s.URL)
	x.Kind = "mihomo"
	got, err := (probe.Runner{}).InspectMihomo(context.Background(), testkit.Config(), x)
	if err != nil || got.Version != "" || got.Mode != "global" || got.TUN != nil || len(got.Groups) != 0 {
		t.Fatal(got, err)
	}
	x.Adapter.TokenEnv = "SYNTHETIC_LOCAL"
	_, err = (probe.Runner{Secret: func(string) (string, error) { return "wrong", nil }}).InspectMihomo(context.Background(), testkit.Config(), x)
	var f *adapters.Failure
	if !errors.As(err, &f) || f.Code != "authentication_failed" {
		t.Fatal(err)
	}
}

// A local viewer holds the secret it supplied: its secret-bearing reads keep
// the names a public controller's would, sanitized the same way, and every
// other secret redaction. A worker's read of the same controller is unchanged.
func TestSecretReadKeepsNamesOnlyForALocalViewer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-local-secret" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/configs":
			_, _ = io.WriteString(w, `{"mode":"Rule","tun":{"enable":false}}`)
		case "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{"Proxy":{"type":"Selector","now":"test-net‮-a","testUrl":"https://example.invalid/204?k=private","history":[{"time":"2026-01-01T00:00:00Z","delay":42}]},"test-net‮-a":{"type":"Shadowsocks","dialer-proxy":"entry"},"entry":{"type":"Shadowsocks"}}}`)
		case "/providers/proxies":
			_, _ = io.WriteString(w, `{"providers":{"pool":{"proxies":[{"name":"member-a"},{"name":"member-b"}],"updatedAt":"2026-01-01T00:00:00Z"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	x := httpCheck(s.URL)
	x.Kind = "mihomo"
	x.Adapter = model.AdapterOptions{TokenEnv: "SYNTHETIC_LOCAL", RootProxy: "Proxy", ExpectedMode: "rule", ExpectedProviders: map[string][]string{"pool": {"member-a"}}}
	resolver := func(string) (string, error) { return "synthetic-local-secret", nil }
	worker := (probe.Runner{Secret: resolver}).Run(context.Background(), testkit.Config(), x)
	local := (probe.Runner{Secret: resolver, LocalViewer: true}).Run(context.Background(), testkit.Config(), x)
	if worker.Facts["reported_chain"] != "undeclared -> undeclared" || worker.Facts["reported_selection"] != "undeclared" || worker.Facts["peer_text"] != adapters.Withheld || worker.Facts["provider.pool"] != "member-a,undeclared" {
		t.Fatal("worker redaction changed", worker.Facts)
	}
	if local.Facts["reported_chain"] != "entry -> test-net-a" || local.Facts["reported_selection"] != "test-net-a" || local.Facts["peer_text"] != adapters.NamesKeptLocal || local.Facts["provider.pool"] != "member-a,member-b" {
		t.Fatal("local viewer lost or did not sanitize names", local.Facts)
	}
	for _, o := range []model.Observation{worker, local} {
		if o.Outcome != "policy" || o.Code != "provider_membership_mismatch" || o.Facts["mode"] != "rule" || o.Facts["provider_comparison"] != "pool=mismatch" {
			t.Fatal(o.Outcome, o.Code, o.Facts)
		}
		for _, key := range []string{"test_url", "core_delay_ms", "core_last_checked", "provider_updated_at.pool"} {
			if _, ok := o.Facts[key]; ok {
				t.Fatal("secret-bearing read kept", key, o.Facts)
			}
		}
	}
}
