//go:build webui

package webui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebHostOriginAndReadOnlySurface(t *testing.T) {
	called := false
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Origin") != "" {
			t.Fatal("collector CORS leaked")
		}
		w.WriteHeader(204)
	})
	h := Handler(backend, "127.0.0.1:8080", "http")
	for _, tc := range []struct {
		method, path, host, origin string
		want                       int
		called                     bool
	}{
		{"GET", "/", "127.0.0.1:8080", "", 200, false},
		{"GET", "/app.js", "localhost:8080", "", 200, false},
		{"GET", "/", "evil.test:8080", "", 403, false},
		{"GET", "/v1/web/view", "127.0.0.1:8080", "https://evil.test", 403, false},
		{"GET", "/v1/web/view", "127.0.0.1:8080", "http://127.0.0.1:8080", 204, true},
		{"POST", "/v1/observations", "127.0.0.1:8080", "", 404, false},
		{"GET", "/v1/worker", "127.0.0.1:8080", "", 404, false},
		{"PUT", "/v1/config", "127.0.0.1:8080", "", 404, false},
		{"GET", "/assets/", "127.0.0.1:8080", "", 404, false},
	} {
		t.Run(tc.path+tc.host+tc.origin, func(t *testing.T) {
			called = false
			r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || called != tc.called {
				t.Fatal(w.Code, called, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("missing security headers")
			}
		})
	}
}
func TestWebListenerValidationAndLifecycle(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8080", "192.0.2.1:8080", "example.com:8080", ":8080", "127.0.0.1:no"} {
		if stop, _, err := Start(addr, http.NotFoundHandler(), nil, make(chan error, 1)); err == nil {
			stop()
			t.Fatal("unsafe listener accepted", addr)
		}
	}
	stop, addr, err := Start("127.0.0.1:0", http.NotFoundHandler(), nil, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Topophany") {
		t.Fatal(resp.StatusCode)
	}
	stop()
	stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/", nil)
	if resp, err = client.Do(r); err == nil {
		resp.Body.Close()
		t.Fatal("listener survived stop")
	}
}
