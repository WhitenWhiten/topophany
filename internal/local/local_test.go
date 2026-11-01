package local

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const syntheticSecret = "synthetic-local-secret-canary"

// fakeEngine is a synthetic Mihomo controller on an ephemeral loopback port.
type fakeEngine struct {
	*httptest.Server
	mu     sync.Mutex
	secret string
	mode   string
	tun    string
	calls  []string
}

func newFakeEngine(t *testing.T, secret string) *fakeEngine {
	t.Helper()
	f := &fakeEngine{secret: secret, mode: "rule", tun: "false"}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		secret, mode, tun := f.secret, f.mode, f.tun
		f.mu.Unlock()
		if r.Method != "GET" {
			t.Error("engine written to", r.Method, r.URL.Path)
		}
		if secret != "" && r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, `{"message":"Unauthorized"}`, 401)
			return
		}
		switch r.URL.Path {
		case "/configs":
			_, _ = io.WriteString(w, `{"mode":"`+mode+`","tun":{"enable":`+tun+`},"secret":"must-not-leak"}`)
		case "/version":
			_, _ = io.WriteString(w, `{"meta":true,"version":"v1.19.99"}`)
		case "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{"GLOBAL":{"type":"Selector","now":"Proxy","all":["DIRECT","test-net-a","Proxy","Auto"]},"Proxy":{"type":"Selector","now":"DIRECT","all":["Auto","test-net-a","DIRECT"]},"Auto":{"type":"URLTest","now":"test-net-a","all":["test-net-a"]},"test-net-a":{"type":"Shadowsocks"},"DIRECT":{"type":"Direct"}}}`)
		case "/providers/proxies":
			_, _ = io.WriteString(w, `{"providers":{}}`)
		default:
			t.Error("unexpected engine read", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeEngine) set(mode, tun string) {
	f.mu.Lock()
	f.mode, f.tun = mode, tun
	f.mu.Unlock()
}

func syntheticCheck(url string) Check {
	no := false
	return Check{ID: "lc-test", Name: "Proxy engine", Adapter: "mihomo", URL: url, Interval: 30, Timeout: 5, Stale: 90, RootProxy: "Proxy", ExpectedMode: "rule", ExpectedTUN: &no}
}

// closedURL is a loopback address nothing listens on any longer.
func closedURL(t *testing.T) string {
	s := httptest.NewServer(http.NotFoundHandler())
	u := s.URL
	s.Close()
	return u
}

func TestNormalizeURLAcceptsOnlyLoopbackControllers(t *testing.T) {
	for raw, want := range map[string]string{
		"http://127.0.0.1:9090":     "http://127.0.0.1:9090",
		"http://127.0.0.1:9090/":    "http://127.0.0.1:9090",
		"HTTP://LOCALHOST:9090":     "http://localhost:9090",
		"https://127.8.9.10":        "https://127.8.9.10",
		"http://[::1]:9097":         "http://[::1]:9097",
		"http://[0:0:0:0:0:0:0:1]":  "http://[::1]",
		"http://localhost":          "http://localhost",
		"http://127.0.0.1:09090":    "http://127.0.0.1:9090",
		"http://[::ffff:127.0.0.1]": "http://127.0.0.1",
	} {
		got, err := NormalizeURL(raw)
		if err != nil || got != want {
			t.Errorf("%s: got %q %v, want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "127.0.0.1:9090", "ftp://127.0.0.1", "http://192.0.2.1:9090", "http://0.0.0.0:9090", "http://example.invalid:9090", "http://user:pw@127.0.0.1:9090", "http://127.0.0.1:9090/ui", "http://127.0.0.1:9090?secret=x", "http://127.0.0.1:9090#x", "http://127.0.0.1:0", "http://127.0.0.1:70000", "http://[fe80::1%25lo0]:9090", "http://localhost.example.invalid", " http://127.0.0.1", "http://127.1:9090"} {
		if _, err := NormalizeURL(raw); err == nil || AsError(err).Code != "not_local_address" {
			t.Errorf("%q accepted: %v", raw, err)
		}
	}
	if got := syntheticCheck("http://localhost:9090").target(); got != "http://127.0.0.1:9090" {
		t.Fatal("localhost dialed through the resolver", got)
	}
}

func TestCheckBoundsAndDecoding(t *testing.T) {
	valid := syntheticCheck("http://127.0.0.1:9090")
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Check){
		"id":            func(c *Check) { c.ID = "bad id" },
		"long id":       func(c *Check) { c.ID = strings.Repeat("a", 65) },
		"name":          func(c *Check) { c.Name = " " },
		"control name":  func(c *Check) { c.Name = "a‮b" },
		"adapter":       func(c *Check) { c.Adapter = "tailscale" },
		"interval low":  func(c *Check) { c.Interval = 4 },
		"interval high": func(c *Check) { c.Interval = 3601 },
		"timeout":       func(c *Check) { c.Timeout = 31 },
		"timeout order": func(c *Check) { c.Interval, c.Timeout, c.Stale = 5, 5, 10 },
		"stale low":     func(c *Check) { c.Stale = 59 },
		"stale high":    func(c *Check) { c.Interval, c.Stale = 3600, 86401 },
		"root":          func(c *Check) { c.RootProxy = strings.Repeat("r", 129) },
		"mode":          func(c *Check) { c.ExpectedMode = "script" },
	} {
		c := valid
		change(&c)
		if err := c.Validate(); err == nil || AsError(err).Code != "invalid_request" {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	var c Check
	if err := json.Unmarshal([]byte(`{"id":"lc-1","name":"Engine","adapter":"mihomo","url":"http://127.0.0.1:9090","interval_seconds":60,"expected_tun":null}`), &c); err != nil || c.Timeout != 5 || c.Stale != 180 || c.ExpectedTUN != nil || c.Validate() != nil {
		t.Fatal(c, err)
	}
	for _, raw := range []string{`{"id":null}`, `{"id":"lc-1","id":"lc-2"}`, `{"interval_seconds":"30"}`} {
		var c Check
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Errorf("%s decoded", raw)
		}
	}
	if err := json.Unmarshal([]byte(`{"ID":"alias","id":"lc-1"}`), &c); err != nil || c.ID != "lc-1" {
		t.Fatal("case alias decoded", c.ID, err)
	}
	for _, raw := range []string{`{"mode":"none","token":"x"}`, `{"mode":"token"}`, `{"mode":"token","token":" padded"}`, `{"mode":"env","name":"X"}`, `{"mode":"saved","token":""}`} {
		var a Auth
		if json.Unmarshal([]byte(raw), &a) == nil {
			t.Errorf("auth %s accepted", raw)
		}
	}
	if _, err := Decode(strings.NewReader(`{"schema":2}`)); err == nil {
		t.Fatal("schema 2 accepted")
	}
	if _, err := Decode(bytes.NewReader(append([]byte(`{"schema":1,"x":"`), append(bytes.Repeat([]byte("x"), MaxInput), '"', '}')...))); err == nil {
		t.Fatal("oversized request accepted")
	}
}

func TestStoreKeepsSecretsPrivateAndVersionsEveryWrite(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "local")
	s, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := s.Load(); err != nil || p.Revision != "" || len(p.Checks) != 0 {
		t.Fatal(p, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("load created the root")
	}
	check := syntheticCheck("http://127.0.0.1:9090/")
	p, err := s.Save(ctx, "", check, Auth{Mode: "token", Token: syntheticSecret})
	if err != nil || p.Revision == "" || p.Checks[0].Check.URL != "http://127.0.0.1:9090" {
		t.Fatal(p, err)
	}
	if _, err := s.Save(ctx, "", check, Auth{Mode: "none"}); AsError(err).Code != "profile_conflict" {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "checks.json"))
	if bytes.Contains(b, []byte(syntheticSecret)) {
		t.Fatal("secret written into the configuration")
	}
	entries, _ := os.ReadDir(root)
	credentials := 0
	for _, e := range entries {
		info, _ := e.Info()
		if strings.HasPrefix(e.Name(), "credential-") {
			credentials++
			if info.Mode().Perm() != 0600 {
				t.Fatal("secret file mode", info.Mode())
			}
		}
	}
	if info, _ := os.Stat(root); credentials != 1 || info.Mode().Perm() != 0700 {
		t.Fatal(credentials, info.Mode())
	}
	if secrets, err := s.Secrets(p); err != nil || secrets[check.ID] != syntheticSecret {
		t.Fatal(secrets, err)
	}
	// Saved keeps the secret only at the same address.
	moved := check
	moved.URL = "http://127.0.0.1:9091"
	if _, err := s.Save(ctx, p.Revision, moved, Auth{Mode: "saved"}); AsError(err).Code != "saved_auth_mismatch" {
		t.Fatal(err)
	}
	renamed := check
	renamed.Name = "Renamed"
	next, err := s.Save(ctx, p.Revision, renamed, Auth{Mode: "saved"})
	if err != nil || next.Revision == p.Revision || next.Checks[0].Credential != p.Checks[0].Credential {
		t.Fatal(next, err)
	}
	// None deletes the stored secret.
	cleared, err := s.Save(ctx, next.Revision, renamed, Auth{Mode: "none"})
	if err != nil || cleared.Checks[0].AuthMode != "none" {
		t.Fatal(cleared, err)
	}
	if _, err := os.Stat(filepath.Join(root, "credential-"+p.Checks[0].Credential+".json")); !os.IsNotExist(err) {
		t.Fatal("none kept the old secret")
	}
	rev := cleared.Revision
	for i := 2; i <= MaxChecks; i++ {
		c := check
		c.ID = "lc-" + string(rune('0'+i))
		p, err := s.Save(ctx, rev, c, Auth{Mode: "token", Token: syntheticSecret})
		if err != nil {
			t.Fatal(err)
		}
		rev = p.Revision
	}
	extra := check
	extra.ID = "lc-extra"
	if _, err := s.Save(ctx, rev, extra, Auth{Mode: "none"}); AsError(err).Code != "check_limit" {
		t.Fatal(err)
	}
	if _, err := s.Remove(ctx, rev, "lc-absent"); AsError(err).Code != "not_found" {
		t.Fatal(err)
	}
	if _, err := s.Remove(ctx, "stale", "lc-2"); AsError(err).Code != "profile_conflict" {
		t.Fatal(err)
	}
	last, _ := s.Load()
	removed, err := s.Remove(ctx, rev, "lc-2")
	if err != nil || len(removed.Checks) != MaxChecks-1 {
		t.Fatal(removed, err)
	}
	gone, _ := last.Find("lc-2")
	if _, err := os.Stat(filepath.Join(root, "credential-"+gone.Credential+".json")); !os.IsNotExist(err) {
		t.Fatal("remove kept the secret")
	}
	if err := os.Chmod(filepath.Join(root, "checks.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); AsError(err).Code != "storage_error" {
		t.Fatal("readable configuration accepted", err)
	}
}

func TestInspectMapsEngineAnswers(t *testing.T) {
	engine := newFakeEngine(t, syntheticSecret)
	check := syntheticCheck(engine.URL)
	got, err := Inspect(context.Background(), check, syntheticSecret)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	if got.Engine.Version != "v1.19.99" || got.Engine.Mode != "rule" || got.Engine.TUN == nil || *got.Engine.TUN || len(got.Groups) != 3 || got.Groups[0].Name != "Proxy" || got.Truncated || len(got.Warnings) != 0 {
		t.Fatal(string(b))
	}
	for _, leak := range []string{syntheticSecret, "must-not-leak", "test-net-a"} {
		if strings.Contains(string(b), leak) {
			t.Fatal("inspection leaked", leak)
		}
	}
	check.RootProxy = "Absent"
	if got, err := Inspect(context.Background(), check, syntheticSecret); err != nil || len(got.Warnings) != 1 || got.Warnings[0] != "root_proxy_not_found" {
		t.Fatal(got, err)
	}
	for _, token := range []string{"", "wrong"} {
		if _, err := Inspect(context.Background(), check, token); AsError(err).Code != "authentication_failed" || AsError(err).Retryable {
			t.Fatal(token, err)
		}
	}
	stopped := syntheticCheck(closedURL(t))
	if _, err := Inspect(context.Background(), stopped, ""); AsError(err).Code != "connection_failed" || !AsError(err).Retryable {
		t.Fatal(err)
	}
	notEngine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `[]`) }))
	defer notEngine.Close()
	if _, err := Inspect(context.Background(), syntheticCheck(notEngine.URL), ""); AsError(err).Code != "unsupported_response_shape" {
		t.Fatal(err)
	}
}
