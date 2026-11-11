package cfservermonitor

import (
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
)

func TestOptionalMetricsAvailability(t *testing.T) {
	for _, tc := range []struct{ name, raw, key, state string }{
		{"missing", ``, "network.rx_bytes_per_second", model.FieldMissing},
		{"null", `,"net_in_speed":null`, "network.rx_bytes_per_second", model.FieldMissing},
		{"empty string", `,"net_in_speed":""`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"null string", `,"swap_total":"null"`, "swap.total_bytes", model.FieldInvalid},
		{"negative", `,"net_in_speed":-1`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"negative zero swap", `,"swap_total":-0`, "swap.total_bytes", model.FieldInvalid},
		{"negative underflow swap", `,"swap_used":-1e-999`, "swap.used_bytes", model.FieldInvalid},
		{"swap overrun before rounding", `,"swap_total":0.0000001,"swap_used":0.0000002`, "swap.used_bytes", model.FieldInvalid},
		{"overflow swap", `,"swap_total":1e308`, "swap.total_bytes", model.FieldInvalid},
		{"bad load", `,"load_avg":"0 -1 2"`, "load.avg_5m", model.FieldInvalid},
		{"wrong load shape", `,"load_avg":[1,2,3]`, "load.avg_1m", model.FieldInvalid},
		{"null load", `,"load_avg":null`, "load.avg_1m", model.FieldMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := resource(detail(`"last_updated":1793653320123,"cpu":0`+tc.raw), testObject)
			if err != nil || r == nil {
				t.Fatalf("resource: %v", err)
			}
			if r.Fields[tc.key] != tc.state {
				t.Fatalf("%s = %q, want %q", tc.key, r.Fields[tc.key], tc.state)
			}
			if _, err := report.Normalize(*r, model.MaxReportBytes); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, raw := range []string{`"net_in_speed":-1`, `"swap_total":-0`, `"load_avg":"bad"`} {
		r, err := resource(detail(`"last_updated":1793653320123,`+raw), testObject)
		if r != nil || source.Code(err) != "cfsm_metrics" {
			t.Fatalf("invalid-only sample accepted: %s, %v", raw, err)
		}
	}
	r, err := resource(detail(`"last_updated":1793653320123,"net_in_speed": "12.5","net_out_speed":"0","swap_total":"0","swap_used":"0","load_avg":"0 0 0"`), testObject)
	if err != nil || r == nil {
		t.Fatalf("string metrics: %v", err)
	}
	if r.Resource.Network.RXBytesPerSecond == nil || *r.Resource.Network.RXBytesPerSecond != 12.5 || r.Resource.Swap.TotalBytes == nil || *r.Resource.Swap.TotalBytes != 0 {
		t.Fatal("lost string rates or zero swap", r.Resource)
	}
	if _, err := report.Normalize(*r, model.MaxReportBytes); err != nil {
		t.Fatal(err)
	}
}
