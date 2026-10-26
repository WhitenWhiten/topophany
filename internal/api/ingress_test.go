package api

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestViewerIngressSourcesArePseudonymousByDefault(t *testing.T) {
	srv, h, b := fixture(t)
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatalf("ingest: %d %s", w.Code, w.Body.String())
	}
	ing := NewIngressCounter(time.Minute)
	ing.now = srv.Now
	ing.observe("http: TLS handshake error from 100.74.137.62:44484: i/o timeout")
	ing.observe("http: TLS handshake error from 100.74.137.62:44485: EOF")
	srv.Ingress = ing

	w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	if w.Code != 200 {
		t.Fatalf("snapshot: %d %s", w.Code, w.Body.String())
	}
	var snap model.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Ingress == nil || snap.Ingress.Failures != 2 || snap.Ingress.SourceCount != 1 || len(snap.Ingress.Sources) != 1 {
		t.Fatalf("snapshot did not carry ingress: %+v", snap.Ingress)
	}
	got := snap.Ingress.Sources[0]
	if got.Address == "100.74.137.62" || !strings.HasPrefix(got.Address, "source-") {
		t.Fatalf("viewer learned the raw source address: %+v", got)
	}
	if got.Count != 2 {
		t.Fatalf("pseudonym lost the count: %+v", got)
	}
}

func TestIngressCountsOnlyHandshakeFailures(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c := NewIngressCounter(time.Minute)
	c.now = func() time.Time { return now }
	var logged []string
	w := c.Writer(func(line string) { logged = append(logged, line) })
	for _, line := range []string{
		"http: TLS handshake error from 100.74.137.62:44484: EOF",
		"http: TLS handshake error from 100.74.137.62:44485: read tcp 100.111.75.1:7447->100.74.137.62:44485: i/o timeout",
		"http: TLS handshake error from [2605:52c0::1]:443: remote error: tls: bad certificate",
		"2026/10/17 06:39:20 http: TLS handshake error from 100.75.127.43:1: i/o timeout",
		"prefix http: TLS handshake error from 198.51.100.9:443: quoted",
		"2026/10/17 06:39:20 incident http: TLS handshake error from 198.51.100.10:443: quoted",
		"http: TLS handshake error from 100.75.127.43:1",
		"http: TLS handshake error from 10.0.0.1:not-a-port: EOF",
		"incident opened: s2-local/path/s2-collector",
		"http: TLS handshake error from bad",
	} {
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if len(logged) != 10 || logged[0] != "collector ingress stage=tls code=connection_closed" {
		t.Fatalf("writer did not sanitize handshake diagnostics: %q", logged)
	}
	got := c.Summary(now)
	if got == nil || got.Failures != 4 || got.SourceCount != 3 || got.WindowSeconds != 60 {
		t.Fatalf("counted %+v", got)
	}
	if len(got.Sources) != 3 {
		t.Fatalf("sources %+v", got.Sources)
	}
	if got.Sources[0].Address != "100.74.137.62" || got.Sources[0].Count != 2 {
		t.Fatalf("busiest source first: %+v", got.Sources)
	}
	if !got.Last.Equal(now) {
		t.Fatalf("last failure %v", got.Last)
	}
}

func TestIngressZeroValueIsSafe(t *testing.T) {
	var c IngressCounter
	if _, err := c.Writer(nil).Write([]byte("http: TLS handshake error from 192.0.2.1:443: EOF\n")); err != nil {
		t.Fatal(err)
	}
	if got := c.Summary(time.Now()); got != nil {
		t.Fatalf("zero counter counted: %+v", got)
	}
}

func TestIngressShowsTheBusiestAndCountsEverySource(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c := NewIngressCounter(time.Minute)
	c.now = func() time.Time { return now }
	for i := 0; i < 12; i++ {
		c.observe("http: TLS handshake error from 192.0.2." + itoa(i) + ":1: EOF")
	}
	got := c.Summary(now)
	if got == nil || got.Failures != 12 || got.SourceCount != 12 || got.Truncated {
		t.Fatalf("source count: %+v", got)
	}
	if len(got.Sources) != shownIngressSources {
		t.Fatalf("source list not bounded: %+v", got.Sources)
	}
}

func TestIngressReadsTheServersFormattedLog(t *testing.T) {
	c := NewIngressCounter(time.Minute)
	var kept bytes.Buffer
	// The collector gives this writer to log.Logger with LstdFlags, so the
	// line arrives with a timestamp in front of net/http's message.
	logger := log.New(c.Writer(func(line string) { kept.WriteString(line + "\n") }), "", log.LstdFlags)
	logger.Print("http: TLS handshake error from 100.74.137.62:44484: i/o timeout")
	summary := c.Summary(time.Now())
	if summary == nil || summary.Failures != 1 || summary.SourceCount != 1 || summary.Sources[0].Address != "100.74.137.62" {
		t.Fatalf("formatted log line was not read: %+v", summary)
	}
	if !strings.Contains(kept.String(), "collector ingress stage=tls code=tls_timeout") {
		t.Fatalf("line was not safely classified: %q", kept.String())
	}
}

func TestIngressForgetsOutsideTheWindowAndBoundsSources(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c := NewIngressCounter(time.Minute)
	c.now = func() time.Time { return now }
	c.observe("http: TLS handshake error from 10.0.0.1:1: EOF")
	if s := c.Summary(now); s == nil || s.Failures != 1 {
		t.Fatal("failure not counted", s)
	}
	now = now.Add(2 * time.Minute)
	if s := c.Summary(now); s != nil {
		t.Fatal("failure outlived its window", s)
	}

	// More addresses than the bound must not grow the map without limit.
	// A live source already in the map must survive the flood, and failures
	// from addresses that cannot be tracked still count in the total.
	c.observe("http: TLS handshake error from 192.0.2.10:1: EOF")
	for i := 0; i < maxIngressSources+32; i++ {
		now = now.Add(time.Millisecond)
		c.observe("http: TLS handshake error from 10.1." + itoa(i/256) + "." + itoa(i%256) + ":1: EOF")
	}
	if len(c.sources) > maxIngressSources {
		t.Fatalf("source map grew past its bound: %d", len(c.sources))
	}
	if _, ok := c.sources["192.0.2.10"]; !ok {
		t.Fatal("a live source was evicted to make room")
	}
	s := c.Summary(now)
	if s == nil || s.Failures != 1+maxIngressSources+32 || s.SourceCount != maxIngressSources || !s.Truncated {
		t.Fatalf("summary lost failures or sources: %+v", s)
	}
	if len(s.Sources) > shownIngressSources {
		t.Fatalf("summary not bounded: %+v", s)
	}
}

func TestIngressWindowIsRollingNotLifetime(t *testing.T) {
	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	now := base
	c := NewIngressCounter(10 * time.Minute)
	c.now = func() time.Time { return now }
	for i := 0; i < 30; i++ {
		now = base.Add(time.Duration(i) * time.Minute)
		c.observe("http: TLS handshake error from 192.0.2.10:1: EOF")
	}
	got := c.Summary(base.Add(29 * time.Minute))
	if got == nil {
		t.Fatal("rolling window lost every failure")
	}
	if got.Failures >= 30 {
		t.Fatalf("window kept a lifetime count: %+v", got)
	}
	if got.Failures < 9 || got.Failures > 11 {
		t.Fatalf("window count is not the last ten minutes: %+v", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestSnapshotCarriesIngressHandshakeFailures(t *testing.T) {
	srv, h, b := fixture(t)
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatalf("ingest: %d %s", w.Code, w.Body.String())
	}
	ing := NewIngressCounter(time.Minute)
	ing.now = srv.Now
	ing.observe("http: TLS handshake error from 100.74.137.62:44484: i/o timeout")
	ing.observe("http: TLS handshake error from 100.74.137.62:44485: EOF")
	srv.Ingress = ing
	srv.ExposeIngressSources = true

	w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	if w.Code != 200 {
		t.Fatalf("snapshot: %d %s", w.Code, w.Body.String())
	}
	var snap model.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Ingress == nil || snap.Ingress.Failures != 2 || snap.Ingress.SourceCount != 1 || len(snap.Ingress.Sources) != 1 {
		t.Fatalf("snapshot did not carry ingress: %+v", snap.Ingress)
	}
	if snap.Ingress.Sources[0].Address != "100.74.137.62" || snap.Ingress.Sources[0].Count != 2 {
		t.Fatalf("source summary: %+v", snap.Ingress.Sources)
	}
}
