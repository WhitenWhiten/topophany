package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// An old worker, or a previously stored observation, can still contain unsafe
// version strings. All viewer-facing projections must withhold them.
func TestCloudflareLegacyVersionsWithheldFromViewer(t *testing.T) {
	srv, h, _ := fixture(t)
	c := srv.Config
	c.Checks = slices.Clone(c.Checks)
	for i := range c.Checks {
		if c.Checks[i].ID == "home-http" {
			c.Checks[i].Kind = "cloudflare"
			c.Checks[i].Adapter = model.AdapterOptions{TokenEnv: "CF_TOKEN", Cloudflare: &model.CloudflareOptions{Read: "tunnel", AccountID: strings.Repeat("1", 32), TunnelID: strings.Repeat("2", 32)}}
		}
	}
	if err := srv.Reload(c, srv.Auth); err != nil {
		t.Fatal(err)
	}
	o := testkit.Observation(c, "home-http", 1, srv.Now().Add(-time.Second), "ok")
	o.Evidence, o.Stage, o.Connectivity = "reported", "adapter_api", false
	o.PathEvidence = model.RemotePlatformPathEvidence
	o.Facts = map[string]string{"tunnel_status": "healthy", "tunnel_connections": "2", "connector_versions": "v-secret-fragment-one,v-secret-fragment-two"}
	b := model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{o}}
	if w := request(h, http.MethodPost, "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != http.StatusAccepted {
		t.Fatalf("ingest: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/snapshot", "/v1/history?check=home-http"} {
		w := request(h, http.MethodGet, path, viewerToken, "", nil)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "connector_versions") || strings.Contains(w.Body.String(), "secret-fragment") || !strings.Contains(w.Body.String(), "tunnel_connections") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	history, err := srv.MonitorHistory("home-http", 1)
	if err != nil || len(history) != 1 || history[0].Facts["connector_versions"] != "" || history[0].Facts["tunnel_connections"] != "2" {
		raw, _ := json.Marshal(history)
		t.Fatalf("monitor history: %s %v", raw, err)
	}
	rows, _ := srv.Store.View()
	if len(rows) != 1 || rows[0].Facts["connector_versions"] == "" {
		t.Fatal("viewer projection mutated the stored observation")
	}
}
