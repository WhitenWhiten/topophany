package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestSnapshotPoliciesIsolationAndExpiry(t *testing.T) {
	var hits atomic.Int32
	var broken atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if broken.Load() {
			w.WriteHeader(503)
			return
		}
		switch r.URL.Path {
		case "/configs":
			io.WriteString(w, `{"mode":"rule","tun":{"enable":true}}`)
		case "/proxies":
			io.WriteString(w, `{"proxies":{"DIRECT":{"type":"Direct"}}}`)
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer s.Close()
	cache := &SnapshotCache{}
	token := "first"
	deps := Dependencies{Cache: cache, Secret: func(string) (string, error) { return token, nil }, Client: func(model.Check, string) (*http.Client, func(), error) { return s.Client(), func() {}, nil }}
	x := model.Check{Adapter: model.AdapterOptions{ExpectedMode: "rule"}}
	collect := func() (*model.Observation, error) {
		o := &model.Observation{Facts: map[string]string{}}
		return o, Collect(context.Background(), deps, x, s.URL, o)
	}
	first, err := collect()
	if err != nil {
		t.Fatal(err)
	}
	x.Adapter.ExpectedMode = "global"
	second, err := collect()
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || first.Outcome != "ok" || second.Outcome != "policy" || !first.SampledAt.Equal(second.SampledAt) {
		t.Fatalf("hits=%d first=%+v second=%+v", hits.Load(), first, second)
	}
	token = "rotated"
	if _, err = collect(); err != nil {
		t.Fatal(err)
	}
	deps.Scope = "different route"
	if _, err = collect(); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 6 {
		t.Fatal("credential/route isolation", hits.Load())
	}
	// Expire without sleeping. No collection is concurrently active.
	for _, s := range cache.entries {
		s.started = time.Now().Add(-2 * time.Second)
	}
	broken.Store(true)
	if _, err = collect(); err == nil {
		t.Fatal("error accepted")
	}
	broken.Store(false)
	if _, err = collect(); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 9 {
		t.Fatal("failed collection was reused", hits.Load())
	}
}

func TestSnapshotCancellationAndMemoryBound(t *testing.T) {
	cache := &SnapshotCache{}
	s, release, err := cache.acquire(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = cache.acquire(ctx, "key"); err == nil {
		t.Fatal("blocked read ignored cancellation")
	}
	s.retain("large", make([]byte, 8<<20))
	s.retain("extra", []byte{1})
	if s.bytes != 8<<20 || len(s.bodies) != 1 {
		t.Fatal("memory budget exceeded")
	}
	release()
	for _, key := range []string{"two", "three", "four"} {
		_, release, err = cache.acquire(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	s, release, err = cache.acquire(context.Background(), "five")
	if err != nil || s != nil {
		t.Fatal("entry budget exceeded")
	}
	release()
}

func TestLargeReferenceMembershipBoundary(t *testing.T) {
	for _, count := range []int{4096, 4097} {
		members := make([]string, count)
		for i := range members {
			members[i] = fmt.Sprintf("node-%d", i)
		}
		body, _ := json.Marshal(map[string]any{"schema": 1, "published_at": time.Now().UTC(), "providers": map[string]any{"pool": members}})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
		x := model.Check{Adapter: model.AdapterOptions{ProviderReference: &model.MihomoProviderReference{URL: srv.URL, Providers: []string{"pool"}, MaxAgeSeconds: 60}}}
		deps := Dependencies{Client: func(model.Check, string) (*http.Client, func(), error) { return srv.Client(), func() {}, nil }}
		_, err := readReference(context.Background(), deps, x)
		srv.Close()
		if (err == nil) != (count == 4096) {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
}
