package outbox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestAliasFailoverRetainsFailedAddressDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		attempts   int
		status     int
	}{
		{"identity_unavailable", "tcp_failure", 2, 0},
		{"gateway", "server_error", 2, 503},
		{"interrupted_post", "connection_closed", 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testkit.Config()
			good, st := collector(t, c, nil)
			target, err := url.Parse(good.URL)
			if err != nil {
				t.Fatal(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					proxy.ServeHTTP(w, r)
					return
				}
				if tc.name == "interrupted_post" {
					_, _ = io.Copy(io.Discard, r.Body)
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				w.WriteHeader(503)
			}))
			defer primary.Close()
			if tc.name == "identity_unavailable" {
				primary.Close()
			}
			d, err := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: primary.URL, AlternateURLs: []string{good.URL}, CollectorID: st.CollectorID(), Token: workerToken}}}, config.Revision(c))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			now := time.Now().UTC()
			d.Now = func() time.Time { return now }
			q := queue(t)
			if err = q.Add(makeBatch(1), d.Recipients()); err != nil {
				t.Fatal(err)
			}
			r := d.Tick(context.Background(), q, nil)[0]
			status := d.Statuses()[0]
			if r.Error != "" || r.Accepted != 1 || r.Code != "delivery_failover_recovered" || r.Diagnostic.Attempts != tc.attempts || r.Diagnostic.AddressAttempts != 2 || r.Diagnostic.RetryOutcome != "recovered" || r.Diagnostic.PreviousCode != tc.code || r.Diagnostic.PreviousHTTPStatus != tc.status {
				t.Fatal("failover diagnostics were overwritten", r)
			}
			failure := status.LastFailure
			if status.Code != "" || failure.Code != tc.code || failure.HTTPStatus != tc.status || failure.At.IsZero() || !model.ValidDelivery(d.Statuses(), now) {
				t.Fatal("failed address was lost or invalid", status)
			}
			raw, _ := json.Marshal(status)
			if strings.Contains(string(raw), workerToken) || strings.Contains(string(raw), primary.URL) {
				t.Fatal("diagnostics contain credentials or addresses")
			}
			// A later healthy round must not erase the retained failure.
			now = now.Add(10 * time.Second)
			if err = q.Add(makeBatch(2), d.Recipients()); err != nil {
				t.Fatal(err)
			}
			if r = d.Tick(context.Background(), q, nil)[0]; r.Error != "" || r.Accepted != 1 || r.Diagnostic.AddressAttempts != 1 || d.Statuses()[0].LastFailure != failure {
				t.Fatal("healthy round erased failure or repeated failover", r, d.Statuses())
			}
			hb := &model.Heartbeat{Observer: "home", Boot: "worker", SampledAt: now, Contracts: config.ObserverContracts(c, "home")}
			if r = d.Tick(context.Background(), q, hb)[0]; r.Error != "" {
				t.Fatal("collector rejected enriched diagnostics", r)
			}
			_, beats := st.View()
			if h := beats["home"]; len(h.Delivery) != 1 || h.Delivery[0].LastFailure != failure {
				t.Fatal("retained failure did not cross the wire", h)
			}
			now = now.Add(failureRetention + time.Second)
			r = d.Tick(context.Background(), q, nil)[0]
			if !r.Diagnostic.At.IsZero() || !d.Statuses()[0].LastFailure.At.IsZero() {
				t.Fatal("retained failure outlived its window")
			}
		})
	}
}

func TestFailedAliasesKeepBoundedAttemptCounts(t *testing.T) {
	addresses := make([]string, 8)
	servers := make([]*httptest.Server, len(addresses))
	for i := range addresses {
		srv := httptest.NewServer(http.NotFoundHandler())
		addresses[i] = srv.URL
		servers[i] = srv
	}
	for _, srv := range servers {
		srv.Close()
	}
	c := testkit.Config()
	d, err := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: addresses[0], AlternateURLs: addresses[1:], CollectorID: strings.Repeat("a", 64), Token: workerToken}}}, config.Revision(c))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	q := queue(t)
	if err = q.Add(makeBatch(1), d.Recipients()); err != nil {
		t.Fatal(err)
	}
	r := d.Tick(context.Background(), q, nil)[0]
	if r.Error == "" || r.Accepted != 0 || r.Diagnostic.Attempts != 8 || r.Diagnostic.AddressAttempts != 8 || r.Diagnostic.RetryOutcome != "failed" || !model.ValidDelivery(d.Statuses(), time.Now()) {
		t.Fatal("failed aliases lost bounded telemetry", r, d.Statuses())
	}
	if n, _ := q.Stats(); n != 1 {
		t.Fatal("failed delivery consumed pending sample", n)
	}
}

func TestCombinedAttemptDurationStaysWithinWireBudget(t *testing.T) {
	now := time.Now().UTC()
	first := model.TransportEvent{At: now, Stage: "response", Code: "response_timeout", Attempts: 1, AddressAttempts: 1, DurationMS: 80000}
	second := model.TransportEvent{At: now, Stage: "tls", Code: "tls_timeout", Attempts: 1, AddressAttempts: 1, DurationMS: 80000}
	e := combineAttempts(first, second)
	if e.DurationMS != 120000 || !model.ValidTransportEvent(e, now) {
		t.Fatal("combined elapsed time violates wire contract", e)
	}
}
