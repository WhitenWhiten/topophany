package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSnapshotBusyRetryRecovers(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "999999") // cannot extend the fixed wait
			w.WriteHeader(429)
			json.NewEncoder(w).Encode(map[string]string{"error": "snapshot_busy"})
			return
		}
		json.NewEncoder(w).Encode(snapshot())
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, server, err := newClient(t, srv.URL).Snapshot(ctx, "")
	if err != nil || snap.Project != "lab" || server != srv.URL || hits.Load() != 2 {
		t.Fatal("busy snapshot did not recover", server, err, hits.Load())
	}
}

func TestSnapshotRotationRetryRecovers(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Connection", "close")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]string{"error": "connection_rotating"})
			return
		}
		json.NewEncoder(w).Encode(snapshot())
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, _, err := newClient(t, srv.URL).Snapshot(ctx, "")
	if err != nil || snap.Project != "lab" || hits.Load() != 2 {
		t.Fatal("connection rotation escaped the read's retry budget", err, hits.Load())
	}
}

func TestSnapshotBusyRetryBoundedAndSelective(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		status     int
		wantHits   int32
	}{
		{"snapshot_busy", "snapshot_busy", 429, 2},
		{"principal_rate", "rate_limited", 429, 1},
		{"server_error", "snapshot_busy", 503, 1},
		{"connection_rotation", "connection_rotating", 503, 2},
		{"auth", "snapshot_busy", 401, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(map[string]string{"error": tc.code})
			}))
			defer srv.Close()
			if _, _, err := newClient(t, srv.URL).Snapshot(context.Background(), ""); err == nil || hits.Load() != tc.wantHits {
				t.Fatal("wrong retry budget", err, hits.Load())
			}
		})
	}
}

func TestSnapshotBusyRetryCancellation(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.WriteHeader(429)
		json.NewEncoder(w).Encode(map[string]string{"error": "snapshot_busy"})
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	c := newClient(t, srv.URL)
	go func() { _, _, err := c.Snapshot(ctx, ""); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("busy retry ignored cancellation")
	}
}

func TestSnapshotBusyTriesFailoverBeforeRetry(t *testing.T) {
	var hits atomic.Int32
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(429)
		json.NewEncoder(w).Encode(map[string]string{"error": "snapshot_busy"})
	}))
	defer busy.Close()
	good := jsonServer(t, 200, snapshot())
	if _, server, err := newClient(t, busy.URL, good.URL).Snapshot(context.Background(), ""); err != nil || server != good.URL || hits.Load() != 1 {
		t.Fatal("busy response deferred healthy failover", server, err, hits.Load())
	}
	denied := jsonServer(t, 403, map[string]string{"error": "role_forbidden"})
	if _, _, err := newClient(t, busy.URL, denied.URL).Snapshot(context.Background(), ""); err == nil || errors.Is(err, ErrNoCollector) || hits.Load() != 2 {
		t.Fatal("busy response retried past an authorization rejection", err, hits.Load())
	}
}
