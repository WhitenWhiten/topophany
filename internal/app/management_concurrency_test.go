//go:build linux || darwin

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/management"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestManagedSourcesRespectSharedConcurrency(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer server.Close()
	defer close(release)
	c := model.Config{Schema: 1, Project: "home", Observers: []model.Observer{{ID: "a", Context: "test"}}}
	for i := range 4 {
		id := fmt.Sprintf("source-%d", i)
		c.Nodes = append(c.Nodes, model.Node{ID: id, ResourceChecks: []string{id}})
		c.Sources = append(c.Sources, model.Source{ID: id, Observer: "a", Adapter: "komari", Target: server.URL})
		c.Checks = append(c.Checks, model.Check{ID: id, Kind: "external", Observer: "a", External: &model.ExternalBinding{Source: id, Object: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Report: "resource", Node: id, MaxAgeSeconds: 180}})
	}
	config.Defaults(&c)
	limits, err := management.ConstraintsFor(c, "a")
	if err != nil {
		t.Fatal(err)
	}
	limits.MaxConcurrency = 1
	if err = limits.Check(c, "a", 1); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "management")
	if err = management.MakePrivateDirectory(stateDir, false); err != nil {
		t.Fatal(err)
	}
	topologyRaw, limitsRaw := management.Marshal(c), management.Marshal(limits)
	topologyDigest, limitsDigest := management.Digest(topologyRaw), management.Digest(limitsRaw)
	state := management.State{Schema: 1, Installation: "worker-a", Controller: "review", Project: "home", Role: "worker", Observer: "a", Epoch: 1, Config: management.ChannelState{ActiveGeneration: 1, ActiveDigest: topologyDigest, ActiveConstraints: limitsDigest}}
	for name, raw := range map[string][]byte{topologyDigest + ".json": topologyRaw, limitsDigest + ".json": limitsRaw, "state.json": management.Marshal(state)} {
		if err = management.WriteLocal(filepath.Join(stateDir, name), raw); err != nil {
			t.Fatal(err)
		}
	}
	agent, loaded, err := management.NewAgent("", dir, c, "worker", "a", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	if !agent.Managed() {
		t.Fatal("test setup is not managed")
	}
	scheduler, err := prepareScheduler(loaded, "a", 1, func([]model.Observation) error { return nil }, agent)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	defer func() { cancel(); <-done }()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	select {
	case <-entered:
	case <-timeout.C:
		t.Fatal("source never started")
	}
	select {
	case <-entered:
		t.Fatal("concurrency grant exceeded")
	case <-time.After(1200 * time.Millisecond):
	}
}
