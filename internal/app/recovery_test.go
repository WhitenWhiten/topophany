package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/outbox"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestRecoveryVerifiesCloneAndRefusesActiveWriter(t *testing.T) {
	dir := t.TempDir()
	c := testkit.Config()
	path := filepath.Join(t.TempDir(), "topology.json")
	saveConfig(t, path, c)
	st, err := store.Open(dir, store.Options{Revision: config.Revision(c), Observers: observerIDs(c)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke("", "recovery", "--data", dir, "--config", path); err == nil {
		t.Fatal("opened an active writer")
	}
	if err = st.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", 1, time.Now(), "ok")}}); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "journal.jsonl"))
	out, _, err := invoke("", "recovery", "--data", dir, "--config", path)
	if err != nil {
		t.Fatal(err)
	}
	var report recoveryReport
	if json.Unmarshal([]byte(out), &report) != nil || !report.Recoverable || report.Files == 0 || report.Digest == "" {
		t.Fatal(out)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "journal.jsonl"))
	if string(before) != string(after) {
		t.Fatal("verification rewrote original data")
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(path, filepath.Join(dir, "unexpected-link")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := invoke("", "recovery", "--data", dir, "--config", path); err == nil {
			t.Fatal("followed backup symlink")
		}
	}
}
func TestRecoveryWorkerKeepsPendingEvidence(t *testing.T) {
	dir := t.TempDir()
	c := testkit.Config()
	path := filepath.Join(t.TempDir(), "topology.json")
	saveConfig(t, path, c)
	q, err := outbox.Open(dir, c.Project, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Add(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", 1, time.Now(), "ok")}}, []string{outbox.Recipient("https://collector.invalid")}); err != nil {
		t.Fatal(err)
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	out, _, err := invoke("", "recovery", "--role", "worker", "--data", dir, "--config", path)
	if err != nil {
		t.Fatal(err)
	}
	var report recoveryReport
	if json.Unmarshal([]byte(out), &report) != nil || !report.Recoverable || report.Pending != 1 {
		t.Fatal(out)
	}
	q, err = outbox.Open(dir, c.Project, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	n, _ := q.Stats()
	if n != 1 {
		t.Fatal("source queue changed")
	}
}
