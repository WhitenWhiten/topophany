package app

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"time"
)

type DiagnosticFinding struct {
	Code   string `json:"code"`
	Count  int    `json:"count"`
	Action string `json:"action"`
}

func operationalFindings(s model.Snapshot, now time.Time) []DiagnosticFinding {
	out := []DiagnosticFinding{}
	if s.GeneratedAt.IsZero() || now.Sub(s.GeneratedAt) > model.SnapshotLifetime {
		return out
	}
	silent, outdated, quarantine, delivery, pressure := 0, 0, 0, 0, 0
	deadlines := map[string]time.Duration{}
	for _, o := range s.Topology.Observers {
		deadlines[o.ID] = time.Duration(o.StaleSeconds) * time.Second
	}
	for _, o := range s.Observers {
		if o.Health != "up" {
			silent++
			continue
		}
		if o.Contracts == "outdated" {
			outdated++
		}
		for _, d := range o.Delivery {
			deadline := deadlines[o.ID]
			if deadline <= 0 {
				deadline = 90 * time.Second
			}
			if d.ReportedAt.IsZero() || now.Sub(d.ReportedAt) > deadline {
				continue
			}
			if d.QuarantineBytes > 0 {
				quarantine++
			}
			if d.Code != "" {
				delivery++
			}
		}
	}
	if s.Storage != nil {
		for _, o := range s.Storage.Observers {
			if o.Fills {
				pressure++
			}
		}
	}
	add := func(code string, n int, action string) {
		if n > 0 {
			out = append(out, DiagnosticFinding{code, n, action})
		}
	}
	add("observer_silent", silent, "Inspect topo show observers; check the worker process, clock and transport before judging its targets.")
	add("configuration_mismatch", outdated, "Distribute the same reviewed topology, compare topo validate contracts, then reload collector and workers.")
	add("delivery_quarantine", quarantine, "Inspect worker quarantine and rejection codes; archive rejected records before migration and preserve original sample timestamps.")
	add("delivery_failure", delivery, "Inspect topo show observers for delivery stage, HTTP status, retry and independent collector aliases.")
	add("storage_pressure", pressure, "Run topo capacity against the reviewed topology and inspect collector quota/headroom before changing retention.")
	return out
}
