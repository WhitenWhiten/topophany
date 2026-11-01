package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestLiteralSecretTopologyMustBePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	c := testkit.Config()
	if config.HasLiteralSecret(c) {
		t.Fatal("fixture carries a literal secret")
	}
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.json")
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(shared, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(shared); err != nil {
		t.Fatal("secret-free topology", err)
	}
	c.Checks[0].HTTP.RequestHeaders = map[string]string{"X-Key": "literal"}
	if !config.HasLiteralSecret(c) {
		t.Fatal("request header was not a literal secret")
	}
	raw, _ = json.Marshal(c)
	loose := filepath.Join(dir, "loose.json")
	if err := os.WriteFile(loose, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(loose); err == nil {
		t.Fatal("group-readable secret topology accepted")
	}
	tight := filepath.Join(dir, "tight.json")
	if err := os.WriteFile(tight, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(tight); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(tight, link); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(link); err == nil {
		t.Fatal("symlink to a secret topology accepted")
	}
}

// Secret classification and requested save modes are platform independent.
func TestLiteralSecretClassificationAndWriteMode(t *testing.T) {
	c := testkit.Config()
	if config.HasLiteralSecret(c) {
		t.Fatal("secret-free fixture")
	}
	c.Checks[0].HTTP.RequestHeaders = map[string]string{"X-Key": "literal"}
	if !config.HasLiteralSecret(c) || config.WriteMode(c, 0644) != 0600 {
		t.Fatal("literal header must request private save mode")
	}
	c.Checks[0].HTTP.RequestHeaders = nil
	c.Checks[0].Target = "https://user:password@192.0.2.10/"
	if !config.HasLiteralSecret(c) {
		t.Fatal("url userinfo was not a literal secret")
	}
	c.Checks[0].Target = "https://192.0.2.10/?token=secret"
	if !config.HasLiteralSecret(c) {
		t.Fatal("url query was not a literal secret")
	}
	if config.WriteMode(c, 0644) != 0600 {
		t.Fatal("secret topology kept a shared mode")
	}
	c.Checks[0].Target = ""
	c.Checks[0].HTTP.RequestHeaders = nil
	if config.WriteMode(c, 0644) != 0644 || config.WriteMode(c, 0) != 0600 {
		t.Fatal("secret-free mode", config.WriteMode(c, 0644))
	}
}

func TestJSONAssertionsRequirePrivateTopology(t *testing.T) {
	c := testkit.Config()
	c.Checks[0].HTTP.JSONEquals = map[string]json.RawMessage{"/token": json.RawMessage(`"private-expected-token"`)}
	if !config.HasLiteralSecret(c) || config.WriteMode(c, 0644) != 0600 {
		t.Fatal("private expected values must request private save mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "topology.json")
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("shared file exposed private JSON assertions")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatal(err)
	}
}
