package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/outbox"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func invoke(input string, args ...string) (string, string, error) {
	var out, stderr bytes.Buffer
	a := &App{In: strings.NewReader(input), Out: &out, Err: &stderr}
	e := a.Run(context.Background(), args)
	return out.String(), stderr.String(), e
}

func credentialFile(t *testing.T, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(path, []byte(token), 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func initialized(t *testing.T) (string, model.Config, api.Auth, client.Credentials) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	if _, _, e := invoke("", "init", "--dir", dir); e != nil {
		t.Fatal(e)
	}
	c, e := config.Load(filepath.Join(dir, "topology.json"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := api.LoadAuth(filepath.Join(dir, "auth.json"), c)
	if e != nil {
		t.Fatal(e)
	}
	cr, e := client.Load(filepath.Join(dir, "credentials.json"))
	if e != nil {
		t.Fatal(e)
	}
	return dir, c, a, cr
}
func saveConfig(t *testing.T, path string, c model.Config) {
	t.Helper()
	b, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = secure.AtomicWrite(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestCLIDispatchAndHelp(t *testing.T) {
	out, _, e := invoke("", "--help")
	if e != nil || !strings.Contains(out, "topo --token-stdin") {
		t.Fatal(e, out)
	}
	out, _, e = invoke("", "version")
	if e != nil || !strings.Contains(out, "Topophany "+model.Version) {
		t.Fatal(e, out)
	}
	for _, cmd := range []string{"serve", "worker", "login", "init", "validate", "export", "once", "token", "edit", "preview", "status", "show", "history"} {
		_, stderr, e := invoke("", cmd, "--help")
		if !errors.Is(e, flag.ErrHelp) || !strings.Contains(stderr, "Usage of") {
			t.Fatal(cmd, e, stderr)
		}
	}
	for _, args := range [][]string{{"missing"}, {"--unknown"}, {"token", "extra"}, {"validate", "--config", "missing"}} {
		if _, _, e = invoke("", args...); e == nil {
			t.Fatal("bad command accepted", args)
		}
	}
	out, _, e = invoke("", "token")
	var token map[string]string
	if e != nil || json.Unmarshal([]byte(out), &token) != nil || secure.Hash(token["token"]) != token["token_sha256"] {
		t.Fatal(e, out)
	}
}
func TestInitFreshCredentialsNoOverwriteAndSanitizedExport(t *testing.T) {
	dir, c, a, cr := initialized(t)
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
	v := cr.Profiles["demo"].Servers[0].Token
	if a.Tokens[0].TokenSHA256 != secure.Hash(v) {
		t.Fatal("auth/credential mismatch")
	}
	worker, e := client.Load(filepath.Join(dir, "worker-credentials.json"))
	if e != nil {
		t.Fatal(e)
	}
	if worker.Profiles["demo"].Servers[0].Token == v {
		t.Fatal("roles share token")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if strings.Contains(string(raw), v) {
		t.Fatal("plaintext token in auth")
	}
	_, _, _, cr2 := initialized(t)
	if v == cr2.Profiles["demo"].Servers[0].Token {
		t.Fatal("hardcoded credentials")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if _, _, e = invoke("", "init", "--dir", dir); e == nil {
		t.Fatal("init overwrote credentials")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("init modified existing")
	}
	out, _, e := invoke("", "validate", "--config", filepath.Join(dir, "topology.json"))
	if e != nil || !strings.Contains(out, config.Revision(c)) {
		t.Fatal(e, out)
	}
	out, _, e = invoke("", "export", "--config", filepath.Join(dir, "topology.json"))
	if e != nil || strings.Contains(out, v) {
		t.Fatal(e)
	}
	for _, x := range c.Checks {
		if !strings.Contains(config.Target(c, x), "127.0.0.1") {
			t.Fatal("demo performs external probes")
		}
	}
}
func TestLoginRemoteJSONAndSavedDefault(t *testing.T) {
	dir, c, a, cr := initialized(t)
	st, e := store.Open(filepath.Join(dir, "collector-test"), store.Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	srv := httptest.NewServer(api.New(c, "remote", st, a).Handler())
	defer srv.Close()
	token := cr.Profiles["demo"].Servers[0].Token
	credpath := filepath.Join(dir, "new-client.json")
	out, _, e := invoke(token+"\n", "login", "--server", srv.URL, "--project", c.Project, "--credentials", credpath, "--token-stdin")
	if e != nil || !strings.Contains(out, "Saved verified project") {
		t.Fatal(e, out)
	}
	out, _, e = invoke("", "--credentials", credpath, "--json")
	if e != nil {
		t.Fatal(e)
	}
	var snap model.Snapshot
	if json.Unmarshal([]byte(out), &snap) != nil || snap.Instance != "remote" {
		t.Fatal(out)
	}
	if _, _, e = invoke("", "--credentials", credpath, "--json", "--credential-file", credentialFile(t, strings.Repeat("e", 64))); e == nil {
		t.Fatal("bad override not checked")
	}
	if _, _, e = invoke("", "--credentials", credpath, "--profile", "missing", "--json"); e == nil {
		t.Fatal("unknown profile")
	}
	out, _, e = invoke("", "--credentials", filepath.Join(dir, "missing"), "--server", srv.URL, "--project", c.Project, "--credential-file", credentialFile(t, token), "--json")
	if e != nil || !strings.Contains(out, "remote") {
		t.Fatal(e)
	}
	bad := filepath.Join(dir, "bad.json")
	secure.AtomicWrite(bad, []byte("corrupt"), 0600)
	if _, _, e = invoke(token, "login", "--server", srv.URL, "--project", c.Project, "--credentials", bad, "--token-stdin"); e == nil {
		t.Fatal("login replaced corrupt credentials")
	}
	raw, _ := os.ReadFile(bad)
	if string(raw) != "corrupt" {
		t.Fatal("modified corrupt file")
	}
	for _, tc := range []struct{ project, token string }{{"other", token}, {c.Project, "bad"}, {c.Project, strings.Repeat("z", 1025)}} {
		if _, _, e = invoke(tc.token, "login", "--server", srv.URL, "--project", tc.project, "--credentials", filepath.Join(dir, "must-not-exist"), "--token-stdin"); e == nil {
			t.Fatal("invalid login")
		}
	}
	if _, e = os.Stat(filepath.Join(dir, "must-not-exist")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("failed login wrote credentials")
	}
	if _, _, e = invoke("", "--credentials", credpath); e == nil {
		t.Fatal("nonterminal UI accepted")
	}
}
func TestOnceRealLocalHTTP(t *testing.T) {
	dir, c, _, _ := initialized(t)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer httpServer.Close()
	c.Endpoints[0].Address = httpServer.URL
	saveConfig(t, filepath.Join(dir, "once.json"), c)
	out, _, e := invoke("", "once", "--config", filepath.Join(dir, "once.json"), "--observer", "demo")
	if e != nil {
		t.Fatal(e)
	}
	var v model.Observation
	if json.Unmarshal([]byte(out), &v) != nil || v.Outcome != "ok" || v.Observer != "demo" || v.Sequence != 1 || v.ID == "" {
		t.Fatal(out)
	}
	if _, _, e = invoke("", "once", "--config", filepath.Join(dir, "once.json")); e == nil {
		t.Fatal("once without observer")
	}
}

type lockedWriter struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(b)
}
func (w *lockedWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.b.String() }
func TestServeActualHTTPAndGracefulShutdown(t *testing.T) {
	dir, c, _, cr := initialized(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer target.Close()
	c.Endpoints[0].Address = target.URL
	c.Checks[0].IntervalSeconds = 1
	saveConfig(t, filepath.Join(dir, "topology.json"), c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &lockedWriter{}
	a := &App{In: strings.NewReader(""), Out: io.Discard, Err: log}
	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx, []string{"serve", "--config", filepath.Join(dir, "topology.json"), "--auth", filepath.Join(dir, "auth.json"), "--data", filepath.Join(dir, "data"), "--listen", "127.0.0.1:0", "--observer", "demo"})
	}()
	re := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	var addr string
	testkit.Wait(t, 3*time.Second, func() bool {
		m := re.FindStringSubmatch(log.String())
		if len(m) > 1 {
			addr = m[1]
			return true
		}
		return false
	})
	p := cr.Profiles["demo"]
	p.Servers[0].URL = "http://" + addr
	cl, e := client.New(p)
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	testkit.Wait(t, 3*time.Second, func() bool {
		s, _, e := cl.Snapshot(context.Background(), "demo")
		return e == nil && len(s.Checks) > 0 && s.Checks[0].Health == "up"
	})
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve shutdown leaked goroutines")
	}
	st, e := store.Open(filepath.Join(dir, "data"), store.Options{Revision: config.Revision(c)})
	if e != nil {
		t.Fatal("server did not release journal", e)
	}
	st.Close()
}
func TestWorkerActualRemoteIngestion(t *testing.T) {
	dir, c, auth, _ := initialized(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer target.Close()
	c.Endpoints[0].Address = target.URL
	c.Checks[0].IntervalSeconds = 1
	saveConfig(t, filepath.Join(dir, "topology.json"), c)
	st, e := store.Open(filepath.Join(dir, "server-data"), store.Options{Revision: config.Revision(c)})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	srv := httptest.NewServer(api.New(c, "remote", st, auth).Handler())
	defer srv.Close()
	credPath := filepath.Join(dir, "worker-credentials.json")
	cr, e := client.Load(credPath)
	if e != nil {
		t.Fatal(e)
	}
	p := cr.Profiles["demo"]
	p.Servers[0].URL = srv.URL
	cr.Profiles["demo"] = p
	client.Save(credPath, cr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	app := &App{In: strings.NewReader(""), Out: io.Discard, Err: &lockedWriter{}}
	go func() {
		done <- app.Run(ctx, []string{"worker", "--config", filepath.Join(dir, "topology.json"), "--observer", "demo", "--credentials", credPath, "--data", filepath.Join(dir, "worker-data")})
	}()
	testkit.Wait(t, 5*time.Second, func() bool {
		rows, beats := st.View()
		return len(rows) > 0 && rows[0].Outcome == "ok" && beats["demo"].Observer == "demo"
	})
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}
func TestServeTransportAndArgumentGuards(t *testing.T) {
	for _, args := range [][]string{{"--listen", "0.0.0.0:7447"}, {"--listen", "broken"}, {"--tls-cert", "one.pem"}, {"--instance", "../bad"}, {"--retention", "1s"}, {"--retention", "9999h"}} {
		_, _, e := invoke("", append([]string{"serve"}, args...)...)
		if e == nil {
			t.Fatal(args)
		}
	}
	_, _, e := invoke("", "login", "--project", "wrong id")
	if e == nil {
		t.Fatal("invalid project")
	}
	a := New()
	if a.In == nil || a.Out == nil || a.TerminalIn == nil {
		t.Fatal("default streams")
	}
	_ = fmt.Sprint(a)
}

func TestShippedTopologyExamplesValidateAndHomelabIsPaused(t *testing.T) {
	for _, name := range []string{"minimal.json", "homelab.json", "host-resources.json", "cloudflare.json"} {
		t.Run(name, func(t *testing.T) {
			c, err := config.Load(filepath.Join("..", "..", "examples", name))
			if err != nil {
				t.Fatal(err)
			}
			if err := config.Validate(c); err != nil {
				t.Fatal(err)
			}
			if name == "homelab.json" {
				if len(c.Nodes) < 10 || len(c.Checks) < 20 {
					t.Fatal("homelab example lost scenarios")
				}
				for _, check := range c.Checks {
					if !check.Disabled {
						t.Fatalf("production template enabled %s", check.ID)
					}
				}
			}
		})
	}
}

// The collector's TLS key is a credential like auth.json: a key another
// user could read, a link or anything but a file stops it before it
// listens, and one that is private serves TLS.
func TestServeTLSKeyMustBePrivate(t *testing.T) {
	dir, _, _, cr := initialized(t)
	cert, key, ca := testkit.Certificate(t, "localhost")
	serve := func(ctx context.Context, key string, log io.Writer) error {
		a := &App{In: strings.NewReader(""), Out: io.Discard, Err: log}
		return a.Run(ctx, []string{"serve", "--config", filepath.Join(dir, "topology.json"), "--auth", filepath.Join(dir, "auth.json"), "--data", filepath.Join(dir, "data"), "--listen", "127.0.0.1:0", "--tls-cert", cert, "--tls-key", key})
	}
	b, e := os.ReadFile(key)
	if e != nil {
		t.Fatal(e)
	}
	bad := map[string]string{"missing": filepath.Join(t.TempDir(), "missing.pem"), "directory": t.TempDir(), "link": filepath.Join(t.TempDir(), "link.pem")}
	if e = os.Symlink(key, bad["link"]); e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS != "windows" {
		bad["group-readable"] = filepath.Join(t.TempDir(), "shared.pem")
		if e = os.WriteFile(bad["group-readable"], b, 0640); e != nil {
			t.Fatal(e)
		}
	}
	for name, path := range bad {
		log := &lockedWriter{}
		e := serve(context.Background(), path, log)
		if e == nil || !strings.Contains(e.Error(), "tls-key") || strings.Contains(log.String(), "listening") {
			t.Fatal(name, e, log.String())
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &lockedWriter{}
	done := make(chan error, 1)
	go func() { done <- serve(ctx, key, log) }()
	re := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	var addr string
	testkit.Wait(t, 3*time.Second, func() bool {
		m := re.FindStringSubmatch(log.String())
		if len(m) > 1 {
			addr = m[1]
			return true
		}
		return false
	})
	p := cr.Profiles["demo"]
	p.Servers[0].URL = "https://" + addr
	p.Servers[0].CAFile = ca
	cl, e := client.New(p)
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	if _, _, e = cl.Snapshot(context.Background(), "demo"); e != nil {
		t.Fatal(e)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

// A topology's values reach no terminal raw: an error naming one it
// refuses quotes it, controls and bidi runes escaped, whichever command
// loaded it, and what topo writes to stderr carries none.
func TestTopologyErrorsCarryNoControls(t *testing.T) {
	dir, base, _, _ := initialized(t)
	path := filepath.Join(dir, "crafted.json")
	bad := "x\x1b[2J\x1b]0;pwn\a\u009b31m‮⁦\x00y"
	for name, craft := range map[string]func(c *model.Config){
		"endpoint":   func(c *model.Config) { c.Endpoints[0].ID = bad },
		"link":       func(c *model.Config) { c.Links = append(c.Links, model.Link{ID: bad}) },
		"path":       func(c *model.Config) { c.Paths[0].ID = bad },
		"path node":  func(c *model.Config) { c.Paths[0].Nodes = []string{bad} },
		"dependency": func(c *model.Config) { c.Paths[0].DependsOn = []string{bad} },
		"kind":       func(c *model.Config) { c.Checks[0].Kind = bad },
	} {
		c := base
		c.Endpoints = slices.Clone(base.Endpoints)
		c.Links = slices.Clone(base.Links)
		c.Paths = slices.Clone(base.Paths)
		c.Checks = slices.Clone(base.Checks)
		craft(&c)
		saveConfig(t, path, c)
		for _, args := range [][]string{{"validate", "--config", path}, {"export", "--config", path}, {"once", "--config", path}, {"serve", "--config", path, "--auth", filepath.Join(dir, "auth.json"), "--data", filepath.Join(t.TempDir(), "data"), "--listen", "127.0.0.1:0"}} {
			_, stderr, e := invoke("", args...)
			if e == nil {
				t.Fatal(name, args[0], "accepted")
			}
			for _, r := range e.Error() + stderr {
				if unicode.IsControl(r) && r != '\n' || unicode.Is(unicode.Bidi_Control, r) {
					t.Fatalf("%s, %s: %q", name, args[0], e.Error()+stderr)
				}
			}
		}
	}
	var b bytes.Buffer
	fmt.Fprintf(secure.Terminal(&b), "topo: %s\n\tnext", bad)
	if b.String() != "topo: x[2J]0;pwn31my\n\tnext" {
		t.Fatalf("%q", b.String())
	}
}

// A worker whose collectors have all stopped taking its samples stops
// once its outbox has room for them at none, rather than sample on and
// drop every sample.
func TestWorkerStopsWhenNoCollectorHasRoom(t *testing.T) {
	dir, c, _, _ := initialized(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer target.Close()
	c.Endpoints[0].Address = target.URL
	c.Checks[0].IntervalSeconds = 1
	saveConfig(t, filepath.Join(dir, "topology.json"), c)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer down.Close()
	credPath := filepath.Join(dir, "worker-credentials.json")
	cr, e := client.Load(credPath)
	if e != nil {
		t.Fatal(e)
	}
	p := cr.Profiles["demo"]
	p.Servers = []client.Endpoint{{URL: down.URL + "/a", Token: p.Servers[0].Token, CollectorID: strings.Repeat("a", 64)}, {URL: down.URL + "/b", Token: p.Servers[0].Token, CollectorID: strings.Repeat("b", 64)}}
	cr.Profiles["demo"] = p
	if e = client.Save(credPath, cr); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := &App{In: strings.NewReader(""), Out: io.Discard, Err: &lockedWriter{}}
	e = a.Run(ctx, []string{"worker", "--config", filepath.Join(dir, "topology.json"), "--observer", "demo", "--credentials", credPath, "--data", filepath.Join(dir, "worker-data"), "--outbox-bytes", "4096"})
	if !errors.Is(e, outbox.ErrFull) {
		t.Fatal("worker went on with no collector taking its samples", e)
	}
}

func TestServeRecoversExternalJournalBeforeAPIInstallsContext(t *testing.T) {
	dir, c, _, _ := initialized(t)
	c.Sources = append(c.Sources, model.Source{
		ID: "review-source", Adapter: "synthetic-list", Observer: "demo",
		Target: "https://source.invalid/v1/objects", IntervalSeconds: 30, TimeoutSeconds: 5, StaleSeconds: 90,
		MaxResponseBytes: 1 << 20, MaxPages: 1, MaxObjects: 10, MaxReportBytes: 16 << 10,
	})
	c.Checks = append(c.Checks, model.Check{ID: "review-ext", Observer: "demo", Kind: "external", External: &model.ExternalBinding{Source: "review-source", Object: "vm/review", Report: model.ReportTypeResource, Node: "collector", MaxAgeSeconds: 60}})
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	saveConfig(t, filepath.Join(dir, "topology.json"), c)

	now := time.Now().UTC()
	percent := 25.0
	x, _ := config.Check(c, "review-ext")
	report := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: x.External.Object, SampleID: "sample-1", ObservedAt: now.Add(-time.Minute), TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}}}
	obs := model.Observation{ID: "demo.boot.1", Project: c.Project, Contract: config.Contract(c, x), Check: x.ID, Observer: "demo", Boot: "boot", Sequence: 1, SampledAt: now, Outcome: "ok", Evidence: "reported", Stage: "external_source", DurationMS: 4, PathEvidence: "external_source", External: &model.ExternalObservation{Source: "review-source", SourceGeneration: "gen-1", AttemptedAt: now, Contact: true, Report: &report}}
	data := filepath.Join(dir, "data")
	seeded, err := store.Open(data, store.Options{}.WithTopology(c))
	if err != nil {
		t.Fatal(err)
	}
	if err := seeded.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{obs}}); err != nil {
		t.Fatal(err)
	}
	// Deliberately no Checkpoint: production must recover the journal.
	if err := seeded.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Startup and storage diagnostics are written by different goroutines.
	var out, stderr lockedWriter
	a := &App{In: strings.NewReader(""), Out: &out, Err: &stderr}
	if err := a.Run(ctx, []string{"serve", "--config", filepath.Join(dir, "topology.json"), "--auth", filepath.Join(dir, "auth.json"), "--data", data, "--listen", "127.0.0.1:0"}); err != nil {
		t.Fatalf("production serve could not reopen the external journal: %v\n%s", err, stderr.String())
	}
	reopened, err := store.Open(data, store.Options{}.WithTopology(c))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	state := reopened.ExternalView()
	if state == nil || state.Bindings["review-ext"].ReportSamples != 1 || state.Bindings["review-ext"].Last == nil {
		t.Fatalf("serve did not restore the external report: %+v", state)
	}
}
