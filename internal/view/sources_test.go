package view

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestD1SourceStatesCountsAndClocks(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	until := now.Add(10 * time.Second)
	sample := now.Add(-time.Minute)
	n := 25.0
	base := model.Snapshot{GeneratedAt: now, Direct: &model.DirectInfo{Sources: []model.DirectSource{{ID: "bs-a", Name: "A", Adapter: "beszel", State: "connected", LastAttemptAt: &now, LastContactAt: &now, ContactValidUntil: &until}}}, Topology: model.Config{Checks: []model.Check{{ID: "resource", External: &model.ExternalBinding{Source: "bs-a", Object: "aaaaaaaaaaaaaaa", Report: "resource"}}}}, External: []model.ExternalBindingView{{Check: "resource", Source: "bs-a", Object: "aaaaaaaaaaaaaaa", ReportType: "resource", Freshness: "fresh", SampledAt: sample, DataDeadline: until, ContactDeadline: until, ValidUntil: until, Report: &model.ExternalReport{Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &n}}}}}}
	for _, tc := range []struct {
		name, state string
		change      func(*model.Snapshot)
	}{
		{"ready", "ready", func(*model.Snapshot) {}}, {"loading", "loading", func(s *model.Snapshot) {
			s.Direct.Sources[0].State = "loading"
			s.Direct.Sources[0].LastAttemptAt = nil
			s.External = nil
		}},
		{"empty samples", "no_data", func(s *model.Snapshot) { s.External = nil }}, {"auth", "unavailable", func(s *model.Snapshot) { s.Direct.Sources[0].State = "auth_required"; s.External = nil }},
		{"expired", "unavailable", func(s *model.Snapshot) { s.External[0].Freshness = "stale" }}, {"expired contact", "unavailable", func(s *model.Snapshot) {
			past := now.Add(-time.Second)
			s.Direct.Sources[0].ContactValidUntil = &past
			s.External[0].Freshness = "stale"
		}}, {"empty profile", "no_data", func(s *model.Snapshot) { s.Direct.Sources = nil; s.Topology.Checks = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			s.Direct = &model.DirectInfo{Sources: append([]model.DirectSource(nil), base.Direct.Sources...)}
			s.External = append([]model.ExternalBindingView(nil), base.External...)
			tc.change(&s)
			status, deadline := (View{Snapshot: s, Now: now}).SourcesStatus()
			if (status.Reason == "samples_stale") != (tc.name == "expired") {
				t.Fatalf("unexpected acquisition reason: %+v", status)
			}
			if tc.name == "expired" {
				verdict := (View{Snapshot: s, Now: now}).directVerdict()
				if verdict.Text != "RESOURCE SAMPLES EXPIRED" || status.Sources[0].State != "connected" {
					t.Fatal(verdict, status)
				}
			}
			if status.State != tc.state {
				t.Fatal(status)
			}
			if !deadline.After(now) || deadline.After(now.Add(30*time.Second)) {
				t.Fatal(deadline)
			}
			if status.Counts.Current+status.Counts.Stale+status.Counts.Missing != status.Counts.Selected || status.Counts.Attention != status.Counts.Stale+status.Counts.Missing {
				t.Fatal(status)
			}
			if tc.state == "ready" && deadline != until {
				t.Fatal("status renewed evidence")
			}
		})
	}
	base.Direct.Sources = append(base.Direct.Sources, model.DirectSource{ID: "bs-b", Name: "B", Adapter: "future", State: "forbidden", LastAttemptAt: &now})
	base.Topology.Checks = append(base.Topology.Checks, model.Check{ID: "other", External: &model.ExternalBinding{Source: "bs-b", Object: "aaaaaaaaaaaaaaa", Report: "resource"}})
	status, _ := (View{Snapshot: base, Now: now}).SourcesStatus()
	if status.State != "partial" || status.Counts.Selected != 2 || status.Counts.Missing != 1 {
		t.Fatal(status)
	}
}
