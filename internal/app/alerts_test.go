package app

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/alert"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlertQuietAndResumePreservePrivateConfiguration(t *testing.T) {
	dir, c, _, _ := initialized(t)
	path := filepath.Join(dir, "alerts.json")
	cfg := alert.Config{Schema: 1, Webhooks: []alert.Webhook{{ID: "ops", Format: "json", URL: "https://example.invalid/?key=secret", Bearer: "secret-bearer"}}}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"alerts", "quiet", "--config", filepath.Join(dir, "topology.json"), "--alerts", path, "--webhook", "ops", "--for", "30m"}
	out, _, err := invoke("", append(args, "--dry-run")...)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != string(data) {
		t.Fatal("dry run wrote config")
	}
	out, _, err = invoke("", args...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "secret") {
		t.Fatal("quiet output exposed credential")
	}
	changed, err := alert.Load(path, c)
	if err != nil || changed.Webhooks[0].QuietUntil.IsZero() || changed.Webhooks[0].Bearer != "secret-bearer" {
		t.Fatal(changed, err)
	}
	if _, _, err = invoke("", "alerts", "resume", "--config", filepath.Join(dir, "topology.json"), "--alerts", path, "--webhook", "ops"); err != nil {
		t.Fatal(err)
	}
	changed, err = alert.Load(path, c)
	if err != nil || !changed.Webhooks[0].QuietUntil.IsZero() {
		t.Fatal(err)
	}
}
