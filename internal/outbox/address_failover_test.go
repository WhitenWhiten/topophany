package outbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestAliasGatewayFailoverPreservesRecipientRejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
		retry  bool
	}{
		{"bad_gateway", 502, "", true},
		{"unavailable", 503, "", true},
		{"gateway_timeout", 504, "", true},
		{"edge_gateway", 520, "", true},
		{"authorization", 401, "invalid_token", false},
		{"permission", 403, "role_forbidden", false},
		{"configuration", 409, "contract_mismatch", false},
		{"throttling", 429, "rate_limited", false},
		{"storage", 503, "storage_receipt_capacity", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testkit.Config()
			good, st := collector(t, c, nil)
			target, err := url.Parse(good.URL)
			if err != nil {
				t.Fatal(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			var posts atomic.Int32
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					proxy.ServeHTTP(w, r)
					return
				}
				posts.Add(1)
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": tc.code})
			}))
			defer primary.Close()
			d, err := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: primary.URL, AlternateURLs: []string{good.URL}, CollectorID: st.CollectorID(), Token: workerToken}}}, config.Revision(c))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			q := queue(t)
			batch := makeBatch(1)
			if err = q.Add(batch, d.Recipients()); err != nil {
				t.Fatal(err)
			}
			result := d.Tick(context.Background(), q, nil)[0]
			rows, _ := st.View()
			if posts.Load() != 1 {
				t.Fatal("primary POST count", posts.Load())
			}
			if !tc.retry {
				if result.Error == "" || result.Accepted != 0 || len(rows) != 0 || d.destinations[0].address != 0 {
					t.Fatal("recipient rejection bypassed", result, rows)
				}
				return
			}
			if result.Error != "" || result.Accepted != 1 || len(rows) != 1 || rows[0].ID != batch.Observations[0].ID || !rows[0].SampledAt.Equal(batch.Observations[0].SampledAt) {
				t.Fatal("gateway failover changed or lost sample", result, rows)
			}
			if n, _ := q.Stats(); n != 0 || d.destinations[0].address != 1 {
				t.Fatal("successful address was not retained or receipt still pending", n)
			}
		})
	}
}
