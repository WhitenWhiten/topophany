package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStructuredErrorsKeepSecretsAndPathsOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-private-name")
	out, stderr, err := invoke("", "--json-errors", "validate", "--config", path)
	var result struct {
		Schema int
		Error  errorDiagnostic
	}
	if exitCode(err) != 1 || out != "" || json.Unmarshal([]byte(stderr), &result) != nil || result.Error.Code != "file_not_found" || strings.Contains(stderr, path) {
		t.Fatal(out, stderr, err)
	}
	if err := os.WriteFile(path, []byte(`{"schema":"secret-type-canary","project":"test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = invoke("", "--json-errors", "validate", "--config", path)
	if json.Unmarshal([]byte(stderr), &result) != nil || result.Error.Code != "invalid_field_type" || result.Error.Field != "schema" || strings.Contains(stderr, "secret-type-canary") {
		t.Fatal(stderr, err)
	}
}
func TestStructuredErrorsPreserveHealthStatusOutput(t *testing.T) {
	plain, _, err := invoke("", "preview", "--scenario", "1", "status", "--json")
	got, stderr, wrapped := invoke("", "--json-errors", "preview", "--scenario", "1", "status", "--json")
	if exitCode(err) != exitCode(wrapped) || stderr != "" || len(plain) == 0 || len(got) == 0 {
		t.Fatal(wrapped, stderr)
	}
}
