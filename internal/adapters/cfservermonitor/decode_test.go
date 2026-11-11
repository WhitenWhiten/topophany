package cfservermonitor

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
)

const testObject = "12345678-1234-4234-8234-123456789abc"

func TestCFServerMonitorResourceMapping(t *testing.T) {
	body, err := os.ReadFile("testdata/server_detail.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := resource(body, testObject)
	if err != nil || r == nil {
		t.Fatalf("resource = %+v, %v", r, err)
	}
	wantTime := time.UnixMilli(1793653320123).UTC()
	if r.Object != testObject || !r.ObservedAt.Equal(wantTime) || r.TimeBasis != model.TimeBasisUpstreamSample || r.SampleID == "" {
		t.Fatalf("sample identity = %+v", r.ReportMeta)
	}
	got, err := report.Normalize(*r, model.MaxReportBytes)
	if err != nil {
		t.Fatal(err)
	}
	cpu, mem, disks := got.Resource.CPU, got.Resource.Memory, got.Resource.Disks
	if cpu == nil || cpu.Percent == nil || *cpu.Percent != 12.5 || cpu.LogicalCores == nil || *cpu.LogicalCores != 4 {
		t.Fatalf("CPU = %+v", cpu)
	}
	if mem == nil || mem.TotalBytes == nil || *mem.TotalBytes != 8<<30 ||
		mem.UsedBytes == nil || *mem.UsedBytes != (3<<30)+(1<<19) || mem.AvailableBytes != nil {
		t.Fatalf("memory = %+v", mem)
	}
	if len(disks) != 1 || disks[0].ID != "aggregate" || disks[0].Path != "" || disks[0].Percent == nil || *disks[0].Percent != 20 || disks[0].FreeBytes != nil {
		t.Fatalf("disk = %+v", disks)
	}
	same, err := resource(detail(`"last_updated":1793653320123,"name":"Renamed"`), testObject)
	if err != nil || same != nil {
		t.Fatalf("response without metrics = %+v, %v", same, err)
	}
	// Identity comes from the persisted sample time.
	for _, millis := range []int64{1793653320123, 1793653320124} {
		next, err := resource(detail(fmt.Sprintf(`"last_updated":%d,"cpu":12.5,"name":"Renamed"`, millis)), testObject)
		if err != nil || next == nil || (next.SampleID == r.SampleID) != (millis == wantTime.UnixMilli()) {
			t.Fatalf("identity for %d = %+v, %v", millis, next, err)
		}
	}
}

func TestCFServerMonitorUnavailableAndInvalidMetrics(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		code string
	}{
		{"metadata only", detail(`"timestamp":1793653200000`), ""},
		{"zero timestamp", detail(`"last_updated":0,"cpu":0`), ""},
		{"empty metrics", detail(`"last_updated":1793653320123,"ram_total":0,"ram_used":0,"cpu_cores":0`), ""},
		{"fractional timestamp", detail(`"last_updated":1793653320123.1,"cpu":10`), "cfsm_sample_time"},
		{"negative timestamp", detail(`"last_updated":-1000,"cpu":10`), "cfsm_sample_time"},
		{"oversized timestamp", detail(`"last_updated":253402300800000,"cpu":10`), "cfsm_sample_time"},
		{"invalid metrics", detail(`"last_updated":1793653320123,"cpu":101,"ram_total":17592186044416`), "cfsm_metrics"},
		{"duplicate timestamp", detail(`"last_updated":1793653320123,"last_updated":1793653320124,"cpu":10`), "cfsm_json"},
		{"wrong server", []byte(`{"id":"23456789-2345-4234-8234-123456789abc","last_updated":1793653320123,"cpu":10}`), "cfsm_record_identity"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := resource(c.body, testObject)
			if r != nil || c.code == "" && err != nil || c.code != "" && source.Code(err) != c.code {
				t.Fatalf("resource = %+v, %v, want %q", r, err, c.code)
			}
		})
	}

	r, err := resource(detail(`"last_updated":1793653320123,"cpu":0,"CPU":91,"ram_total":0,"ram_used":0,"disk_total":1,"disk_used":2`), testObject)
	if err != nil || r == nil {
		t.Fatalf("partial resource = %+v, %v", r, err)
	}
	if r.Resource.CPU == nil || r.Resource.CPU.Percent == nil || *r.Resource.CPU.Percent != 0 ||
		r.Resource.Memory != nil || r.Fields["memory.percent"] != model.FieldMissing ||
		len(r.Resource.Disks) != 1 || r.Resource.Disks[0].UsedBytes != nil || r.Fields["disk.aggregate.percent"] != model.FieldInvalid {
		t.Fatalf("partial resource incorrectly filled missing values: %+v", r)
	}
	if _, err := report.Normalize(*r, model.MaxReportBytes); err != nil {
		t.Fatal(err)
	}
}

func detail(fields string) []byte {
	return []byte(`{"id":"` + testObject + `",` + fields + `}`)
}

func FuzzCFServerMonitorResource(f *testing.F) {
	f.Add(detail(`"last_updated":1793653320123,"cpu":0,"disk_total":100,"disk_used":0`))
	f.Add(detail(`"timestamp":1793653200000`))
	f.Fuzz(func(t *testing.T, body []byte) {
		r, err := resource(body, testObject)
		if err == nil && r != nil {
			if _, err := report.Normalize(*r, model.MaxReportBytes); err != nil {
				t.Fatal(err)
			}
		}
	})
}
