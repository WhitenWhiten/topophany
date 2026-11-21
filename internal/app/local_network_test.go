package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A website, a port or a host is watched from this machine with no secret,
// and may be saved while it is down.
func TestLocalCommandWatchesNetworkTargetsWithoutSecrets(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected request", r.Method, r.Header)
		}
		w.WriteHeader(204)
	}))
	defer site.Close()
	root := filepath.Join(t.TempDir(), "local")
	run := func(cmd string, request map[string]any, want int) map[string]any {
		t.Helper()
		in, _ := json.Marshal(request)
		var out, diagnostic bytes.Buffer
		a := &App{In: bytes.NewReader(in), Out: &out, Err: &diagnostic}
		flag := "--json-stdin"
		if cmd == "list" {
			flag = "--json"
		}
		e := a.Run(context.Background(), []string{"local", cmd, flag, "--local-root", root})
		exit := 0
		var ee *ExitError
		if errors.As(e, &ee) {
			exit = ee.Code
		} else if e != nil {
			t.Fatal(e)
		}
		var result map[string]any
		if json.Unmarshal(out.Bytes(), &result) != nil || exit != want || result["ok"] != (want == 0) {
			t.Fatalf("%s exit=%d want=%d: %s", cmd, exit, want, out.String())
		}
		return result
	}
	code := func(result map[string]any) string { return result["error"].(map[string]any)["code"].(string) }
	website := map[string]any{"id": "", "name": "Site", "adapter": "http", "url": site.URL + "/health", "interval_seconds": 60}

	// The union may be left out: there is no secret to give.
	inspected := run("inspect", map[string]any{"schema": 1, "request_id": "r1", "check": website}, 0)["data"].(map[string]any)
	probe := inspected["probe"].(map[string]any)
	if probe["outcome"] != "ok" || probe["http_status"] != float64(204) || inspected["check"].(map[string]any)["id"] != "" {
		t.Fatal(inspected)
	}
	if _, e := os.Stat(root); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("inspect wrote files", e)
	}
	for _, auth := range []map[string]any{{"mode": "token", "token": localSecret}, {"mode": "saved"}} {
		if result := run("save", map[string]any{"schema": 1, "request_id": "r2", "base_revision": "", "check": website, "auth": auth}, 2); code(result) != "invalid_request" {
			t.Fatal(result)
		}
	}
	if _, e := os.Stat(root); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("refused save wrote files", e)
	}
	saved := run("save", map[string]any{"schema": 1, "request_id": "r3", "base_revision": "", "check": website, "auth": map[string]any{"mode": "none"}}, 0)["data"].(map[string]any)
	revision, _ := saved["profile_revision"].(string)
	if id, _ := saved["check_id"].(string); len(id) != 19 || revision == "" {
		t.Fatal(saved)
	}

	// Nothing listens here any longer: still worth watching.
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	closed := l.Addr().String()
	l.Close()
	port := map[string]any{"id": "lc-port", "name": "Port", "adapter": "tcp", "target": closed}
	down := run("inspect", map[string]any{"schema": 1, "request_id": "r4", "check": port}, 0)["data"].(map[string]any)["probe"].(map[string]any)
	if down["outcome"] != "fail" || down["code"] == nil {
		t.Fatal(down)
	}
	saved = run("save", map[string]any{"schema": 1, "request_id": "r5", "base_revision": revision, "check": port}, 0)["data"].(map[string]any)
	if saved["check_id"] != "lc-port" {
		t.Fatal(saved)
	}
	listed := run("list", nil, 0)["data"].(map[string]any)
	entries := listed["checks"].([]any)
	if len(entries) != 2 {
		t.Fatal(listed)
	}
	for _, entry := range entries {
		if entry.(map[string]any)["auth_mode"] != "none" {
			t.Fatal(entry)
		}
	}
	if kept := entries[0].(map[string]any)["check"].(map[string]any); kept["adapter"] != "http" || kept["url"] != site.URL+"/health" {
		t.Fatal(kept)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "credential-*"))
	if len(matches) != 0 {
		t.Fatal("credential written", matches)
	}

	for _, bad := range []map[string]any{
		{"id": "", "name": "Site", "adapter": "http", "url": "example.com"},
		{"id": "", "name": "Port", "adapter": "tcp", "target": "example.com"},
		{"id": "", "name": "Host", "adapter": "icmp", "target": "example.com"},
	} {
		if result := run("inspect", map[string]any{"schema": 1, "request_id": "r6", "check": bad}, 2); code(result) != "invalid_target" {
			t.Fatal(result)
		}
	}
}
