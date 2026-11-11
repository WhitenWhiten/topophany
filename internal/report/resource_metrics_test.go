package report_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

func TestNormalizeOptionalResourceMetrics(t *testing.T) {
	r := model.ExternalReport{
		ReportMeta: meta("vm/one", model.TimeBasisUpstreamSample, time.Now().UTC()),
		Resource: &model.ResourceReport{ResourceMetrics: model.ResourceMetrics{
			Network: model.NetworkReport{RXBytesPerSecond: f(0), TXBytesPerSecond: f(4096)},
			Swap:    model.SwapReport{TotalBytes: u(9007199254740993), UsedBytes: u(0)},
			Load:    model.LoadReport{Avg1m: f(128.5)},
		}},
	}
	got, err := report.Normalize(r, model.MaxReportBytes)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(raw), `"total_bytes":9007199254740993`) || !strings.Contains(string(raw), `"rx_bytes_per_second":0`) || strings.Contains(string(raw), "avg_5m") {
		t.Fatalf("optional metrics wire semantics: %s, %v", raw, err)
	}
	*got.Resource.Swap.TotalBytes = 1
	*got.Resource.Network.RXBytesPerSecond = 1
	*got.Resource.Load.Avg1m = 1
	if *r.Resource.Swap.TotalBytes != 9007199254740993 || *r.Resource.Network.RXBytesPerSecond != 0 || *r.Resource.Load.Avg1m != 128.5 {
		t.Fatal("normalization aliases metrics")
	}
	for _, value := range []float64{-1, math.Copysign(0, -1), math.Inf(1), math.NaN()} {
		for _, load := range []bool{false, true} {
			x := model.CloneExternalReport(r)
			if load {
				x.Resource.Load.Avg1m = f(value)
			} else {
				x.Resource.Network.RXBytesPerSecond = f(value)
			}
			if _, err := report.Normalize(x, model.MaxReportBytes); report.Code(err) != "invalid_resource_number" {
				t.Fatalf("invalid metric accepted: %v, %v", value, err)
			}
		}
	}
	x := model.CloneExternalReport(r)
	x.Resource.Swap = model.SwapReport{TotalBytes: u(0), UsedBytes: u(1)}
	if _, err := report.Normalize(x, model.MaxReportBytes); report.Code(err) != "invalid_capacity" {
		t.Fatal("swap overrun accepted", err)
	}
	x.Resource.Swap.UsedBytes = u(0)
	if _, err := report.Normalize(x, model.MaxReportBytes); err != nil {
		t.Fatal("zero swap rejected", err)
	}
}
