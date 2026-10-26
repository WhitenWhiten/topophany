package api

import (
	"strings"
	"testing"
	"time"
)

func TestIngressKindsHaveWindowAndNeverLogPeerText(t *testing.T) {
	counter := NewIngressCounter(time.Minute)
	now := time.Now()
	counter.now = func() time.Time { return now }
	var logs []string
	w := counter.Writer(func(line string) { logs = append(logs, line) })
	for _, reason := range []string{"i/o timeout", "EOF", "remote error: tls: bad certificate", "tls: first record does not look like TLS", "private-token-from-peer"} {
		w.Write([]byte("http: TLS handshake error from 127.0.0.1:5555: " + reason + "\n"))
	}
	got := counter.Summary(now)
	if got.WindowSeconds != 60 || len(got.FailureTypes) != 5 || got.FailureTypes["tls_timeout"] != 1 || got.Failures != 5 {
		t.Fatal(got)
	}
	if strings.Contains(strings.Join(logs, "\n"), "private-token-from-peer") {
		t.Fatal("peer text logged")
	}
	now = now.Add(2 * time.Minute)
	if counter.Summary(now) != nil {
		t.Fatal("types did not expire with totals")
	}
}
