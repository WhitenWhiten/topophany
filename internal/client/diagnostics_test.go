package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadDiagnosticsRetainFailureWithoutSecrets(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":"observer_heartbeat_rate","private":"secret-response"}`)
			return
		}
		json.NewEncoder(w).Encode(snapshot())
	}))
	defer server.Close()
	c := newClient(t, server.URL)
	if _, _, err := c.Snapshot(context.Background(), ""); err == nil {
		t.Fatal("missing 429")
	}
	if _, _, err := c.Snapshot(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	events := c.Diagnostics()
	if len(events) != 2 || events[0].Code != "rate_limited" || events[0].RetryAfterSeconds != 7 || events[0].HTTPStatus != 429 || events[1].Stage != "complete" || !events[1].ConnectionReused {
		t.Fatal(events)
	}
	raw, _ := json.Marshal(events)
	for _, secret := range []string{token, "secret-response", "Authorization", "private"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("diagnostics leaked", secret)
		}
	}
	c.Now = func() time.Time { return time.Now().Add(31 * time.Minute) }
	if len(c.Diagnostics()) != 0 {
		t.Fatal("history did not expire")
	}
}
