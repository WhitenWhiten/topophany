//go:build linux || darwin

package management

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestCoordinatedContractChangeRequiresDrainedWorkersAndActivatedCollectors(t *testing.T) {
	_, trust, key, _ := fixture(t)
	dir := t.TempDir()
	before := topology()
	after := config.Clone(before)
	after.Endpoints[0].Address = "127.0.0.1:2345"
	registrations := []Registration{{"collector-one", "home", "collector", ""}, {"collector-two", "home", "collector", ""}, {"worker-one", "home", "worker", "a"}}
	tokens := []string{"1111111111111111111111111111111111111111111111111111111111111111", "2222222222222222222222222222222222222222222222222222222222222222", "3333333333333333333333333333333333333333333333333333333333333333"}
	admin := "4444444444444444444444444444444444444444444444444444444444444444"
	ids := []string{}
	auth := Auth{Schema: 1}
	for i, r := range registrations {
		ids = append(ids, r.ID)
		auth.Tokens = append(auth.Tokens, ManagementPrincipal{Hash: Digest([]byte(tokens[i])), Role: "management-agent", Installation: r.ID, Project: "home"})
	}
	auth.Tokens = append(auth.Tokens, ManagementPrincipal{Hash: Digest([]byte(admin)), Role: "config-publish", Project: "home", Installations: ids})
	cp := ControllerPolicy{InventoryComplete: true, Schema: 1, Enabled: true, ID: trust.ID, Epoch: 1, Data: filepath.Join(dir, "controller"), Registrations: registrations, Auth: put(t, dir, "auth.json", auth), SignKey: put(t, dir, "key.json", SigningKey{"one", base64.RawURLEncoding.EncodeToString(key)})}
	controller, e := NewController(cp)
	if e != nil {
		t.Fatal(e)
	}
	defer controller.Close()
	server := httptest.NewTLSServer(controller)
	defer server.Close()
	ca := filepath.Join(dir, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	agents := []*Agent{}
	defer func() {
		cancel()
		wg.Wait()
		for _, a := range agents {
			a.Close()
		}
	}()
	var pending atomic.Int64
	pending.Store(1)
	applied := make([]atomic.Pointer[model.Config], 3)
	for i, reg := range registrations {
		root := filepath.Join(dir, reg.ID)
		os.Mkdir(root, 0700)
		limits, e := ConstraintsFor(before, reg.Observer)
		if e != nil {
			t.Fatal(e)
		}
		next, e := ConstraintsFor(after, reg.Observer)
		if e != nil {
			t.Fatal(e)
		}
		if e = limits.Merge(next); e != nil {
			t.Fatal(e)
		}
		p := Policy{Schema: 1, Project: "home", Installation: reg.ID, Role: reg.Role, Observer: reg.Observer, Controller: ControllerRef{ID: trust.ID, URL: server.URL, CAFile: ca, CredentialFile: put(t, root, "token.json", Credential{tokens[i]}), CommandTrustFile: put(t, root, "trust.json", trust)}, Config: ConfigPolicy{Accept: true, Session: "session", ConstraintsFile: put(t, root, "limits.json", limits)}}
		policy := put(t, root, "policy.json", p)
		os.WriteFile(policy+".lock", nil, 0644)
		agent, _, e := NewAgent(policy, root, before, reg.Role, reg.Observer, 4)
		if e != nil {
			t.Fatal(e)
		}
		agents = append(agents, agent)
		if reg.Role == "collector" {
			agent.CollectorID = reg.ID
		} else {
			agent.Collectors = func() []string { return ids[:2] }
			agent.Pending = func() (int, uint64) { return int(pending.Load()), 0 }
		}
		index := i
		changes := agent.Changes
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case req := <-changes:
					e := req.Context.Err()
					if e == nil && req.Check != nil {
						e = req.Check()
					}
					if e == nil && req.Operation == "" {
						c := req.Config
						applied[index].Store(&c)
					}
					req.Done <- e
				}
			}
		}()
		if e = agent.Poll(ctx); e != nil {
			t.Fatal(e)
		}
	}
	call := func(route string, v any, want int) {
		t.Helper()
		_, status, e := Exchange(ctx, server.Client(), admin, "POST", server.URL+route, Marshal(v), 1<<20)
		if status != want {
			t.Fatalf("%s status %d want %d: %v", route, status, want, e)
		}
	}
	plan := ConfigDeployment{"change-target", ids, map[string]uint64{ids[0]: 0, ids[1]: 0, ids[2]: 0}, Marshal(after)}
	controller.mu.Lock()
	controller.Policy.InventoryComplete = false
	controller.mu.Unlock()
	call("/management/v1/config-deployments", plan, 409)
	controller.mu.Lock()
	controller.Policy.InventoryComplete = true
	controller.mu.Unlock()
	call("/management/v1/config-deployments", plan, 201)
	if e = agents[0].Poll(ctx); e != nil {
		t.Fatal(e)
	}
	call("/management/v1/quiesce", Advance{plan.ID, ids}, 409)
	for _, a := range agents[1:] {
		if e = a.Poll(ctx); e != nil {
			t.Fatal(e)
		}
	}
	call("/management/v1/quiesce", Advance{plan.ID, ids}, 200)
	for _, a := range agents {
		if e = a.Poll(ctx); e != nil {
			t.Fatal(e)
		}
		a.checkPause(ctx)
		if e = a.Poll(ctx); e != nil {
			t.Fatal(e)
		}
	}
	call("/management/v1/activate", Advance{plan.ID, ids[:1]}, 409)
	pending.Store(0)
	agents[2].checkPause(ctx)
	if e = agents[2].Poll(ctx); e != nil {
		t.Fatal(e)
	}
	call("/management/v1/activate", Advance{plan.ID, ids[2:]}, 409)
	call("/management/v1/activate", Advance{plan.ID, ids[:1]}, 200)
	if e = agents[0].Poll(ctx); e != nil {
		t.Fatal(e)
	}
	call("/management/v1/activate", Advance{plan.ID, ids[2:]}, 409)
	call("/management/v1/activate", Advance{plan.ID, ids[1:2]}, 200)
	if e = agents[1].Poll(ctx); e != nil {
		t.Fatal(e)
	}
	call("/management/v1/activate", Advance{plan.ID, ids[2:]}, 200)
	if e = agents[2].Poll(ctx); e != nil {
		t.Fatal(e)
	}
	for i, a := range agents {
		if a.Paused() || a.store.Snapshot().Config.ActiveGeneration != 1 || applied[i].Load() == nil {
			t.Fatalf("participant %d not activated", i)
		}
	}
}
func TestPauseExpiresAndQuarantineAbortsDrain(t *testing.T) {
	// The expiry mechanism is independent of control polling/backoff.
	p, _, _, _ := fixture(t)
	dir := t.TempDir()
	p.Config.ConstraintsFile = put(t, dir, "constraints.json", Constraints{})
	path := put(t, dir, "policy.json", p)
	st, e := Open(filepath.Join(dir, "state"), p, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	a := &Agent{path: path, store: st, Changes: make(chan ApplyRequest), paused: true, pauseUntil: time.Now().Add(-time.Second), pauseSession: p.Config.Session}
	a.pausedFlag.Store(true)
	done := make(chan string, 1)
	go func() { req := <-a.Changes; done <- req.Operation; req.Done <- nil }()
	a.checkPause(context.Background())
	if <-done != "resume" || a.Paused() {
		t.Fatal("expired pause did not resume")
	}
}

func TestQuiesceDetectsQuarantineWhileSchedulerStops(t *testing.T) {
	a, p, key, _, _ := reviewAgent(t, nil)
	_, _, _, cmd := fixture(t)
	env := signed(t, key, cmd)
	if _, _, err := a.store.Begin(cmd, env, time.Now()); err != nil {
		t.Fatal(err)
	}
	cmd.LeaseSeconds = 180
	var quarantined atomic.Uint64
	a.Pending = func() (int, uint64) { return 0, quarantined.Load() }
	go func() {
		request := <-a.Changes
		quarantined.Add(1) // A delivery fails while the scheduler is stopping.
		request.Done <- nil
	}()
	if _, err := a.quiesce(context.Background(), cmd, p); err != nil {
		t.Fatal(err)
	}
	resumed := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case request := <-a.Changes:
			resumed <- request.Operation
			request.Done <- nil
		case <-ctx.Done():
		}
	}()
	a.checkPause(ctx)
	state := a.store.Snapshot().Config
	if state.Quiesced || a.Paused() || state.Last.Code != "configuration_drain_quarantined" {
		t.Fatalf("lost observations counted as a successful drain: %+v", state)
	}
	if <-resumed != "resume" {
		t.Fatal("aborted barrier did not resume monitoring")
	}
}
