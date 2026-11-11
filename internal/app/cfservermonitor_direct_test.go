package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

func TestCFServerMonitorDirectSetupAndViewer(t *testing.T) {
	const id = "12345678-1234-4234-8234-123456789abc"
	at := time.Now().UTC().Truncate(time.Millisecond)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
			t.Error("anonymous Worker read sent unexpected method or credentials")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RequestURI() {
		case "/api/servers":
			fmt.Fprintf(w, `{"servers":[{"id":%q,"name":"Worker fixture"}]}`, id)
		case "/api/server?id=" + id:
			fmt.Fprintf(w, `{"id":%q,"last_updated":%d,"cpu":25,"ram_total":1024,"ram_used":512,"disk_total":1000,"disk_used":250}`, id, at.UnixMilli())
		default:
			t.Error("unexpected endpoint", r.URL.RequestURI())
			w.WriteHeader(404)
		}
	}))
	defer h.Close()
	root := filepath.Join(t.TempDir(), "direct")
	input := strings.Join([]string{"desktop", "cfsm", "Home Worker", "cf-server-monitor", h.URL, "anonymous", id, ""}, "\n")
	if out, diag, err := invoke(input, "setup", "--task", "direct", "--direct-root", root); err != nil ||
		!strings.Contains(out, "Worker fixture") || !strings.Contains(out, "Saved profile desktop") {
		t.Fatalf("setup failed: %v\n%s\n%s", err, out, diag)
	}
	flags := []string{"--source", "direct", "--profile", "desktop", "--direct-root", root}
	out, diag, err := invoke("", append(flags, "--json")...)
	if err != nil {
		t.Fatalf("direct viewer failed: %v\n%s\n%s", err, out, diag)
	}
	var snap model.Snapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil || len(snap.External) != 1 || snap.External[0].Freshness != "fresh" {
		t.Fatal("viewer has no Worker resource sample", err)
	}
	m := tui.Model{Snapshot: snap, Direct: true, Now: time.Now(), Width: 140, Height: 40, NoColor: true}
	if rendered := strings.Join(m.Render(), "\n"); !strings.Contains(rendered, "CF-Server-Monitor direct") ||
		!strings.Contains(rendered, "RESOURCE DATA CURRENT") {
		t.Fatal("TUI did not show current CF-Server-Monitor resources")
	}
}
