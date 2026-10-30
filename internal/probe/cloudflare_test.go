package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func cloudflareCheck(target string) model.Check {
	return model.Check{
		ID: "cf-api", Name: "Cloudflare API", Observer: "home", Kind: "cloudflare",
		Target: target, TimeoutSeconds: 5,
		Adapter: model.AdapterOptions{
			TokenEnv: "TOPH_CLOUDFLARE_PROBE_TOKEN",
			Cloudflare: &model.CloudflareOptions{
				Read:      "tunnel",
				AccountID: "11111111111111111111111111111111",
				TunnelID:  "22222222222222222222222222222222",
			},
		},
	}
}

// Run must mark an API read as reported evidence, drop the request-stream fact
// and keep the monitor source separate from target reachability.
func TestRunCloudflareReportedRead(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_PROBE_TOKEN", "test-token")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, `{"success":true,"result":{"status":"healthy","connections":[{"client_version":"2026.10.0","colo_name":"HKG","is_pending_reconnect":false,"opened_at":"2026-10-19T08:00:00Z","origin_ip":"192.0.2.10"}]}}`)
	}))
	defer srv.Close()
	c := model.Config{Schema: 1, Project: "lab"}
	o := Runner{}.Run(context.Background(), c, cloudflareCheck(srv.URL))
	if o.Outcome != "ok" || o.Evidence != "reported" || o.Stage != "adapter_api" {
		t.Fatalf("observation %+v", o)
	}
	if o.PathEvidence != cloudflare.PathEvidence || o.Connectivity {
		t.Fatalf("envelope %+v", o)
	}
	if o.Facts["tunnel_status"] != "healthy" || o.Facts["tunnel_colos"] != "HKG" || o.Facts["peer_text"] != "withheld_secret_request" {
		t.Fatalf("facts %+v", o.Facts)
	}
	if _, ok := o.Facts["connection_semantics"]; ok {
		t.Fatal("cloudflare keeps the request-stream fact")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("requests=%d", got)
	}
}

// A reviewed topology with a canonical UUID must reach the existing tunnel
// endpoint through the production transport. Compact IDs keep their spelling.
func TestRunCloudflareTunnelID(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_PROBE_TOKEN", "test-token")
	for _, id := range []string{
		"1bedc50d-42b3-473c-b108-ff3d10c0d925",
		"1BEDC50D-42B3-473C-B108-FF3D10C0D925",
		"22222222222222222222222222222222",
	} {
		t.Run(id, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				want := "/client/v4/accounts/11111111111111111111111111111111/cfd_tunnel/" + id
				if r.Method != http.MethodGet || r.URL.EscapedPath() != want || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing synthetic bearer token")
				}
				io.WriteString(w, `{"success":true,"result":{"status":"healthy"}}`)
			}))
			defer srv.Close()
			x := cloudflareCheck(srv.URL + "/client/v4")
			x.Adapter.Cloudflare.TunnelID = id
			c := testkit.Config()
			c.Checks = []model.Check{x}
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			c, err = config.Decode(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			o := Runner{}.Run(context.Background(), c, c.Checks[0])
			if hits.Load() != 1 || o.Outcome != "ok" || o.Evidence != "reported" || o.PathEvidence != cloudflare.PathEvidence || o.Connectivity {
				t.Fatalf("requests=%d observation=%+v", hits.Load(), o)
			}
		})
	}
}

// A 401 is a monitor error: no downtime, no target failure.
func TestRunCloudflareAuthenticationFailure(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_PROBE_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	o := Runner{}.Run(context.Background(), model.Config{Schema: 1, Project: "lab"}, cloudflareCheck(srv.URL))
	if o.Outcome != "error" || o.Stage != "adapter_api" || o.Code != "authentication_failed" || o.Connectivity {
		t.Fatalf("observation %+v", o)
	}
}

func TestRunCloudflareMissingTokenIsSetupError(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_PROBE_TOKEN", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request sent without a token")
	}))
	defer srv.Close()
	o := Runner{}.Run(context.Background(), model.Config{Schema: 1, Project: "lab"}, cloudflareCheck(srv.URL))
	if o.Outcome != "error" || o.Stage != "setup" || o.Code != "cloudflare_token_missing" {
		t.Fatalf("observation %+v", o)
	}
}

// The production client must not follow a redirect: the bearer token may not
// be forwarded to whatever Location names. The 3xx becomes a monitor error.
func TestRunCloudflareRefusesRedirect(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_PROBE_TOKEN", "test-token")
	var redirected atomic.Bool
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Store(true)
		io.WriteString(w, `{"success":true,"result":{"status":"healthy","connections":[]}}`)
	}))
	defer targetSrv.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetSrv.URL, http.StatusFound)
	}))
	defer srv.Close()
	o := Runner{}.Run(context.Background(), model.Config{Schema: 1, Project: "lab"}, cloudflareCheck(srv.URL))
	if o.Outcome != "error" || o.Stage != "adapter_api" || o.Code != "http_302" {
		t.Fatalf("observation %+v", o)
	}
	if redirected.Load() {
		t.Fatal("the bearer token followed a redirect")
	}
}
