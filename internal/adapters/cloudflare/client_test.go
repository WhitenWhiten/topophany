package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func testDependencies(t *testing.T) Dependencies {
	t.Helper()
	return Dependencies{Client: func(model.Check, string) (*http.Client, func(), error) {
		tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 32768}
		return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tr.CloseIdleConnections, nil
	}}
}

func adapterCheck(options model.CloudflareOptions) model.Check {
	return model.Check{ID: "api-read", Name: "Cloudflare API", Observer: "home", Kind: "cloudflare", TimeoutSeconds: 5,
		Adapter: model.AdapterOptions{TokenEnv: "TOPH_CLOUDFLARE_TEST_TOKEN", Cloudflare: &options}}
}

func failureCode(err error) string {
	var failure *adapters.Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

func TestReadOnceAllowlist(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, `{"success":true,"result":{}}`)
	}))
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, tc := range []struct {
		name    string
		request readRequest
		allowed bool
	}{
		{"tunnel", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID}, true},
		{"tunnel uppercase UUID", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + strings.ToUpper(testTunnelID)}, true},
		{"tunnel legacy compact", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + legacyTunnelID}, true},
		{"pool", readRequest{path: "/accounts/" + testAccountID + "/load_balancers/pools/" + testPoolID + "/health"}, true},
		{"dns", readRequest{path: "/zones/" + testZoneID + "/dns_records", query: map[string][]string{"name": {"www.example.com"}, "type": {"A"}}}, true},
		{"connections endpoint", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID + "/connections"}, false},
		{"tunnel extra suffix", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID + "x"}, false},
		{"tunnel extra prefix", readRequest{path: "/other/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID}, false},
		{"tunnel trailing slash", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID + "/"}, false},
		{"tunnel encoded slash", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID + "%2fconnections"}, false},
		{"tunnel encoded traversal", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID + "/%2e%2e"}, false},
		{"tunnel malformed UUID", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/1bedc50d4-2b3-473c-b108-ff3d10c0d925"}, false},
		{"tunnel UUID account", readRequest{path: "/accounts/" + testTunnelID + "/cfd_tunnel/" + testTunnelID}, false},
		{"pool UUID resource", readRequest{path: "/accounts/" + testAccountID + "/load_balancers/pools/" + testTunnelID + "/health"}, false},
		{"dns UUID zone", readRequest{path: "/zones/" + testTunnelID + "/dns_records", query: map[string][]string{"name": {"www.example.com"}, "type": {"A"}}}, false},
		{"traversal", readRequest{path: "/accounts/../cfd_tunnel/x"}, false},
		{"encoded traversal", readRequest{path: "/accounts/%2e%2e/cfd_tunnel/x"}, false},
		{"verify token", readRequest{path: "/user/tokens/verify"}, false},
		{"prefix", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel"}, false},
		{"suffix", readRequest{path: "/zones/" + testZoneID + "/dns_records/extra"}, false},
		{"dns extra query", readRequest{path: "/zones/" + testZoneID + "/dns_records", query: map[string][]string{"name": {"www.example.com"}, "type": {"A"}, "per_page": {"1000"}}}, false},
		{"dns wrong type", readRequest{path: "/zones/" + testZoneID + "/dns_records", query: map[string][]string{"name": {"www.example.com"}, "type": {"MX"}}}, false},
		{"dns query on tunnel", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID, query: map[string][]string{"name": {"x"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := hits.Load()
			_, err := readOnce(context.Background(), client, srv.URL, tc.request, "token", 1<<10)
			if tc.allowed {
				if err != nil {
					t.Fatalf("allowed read failed: %v", err)
				}
				return
			}
			if code := failureCode(err); code != "api_path_not_allowed" {
				t.Fatalf("code=%q err=%v", code, err)
			}
			if hits.Load() != before {
				t.Fatal("a disallowed path reached the server")
			}
		})
	}
	if got := hits.Load(); got != 5 {
		t.Fatalf("requests=%d want 5 allowed requests", got)
	}
}

func TestReadOnceStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"ok", 200, ""},
		{"unauthorized", 401, "authentication_failed"},
		{"forbidden", 403, "authentication_failed"},
		{"rate limited", 429, "http_429"},
		{"server error", 500, "http_500"},
		{"redirect", 302, "http_302"},
		{"not found", 404, "http_404"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, `{"success":true,"result":{}}`)
			}))
			defer srv.Close()
			client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			request, _ := readFor(tunnelOptions())
			_, err := readOnce(context.Background(), client, srv.URL, request, "token", 1<<10)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("200 refused: %v", err)
				}
				return
			}
			var failure *adapters.Failure
			if !errors.As(err, &failure) || failure.Code != tc.code || !failure.Local || failure.Stage != "adapter_api" {
				t.Fatalf("failure=%#v err=%v", failure, err)
			}
		})
	}
}

// Each read builds the documented exact path (and only the DNS read a query)
// and sends one GET with the bearer token after the allowlist check.
func TestReadOnceBuildsEachPath(t *testing.T) {
	for _, tc := range []struct {
		name, path, query string
		options           model.CloudflareOptions
	}{
		{"tunnel", "/client/v4/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID, "", tunnelOptions()},
		{"dns", "/client/v4/zones/" + testZoneID + "/dns_records", "name=www.example.com&type=A", dnsOptions()},
		{"pool", "/client/v4/accounts/" + testAccountID + "/load_balancers/pools/" + testPoolID + "/health", "", poolOptions()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var method, path, authorization, query string
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				method, path, query = r.Method, r.URL.Path, r.URL.RawQuery
				authorization = r.Header.Get("Authorization")
				io.WriteString(w, `{"success":true,"result":{}}`)
			}))
			defer srv.Close()
			client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			request, _ := readFor(tc.options)
			if _, err := readOnce(context.Background(), client, srv.URL+"/client/v4/", request, "test-token", 1<<10); err != nil {
				t.Fatal(err)
			}
			if got := hits.Load(); got != 1 || method != http.MethodGet {
				t.Fatalf("hits=%d method=%s", got, method)
			}
			if path != tc.path || query != tc.query {
				t.Fatalf("path=%s query=%s want %s?%s", path, query, tc.path, tc.query)
			}
			if authorization != "Bearer test-token" {
				t.Fatalf("authorization=%q", authorization)
			}
		})
	}
}

func TestReadOnceRefusesRedirect(t *testing.T) {
	var redirected atomic.Bool
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Store(true)
		io.WriteString(w, `{"success":true}`)
	}))
	defer targetSrv.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetSrv.URL, http.StatusFound)
	}))
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_, err := readOnce(context.Background(), client, srv.URL, readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID}, "token", 1<<10)
	if code := failureCode(err); code != "http_302" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	if redirected.Load() {
		t.Fatal("the bearer token followed a redirect")
	}
}

func TestReadOnceBudgetsAndShape(t *testing.T) {
	body := `{"success":true,"result":{}}`
	for _, tc := range []struct {
		name   string
		status int
		body   string
		limit  int64
		code   string
	}{
		{"oversized", 200, body, int64(len(body) - 1), "response_too_large"},
		{"invalid json", 200, `<html>`, 1 << 10, "unsupported_response_shape"},
		{"deep json", 200, strings.Repeat("[", 64) + strings.Repeat("]", 64), 1 << 10, "unsupported_response_shape"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			client := &http.Client{Transport: &http.Transport{Proxy: nil}}
			_, err := readOnce(context.Background(), client, srv.URL, readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID}, "token", tc.limit)
			if code := failureCode(err); code != tc.code {
				t.Fatalf("code=%q want %q err=%v", code, tc.code, err)
			}
		})
	}
}

func TestReadOnceNetworkFailureIsLocal(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	_, err := readOnce(context.Background(), client, "http://127.0.0.1:1", readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID}, "token", 1<<10)
	var failure *adapters.Failure
	if !errors.As(err, &failure) || !failure.Local || failure.Code != "api_unavailable" || failure.Stage != "adapter_api" {
		t.Fatalf("not a local api_unavailable failure: %#v %v", failure, err)
	}
}

func TestCollectOneGetPerCheck(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", "test-token")
	for _, tc := range []struct {
		options model.CloudflareOptions
		body    []byte
	}{
		{tunnelOptions(), fixture(t, "tunnel-healthy.json")},
		{dnsOptions(), fixture(t, "dns-records.json")},
		{poolOptions(), fixture(t, "pool-health.json")},
	} {
		t.Run(tc.options.Read, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Write(tc.body)
			}))
			defer srv.Close()
			o := observation()
			if err := Collect(context.Background(), testDependencies(t), adapterCheck(tc.options), srv.URL+"/client/v4", o); err != nil {
				t.Fatal(err)
			}
			if got := hits.Load(); got != 1 {
				t.Fatalf("requests=%d", got)
			}
			if o.PathEvidence != PathEvidence || o.Stage != "adapter_api" || o.Facts["peer_text"] != adapters.Withheld {
				t.Fatalf("envelope %+v", o)
			}
		})
	}
}

func TestCollectMissingToken(t *testing.T) {
	for _, tc := range []struct{ name, env, value string }{
		{"empty token_env", "", ""},
		{"unset variable", "TOPH_CLOUDFLARE_UNSET_TOKEN", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
			}
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
			defer srv.Close()
			x := adapterCheck(tunnelOptions())
			x.Adapter.TokenEnv = tc.env
			o := observation()
			err := Collect(context.Background(), testDependencies(t), x, srv.URL, o)
			var failure *adapters.Failure
			if !errors.As(err, &failure) || failure.Stage != "setup" || failure.Code != "cloudflare_token_missing" || !failure.Local {
				t.Fatalf("failure=%#v err=%v", failure, err)
			}
			if hits.Load() != 0 {
				t.Fatal("a request was sent without a token")
			}
		})
	}
}

func TestCollectSuccessFalseIsMonitorError(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", "test-token")
	for _, options := range []model.CloudflareOptions{tunnelOptions(), dnsOptions(), poolOptions()} {
		t.Run(options.Read, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"success":false,"errors":[{"code":10000,"message":"synthetic failure text"}],"result":null}`)
			}))
			defer srv.Close()
			o := observation()
			err := Collect(context.Background(), testDependencies(t), adapterCheck(options), srv.URL, o)
			var failure *adapters.Failure
			if !errors.As(err, &failure) || !failure.Local || failure.Code != "api_unavailable" {
				t.Fatalf("failure=%#v err=%v", failure, err)
			}
			if o.Outcome != "unknown" {
				t.Fatalf("outcome=%s", o.Outcome)
			}
		})
	}
}

// A peer that receives the token can put it in any string field. Every read
// must keep the observation free of it: the facts are closed words, numbers,
// shaped tokens and declared comparisons, and diagnostics are closed codes.
func TestBearerTokenCannotEchoIntoObservation(t *testing.T) {
	const token = "cf-test-token/with+symbols:and-more-than-a-shape"
	t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", token)
	quote := func(s string) string {
		raw, _ := json.Marshal(s)
		return string(raw)
	}
	escape := quote(token)
	short, long := quote(token[:3]), quote(token[:24])
	cases := []struct {
		name    string
		options model.CloudflareOptions
		body    string
		code    string
		facts   map[string]string
	}{
		{
			// The status is a closed word, so the connections are parsed and
			// every string field they carry is exercised.
			name:    "tunnel",
			options: tunnelOptions(),
			body: `{"success":true,"result":{"id":` + escape + `,"name":` + escape + `,"status":"healthy","connections":[` +
				`{"client_version":` + escape + `,"colo_name":` + escape + `,"opened_at":` + escape + `,"origin_ip":` + escape + `,"id":` + escape + `},` +
				`{"client_version":` + long + `,"colo_name":` + short + `,"opened_at":` + long + `,"origin_ip":` + long + `,"id":` + long + `}]}}`,
			facts: map[string]string{"tunnel_status": "healthy", "tunnel_connections": "2"},
		},
		{
			name:    "tunnel status",
			options: tunnelOptions(),
			body:    `{"success":true,"result":{"id":` + escape + `,"name":` + escape + `,"status":` + escape + `,"connections":[{"client_version":` + escape + `,"colo_name":` + escape + `}]}}`,
			code:    "unsupported_response_shape",
		},
		{
			name:    "dns",
			options: dnsOptions(),
			body:    `{"success":true,"result":[{"name":` + escape + `,"content":` + escape + `,"proxied":true,"ttl":300}]}`,
		},
		{
			name:    "pool",
			options: poolOptions(),
			body:    `{"success":true,"result":{"pool_id":` + escape + `,"pop_health":{` + escape + `:{"healthy":true,"origins":[{` + escape + `:{"healthy":true}}]}}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			o := observation()
			err := Collect(context.Background(), testDependencies(t), adapterCheck(tc.options), srv.URL, o)
			if code := failureCode(err); code != tc.code {
				t.Fatalf("code=%q want %q err=%v", code, tc.code, err)
			}
			for key, value := range tc.facts {
				if o.Facts[key] != value {
					t.Fatalf("%s=%q want %q: the echo was not read", key, o.Facts[key], value)
				}
			}
			raw, marshalErr := json.Marshal(o)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			// The whole token and any run of it long enough to matter.
			for _, echo := range []string{token, token[:24]} {
				if strings.Contains(string(raw), echo) {
					t.Fatalf("token reached the observation: %s", raw)
				}
			}
			for key, value := range o.Facts {
				if strings.Contains(key, token) || strings.Contains(value, token) {
					t.Fatalf("token in fact %q=%q", key, value)
				}
			}
			if err != nil && strings.Contains(err.Error(), token) {
				t.Fatalf("token in error: %v", err)
			}
		})
	}
}

// A 200 with success:false may carry a message that repeats request material;
// the adapter reads only the flag and never the message.
func TestErrorMessageTextIsNeverKept(t *testing.T) {
	const token = "cf-test-token/with+symbols:and-more-than-a-shape"
	t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", token)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":false,"errors":[{"code":10000,"message":"echo `+token+`"}],"result":null}`)
	}))
	defer srv.Close()
	o := observation()
	err := Collect(context.Background(), testDependencies(t), adapterCheck(tunnelOptions()), srv.URL, o)
	raw, _ := json.Marshal(o)
	if strings.Contains(string(raw), token) || err != nil && strings.Contains(err.Error(), token) {
		t.Fatal("error message text was kept")
	}
}

// Every read has a body budget; a body just over it is rejected as a monitor
// error rather than parsed.
func TestCollectResponseTooLarge(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", "test-token")
	body := `{"success":true,"result":{"status":"healthy","connections":[],"padding":"` + strings.Repeat("x", maxTunnelBytes) + `"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()
	err := Collect(context.Background(), testDependencies(t), adapterCheck(tunnelOptions()), srv.URL, observation())
	if code := failureCode(err); code != "response_too_large" {
		t.Fatalf("code=%q err=%v", code, err)
	}
}

// A credential that happens to have a reportable shape (a 24-character
// version token, a three-letter colo) is still dropped when the peer echoes it: shape
// validation alone must not let the token itself through.
func TestShapedTokenCannotEchoAsVersion(t *testing.T) {
	const token = "A1b2C3d4E5f6G7h8I9j0K1l2"
	if len(token) != 24 || !versionToken.MatchString(token) {
		t.Fatalf("test token is not a shaped version token: len=%d", len(token))
	}
	t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", token)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"result":{"status":"healthy","connections":[{"client_version":"`+token+`","colo_name":"HKG","opened_at":"2026-10-19T08:00:00Z"}]}}`)
	}))
	defer srv.Close()
	o := observation()
	if err := Collect(context.Background(), testDependencies(t), adapterCheck(tunnelOptions()), srv.URL, o); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(o)
	if strings.Contains(string(raw), token) {
		t.Fatalf("shaped token echoed: %s", raw)
	}
	if _, ok := o.Facts["connector_versions"]; ok {
		t.Fatalf("token was kept as a version: %+v", o.Facts)
	}
}

// A peer that splits the token over several shaped versions defeats an
// exact-match rule: each half passes the version shape. Every member that is a
// long run of the token is dropped, and with it the whole fact.
func TestTokenFragmentsCannotEchoAsVersions(t *testing.T) {
	const token = "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0"
	first, second := token[:24], token[16:]
	for _, fragment := range []string{first, second} {
		if !versionToken.MatchString(fragment) {
			t.Fatalf("test fragment is not a shaped version token: %q", fragment)
		}
	}
	t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", token)
	for _, tc := range []struct {
		name     string
		versions []string
		want     string
	}{
		{"fragments only", []string{first, second}, ""},
		{"fragment beside a version", []string{first, "2026.10.0"}, "2026.10.0"},
		{"eight bytes is a fragment", []string{token[3:11], "2026.10.0"}, "2026.10.0"},
		{"seven bytes is not", []string{token[3:10]}, token[3:10]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var connections []string
			for _, version := range tc.versions {
				connections = append(connections, `{"client_version":"`+version+`","colo_name":"HKG"}`)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"success":true,"result":{"status":"healthy","connections":[`+strings.Join(connections, ",")+`]}}`)
			}))
			defer srv.Close()
			o := observation()
			if err := Collect(context.Background(), testDependencies(t), adapterCheck(tunnelOptions()), srv.URL, o); err != nil {
				t.Fatal(err)
			}
			got, present := o.Facts["connector_versions"]
			if tc.want == "" && present || tc.want != "" && got != tc.want {
				t.Fatalf("connector_versions=%q present=%v want %q", got, present, tc.want)
			}
			raw, _ := json.Marshal(o)
			for _, fragment := range []string{first, second} {
				if strings.Contains(string(raw), fragment) {
					t.Fatalf("token fragment echoed: %s", raw)
				}
			}
			if o.Facts["tunnel_connections"] != strconv.Itoa(len(tc.versions)) {
				t.Fatalf("a count was erased: %+v", o.Facts)
			}
		})
	}
}

// scrubToken looks only at the facts that carry peer strings. A timestamp or a
// colo list that is a run of the token goes with it; a number never does, even
// when the token contains the same digits.
func TestScrubTokenFragmentsAndNumbers(t *testing.T) {
	const token = "xx2026-10-19T08:00:00Zyy"
	o := &model.Observation{Facts: map[string]string{
		"oldest_connection_at": "2026-10-19T08:00:00Z",
		"tunnel_colos":         "HKG,LAX",
		"connector_versions":   "10-19T08:00:00Z,2026.10.0",
		"tunnel_connections":   "2026",
		"origins_total":        "08",
	}}
	scrubToken(o, token)
	want := map[string]string{
		"tunnel_colos":       "HKG,LAX",
		"connector_versions": "2026.10.0",
		"tunnel_connections": "2026",
		"origins_total":      "08",
	}
	if len(o.Facts) != len(want) {
		t.Fatalf("facts=%+v", o.Facts)
	}
	for key, value := range want {
		if o.Facts[key] != value {
			t.Fatalf("%s=%q want %q", key, o.Facts[key], value)
		}
	}
}

// The detail of a read whose connection list was not reported is the adapter's
// own; the generic detail applies only when none was set.
func TestCollectKeepsTunnelDetail(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", "test-token")
	for _, tc := range []struct {
		name, body, detail string
	}{
		{"list not reported", `{"success":true,"result":{"status":"healthy","connections":[]}}`, "Cloudflare API state reported; the connections list was not reported"},
		{"list reported", string(fixture(t, "tunnel-healthy.json")), "Cloudflare API state reported; no request to the zone's own hostname was made"},
		{"zero is a count", `{"success":true,"result":{"status":"down","connections":[]}}`, "Cloudflare API state reported; no request to the zone's own hostname was made"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			o := observation()
			if err := Collect(context.Background(), testDependencies(t), adapterCheck(tunnelOptions()), srv.URL, o); err != nil {
				t.Fatal(err)
			}
			if o.Outcome != "ok" || o.Detail != tc.detail || o.Stage != "adapter_api" {
				t.Fatalf("outcome=%s stage=%s detail=%q", o.Outcome, o.Stage, o.Detail)
			}
		})
	}
}

func TestReadOnceExactBudgetBoundary(t *testing.T) {
	body := `{"success":true}`
	for _, tc := range []struct {
		name  string
		limit int64
		ok    bool
	}{
		{"exactly at budget", int64(len(body)), true},
		{"one over budget", int64(len(body)) - 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, body)
			}))
			defer srv.Close()
			client := &http.Client{Transport: &http.Transport{Proxy: nil}}
			_, err := readOnce(context.Background(), client, srv.URL, readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID}, "token", tc.limit)
			if tc.ok && err != nil {
				t.Fatal(err)
			}
			if !tc.ok && failureCode(err) != "response_too_large" {
				t.Fatalf("code=%q err=%v", failureCode(err), err)
			}
		})
	}
}

func TestReadOnceCancellationIsMonitorError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	_, err := readOnce(ctx, client, srv.URL, readRequest{path: "/accounts/" + testAccountID + "/cfd_tunnel/" + testTunnelID}, "token", 1<<10)
	var failure *adapters.Failure
	if !errors.As(err, &failure) || !failure.Local || failure.Code != "api_unavailable" || failure.Stage != "adapter_api" {
		t.Fatalf("failure=%#v err=%v", failure, err)
	}
}
