package local

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func networkCheck(adapter, address string) Check {
	c := Check{ID: "lc-net", Name: "Site", Adapter: adapter, Interval: 60, Timeout: 5, Stale: 180}
	if adapter == "http" {
		c.URL = address
	} else {
		c.Target = address
	}
	return c
}

func TestNetworkCheckAddressesAndBounds(t *testing.T) {
	for _, c := range []Check{
		networkCheck("http", "https://example.com"),
		networkCheck("http", "http://192.0.2.10:8080/health?probe=1"),
		networkCheck("tcp", "example.com:22"),
		networkCheck("tcp", "[2001:db8::1]:443"),
		networkCheck("tls", "example.com:443"),
		networkCheck("icmp", "192.0.2.1"),
	} {
		if err := c.Validate(); err != nil {
			t.Errorf("%s %s%s refused: %v", c.Adapter, c.URL, c.Target, err)
		}
	}
	for _, c := range []Check{
		networkCheck("http", "example.com"),
		networkCheck("http", "ftp://example.com"),
		networkCheck("http", "https://user:pass@example.com"),
		networkCheck("http", "https://example.com/#top"),
		networkCheck("http", "https://"),
		networkCheck("http", "https://example.com/ a"),
		networkCheck("http", "https://${site}/"),
		networkCheck("http", ""),
		networkCheck("tcp", "example.com"),
		networkCheck("tcp", "example.com:0"),
		networkCheck("tcp", "https://example.com:443"),
		networkCheck("tls", "-oProxyCommand=x:443"),
		networkCheck("icmp", "example.com"),
		networkCheck("icmp", "2001:db8::1"),
		{ID: "lc-net", Name: "Both", Adapter: "http", URL: "https://example.com", Target: "example.com:443", Interval: 60, Timeout: 5, Stale: 180},
		{ID: "lc-net", Name: "Both", Adapter: "tcp", URL: "https://example.com", Target: "example.com:443", Interval: 60, Timeout: 5, Stale: 180},
	} {
		if err := c.Validate(); err == nil || AsError(err).Code != "invalid_target" {
			t.Errorf("%s %q %q: %v", c.Adapter, c.URL, c.Target, err)
		}
	}
	https := networkCheck("http", "https://example.com")
	for name, change := range map[string]func(*Check){
		"status low":          func(c *Check) { c.ExpectedStatus = 99 },
		"status high":         func(c *Check) { c.ExpectedStatus = 600 },
		"status on tcp":       func(c *Check) { *c = networkCheck("tcp", "example.com:22"); c.ExpectedStatus = 200 },
		"expiry on http":      func(c *Check) { c.URL = "http://example.com"; c.TLSMinDays = 14 },
		"expiry on tcp":       func(c *Check) { *c = networkCheck("tcp", "example.com:443"); c.TLSMinDays = 14 },
		"expiry on icmp":      func(c *Check) { *c = networkCheck("icmp", "192.0.2.1"); c.TLSMinDays = 14 },
		"expiry range":        func(c *Check) { c.TLSMinDays = 3651 },
		"budget range":        func(c *Check) { c.MaxDurationMS = 300001 },
		"engine field":        func(c *Check) { c.RootProxy = "Proxy" },
		"engine mode":         func(c *Check) { c.ExpectedMode = "rule" },
		"engine tun":          func(c *Check) { no := false; c.ExpectedTUN = &no },
		"engine with target":  func(c *Check) { *c = syntheticCheck("http://127.0.0.1:9090"); c.Target = "example.com:443" },
		"engine with status":  func(c *Check) { *c = syntheticCheck("http://127.0.0.1:9090"); c.ExpectedStatus = 200 },
		"engine with expiry":  func(c *Check) { *c = syntheticCheck("https://localhost:9090"); c.TLSMinDays = 14 },
		"unknown kind of net": func(c *Check) { c.Adapter = "dns" },
	} {
		c := https
		change(&c)
		if err := c.Validate(); err == nil || AsError(err).Code != "invalid_request" {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	tls := networkCheck("tls", "Example.COM:443")
	tls.TLSMinDays, tls.MaxDurationMS = 30, 800
	if err := tls.Validate(); err != nil || tls.Normalized().Target != "example.com:443" {
		t.Fatal(tls.Normalized(), err)
	}
	if got := networkCheck("http", "HTTPS://Example.COM/Path?q=1").Normalized().URL; got != "https://example.com/Path?q=1" {
		t.Fatal(got)
	}
	if got := networkCheck("icmp", "192.0.2.1").Normalized().Target; got != "192.0.2.1" {
		t.Fatal(got)
	}
	// What an engine's stored JSON never had stays out of it.
	b, _ := json.Marshal(syntheticCheck("http://127.0.0.1:9090"))
	for _, key := range []string{"target", "expected_status", "tls_min_days", "max_duration_ms"} {
		if strings.Contains(string(b), key) {
			t.Fatal("engine check gained", key, string(b))
		}
	}
	var decoded Check
	if err := json.Unmarshal([]byte(`{"id":"lc-1","name":"Site","adapter":"http","url":"https://example.com","interval_seconds":60,"tls_min_days":14,"expected_status":204,"max_duration_ms":800}`), &decoded); err != nil || decoded.Validate() != nil || decoded.TLSMinDays != 14 || decoded.ExpectedStatus != 204 || decoded.MaxDurationMS != 800 || decoded.Stale != 180 {
		t.Fatal(decoded, err)
	}
}

func TestNetworkChecksRunAsThisMachinesChecksWithoutSecrets(t *testing.T) {
	site := networkCheck("http", "https://example.com/health")
	site.TLSMinDays, site.MaxDurationMS = 14, 800
	exact := networkCheck("http", "http://192.0.2.10/")
	exact.ID, exact.ExpectedStatus = "lc-exact", 204
	port := networkCheck("tcp", "example.com:22")
	port.ID = "lc-port"
	host := networkCheck("icmp", "192.0.2.1")
	host.ID = "lc-host"
	// A network check sends no secret, whatever the store is asked.
	cfg, err := Config([]Check{site, exact, port, host}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Checks) != 4 || len(cfg.Observers) != 1 {
		t.Fatal(cfg)
	}
	for _, x := range cfg.Checks {
		if x.Observer != Observer || x.Adapter.TokenEnv != "" || x.HTTP.BearerEnv != "" || len(x.HTTP.RequestHeaders) != 0 || x.FailureThreshold != FailureThreshold || x.RecoveryThreshold != RecoveryThreshold {
			t.Fatal(x)
		}
	}
	a, b, c, d := cfg.Checks[0], cfg.Checks[1], cfg.Checks[2], cfg.Checks[3]
	if a.Kind != "http" || a.Target != "https://example.com/health" || a.TLSMinDays != 14 || a.MaxDurationMS != 800 || a.HTTP.Method != "GET" || !slices.Equal(a.HTTP.Status, answered) {
		t.Fatal(a)
	}
	// A redirect is the site answering; a server error is not.
	if !slices.Contains(a.HTTP.Status, 301) || !slices.Contains(a.HTTP.Status, 200) || slices.Contains(a.HTTP.Status, 404) || slices.Contains(a.HTTP.Status, 500) {
		t.Fatal(a.HTTP.Status)
	}
	if !slices.Equal(b.HTTP.Status, []int{204}) || c.Kind != "tcp" || c.Target != "example.com:22" || d.Kind != "icmp" || d.Target != "192.0.2.1" {
		t.Fatal(b, c, d)
	}
	many := make([]Check, 0, MaxChecks+1)
	for i := 0; i <= MaxChecks; i++ {
		x := networkCheck("icmp", "192.0.2.1")
		x.ID = "lc-" + strconv.Itoa(i)
		many = append(many, x)
	}
	if _, err := Config(many, nil); AsError(err).Code != "check_limit" {
		t.Fatal(err)
	}
	if _, err := Config(many[:MaxChecks], nil); err != nil {
		t.Fatal(err)
	}
}

func TestInspectNetworkReportsOneRunAndWritesNothing(t *testing.T) {
	var status atomic.Int32
	status.Store(200)
	var reads atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected request", r.Method, r.Header)
		}
		if code := int(status.Load()); code >= 300 && code < 400 {
			http.Redirect(w, r, "/elsewhere", code)
		} else {
			w.WriteHeader(code)
		}
	}))
	defer site.Close()
	check := networkCheck("http", site.URL+"/health")
	check.ID = ""
	inspect := func(c Check) *Probe {
		t.Helper()
		got, err := Inspect(context.Background(), c, "")
		if err != nil || got.Probe == nil || got.Check.URL != c.Normalized().URL || got.Groups == nil || got.Warnings == nil || got.Engine != (Engine{}) {
			t.Fatal(got, err)
		}
		return got.Probe
	}
	if p := inspect(check); p.Outcome != "ok" || p.Code != "" || p.Stage != "" || p.HTTPStatus != 200 || p.TLSDaysRemaining != nil || p.DurationMS < 0 {
		t.Fatal(*p)
	}
	status.Store(302)
	before := reads.Load()
	if p := inspect(check); p.Outcome != "ok" || p.HTTPStatus != 302 || reads.Load() != before+1 {
		t.Fatal("a redirect is an answer, and is not followed", *p, reads.Load()-before)
	}
	status.Store(503)
	if p := inspect(check); p.Outcome != "fail" || p.Code != "unexpected_status" || p.HTTPStatus != 503 {
		t.Fatal(*p)
	}
	status.Store(204)
	exact := check
	exact.ExpectedStatus = 204
	if p := inspect(exact); p.Outcome != "ok" || p.HTTPStatus != 204 {
		t.Fatal(*p)
	}
	exact.ExpectedStatus = 200
	if p := inspect(exact); p.Outcome != "fail" || p.Code != "unexpected_status" || p.HTTPStatus != 204 {
		t.Fatal(*p)
	}

	// A port: open, then closed. Neither is an error of the command.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	port := networkCheck("tcp", address)
	if p := inspect(port); p.Outcome != "ok" {
		t.Fatal(*p)
	}
	l.Close()
	if p := inspect(port); p.Outcome != "fail" || p.Code == "" || p.Stage == "" {
		t.Fatal(*p)
	}

	// A certificate this machine does not trust is a failed handshake, and
	// reports no expiry: nothing unverified is read.
	secure := httptest.NewTLSServer(http.NotFoundHandler())
	defer secure.Close()
	if p := inspect(networkCheck("tls", strings.TrimPrefix(secure.URL, "https://"))); p.Outcome != "fail" || p.Stage != "tls" || p.TLSDaysRemaining != nil {
		t.Fatal(*p)
	}
	if _, err := Inspect(context.Background(), networkCheck("tcp", "nowhere"), ""); AsError(err).Code != "invalid_target" {
		t.Fatal(err)
	}
}

func TestStoreKeepsNoSecretForANetworkCheck(t *testing.T) {
	root := filepath.Join(t.TempDir(), "local")
	s, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	check := networkCheck("http", "https://example.com")
	for _, auth := range []Auth{{Mode: "token", Token: syntheticSecret}, {Mode: "saved"}} {
		if _, err := s.Save(ctx, "", check, auth); AsError(err).Code != "invalid_request" {
			t.Fatal(auth.Mode, err)
		}
	}
	p, err := s.Save(ctx, "", check, Auth{Mode: "none"})
	if err != nil || len(p.Checks) != 1 || p.Checks[0].AuthMode != "none" || p.Checks[0].Credential != "" {
		t.Fatal(p, err)
	}
	loaded, secrets, err := s.Snapshot(ctx, p.Revision)
	if err != nil || len(loaded.Checks) != 1 || loaded.Checks[0].Check.Adapter != "http" || len(secrets) != 0 {
		t.Fatal(loaded, secrets, err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "credential-*"))
	if len(matches) != 0 {
		t.Fatal("credential written for a network check", matches)
	}
}
