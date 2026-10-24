//go:build webui

package app

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestCollectorWebLifecycle(t *testing.T) {
	dir, _, _, _ := initialized(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &lockedWriter{}
	done := make(chan error, 1)
	a := &App{In: strings.NewReader(""), Out: io.Discard, Err: log}
	go func() {
		done <- a.Run(ctx, []string{"serve", "--config", filepath.Join(dir, "topology.json"), "--auth", filepath.Join(dir, "auth.json"), "--data", filepath.Join(dir, "web-data"), "--listen", "127.0.0.1:0", "--web", "--web-listen", "127.0.0.1:0"})
	}()
	re := regexp.MustCompile(`Native Web UI listening on http://(127\.0\.0\.1:\d+)`)
	var addr string
	testkit.Wait(t, 5*time.Second, func() bool {
		m := re.FindStringSubmatch(log.String())
		if len(m) > 1 {
			addr = m[1]
		}
		return addr != ""
	})
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("collector/Web UI shutdown stalled")
	}
	if resp, err = client.Get("http://" + addr + "/"); err == nil {
		resp.Body.Close()
		t.Fatal("Web listener survived collector exit")
	}
}
func TestWebBuildDisabledDoesNotBind(t *testing.T) {
	enabled := false
	bad := "not-an-address"
	stop, err := New().startWeb(webFlags{&enabled, &bad}, http.NotFoundHandler(), nil, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	stop()
}
