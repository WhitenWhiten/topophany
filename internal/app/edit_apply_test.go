package app

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"os"
	"path/filepath"
	"testing"
)

func TestBatchEditRequiresBothReviewedRevisions(t *testing.T) {
	dir := t.TempDir()
	path, candidate := filepath.Join(dir, "topology.json"), filepath.Join(dir, "next.json")
	c := testkit.Config()
	saveConfig(t, path, c)
	next := config.Clone(c)
	next.Nodes[0].Name = "Renamed"
	saveConfig(t, candidate, next)
	before, _ := os.ReadFile(path)
	out, _, err := invoke("", "edit", "--config", path, "--apply", candidate, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var plan EditPlan
	if json.Unmarshal([]byte(out), &plan) != nil || !plan.Changed || len(plan.ChangedChecks) != 0 {
		t.Fatal(out)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("preview wrote target")
	}
	args := []string{"edit", "--config", path, "--apply", candidate, "--expect-revision", plan.Before}
	if _, _, err = invoke("", args...); err == nil {
		t.Fatal("unreviewed candidate accepted")
	}
	args = append(args, "--expect-candidate-revision", plan.After)
	if _, _, err = invoke("", args...); err != nil {
		t.Fatal(err)
	}
	if _, _, err = invoke("", args...); err == nil {
		t.Fatal("stale target revision accepted")
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.Nodes[0].Name != "Renamed" {
		t.Fatal("batch not applied")
	}
}
