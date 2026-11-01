package app

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"testing"
	"time"
)

func TestRolloutDoesNotMistakeOldCollectorAgreementForDeployment(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	s := model.Snapshot{GeneratedAt: now, Revision: config.PublicRevision(c), Topology: config.Public(c), Observers: []model.ObserverState{{ID: "home", Health: "up", Contracts: "current"}}}
	same := assessRollout(c, s, now)
	if !same.Matches || same.Workers[0].State != "matches_collector_contracts" {
		t.Fatal(same)
	}
	c.Nodes[0].Name = "Desired rename"
	old := assessRollout(c, s, now)
	if old.Matches || old.Workers[0].State != "matches_old_collector_contracts" {
		t.Fatal(old)
	}
	if old.Changes == nil || !old.Changes.Changed || len(old.Changes.ChangedChecks) != 0 {
		t.Fatal("rename should not reset measurement", old)
	}
	expired := assessRollout(c, s, now.Add(time.Hour))
	if expired.Code != "snapshot_expired" || expired.Matches || len(expired.Workers) != 0 {
		t.Fatal(expired)
	}
}
