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
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/tui"
	"github.com/WhitenWhiten/topophany/internal/view"
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
			fmt.Fprintf(w, `{"id":%q,"last_updated":%d,"cpu":25,"ram_total":1024,"ram_used":512,"disk_total":1000,"disk_used":250,"net_in_speed":5120,"net_out_speed":2048,"swap_total":1024,"swap_used":128,"load_avg":"0.75 0.50 0.25"}`, id, at.UnixMilli())
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
	now := time.Now()
	m := tui.Model{Snapshot: snap, Direct: true, Tab: view.Servers, Now: now, Width: 140, Height: 40, NoColor: true}
	rendered := strings.Join(m.Render(), "\n")
	// The card says each reading under its panel; laid open, in full.
	for _, reading := range []string{"CF-Server-Monitor direct", "REPORTED RESOURCES", "load", "0.75", "swap", "12%", "↓5.0K", "↑2.0K"} {
		if !strings.Contains(rendered, reading) {
			t.Errorf("direct Servers view omitted %q\n%s", reading, rendered)
		}
	}
	m.Key("enter")
	rendered = strings.Join(m.Render(), "\n")
	for _, reading := range []string{"↓ 5.0 KiB/s", "↑ 2.0 KiB/s", "128.0 MiB / 1.0 GiB", "0.75  0.50  0.25"} {
		if !strings.Contains(rendered, reading) {
			t.Errorf("direct server card omitted %q\n%s", reading, rendered)
		}
	}
	p := monitor.Build(monitor.Config{Source: "direct", Template: monitor.DefaultDirectTemplate()}, monitor.Input{Snapshot: snap}, now)
	servers, ok := p.Rows[1].Value.([]monitor.ServerListEntry)
	if !ok || len(servers) != 1 || servers[0].Metrics == nil {
		t.Fatalf("default monitor output has no direct resource metrics: %+v", p.Rows[1])
	}
	r := servers[0].Metrics
	if r.Network.RXBytesPerSecond == nil || *r.Network.RXBytesPerSecond != 5120 ||
		r.Swap.UsedBytes == nil || *r.Swap.UsedBytes != 128<<20 ||
		r.Load.Avg15m == nil || *r.Load.Avg15m != 0.25 || !r.SampledAt.Equal(at) {
		t.Fatalf("monitor did not preserve the direct reading: %+v", r)
	}
}
