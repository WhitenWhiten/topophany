package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func amplifiedPanel(t testing.TB) Panel {
	t.Helper()
	now := time.Now().UTC()
	c := testConfig()
	c.Template.Rows = nil
	// Valid topology with nearly 1 MiB of service names, repeated in every row.
	topology := model.Config{Schema: 1, Project: "budget", Applications: []model.Application{{ID: "app", Name: "Application"}}, Nodes: []model.Node{{ID: "node"}}}
	for i := 0; i < 256; i++ {
		topology.Services = append(topology.Services, model.Service{ID: fmt.Sprintf("svc%d", i), Node: "node", Application: "app", Name: strings.Repeat("large-name", 350)})
	}
	config.Defaults(&topology)
	if err := config.Validate(topology); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 127; i++ {
		c.Template.Rows = append(c.Template.Rows, Row{ID: fmt.Sprintf("apps%d", i), Metric: "applications.list", Visual: "list"})
	}
	c.Template.Rows = append(c.Template.Rows, Row{ID: "status", Metric: "summary.status", Visual: "text"})
	c.Template.Bar.Row = "status"
	if err := c.Template.Validate("collector"); err != nil {
		t.Fatal(err)
	}
	snapshot := engine.Build(topology, "audit", "", nil, nil, now)
	return Build(c, Input{Snapshot: snapshot}, now)
}

func TestPersistentPanelBudgetBeforeRetention(t *testing.T) {
	p := amplifiedPanel(t)
	for _, row := range p.Rows[:127] {
		if row.Value != nil || row.Reason != "frame_budget_exceeded" {
			t.Fatal("oversized rich projection retained", row.ID)
		}
	}
	if p.Rows[127].Value == nil {
		t.Fatal("scalar lost with oversized rich rows")
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw)+1 > MaxPanelBytes {
		t.Fatal("Build retained an oversized panel", len(raw), err)
	}
	output, err := OpenOutput(filepath.Join(t.TempDir(), "metrics.json"), p.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err = output.Write(p, time.Second, true); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(output.path)
	if err != nil || len(raw) > MaxPanelBytes {
		t.Fatal(len(raw), err)
	}
	if err = json.Unmarshal(raw, &Panel{}); err != nil {
		t.Fatal(err)
	}
}

func TestPanelAggregateBudgetAndCallerOwnership(t *testing.T) {
	p := Panel{Name: "budget", Rows: []Value{}}
	for i := 0; i < 128; i++ {
		p.Rows = append(p.Rows, Value{Row: Row{ID: fmt.Sprintf("r%d", i)}, Value: strings.Repeat("x", 32<<10), Text: "large", Health: "up", Freshness: "fresh"})
	}
	raw, err := MarshalPanel(p)
	if err != nil || len(raw) > MaxPanelBytes {
		t.Fatal(len(raw), err)
	}
	var got Panel
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	withheld := 0
	for i, r := range got.Rows {
		if r.Value == nil {
			withheld++
			if r.Reason != "frame_budget_exceeded" {
				t.Fatal(r)
			}
		}
		if p.Rows[i].Value == nil {
			t.Fatal("changed caller")
		}
	}
	if withheld == 0 {
		t.Fatal("aggregate budget not enforced")
	}
	// Even externally supplied panels are bounded before Output clones or hashes.
	output, err := OpenOutput(filepath.Join(t.TempDir(), "metrics.json"), p.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err = output.Write(p, time.Second, true); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(output.path)
	if err != nil || len(raw) > MaxPanelBytes {
		t.Fatal(len(raw), err)
	}
}

func TestServerListWithResourceMetricsFitsFrame(t *testing.T) {
	now := time.Now().UTC()
	servers := make([]ServerListEntry, 256)
	for i := range servers {
		servers[i] = ServerListEntry{
			ID: fmt.Sprintf("host-%03d", i), Name: "Example Server", Kind: "host", Health: "up",
			Metrics: &ServerResourceMetrics{
				SampledAt: now, ValidUntil: now.Add(15 * time.Second),
				ResourceMetrics: model.ResourceMetrics{
					Network: model.NetworkReport{RXBytesPerSecond: new(1024.0), TXBytesPerSecond: new(256.0)},
					Swap:    model.SwapReport{TotalBytes: new(uint64(1 << 30)), UsedBytes: new(uint64(1 << 26))},
					Load:    model.LoadReport{Avg1m: new(1.0), Avg5m: new(2.0), Avg15m: new(3.0)},
				},
			},
		}
	}
	p := Panel{Rows: []Value{{Row: Row{Metric: "servers.list"}, Value: servers}}}
	raw, err := MarshalPanel(p)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Rows []struct{ Value []ServerListEntry }
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if rows := wire.Rows[0].Value; len(rows) != len(servers) || rows[len(rows)-1].Metrics == nil {
		t.Fatal("server list was withheld or truncated")
	}
}
