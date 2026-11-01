package cloudflare_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func testIntPtr(v int) *int { return &v }

func TestTunnelReadAndPolicyEvidenceRemainDistinct(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", "test-token")
	for _, tc := range []struct {
		name, body, outcome, policy string
		count                       *int
		colos                       []string
	}{
		{"no policy", `{"status":"degraded","connections":[]}`, "ok", "not_configured", nil, nil},
		{"count and colo", `{"status":"degraded","connections":[]}`, "unknown", "unverified", testIntPtr(4), []string{"HKG"}},
		{"count alone", `{"status":"healthy","connections":[]}`, "unknown", "unverified", testIntPtr(4), nil},
		{"colo alone", `{"status":"healthy","connections":[]}`, "unknown", "unverified", nil, []string{"HKG"}},
		{"reported below", `{"status":"down","connections":[]}`, "policy", "violated", testIntPtr(4), []string{"HKG"}},
		{"no live colo to verify", `{"status":"down","connections":[]}`, "unknown", "unverified", nil, []string{"HKG"}},
		{"reported match", `{"status":"healthy","connections":[{"colo_name":"HKG"}]}`, "ok", "matched", testIntPtr(1), []string{"HKG"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"success":true,"result":`+tc.body+`}`)
			}))
			defer srv.Close()
			options := model.CloudflareOptions{Read: "tunnel", AccountID: strings.Repeat("1", 32), TunnelID: strings.Repeat("2", 32)}
			options.ExpectedConnections, options.ExpectedColos = tc.count, tc.colos
			x := model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{TokenEnv: "TOPH_CLOUDFLARE_TEST_TOKEN", Cloudflare: &options}}
			o := &model.Observation{Facts: map[string]string{}}
			if err := cloudflare.Collect(context.Background(), cloudflare.Dependencies{Client: func(model.Check, string) (*http.Client, func(), error) { return srv.Client(), func() {}, nil }}, x, srv.URL, o); err != nil {
				t.Fatal(err)
			}
			o.Evidence = "reported"
			if policy := view.AdapterPolicy(x, model.CheckState{Freshness: "fresh", Last: o}); o.Outcome != tc.outcome || policy != tc.policy || o.Stage != "adapter_api" {
				t.Fatalf("outcome=%s policy=%s observation=%+v", o.Outcome, policy, o)
			}
		})
	}
}
