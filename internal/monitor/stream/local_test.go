package stream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/local"
	"github.com/WhitenWhiten/topophany/internal/monitor"
)

const localStreamSecret = "synthetic-stream-secret-canary"

func localEngine(t *testing.T, reads *atomic.Int32) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != "GET" {
			t.Error("engine written to", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+localStreamSecret {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/configs":
			_, _ = io.WriteString(w, `{"mode":"rule","tun":{"enable":false}}`)
		case "/version":
			_, _ = io.WriteString(w, `{"meta":true,"version":"v1.19.99"}`)
		case "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{"Proxy":{"type":"Selector","now":"DIRECT","all":["test-net-a","DIRECT"]},"test-net-a":{"type":"Shadowsocks"}}}`)
		case "/providers/proxies":
			_, _ = io.WriteString(w, `{"providers":{}}`)
		default:
			t.Error("unexpected engine read", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func localTemplate(check string) monitor.Template {
	history := true
	t := monitor.Template{Schema: 1, Title: "Local", Rows: []monitor.Row{{ID: "engine", Metric: "check.card", Select: monitor.Selector{Check: check}, Visual: "card", History: &history}, {ID: "checks", Metric: "checks.list", Visual: "list"}}}
	t.Bar.Row = "engine"
	return t
}

func rowValue(t *testing.T, row monitor.Value) map[string]any {
	t.Helper()
	b, _ := json.Marshal(row.Value)
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		t.Fatal(string(b))
	}
	return v
}

func TestLocalStreamRunsChecksReloadsAndStopsWithItsLastHandle(t *testing.T) {
	var reads atomic.Int32
	engine := localEngine(t, &reads)
	root := filepath.Join(t.TempDir(), "local")
	store, err := local.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	no := false
	check := local.Check{ID: "lc-engine", Name: "Proxy engine", Adapter: "mihomo", URL: engine.URL, Interval: 30, Timeout: 5, Stale: 90, RootProxy: "Proxy", ExpectedMode: "rule", ExpectedTUN: &no}
	p, err := store.Save(context.Background(), "", check, local.Auth{Mode: "token", Token: localStreamSecret})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Schema: 1, RequestID: "local-1", Source: "local", ProfileRevision: p.Revision, Interval: "1s", Template: localTemplate(check.ID)}
	decoded, e := Decode(line(request))
	if e != nil {
		t.Fatal(e)
	}
	m := NewLocalManager(root, time.Now)
	defer m.Close()
	src, e := m.Open(context.Background(), decoded)
	if e != nil {
		t.Fatal(e)
	}
	if src.Project != "local" {
		t.Fatal(src.Project)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	in, err := src.Fetch(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	c := monitor.Config{Schema: 1, Name: "stream", Revision: "r", Source: "local", Project: "local", Interval: "1s", Template: request.Template}
	frame := monitor.Build(c, in, time.Now())
	b, _ := monitor.MarshalPanel(frame)
	if frame.Source != "local" || frame.Project != "local" || strings.Contains(string(b), localStreamSecret) {
		t.Fatal(string(b))
	}
	card := rowValue(t, frame.Rows[0])
	diagnostic, _ := card["diagnostic"].(map[string]any)
	observer, _ := card["observer"].(map[string]any)
	samples, _ := card["samples"].(map[string]any)
	if card["health"] != "up" || card["freshness"] != "fresh" || diagnostic["outcome"] != "ok" || diagnostic["policy"] != "matched" || observer["id"] != "local" || observer["name"] != "This machine" || len(samples["slots"].([]any)) == 0 {
		t.Fatal(string(b))
	}
	facts := diagnostic["facts"].(map[string]any)
	if facts["mode"] != "rule" || facts["tun_enabled"] != "false" || facts["reported_selection"] != "DIRECT" || facts["peer_text"] != "withheld_secret_request" {
		t.Fatal(facts)
	}
	if list := frame.Rows[1].Value.([]monitor.CheckListEntry); len(list) != 1 || list[0].ID != check.ID || list[0].Health != "up" {
		t.Fatal(frame.Rows[1])
	}

	// A saved change reaches the running stream through its next request.
	check.ExpectedMode = "global"
	p, err = store.Save(context.Background(), p.Revision, check, local.Auth{Mode: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	stale := request
	if _, e := m.Open(context.Background(), stale); e == nil || e.Code != "profile_conflict" {
		t.Fatal(e)
	}
	request.ProfileRevision = p.Revision
	next, e := m.Open(context.Background(), request)
	if e != nil {
		t.Fatal(e)
	}
	src.Close()
	deadline := time.Now().Add(5 * time.Second)
	var policy map[string]any
	for time.Now().Before(deadline) {
		in, _ := next.Fetch(context.Background())
		frame = monitor.Build(c, in, time.Now())
		policy, _ = rowValue(t, frame.Rows[0])["diagnostic"].(map[string]any)
		if policy != nil && policy["outcome"] == "policy" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if policy["outcome"] != "policy" || policy["code"] != "mode_mismatch" || policy["policy"] != "violated" || rowValue(t, frame.Rows[0])["health"] != "degraded" {
		t.Fatal(policy, frame.Rows[0])
	}
	// An unknown check is missing like any other entity.
	request.Template = localTemplate("lc-absent")
	missing, e := m.Open(context.Background(), request)
	if e != nil {
		t.Fatal(e)
	}
	in, _ = missing.Fetch(context.Background())
	c.Template = request.Template
	if frame = monitor.Build(c, in, time.Now()); frame.Rows[0].Reason != "entity_missing" {
		t.Fatal(frame.Rows[0])
	}
	next.Close()
	missing.Close()
	if m.session != nil {
		t.Fatal("session outlived its last handle")
	}
	stopped := reads.Load()
	time.Sleep(200 * time.Millisecond)
	if reads.Load() != stopped {
		t.Fatal("reads continued after the stream closed")
	}
}

func TestLocalRequestFields(t *testing.T) {
	r := Request{Schema: 1, Source: "local", Interval: "5s", Template: localTemplate("lc-engine")}
	if _, e := Decode(line(r)); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*Request){
		"profile":     func(r *Request) { r.Profile = "desktop" },
		"project":     func(r *Request) { r.Project = "local" },
		"credentials": func(r *Request) { r.Credentials = "/synthetic" },
		"observer":    func(r *Request) { r.Observer = "local" },
		"scenario":    func(r *Request) { r.Scenario = 1 },
		"lang":        func(r *Request) { r.Lang = "en" },
		"retry":       func(r *Request) { r.RetryNonce = "n" },
		"metric":      func(r *Request) { r.Template.Rows[1].Metric = "servers.list" },
	} {
		candidate := r
		candidate.Template.Rows = append([]monitor.Row(nil), r.Template.Rows...)
		change(&candidate)
		if _, e := Decode(line(candidate)); e == nil || e.Code != "invalid_request" {
			t.Errorf("%s accepted: %v", name, e)
		}
	}
	r.Source, r.ProfileRevision = "preview", "x"
	r.Lang = "en"
	if _, e := Decode(line(r)); e == nil {
		t.Fatal("profile_revision accepted for preview")
	}
}

func TestLocalStreamFirstFrameAndEOF(t *testing.T) {
	var reads atomic.Int32
	engine := localEngine(t, &reads)
	root := filepath.Join(t.TempDir(), "local")
	store, _ := local.NewStore(root)
	check := local.Check{ID: "lc-engine", Name: "Proxy engine", Adapter: "mihomo", URL: engine.URL, Interval: 30, Timeout: 5, Stale: 90}
	if _, err := store.Save(context.Background(), "", check, local.Auth{Mode: "token", Token: localStreamSecret}); err != nil {
		t.Fatal(err)
	}
	m := NewLocalManager(root, time.Now)
	defer m.Close()
	lines := make(chan Line, 1)
	frames := make(chan monitor.Panel, 64)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- Run(ctx, lines, m.Open, func(p monitor.Panel) error { frames <- p; return nil }) }()
	lines <- line(Request{Schema: 1, RequestID: "first", Source: "local", Interval: "5s", Template: localTemplate(check.ID)})
	first := wait(t, frames)
	if first.Source != "local" || first.Project != "local" || first.Lifecycle != "running" {
		t.Fatal(first)
	}
	// The first read is published as it lands, not an interval later.
	read := wait(t, frames)
	if card := rowValue(t, read.Rows[0]); card["health"] != "up" || card["diagnostic"].(map[string]any)["policy"] != "not_configured" {
		t.Fatal(read.Rows[0])
	}
	close(lines)
	var last monitor.Panel
	for last.Lifecycle != "stopped" {
		last = wait(t, frames)
	}
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
	if m.session != nil {
		t.Fatal("EOF left the session running")
	}
}
