package stream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestCollectorReadFailureRecoveryAndOwnership(t *testing.T) {
	c := testkit.Config()
	var unavailable atomic.Bool
	var forbidden atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if forbidden.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if unavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"private-response-secret"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(engine.Build(c, "fixture", "", nil, nil, time.Now().UTC()))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := client.Save(path, client.Credentials{Schema: 1, Active: "default", Profiles: map[string]client.Profile{
		"default": {Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: strings.Repeat("a", 64)}}},
	}}); err != nil {
		t.Fatal(err)
	}
	r := request()
	r.Source, r.Lang, r.Credentials = "client", "", path
	src, e := OpenSource(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	good, err := src.Fetch(context.Background())
	if err != nil || good.CollectorRead.State != "ok" || good.CollectorRead.LastSuccessAt.IsZero() {
		t.Fatal(good.CollectorRead, err)
	}
	unavailable.Store(true)
	for n := uint64(1); n <= 2; n++ {
		bad, err := src.Fetch(context.Background())
		if err != nil || bad.Unavailable || bad.CollectorRead.State != "error" || bad.CollectorRead.Stage != "http" || bad.CollectorRead.ConsecutiveFailures != n || bad.CollectorRead.LastSuccessAt != good.CollectorRead.LastSuccessAt {
			t.Fatal(bad.CollectorRead, err)
		}
		p := monitor.Build(monitor.Config{Source: "client", Template: r.Template}, bad, time.Now())
		b, _ := json.Marshal(p)
		for _, secret := range []string{server.URL, path, strings.Repeat("a", 64), "private-response-secret"} {
			if strings.Contains(string(b), secret) {
				t.Fatal("private data in diagnostics")
			}
		}
		cloned := p.Clone()
		cloned.CollectorRead.State = "mutated"
		if p.CollectorRead.State != "error" || bad.CollectorRead.State != "error" {
			t.Fatal("aliased diagnostics")
		}
		fresh := monitor.Build(monitor.Config{Source: "client", Template: r.Template}, good, p.GeneratedAt)
		for i, row := range p.Rows {
			if row.ValidUntil != fresh.Rows[i].ValidUntil || row.SampledAt != fresh.Rows[i].SampledAt {
				t.Fatal("failed read extended evidence", row)
			}
		}
		expired := monitor.Build(monitor.Config{Source: "client", Template: r.Template}, bad, good.Snapshot.GeneratedAt.Add(16*time.Second))
		for _, row := range expired.Rows {
			if row.Value != nil {
				t.Fatal("failed read revived expired evidence", row)
			}
		}
	}
	if src.Initial.CollectorRead.State != "waiting" || good.CollectorRead.State != "ok" {
		t.Fatal("old input changed")
	}
	unavailable.Store(false)
	recovered, err := src.Fetch(context.Background())
	if err != nil || recovered.CollectorRead.State != "ok" || recovered.CollectorRead.ConsecutiveFailures != 0 || recovered.CollectorRead.Code != "" || !recovered.CollectorRead.LastSuccessAt.After(good.CollectorRead.LastSuccessAt) {
		t.Fatal(recovered.CollectorRead, err)
	}
	forbidden.Store(true)
	denied, err := src.Fetch(context.Background())
	if err == nil || !denied.Unavailable || denied.CollectorRead.State != "error" {
		t.Fatal("authorization rejection retained live evidence", denied, err)
	}
}
