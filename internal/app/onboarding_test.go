package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
)

func TestWebsiteSetupCreatesUsablePrivateEnrollment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "website")
	if _, _, err := invoke("", "init", "--url", "https://example.invalid/health", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(filepath.Join(dir, "topology.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Checks) != 1 || c.Checks[0].AvailabilityScope != "remote" {
		t.Fatal(c.Checks)
	}
	if _, err := api.LoadAuth(filepath.Join(dir, "auth.json"), c); err != nil {
		t.Fatal(err)
	}
	creds, err := client.Load(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if creds.Profiles[creds.Active].Project != c.Project {
		t.Fatal("project mismatch")
	}
	if _, _, err := invoke("", "init", "--url", "https://example.invalid", "--dir", dir); err == nil {
		t.Fatal("overwrote existing enrollment")
	}
}
func TestWebsiteSetupRejectsSecretsBeforeWriting(t *testing.T) {
	for _, raw := range []string{"ftp://example.invalid", "https://user:secret@example.invalid", "https://example.invalid/?token=secret", "https://example.invalid/#secret"} {
		dir := filepath.Join(t.TempDir(), "website")
		if _, _, err := invoke("", "init", "--url", raw, "--dir", dir); err == nil {
			t.Fatal("accepted unsafe URL")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("created directory on invalid input")
		}
	}
}
