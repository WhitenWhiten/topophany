//go:build linux || darwin

package management

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestIdlePollsReuseConnection(t *testing.T) {
	var mu sync.Mutex
	addresses := map[string]bool{}
	count := 0
	a, _, _, _, _ := reviewAgent(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		addresses[r.RemoteAddr] = true
		count++
		mu.Unlock()
		w.WriteHeader(204)
	}))
	for range 3 {
		if err := a.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("three idle polls: requests=%d TLS_connections=%d", count, len(addresses))
	if count != 6 || len(addresses) != 1 {
		t.Fatal("unexpected regression result")
	}
}

func TestSlowReleaseVerificationAllowsOtherRequests(t *testing.T) {
	_, trust, key, cmd := fixture(t)
	publisherToken, agentToken := Digest([]byte("review publisher")), Digest([]byte("review agent"))
	publisherHash, agentHash := Digest([]byte(publisherToken)), Digest([]byte(agentToken))
	c := &Controller{
		Policy: ControllerPolicy{ID: trust.ID, Epoch: 1, Data: t.TempDir(), InventoryComplete: true},
		key:    SigningKey{ID: "one"}, private: key,
		state: controllerState{Schema: 1, Nodes: map[string]NodeRecord{
			cmd.Installation: {Registration: Registration{ID: cmd.Installation, Project: cmd.Project, Role: "worker", Observer: "a"}, Received: time.Now(), Status: &NodeStatus{UpdateAccept: true, UpdateSession: cmd.Session}},
		}, Blobs: map[string]int64{}},
		auth: map[string]ManagementPrincipal{
			publisherHash: {Hash: publisherHash, Role: "update-publish", Project: cmd.Project, Installations: []string{cmd.Installation}},
			agentHash:     {Hash: agentHash, Role: "management-agent", Project: cmd.Project, Installation: cmd.Installation},
		}, rates: map[string]rate{}, slots: make(chan struct{}, 16),
	}
	entered, unblock := make(chan struct{}), make(chan struct{})
	c.VerifyUpdate = func(context.Context, []byte, NodeStatus, []NodeRecord) error {
		close(entered)
		<-unblock
		return errors.New("simulated slow upstream")
	}
	plan := UpdateDeployment{ID: "review", Targets: []string{cmd.Installation}, Bases: map[string]uint64{cmd.Installation: 0}, Payloads: map[string]json.RawMessage{cmd.Installation: json.RawMessage(`{"version":"v1.7.1"}`)}}
	publish := httptest.NewRequest("POST", "/management/v1/update-deployments", bytes.NewReader(Marshal(plan)))
	publish.Header.Set("Authorization", "Bearer "+publisherToken)
	publishDone := make(chan struct{})
	go func() { defer close(publishDone); c.ServeHTTP(httptest.NewRecorder(), publish) }()
	<-entered
	read := httptest.NewRequest("GET", "/management/v1/capabilities", nil)
	read.Header.Set("Authorization", "Bearer "+agentToken)
	readDone := make(chan struct{})
	go func() { defer close(readDone); c.ServeHTTP(httptest.NewRecorder(), read) }()
	select {
	case <-readDone:
		t.Log("unrelated read completed during upstream verification")
	case <-time.After(200 * time.Millisecond):
		t.Error("unrelated read blocked behind network verification")
	}
	close(unblock)
	<-publishDone
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("read failed to recover")
	}
}

func TestAdministrativeAuditSupportsFullFleet(t *testing.T) {
	c := &Controller{Policy: ControllerPolicy{Data: t.TempDir()}, state: controllerState{Schema: 1, Nodes: map[string]NodeRecord{}, Blobs: map[string]int64{}}}
	deployment := strings.Repeat("d", 87)
	for i := range 255 {
		id := fmt.Sprintf("node-%d", i)
		receipt := &Receipt{CommandID: deployment + ".prepare", Deployment: deployment, CommandDigest: Digest([]byte("command")), Sequence: 2, Generation: 1, PayloadDigest: Digest([]byte("payload")), State: "rejected", Code: "measurement_contract_not_authorized", At: time.Now().UTC()}
		c.state.Nodes[id] = NodeRecord{Registration: Registration{ID: id, Project: "home", Role: "worker", Observer: "a"}, Status: &NodeStatus{Installation: id, Receipt: receipt}}
	}
	err := c.save()
	t.Logf("255 node fleet, valid 95-character command IDs and normal rejection code: receipt_bytes=%d, save=%v, storage_failed=%v", len(Marshal(c.state.Nodes["node-1"].Status.Receipt)), err, c.failed)
	if err != nil || c.failed {
		t.Fatal("unexpected regression result", err)
	}
}

func TestManagedSourceIntervalIsProtected(t *testing.T) {
	c := model.Config{Schema: 1, Project: "home", Nodes: []model.Node{{ID: "server", Name: "Server", ResourceChecks: []string{"resources"}}}, Observers: []model.Observer{{ID: "a", Context: "test"}}, Sources: []model.Source{{ID: "source", Observer: "a", Adapter: "komari", Target: "https://127.0.0.1", IntervalSeconds: 60}}, Checks: []model.Check{{ID: "resources", Name: "Resources", Kind: "external", Observer: "a", External: &model.ExternalBinding{Source: "source", Object: "server", Report: "resource", Node: "server", MaxAgeSeconds: 180}}}}
	config.Defaults(&c)
	limits, err := ConstraintsFor(c, "a")
	if err != nil {
		t.Fatal(err)
	}
	c.Sources[0].IntervalSeconds = 1
	if err := limits.Check(c, "a", 4); err == nil {
		t.Fatal("inconsistent interval accepted")
	}
	c.Checks[0].IntervalSeconds = 1
	if err := limits.Check(c, "a", 4); err == nil {
		t.Fatal("under-budget interval accepted")
	}
	t.Log("both inconsistent and synchronized one-second intervals were rejected")
}

func TestUnauthorizedKeepAlivesReleaseConnections(t *testing.T) {
	token := Digest([]byte("synthetic authorized review agent"))
	hash := Digest([]byte(token))
	c := &Controller{auth: map[string]ManagementPrincipal{hash: {Hash: hash, Role: "management-agent", Installation: "a"}}, rates: map[string]rate{}, slots: make(chan struct{}, 16)}
	server := httptest.NewUnstartedServer(c)
	budget := &connectionBudget{Listener: server.Listener, slots: make(chan struct{}, 16)}
	server.Listener = budget
	server.Config.IdleTimeout = 30 * time.Second
	server.Config.ReadHeaderTimeout = 3 * time.Second
	server.StartTLS()
	defer server.Close()
	clients := []*http.Client{}
	newClient := func() *http.Client {
		client := &http.Client{Transport: server.Client().Transport.(*http.Transport).Clone(), Timeout: time.Second}
		clients = append(clients, client)
		return client
	}
	defer func() {
		for _, client := range clients {
			client.CloseIdleConnections()
		}
	}()
	for range 16 {
		response, err := newClient().Get(server.URL + "/management/v1/capabilities")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal(response.StatusCode)
		}
	}
	authorized := newClient()
	request, _ := http.NewRequest("GET", server.URL+"/management/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := authorized.Do(request)
	if err != nil {
		t.Fatal("anonymous keep-alives blocked authorized request", err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}

	clients[0].CloseIdleConnections()
	deadline := time.Now().Add(time.Second)
	for len(budget.slots) == 16 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	request, _ = http.NewRequest("GET", server.URL+"/management/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = authorized.Do(request)
	if err != nil {
		t.Fatal("did not recover after releasing one anonymous slot", err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
}

func TestUpdateVerificationRejectsConcurrentStateChange(t *testing.T) {
	c := &Controller{state: controllerState{Schema: 1, Nodes: map[string]NodeRecord{"a": {Generation: 1}}}}
	c.VerifyUpdate = func(context.Context, []byte, NodeStatus, []NodeRecord) error {
		c.mu.Lock()
		n := c.state.Nodes["a"]
		n.Generation++
		c.state.Nodes["a"] = n
		c.mu.Unlock()
		return nil
	}
	r := httptest.NewRequest("POST", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), controllerLockKey{}, true))
	c.mu.Lock()
	err := c.verifyOutsideLock(r, r.Context(), nil, NodeStatus{}, "home")
	c.mu.Unlock()
	if err == nil || err.Error() != "update_state_changed_retry" {
		t.Fatal("stale verification was accepted", err)
	}
}

func TestPollRotatesConnectionOnCredentialChange(t *testing.T) {
	var tokens []string
	a, p, _, _, dir := reviewAgent(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		w.WriteHeader(204)
	}))
	if err := a.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := a.httpClient
	token := Digest([]byte("rotated review credential"))
	put(t, dir, "credential.json", Credential{Token: token})
	if err := a.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.httpClient == old || tokens[len(tokens)-1] != "Bearer "+token {
		t.Fatal("credential rotation retained old connection")
	}
	// Invalid trust must prevent further requests, including on a cached connection.
	if err := os.WriteFile(p.Controller.CAFile, []byte("invalid CA"), 0600); err != nil {
		t.Fatal(err)
	}
	count := len(tokens)
	if err := a.Poll(context.Background()); err == nil || len(tokens) != count {
		t.Fatal("invalid CA reused cached authorization")
	}
}
