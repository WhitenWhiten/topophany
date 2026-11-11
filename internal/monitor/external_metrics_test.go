package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func externalPanelInput(now time.Time) Input {
	pct := 25.0
	window := int64(250)
	total, used := uint64(1000), uint64(250)
	check := model.Check{ID: "vm-resources", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "source-a", Object: "vm/dmit", Report: model.ReportTypeResource, Node: "dmit", PrimaryDisk: "root", Assertions: &model.ResourceAssertions{CPUPercentMax: func() *float64 { v := 80.0; return &v }()}}}
	topology := model.Config{Schema: 1, Project: "lab", Nodes: []model.Node{{ID: "dmit", Name: "DMIT"}}, Observers: []model.Observer{{ID: "home", Name: "Home", Node: "home"}}, Checks: []model.Check{check}}
	snap := model.Snapshot{Schema: model.SnapshotSchema, Project: "lab", GeneratedAt: now, Topology: topology, Checks: []model.CheckState{{ID: "vm-resources", Observer: "home", Health: "up", Freshness: "fresh", Evidence: "reported"}}, External: []model.ExternalBindingView{{
		Check: "vm-resources", Source: "source-a", Object: "vm/dmit", ReportType: model.ReportTypeResource, Node: "dmit", Health: "up", Freshness: "fresh", Evidence: "reported", SampledAt: now, TimeBasis: model.TimeBasisUpstreamSample, DataDeadline: now.Add(2 * time.Minute), ContactDeadline: now.Add(time.Minute), ValidUntil: now.Add(30 * time.Second),
		Report: &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/dmit", ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &pct, SampleWindowMS: &window, Basis: "guest_cpu"}, Memory: &model.MemoryReport{TotalBytes: &total, UsedBytes: &used, Percent: &pct, Basis: "guest_memory"}, Disks: []model.DiskReport{{ID: "root", Path: "/", TotalBytes: &total, UsedBytes: &used, Percent: &pct, Basis: "guest_disk"}}}},
	}}}
	return Input{Snapshot: snap}
}

func TestResourceMetricsSelectNodeReportAndHostMetricsStayLocal(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cfg := Config{Template: Template{Schema: 1, Title: "Topophany", Rows: []Row{
		{ID: "rcpu", Metric: "resource.cpu.used_percent", Visual: "bar", Select: Selector{Node: "dmit"}},
		{ID: "rmem", Metric: "resource.memory.used_percent", Visual: "bar", Select: Selector{Node: "dmit"}},
		{ID: "rdisk", Metric: "resource.disk.used_percent", Visual: "bar", Select: Selector{Node: "dmit"}},
		{ID: "hcpu", Metric: "host.cpu.used_percent", Visual: "bar", Select: Selector{Check: "vm-resources"}},
	}}}
	p := Build(cfg, externalPanelInput(now), now)
	p.PublisherExpiresAt = now.Add(2 * time.Minute)
	if p.Rows[0].Value != 25.0 || p.Rows[0].Basis != "guest_cpu" || p.Rows[0].SampleWindowMS != 250 || p.Rows[0].Check != "vm-resources" || p.Rows[0].ValidUntil != now.Add(30*time.Second) {
		t.Fatalf("resource cpu = %+v", p.Rows[0])
	}
	if p.Rows[1].Basis != "guest_memory" || p.Rows[2].Basis != "guest_disk" {
		t.Fatalf("resource bases = %+v %+v", p.Rows[1], p.Rows[2])
	}
	if p.Rows[3].Value != nil || p.Rows[3].Reason != "entity_missing" {
		t.Fatalf("host.* consumed external evidence: %+v", p.Rows[3])
	}
	// The scalar row expires on the resource's own deadline.
	late := Current(p, now.Add(31*time.Second)).Rows[0]
	if late.Value != nil || late.Reason != "sample_expired" {
		t.Fatalf("resource metric survived its deadline: %+v", late)
	}
	// Capability discovery advertises the node-scoped metrics.
	found := map[string]bool{}
	for _, m := range Catalog() {
		if strings.HasPrefix(m.Name, "resource.") {
			found[m.Name] = m.Selector == "node" && m.Unit == "percent"
		}
	}
	if !found["resource.cpu.used_percent"] || !found["resource.memory.used_percent"] || !found["resource.disk.used_percent"] {
		t.Fatalf("catalog = %+v", found)
	}
}

func TestServerCardResourceMetadataIsAdditiveForBar(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cfg := Config{Template: Template{Schema: 1, Title: "Topophany", Rows: []Row{{ID: "card", Metric: "server.card", Visual: "card", Select: Selector{Node: "dmit"}}}}}
	p := Build(cfg, externalPanelInput(now), now)
	p.PublisherExpiresAt = now.Add(30 * time.Second)
	raw, err := json.Marshal(p.Rows[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	res := generic["resources"].([]any)[0].(map[string]any)
	if res["source"] != "source-a" || res["object"] != "vm/dmit" || res["time_basis"] != "upstream_sample" || res["selected"] != true || res["data_deadline"] == nil || res["contact_deadline"] == nil {
		t.Fatalf("resource metadata = %+v", res)
	}
	cpu := res["cpu"].(map[string]any)
	if cpu["percent"] != 25.0 || cpu["basis"] != "guest_cpu" {
		t.Fatalf("cpu = %+v", cpu)
	}
	disk := res["disk"].(map[string]any)
	if disk["path"] != "/" || disk["basis"] != "guest_disk" {
		t.Fatalf("disk = %+v", disk)
	}
	// The existing fields the bar decodes are unchanged.
	for _, key := range []string{"check", "observer", "health", "freshness", "sampled_at", "valid_until", "cpu", "memory", "disk"} {
		if _, ok := res[key]; !ok {
			t.Fatalf("compatibility field %q missing: %s", key, raw)
		}
	}
}

func TestExternalCardMetadataSurvivesCloneAndJSON(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cfg := Config{Template: Template{Schema: 1, Title: "Topophany", Rows: []Row{{ID: "card", Metric: "server.card", Visual: "card", Select: Selector{Node: "dmit"}}}}}
	p := Build(cfg, externalPanelInput(now), now)
	p.PublisherExpiresAt = now.Add(30 * time.Second)
	owned := clone(p)
	card := owned.Rows[0].Value.(ServerCard)
	if len(card.Resources) != 1 || card.Resources[0].Source != "source-a" || !card.Resources[0].Selected || card.Resources[0].CPU.Basis != "guest_cpu" {
		t.Fatalf("clone lost metadata: %+v", card.Resources)
	}
	card.Resources[0].Source = "changed"
	if p.Rows[0].Value.(ServerCard).Resources[0].Source != "source-a" {
		t.Fatal("clone shared resource metadata")
	}
	raw, err := json.Marshal(owned.Rows[0])
	if err != nil {
		t.Fatal(err)
	}
	// The bar decodes ResourceValue independently; a generic roundtrip proves
	// the additive fields do not replace the established resource object.
	var generic struct {
		Value struct {
			Resources []ResourceValue `json:"resources"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	if len(generic.Value.Resources) != 1 || generic.Value.Resources[0].ValidUntil == nil || generic.Value.Resources[0].CPU.Percent == nil {
		t.Fatalf("JSON resource = %+v", generic.Value.Resources)
	}
}

func TestOptionalResourceMetricsInListCardAndScalarRows(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cfg := Config{Source: "direct", Template: Template{Schema: 1, Title: "Topophany", Rows: []Row{
		{ID: "servers", Metric: "servers.list", Visual: "list"},
		{ID: "card", Metric: "server.card", Visual: "card", Select: Selector{Node: "dmit"}},
		{ID: "rx", Metric: "resource.network.rx_bytes_per_second", Visual: "text", Select: Selector{Node: "dmit"}},
		{ID: "swap", Metric: "resource.swap.used_bytes", Visual: "text", Select: Selector{Node: "dmit"}},
		{ID: "load", Metric: "resource.load.avg_1m", Visual: "text", Select: Selector{Node: "dmit"}},
	}}}
	cfg.Template.Bar.Row = "load"
	if err := cfg.Template.Validate("direct"); err != nil {
		t.Fatal(err)
	}
	input := externalPanelInput(now)
	input.Snapshot.Topology.Nodes[0].Kind = "host"
	old := Build(cfg, input, now)
	oldCard := old.Rows[1].Value.(ServerCard)
	if oldCard.Resources[0].CPU.Percent == nil || oldCard.Resources[0].ResourceMetrics != (model.ResourceMetrics{}) ||
		old.Rows[0].Value.([]ServerListEntry)[0].Metrics != nil || old.Rows[2].Reason != "field_missing" {
		t.Fatal("legacy sample produced new readings or lost its CPU sample", old)
	}
	binding := &input.Snapshot.External[0]
	binding.ValidUntil = now.Add(12 * time.Second)
	binding.Report.Resource.ResourceMetrics = model.ResourceMetrics{
		Network: model.NetworkReport{RXBytesPerSecond: new(4096.0), TXBytesPerSecond: new(0.0)},
		Swap:    model.SwapReport{TotalBytes: new(uint64(8192)), UsedBytes: new(uint64(0))},
		Load:    model.LoadReport{Avg1m: new(110.5)},
	}
	p := Build(cfg, input, now)
	p.PublisherExpiresAt = now.Add(15 * time.Second)
	entry := p.Rows[0].Value.([]ServerListEntry)[0]
	if entry.Metrics == nil || entry.Metrics.Network.RXBytesPerSecond == nil || *entry.Metrics.Network.RXBytesPerSecond != 4096 ||
		!entry.Metrics.ValidUntil.Equal(binding.ValidUntil) || p.Rows[2].Value != 4096.0 || p.Rows[3].Value != uint64(0) || p.Rows[4].Value != 110.5 {
		t.Fatal("incorrect new monitor readings", p)
	}
	owned := p.Clone()
	*owned.Rows[0].Value.([]ServerListEntry)[0].Metrics.Network.RXBytesPerSecond = 1
	*owned.Rows[1].Value.(ServerCard).Resources[0].Swap.UsedBytes = 1
	if *entry.Metrics.Network.RXBytesPerSecond != 4096 || *p.Rows[1].Value.(ServerCard).Resources[0].Swap.UsedBytes != 0 {
		t.Fatal("clone aliased resource values")
	}
	// Keep the list and card alive after their nested metrics expire.
	p.Rows[0].ValidUntil, p.Rows[1].ValidUntil = p.PublisherExpiresAt, p.PublisherExpiresAt
	raw, err := MarshalPanel(p)
	if err != nil {
		t.Fatal(err)
	}
	var read Panel
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	fresh := Current(read, now.Add(time.Second))
	if fresh.Rows[0].Value.([]ServerListEntry)[0].Metrics == nil {
		t.Fatal("a current resource was withheld before its deadline", fresh)
	}
	late := Current(read, now.Add(13*time.Second))
	if late.Rows[0].Value.([]ServerListEntry)[0].Metrics != nil ||
		late.Rows[1].Value.(ServerCard).Resources[0].ResourceMetrics != (model.ResourceMetrics{}) ||
		late.Rows[2].Value != nil {
		t.Fatal("expired data survived monitor read", late)
	}
}
