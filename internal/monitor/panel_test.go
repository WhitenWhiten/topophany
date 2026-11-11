package monitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func testConfig() Config {
	c := Config{Schema: 1, Name: "desktop", Enabled: true, Source: "collector", Interval: "5s", Output: filepath.Join(os.TempDir(), "metrics.json"), Template: Template{Schema: 1, Title: "Topophany", Rows: []Row{{ID: "cpu", Metric: "host.cpu.used_percent", Visual: "bar", Format: "percent", Select: Selector{Check: "host"}}}}}
	c.Template.Bar.Row = "cpu"
	return c
}
func hostInput(now time.Time) Input {
	return Input{Snapshot: model.Snapshot{GeneratedAt: now, Project: "test", Topology: model.Config{Checks: []model.Check{{ID: "host", Kind: "host", Observer: "local", StaleSeconds: 10}}, Observers: []model.Observer{{ID: "local"}}}, Checks: []model.CheckState{{ID: "host", Observer: "local", Health: "up", Freshness: "fresh", Last: &model.Observation{SampledAt: now, Outcome: "ok", Evidence: "reported", Facts: map[string]string{"cpu_used_percent": "42"}}}}}}
}
func TestEvidenceExpiresWithoutNewSample(t *testing.T) {
	now := time.Now()
	c := testConfig()
	in := hostInput(now)
	p := Build(c, in, now)
	if p.Rows[0].Value != float64(42) {
		t.Fatal(p)
	}
	p = Build(c, in, now.Add(11*time.Second))
	if p.Rows[0].Value != nil || p.Rows[0].Freshness != "stale" {
		t.Fatal(p)
	}
	if !p.Rows[0].SampledAt.Equal(now) {
		t.Fatal("rewrote evidence timestamp")
	}
}

func TestNativeHostOptionalScalars(t *testing.T) {
	now := time.Now()
	c := testConfig()
	c.Template.Rows = []Row{
		{ID: "rx", Metric: "host.network.rx_bytes_per_second", Visual: "text", Select: Selector{Check: "host"}},
		{ID: "swap", Metric: "host.swap.used_bytes", Visual: "text", Select: Selector{Check: "host"}},
		{ID: "load", Metric: "host.load.avg_5m", Visual: "text", Select: Selector{Check: "host"}},
	}
	c.Template.Bar.Row = "rx"
	if err := c.Template.Validate("worker"); err != nil {
		t.Fatal(err)
	}
	in := hostInput(now)
	facts := in.Snapshot.Checks[0].Last.Facts
	facts["network_rx_bytes_per_second"] = "4096.5"
	facts["swap_used_bytes"] = "9007199254740993"
	facts["load_avg_5m"] = "0"
	rows := Build(c, in, now).Rows
	if rows[0].Value != 4096.5 || rows[0].Unit != "bytes_per_second" ||
		rows[1].Value != uint64(9007199254740993) || rows[2].Value != 0.0 {
		t.Fatal("host scalar output lost a metric", rows)
	}
}
func TestAmbiguityInvalidValuesAndMissingSource(t *testing.T) {
	now := time.Now()
	c := testConfig()
	c.Template.Rows[0].Select.Check = ""
	in := hostInput(now)
	in.Snapshot.Checks = append(in.Snapshot.Checks, in.Snapshot.Checks[0])
	if v := Build(c, in, now).Rows[0]; v.Reason != "selector_ambiguous" {
		t.Fatal(v)
	}
	in = hostInput(now)
	in.Snapshot.Checks[0].Last.Facts["cpu_used_percent"] = "NaN"
	if v := Build(c, in, now).Rows[0]; v.Value != nil {
		t.Fatal(v)
	}
	in.Unavailable = true
	if v := Build(c, in, now).Rows[0]; v.Reason != "source_unavailable" {
		t.Fatal(v)
	}
}
func TestTemplateCapabilitiesAndAtomicConfig(t *testing.T) {
	c := testConfig()
	c.Output = filepath.Join(t.TempDir(), "metrics.json")
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	saved, e := Save(path, c)
	if e != nil {
		t.Fatal(e)
	}
	c.Template.Bar.Row = "missing"
	if _, e = Save(path, c); e == nil {
		t.Fatal("accepted invalid reference")
	}
	loaded, e := Load(path)
	if e != nil || loaded.Revision != saved.Revision || loaded.Template.Bar.Row != "cpu" {
		t.Fatal(loaded, e)
	}
	c = testConfig()
	c.Template.Rows[0].Metric = "summary.status"
	c.Template.Rows[0].Visual = "text"
	c.Template.Rows[0].Select = Selector{}
	c.Template.Rows[0].Format = ""
	if e = c.Template.Validate("worker"); e == nil {
		t.Fatal("worker accepted global metric")
	}
}

func TestSummaryExpiresAtItsEvidenceBoundary(t *testing.T) {
	now := time.Now()
	c := testConfig()
	c.Template.Rows = []Row{{ID: "status", Metric: "summary.status", Visual: "text"}}
	c.Template.Bar.Row = "status"
	in := hostInput(now)
	before := Build(c, in, now).Rows[0]
	if before.Value != "clear" || !before.ValidUntil.Equal(now.Add(10*time.Second)) {
		t.Fatal(before)
	}
	after := Build(c, in, now.Add(11*time.Second)).Rows[0]
	if after.Value == "clear" {
		t.Fatal("summary retained expired healthy evidence", after)
	}
	if in.Snapshot.Checks[0].Freshness != "fresh" {
		t.Fatal("projection mutated input")
	}
}
