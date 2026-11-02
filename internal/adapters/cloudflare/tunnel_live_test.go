package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const liveFixtureDir = "testdata/api-v4/2026-11-02"

// These are sanitized captures from the registered Tunnel acceptance run,
// rather than the original documentation examples with uppercase colo names.
func TestLiveTunnelColoPolicies(t *testing.T) {
	for _, read := range []string{ReadTunnel, ReadTunnelConnections} {
		t.Run(read, func(t *testing.T) {
			name := "tunnel.json"
			if read == ReadTunnelConnections {
				name = "connections.json"
			}
			body, err := os.ReadFile(filepath.Join(liveFixtureDir, name))
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(body)
			}))
			defer srv.Close()
			for _, tc := range []struct {
				name, token, wantOutcome, wantCode, wantColos string
				expected                                      []string
			}{
				{"reported", "test-api-secret", "ok", "", "LAX", nil},
				{"matching city", "test-api-secret", "ok", "", "LAX", []string{"LAX"}},
				{"outside city", "test-api-secret", "policy", "colo_outside_expected", "LAX", []string{"NRT"}},
				{"normalized token echo withheld", "LAX", "ok", "", "", []string{"LAX"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Setenv("TOPH_CLOUDFLARE_TEST_TOKEN", tc.token)
					options := tunnelOptions()
					options.Read, options.ExpectedColos = read, tc.expected
					options.ExpectedConnections = intPtr(2)
					o := observation()
					if err := Collect(context.Background(), testDependencies(t), adapterCheck(options), srv.URL, o); err != nil {
						t.Fatal(err)
					}
					if o.Outcome != tc.wantOutcome || o.Code != tc.wantCode || o.Facts["tunnel_colos"] != tc.wantColos {
						t.Fatalf("unexpected colo policy: %+v", o)
					}
					if o.Facts["tunnel_connections"] != "2" {
						t.Fatalf("city deduplication changed the session count: %+v", o.Facts)
					}
					if read == ReadTunnel && o.Facts["tunnel_status"] != "healthy" || read == ReadTunnelConnections && o.Facts["tunnel_status"] != "" {
						t.Fatalf("unexpected tunnel status: %+v", o.Facts)
					}
					raw, _ := json.Marshal(o)
					for _, forbidden := range []string{tc.token, "lax08", "lax13"} {
						if strings.Contains(string(raw), forbidden) {
							t.Fatalf("raw location or token exported: %s", raw)
						}
					}
				})
			}
		})
	}
}

func TestTunnelColoPolicyValidatesWholeLocation(t *testing.T) {
	for _, tc := range []struct {
		location, wantColo, wantOutcome string
	}{
		{"LAX", "LAX", "ok"},
		{"lax", "LAX", "ok"},
		{"lax08", "LAX", "ok"},
		{"LaX13", "LAX", "ok"},
		{"lax1", "LAX", "ok"},
		{"lax12345", "LAX", "ok"},
		{"nrt12", "NRT", "policy"},
		{"lax123456", "", "unknown"},
		{"lax-secret", "", "unknown"},
		{"lax12tail", "", "unknown"},
		{"lax12\n", "", "unknown"},
		{" lax12", "", "unknown"},
		{"lax１２", "", "unknown"},
		{"làx12", "", "unknown"},
		{"123", "", "unknown"},
		{"la12", "", "unknown"},
		{"laxx12", "", "unknown"},
		{"", "", "unknown"},
	} {
		t.Run(tc.location, func(t *testing.T) {
			location, _ := json.Marshal(tc.location)
			body := []byte(`{"success":true,"result":{"status":"healthy","connections":[{"colo_name":` + string(location) + `}]}}`)
			options := tunnelOptions()
			options.ExpectedColos = []string{"LAX"}
			o := observation()
			if err := collectTunnel(body, options, o); err != nil {
				t.Fatal(err)
			}
			if o.Outcome != tc.wantOutcome || o.Facts["tunnel_colos"] != tc.wantColo {
				t.Fatalf("location %q: %+v", tc.location, o)
			}
			if tc.wantOutcome == "unknown" && o.Code != "connection_colo_unreported" {
				t.Fatalf("invalid location became a policy match: %+v", o)
			}
		})
	}
}
