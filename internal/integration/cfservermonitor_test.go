package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestCFServerMonitorThroughWorkerCollectorAndMonitor(t *testing.T) {
	const (
		object = "12345678-1234-4234-8234-123456789abc"
		token  = "synthetic.cfsm.sig+nature/"
		env    = "TOPO_CFSM_INTEGRATION_JWT"
	)
	at := time.Now().UTC().Truncate(time.Millisecond).Add(-10 * time.Second)
	var reads atomic.Int32
	var reject atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/monitor/api/server" || r.URL.RawQuery != "id="+object ||
			r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("unexpected CF-Server-Monitor request")
		}
		w.Header().Set("Content-Type", "application/json")
		if reject.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": token})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": object, "name": token, "cpu": 12.5, "ram_total": 8192, "ram_used": 3072,
			"disk_total": 102400, "disk_used": 20480, "last_updated": at.UnixMilli(),
		})
	}))
	t.Cleanup(upstream.Close)
	t.Setenv(env, token)

	c := testkit.Config()
	c.Sources = []model.Source{{
		ID: "cfsm", Adapter: "cf-server-monitor", Observer: "cloud", Target: upstream.URL + "/monitor", CredentialEnv: env,
	}}
	c.Checks = []model.Check{{
		ID: "cfsm-resource", Observer: "cloud", Kind: "external",
		External: &model.ExternalBinding{
			Source: "cfsm", Object: object, Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 180, PrimaryDisk: "aggregate",
		},
	}}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	ch := newChain(t, c)
	ch.scheduler.WithSources(probe.NewSourceRuntime(probe.ProductionSourceAdapters()), ch.enqueue, 1)

	ctx := t.Context()
	ch.once(ctx)
	ch.deliver(ctx)
	first := ch.st.ExternalView().Bindings["cfsm-resource"]
	if first.Last == nil || first.ReportSamples != 1 || !first.Last.ObservedAt.Equal(at) || !first.DataDeadline.Equal(at.Add(180*time.Second)) {
		t.Fatalf("source report lost its original time or deadline: %+v", first)
	}

	ch.once(ctx)
	ch.deliver(ctx)
	repeated := ch.st.ExternalView().Bindings["cfsm-resource"]
	if repeated.ReportSamples != 1 || repeated.Duplicates != 1 || !repeated.DataDeadline.Equal(first.DataDeadline) {
		t.Fatalf("repeated read renewed the resource sample: %+v", repeated)
	}
	panel, raw := monitorFrame(t, ch, time.Now().UTC(), []monitor.Row{{
		ID: "host", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "dmit"},
	}})
	card, ok := panel.Rows[0].Value.(monitor.ServerCard)
	if !ok || len(card.Resources) != 1 {
		t.Fatalf("monitor card = %+v", panel.Rows[0])
	}
	r := card.Resources[0]
	if r.CPU.Percent == nil || *r.CPU.Percent != 12.5 || r.Memory.Percent == nil || *r.Memory.Percent != 37.5 ||
		r.Disk.Percent == nil || *r.Disk.Percent != 20 || r.Origin == nil || r.Origin.Adapter != "cf-server-monitor" {
		t.Fatalf("monitor resource = %+v", r)
	}

	reject.Store(true)
	ch.once(ctx)
	ch.deliver(ctx)
	failed := ch.st.ExternalView()
	b := failed.Bindings["cfsm-resource"]
	if reads.Load() != 3 || failed.Sources["cfsm"].LastCode != "cfsm_auth" || b.ConfirmedDown ||
		b.ReportSamples != 1 || !b.DataDeadline.Equal(first.DataDeadline) {
		t.Fatalf("authentication failure changed resource evidence: %+v, %+v", b, failed.Sources["cfsm"])
	}
	for _, batch := range ch.batches {
		for _, o := range batch.Observations {
			if o.Evidence != "reported" || o.Connectivity || o.External == nil {
				t.Fatalf("source collection became measured reachability: %+v", o)
			}
		}
	}
	persisted, err := json.Marshal(ch.batches)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{string(raw), string(persisted)} {
		if strings.Contains(output, token) || strings.Contains(output, env) {
			t.Fatal("CF-Server-Monitor credentials escaped into observations or monitor output")
		}
	}
}
