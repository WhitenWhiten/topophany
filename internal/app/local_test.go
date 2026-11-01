package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const localSecret = "synthetic-engine-secret-canary"

// syntheticEngine is a Mihomo controller fake on an ephemeral loopback port.
func syntheticEngine(t *testing.T, secret string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("engine written to", r.Method, r.URL.Path)
		}
		if secret != "" && r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/configs":
			_, _ = io.WriteString(w, `{"mode":"rule","tun":{"enable":false}}`)
		case "/version":
			_, _ = io.WriteString(w, `{"meta":true,"version":"v1.19.99"}`)
		case "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{"GLOBAL":{"type":"Selector","now":"Proxy","all":["DIRECT","test-net-a","Proxy"]},"Proxy":{"type":"Selector","now":"test-net-a","all":["test-net-a","DIRECT"]},"test-net-a":{"type":"Shadowsocks"},"DIRECT":{"type":"Direct"}}}`)
		case "/providers/proxies":
			_, _ = io.WriteString(w, `{"providers":{}}`)
		default:
			t.Error("unexpected engine read", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestLocalCommandEnvelopesSecretsAndExitCodes(t *testing.T) {
	engine := syntheticEngine(t, localSecret)
	root := filepath.Join(t.TempDir(), "local")
	check := map[string]any{"id": "lc-engine", "name": "Proxy engine", "adapter": "mihomo", "url": engine.URL + "/", "interval_seconds": 30, "timeout_seconds": 5, "stale_seconds": 90, "root_proxy": "Proxy", "expected_mode": "rule", "expected_tun": false}
	request := map[string]any{"schema": 1, "request_id": "r1", "check": check, "auth": map[string]any{"mode": "token", "token": localSecret}, "base_revision": ""}
	run := func(cmd string, want int) map[string]any {
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
		if exit != want {
			t.Fatalf("%s exit=%d want=%d: %s", cmd, exit, want, out.String())
		}
		if strings.Contains(out.String()+diagnostic.String(), localSecret) {
			t.Fatal("secret in output")
		}
		var result map[string]any
		if json.Unmarshal(out.Bytes(), &result) != nil || !bytes.HasSuffix(out.Bytes(), []byte("\n")) || bytes.Count(out.Bytes(), []byte("\n")) != 1 {
			t.Fatal(out.String())
		}
		if result["schema"] != float64(1) || result["ok"] != (want == 0) {
			t.Fatal(result)
		}
		return result
	}
	code := func(result map[string]any) string { return result["error"].(map[string]any)["code"].(string) }

	inspected := run("inspect", 0)["data"].(map[string]any)
	if inspected["check"].(map[string]any)["url"] != engine.URL || inspected["engine"].(map[string]any)["version"] != "v1.19.99" || len(inspected["groups"].([]any)) != 2 || inspected["truncated"] != false {
		t.Fatal(inspected)
	}
	if _, e := os.Stat(root); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("inspect wrote files", e)
	}
	request["auth"] = map[string]any{"mode": "token", "token": "wrong"}
	if result := run("save", 1); code(result) != "authentication_failed" {
		t.Fatal(result)
	}
	if _, e := os.Stat(root); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("refused save wrote files", e)
	}
	request["auth"] = map[string]any{"mode": "token", "token": localSecret}
	saved := run("save", 0)["data"].(map[string]any)
	revision := saved["profile_revision"].(string)
	if saved["check_id"] != "lc-engine" || revision == "" {
		t.Fatal(saved)
	}
	listed := run("list", 0)["data"].(map[string]any)
	entries := listed["checks"].([]any)
	if listed["profile_revision"] != revision || len(entries) != 1 || entries[0].(map[string]any)["auth_mode"] != "token" || entries[0].(map[string]any)["check"].(map[string]any)["expected_tun"] != false {
		t.Fatal(listed)
	}
	if result := run("save", 1); code(result) != "profile_conflict" {
		t.Fatal(result)
	}
	request["base_revision"] = revision
	request["auth"] = map[string]any{"mode": "saved"}
	if run("inspect", 0)["ok"] != true {
		t.Fatal("saved secret not reused")
	}
	check["url"] = strings.Replace(engine.URL, "127.0.0.1", "localhost", 1)
	if result := run("inspect", 1); code(result) != "saved_auth_mismatch" {
		t.Fatal(result)
	}
	check["url"] = "http://192.0.2.1:9090"
	if result := run("inspect", 2); code(result) != "not_local_address" {
		t.Fatal(result)
	}
	check["url"] = engine.URL
	check["expected_tun"] = nil
	resaved := run("save", 0)["data"].(map[string]any)
	if resaved["profile_revision"] == revision {
		t.Fatal("revision unchanged")
	}
	request["base_revision"] = resaved["profile_revision"]
	request["check_id"] = "lc-absent"
	if result := run("remove", 1); code(result) != "not_found" {
		t.Fatal(result)
	}
	request["check_id"] = "lc-engine"
	removed := run("remove", 0)["data"].(map[string]any)
	if removed["check_id"] != "lc-engine" {
		t.Fatal(removed)
	}
	if left, _ := filepath.Glob(filepath.Join(root, "credential-*")); len(left) != 0 {
		t.Fatal("remove kept secrets", left)
	}
	if listed := run("list", 0)["data"].(map[string]any); len(listed["checks"].([]any)) != 0 {
		t.Fatal(listed)
	}
	request["schema"] = "wrong"
	if result := run("save", 2); code(result) != "invalid_request" {
		t.Fatal(result)
	}
	var out bytes.Buffer
	e := (&App{In: strings.NewReader(""), Out: &out, Err: io.Discard}).Run(context.Background(), []string{"local", "list", "--json-stdin"})
	var ee *ExitError
	if !errors.As(e, &ee) || ee.Code != 2 || !strings.Contains(out.String(), `"invalid_request"`) {
		t.Fatal(e, out.String())
	}
}
