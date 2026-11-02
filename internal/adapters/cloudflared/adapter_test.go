package cloudflared

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

const readyFour = `{"status":200,"readyConnections":4,"connectorId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}`

func testClient() func(model.Check, string) (*http.Client, func(), error) {
	return func(model.Check, string) (*http.Client, func(), error) {
		tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 32768}
		return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tr.CloseIdleConnections, nil
	}
}

// collect runs Collect against a loopback listener serving the two fixed
// paths.
func collect(t *testing.T, options model.CloudflaredOptions, ready func(http.ResponseWriter), metrics func(http.ResponseWriter)) (model.Observation, error) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready":
			ready(w)
		case "/metrics":
			metrics(w)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	x := model.Check{ID: "tunnel", Observer: "home", Kind: "cloudflared", TimeoutSeconds: 5, Adapter: model.AdapterOptions{Cloudflared: &options}}
	o := model.Observation{Facts: map[string]string{}, Timings: map[string]int64{}}
	err := Collect(context.Background(), Dependencies{Client: testClient()}, x, s.URL, &o)
	return o, err
}

func write(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { io.WriteString(w, body) }
}

func status(code int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

func connectedMetrics(t *testing.T) func(http.ResponseWriter) {
	t.Helper()
	return write(string(readFixture(t, "metrics-connected.synthetic.txt")))
}

func TestCollectEstablishedTunnel(t *testing.T) {
	o, err := collect(t, model.CloudflaredOptions{}, write(readyFour), connectedMetrics(t))
	if err != nil {
		t.Fatal(err)
	}
	if o.Outcome != "ok" || o.Stage != "adapter" || o.Code != "" || o.Detail != "tunnel established; service behind it unproven" {
		t.Fatalf("got %+v", o)
	}
	if o.PathEvidence != PathEvidence {
		t.Fatal(o.PathEvidence)
	}
	want := map[string]string{
		"ready_connections": "4",
		"connector_id":      "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
		"ha_connections":    "4",
		"connection_colos":  "hkg,lax,nrt,sin",
		"requests_total":    "120",
		"request_errors":    "2",
		"active_streams":    "3",
		"connector_version": "2026.10.0",
	}
	for key, value := range want {
		if o.Facts[key] != value {
			t.Fatalf("%s=%q; want %q (facts %v)", key, o.Facts[key], value, o.Facts)
		}
	}
	if len(o.Facts) != len(want) {
		t.Fatalf("fact budget: %v", o.Facts)
	}
}

func TestCollectUnreadyIsFail(t *testing.T) {
	body := string(readFixture(t, "ready-503.json"))
	for _, tc := range []struct {
		name  string
		ready func(http.ResponseWriter)
	}{
		{"503", status(http.StatusServiceUnavailable, body)},
		{"zero ready", write(`{"status":200,"readyConnections":0,"connectorId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}`)},
		{"body status 503 with 200", write(`{"status":503,"readyConnections":4}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := collect(t, model.CloudflaredOptions{}, tc.ready, connectedMetrics(t))
			if err != nil {
				t.Fatal(err)
			}
			if o.Outcome != "fail" || o.Code != "tunnel_not_ready" || o.Stage != "adapter" {
				t.Fatalf("got %+v", o)
			}
			if o.Facts["ready_connections"] == "" || o.PathEvidence != PathEvidence {
				t.Fatal("connector evidence missing from its own unready statement")
			}
		})
	}
}

func TestCollectMonitorErrors(t *testing.T) {
	oversizedReady := `{"status":200,"readyConnections":4,"connectorId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","pad":"` + strings.Repeat("x", ReadyBudget) + `"}`
	oversizedMetrics := "cloudflared_tunnel_ha_connections 4\n" + strings.Repeat("#", MetricsBudget)
	for _, tc := range []struct {
		name    string
		ready   func(http.ResponseWriter)
		metrics func(http.ResponseWriter)
	}{
		{"ready html", write("<html>not the connector</html>"), connectedMetrics(t)},
		{"ready 500", status(http.StatusInternalServerError, readyFour), connectedMetrics(t)},
		{"ready oversized", write(oversizedReady), connectedMetrics(t)},
		{"ready 204", status(http.StatusNoContent, ""), connectedMetrics(t)},
		{"metrics 404", write(readyFour), status(http.StatusNotFound, "not found")},
		{"metrics html", write(readyFour), write("<html>not metrics</html>")},
		{"metrics oversized", write(readyFour), write(oversizedMetrics)},
		{"metrics without a known family", write(readyFour), write("go_goroutines 24\nprocess_cpu_seconds_total 1\n")},
		{"metrics empty", write(readyFour), write("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := collect(t, model.CloudflaredOptions{}, tc.ready, tc.metrics)
			if err == nil {
				t.Fatalf("want a monitor error; got %+v", o)
			}
			if o.Outcome != "" && o.Outcome != "unknown" {
				t.Fatalf("monitor error changed the observation before the probe classified it: %+v", o)
			}
		})
	}
}

func TestCollectUnreachableAndTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	target := s.URL
	s.Close()
	x := model.Check{ID: "tunnel", Kind: "cloudflared", TimeoutSeconds: 5}
	o := model.Observation{Facts: map[string]string{}}
	if err := Collect(context.Background(), Dependencies{Client: testClient()}, x, target, &o); err == nil {
		t.Fatal("closed listener did not report a monitor error")
	}

	// The listener answers only when the client gives up, so the read can end
	// only through the check's own deadline.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	o = model.Observation{Facts: map[string]string{}}
	if err := Collect(ctx, Dependencies{Client: testClient()}, x, slow.URL, &o); err == nil {
		t.Fatal("slow listener did not time out")
	}
	if took := time.Since(began); took > time.Second {
		t.Fatalf("timeout took %s", took)
	}
}

func TestCollectExpectedConnectionsIsPolicy(t *testing.T) {
	three := write(`{"status":200,"readyConnections":3}`)
	for _, tc := range []struct {
		name     string
		expected int
		outcome  string
		code     string
	}{
		{"below", 4, "policy", "connections_below_expected"},
		{"equal", 3, "ok", ""},
		{"above", 1, "ok", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := collect(t, model.CloudflaredOptions{ExpectedConnections: &tc.expected}, three, connectedMetrics(t))
			if err != nil {
				t.Fatal(err)
			}
			if o.Outcome != tc.outcome || o.Code != tc.code {
				t.Fatalf("got %+v", o)
			}
		})
	}
}

func TestCollectExpectedColosIsPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		colos   []string
		outcome string
		code    string
		metrics func(http.ResponseWriter)
	}{
		{name: "outside", colos: []string{"HKG", "NRT"}, outcome: "policy", code: "colo_outside_expected"},
		{name: "case insensitive match", colos: []string{"SIN", "HKG", "LAX", "NRT"}, outcome: "ok"},
		{name: "lowercase match", colos: []string{"hkg", "lax", "nrt", "sin"}, outcome: "ok"},
		{name: "unset", outcome: "ok"},
		{name: "absent family is no violation", colos: []string{"HKG"}, outcome: "ok", metrics: write("cloudflared_tunnel_ha_connections 4\nbuild_info{version=\"2026.10.0\"} 1\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.metrics
			if m == nil {
				m = connectedMetrics(t)
			}
			o, err := collect(t, model.CloudflaredOptions{ExpectedColos: tc.colos}, write(readyFour), m)
			if err != nil {
				t.Fatal(err)
			}
			if o.Outcome != tc.outcome || o.Code != tc.code {
				t.Fatalf("got %+v", o)
			}
		})
	}
}

// The policy compares every distinct colo the report names, including one
// beyond the eight the fact can carry: a ninth colo that sorts last must still
// be named as outside the declared list.
func TestCollectColoPolicyUsesFullSet(t *testing.T) {
	var lines []string
	for _, colo := range []string{"hkg", "iad", "jfk", "lax", "nrt", "ord", "sea", "sfo", "sin"} {
		lines = append(lines, `cloudflared_tunnel_server_locations{edge_location="`+colo+`"} 1`)
	}
	metrics := write(strings.Join(lines, "\n") + "\n")
	for _, tc := range []struct {
		name    string
		colos   []string
		outcome string
		code    string
	}{
		{"ninth outside the list", []string{"HKG", "IAD", "JFK", "LAX", "NRT", "ORD", "SEA", "SFO"}, "policy", "colo_outside_expected"},
		{"first outside the list, ninth declared", []string{"IAD", "JFK", "LAX", "NRT", "ORD", "SEA", "SFO", "SIN"}, "policy", "colo_outside_expected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := collect(t, model.CloudflaredOptions{ExpectedColos: tc.colos}, write(readyFour), metrics)
			if err != nil {
				t.Fatal(err)
			}
			if o.Outcome != tc.outcome || o.Code != tc.code {
				t.Fatalf("a colo beyond the capped fact was not compared: %+v", o)
			}
			if got := strings.Split(o.Facts["connection_colos"], ","); len(got) != MaxColos || strings.Contains(o.Facts["connection_colos"], "sin") {
				t.Fatalf("fact is not the capped list: %q", o.Facts["connection_colos"])
			}
		})
	}
}

func TestCollectConnectorIDShape(t *testing.T) {
	body := `{"status":200,"readyConnections":1,"connectorId":"not a uuid"}`
	o, err := collect(t, model.CloudflaredOptions{}, write(body), connectedMetrics(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := o.Facts["connector_id"]; ok {
		t.Fatal("non-UUID connector id kept")
	}
	// The metrics fixture's colos still arrive; the fact budget holds.
	if len(o.Facts) > 8 {
		t.Fatal(o.Facts)
	}
}

func TestCollectRejectsInvalidOptionsAndTarget(t *testing.T) {
	x := model.Check{ID: "tunnel", Kind: "cloudflared"}
	o := model.Observation{Facts: map[string]string{}}
	bad := 9
	x.Adapter.Cloudflared = &model.CloudflaredOptions{ExpectedConnections: &bad}
	if err := Collect(context.Background(), Dependencies{Client: testClient()}, x, "http://127.0.0.1:20241", &o); err == nil {
		t.Fatal("invalid options accepted")
	}
	x.Adapter.Cloudflared = nil
	if err := Collect(context.Background(), Dependencies{Client: testClient()}, x, "http://example.com:20241", &o); err == nil {
		t.Fatal("remote target accepted")
	}
}

// /ready decides the outcome. When it says the connector has no session, a
// missing or broken /metrics cannot turn that into a monitor error; the
// metrics read is only a best-effort fact source.
func TestCollectReadyDecidesBeforeMetrics(t *testing.T) {
	unready := string(readFixture(t, "ready-503.json"))
	for _, tc := range []struct {
		name    string
		ready   func(http.ResponseWriter)
		metrics func(http.ResponseWriter)
	}{
		{"503 and metrics 404", status(http.StatusServiceUnavailable, unready), status(http.StatusNotFound, "not found")},
		{"503 and metrics 500", status(http.StatusServiceUnavailable, unready), status(http.StatusInternalServerError, "boom")},
		{"503 and empty metrics", status(http.StatusServiceUnavailable, unready), write("")},
		{"zero and metrics 404", write(`{"status":200,"readyConnections":0}`), status(http.StatusNotFound, "not found")},
		{"zero and empty metrics", write(`{"status":200,"readyConnections":0}`), write("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := collect(t, model.CloudflaredOptions{}, tc.ready, tc.metrics)
			if err != nil {
				t.Fatal(err)
			}
			if o.Outcome != "fail" || o.Code != "tunnel_not_ready" || o.Stage != "adapter" {
				t.Fatalf("got %+v", o)
			}
			if o.Facts["ready_connections"] == "" {
				t.Fatal("the connector's own ready count is missing")
			}
			if _, ok := o.Facts["ha_connections"]; ok {
				t.Fatal("a failed metrics read contributed facts")
			}
		})
	}
	// A 503 whose body is not the documented JSON is a monitor error before
	// readiness is judged.
	o, err := collect(t, model.CloudflaredOptions{}, status(http.StatusServiceUnavailable, "<html>busy</html>"), connectedMetrics(t))
	if err == nil {
		t.Fatalf("non-JSON 503 accepted: %+v", o)
	}
}

// Once /ready says a session exists, unreadable metrics are a monitor error.
func TestCollectEstablishedRequiresMetrics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		metrics func(http.ResponseWriter)
	}{
		{"404", status(http.StatusNotFound, "not found")},
		{"500", status(http.StatusInternalServerError, "boom")},
		{"empty", write("")},
		{"foreign", write("go_goroutines 24\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if o, err := collect(t, model.CloudflaredOptions{}, write(readyFour), tc.metrics); err == nil {
				t.Fatalf("unreadable metrics accepted: %+v", o)
			}
		})
	}
}

// The unready capture and the connected metrics capture are separate facts:
// a live connector with no session still reports its counters.
func TestCollectLiveUnreadyCapture(t *testing.T) {
	o, err := collect(t, model.CloudflaredOptions{},
		write(string(readFixture(t, "ready-503.json"))),
		write(string(readFixture(t, "metrics.txt"))))
	if err != nil {
		t.Fatal(err)
	}
	if o.Outcome != "fail" || o.Code != "tunnel_not_ready" {
		t.Fatalf("got %+v", o)
	}
	want := map[string]string{
		"ready_connections": "0",
		"ha_connections":    "1",
		"requests_total":    "0",
		"request_errors":    "0",
		"connector_version": "2026.10.0",
	}
	for key, value := range want {
		if o.Facts[key] != value {
			t.Fatalf("%s=%q; want %q", key, o.Facts[key], value)
		}
	}
}

func TestCollectReady200Fixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "2026.10.0", "ready-200.synthetic.json"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := collect(t, model.CloudflaredOptions{}, write(string(body)), connectedMetrics(t))
	if err != nil {
		t.Fatal(err)
	}
	if o.Outcome != "ok" || o.Facts["ready_connections"] != "4" {
		t.Fatalf("got %+v", o)
	}
}

func TestCollectReadsReadyThenMetricsWithGET(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/ready":
			io.WriteString(w, readyFour)
		case "/metrics":
			io.WriteString(w, string(readFixture(t, "metrics-connected.synthetic.txt")))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	x := model.Check{ID: "tunnel", Observer: "home", Kind: "cloudflared", TimeoutSeconds: 5}
	o := model.Observation{Facts: map[string]string{}}
	if err := Collect(context.Background(), Dependencies{Client: testClient()}, x, s.URL, &o); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, []string{"GET /ready", "GET /metrics"}) {
		t.Fatalf("reads %v", seen)
	}
}

func TestPartialMetricsPreservesReadyEvidence(t *testing.T) {
	o, err := collect(t, model.CloudflaredOptions{}, write(readyFour), status(500, "unavailable"))
	if err == nil || o.Facts["ready_connections"] != "4" || o.Facts["connector_ready"] != "true" || o.Facts["metrics_status"] != "unavailable" || o.Facts["connector_id"] == "" {
		t.Fatal(err, o)
	}
	if o.Facts["ha_connections"] != "" {
		t.Fatal("invented metrics", o)
	}
}
