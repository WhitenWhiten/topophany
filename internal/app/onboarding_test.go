package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestTaskSetupWebsiteAndIncompleteInput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "website")
	if _, _, err := invoke("website\nhttps://example.invalid/health\n"+dir+"\n", "setup"); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(filepath.Join(dir, "topology.json")); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if _, _, err := invoke("\n"+empty+"\n", "setup", "--task", "website"); err == nil {
		t.Fatal("blank website became demo")
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatal("wrote incomplete setup")
	}
	if _, _, err := invoke("", "setup"); err == nil {
		t.Fatal("EOF accepted")
	}
	if _, _, err := invoke("", "setup", "--task", "invalid"); err == nil {
		t.Fatal("invalid task accepted")
	}
}

func TestTaskSetupDirectDiscoversAndSavesWithoutPolling(t *testing.T) {
	const id = "d4c8d9a1-4ec5-4c1b-9b95-4c1c8f930b0d"
	const secret = "synthetic-setup-api-key"
	calls := 0
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/admin/client/list" || r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("unexpected discovery request", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"uuid":%q,"name":"Test server"}]`, id)
	}))
	defer h.Close()
	root := filepath.Join(t.TempDir(), "direct")
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{"desktop", "km", "Home Komari", "komari", h.URL, "token", id, ""}, "\n")
	out, diag, err := invoke(input, "setup", "--task", "direct", "--direct-root", root, "--credential-file", token)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || strings.Contains(out+diag, secret) {
		t.Fatal("unexpected requests or secret disclosure")
	}
	if _, _, err := invoke(input, "setup", "--task", "direct", "--direct-root", root, "--credential-file", token); err == nil {
		t.Fatal("replaced existing source")
	}
}
