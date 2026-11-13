//go:build linux || darwin

package management

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func reviewAgent(t *testing.T, handler http.Handler) (*Agent, Policy, ed25519.PrivateKey, model.Config, string) {
	t.Helper()
	p, trust, key, _ := fixture(t)
	dir := t.TempDir()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	p.Controller.URL = server.URL
	p.Controller.CAFile = filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(p.Controller.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	p.Controller.CredentialFile = put(t, dir, "credential.json", Credential{Digest([]byte("synthetic agent token"))})
	p.Controller.CommandTrustFile = put(t, dir, "trust.json", trust)
	cfg := topology()
	limits, err := ConstraintsFor(cfg, p.Observer)
	if err != nil {
		t.Fatal(err)
	}
	p.Config.ConstraintsFile = put(t, dir, "constraints.json", limits)
	path := filepath.Join(dir, "policy.json")
	if err = WritePolicy(path, p); err != nil {
		t.Fatal(err)
	}
	agent, _, err := NewAgent(path, dir, cfg, p.Role, p.Observer, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agent.Close() })
	return agent, p, key, cfg, dir
}

func TestAgentReportsCompletedReceiptAfterCommandExpiry(t *testing.T) {
	var desired Envelope
	var received *Receipt
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/v1/status":
			var status NodeStatus
			raw, _ := io.ReadAll(r.Body)
			if err := Decode(raw, &status, 16<<10); err != nil {
				t.Error(err)
			}
			if status.Receipt != nil {
				received = status.Receipt
			}
			w.WriteHeader(204)
		case "/management/v1/desired":
			managementJSON(w, 200, desired)
		default:
			t.Error("completed command fetched its payload again", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	a, p, key, _, dir := reviewAgent(t, handler)
	_, _, _, cmd := fixture(t)
	cmd.Issued = time.Now().Add(-time.Hour)
	cmd.NotBefore = cmd.Issued
	cmd.Expires = cmd.Issued.Add(15 * time.Minute)
	desired = signed(t, key, cmd)
	if _, _, err := a.store.Begin(cmd, desired, cmd.Issued); err != nil {
		t.Fatal(err)
	}
	finished, err := a.store.Finish(cmd, "prepared", "", cmd.Issued)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Poll(context.Background()); err != nil {
		t.Fatalf("lost receipt cannot be retried after expiry: %v", err)
	}
	if received == nil || received.Digest() != finished.Digest() {
		t.Fatal("controller did not receive the exact durable receipt", received)
	}
	// Status-only authorization can acknowledge a past action without enabling
	// it again or requiring the old controller trust to remain current.
	p.Config.Accept = false
	p.Config.Session = "rotated"
	p.Report = true
	put(t, dir, "policy.json", p)
	if err := os.Remove(p.Controller.CommandTrustFile); err != nil {
		t.Fatal(err)
	}
	received = nil
	if err = a.Poll(context.Background()); err != nil || received == nil || received.Digest() != finished.Digest() {
		t.Fatal("status-only reporting lost a durable result", received, err)
	}
	// A different envelope cannot use that reporting path to create a receipt.
	cmd.ID = "unaccepted"
	desired = signed(t, key, cmd)
	received = nil
	if err = a.Poll(context.Background()); err != nil || received != nil {
		t.Fatal("status-only poll executed or acknowledged an unknown command", received, err)
	}
}

func TestAgentRestartTerminatesInterruptedConfiguration(t *testing.T) {
	a, p, key, cfg, dir := reviewAgent(t, nil)
	_, _, _, cmd := fixture(t)
	cmd.Digest, cmd.Length = Digest(Marshal(cfg)), int64(len(Marshal(cfg)))
	env := signed(t, key, cmd)
	if _, _, err := a.store.Begin(cmd, env, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, active, err := NewAgent(a.path, dir, cfg, p.Role, p.Observer, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	state := recovered.store.Snapshot()
	if state.Config.Last.State != "failed" || state.Config.Last.Code != "configuration_interrupted" || state.Config.Sequence != cmd.Sequence || state.Config.ActiveGeneration != 0 || Digest(Marshal(active)) != Digest(Marshal(cfg)) {
		t.Fatalf("interrupted command was not reconciled: %+v", state.Config)
	}
	if receipt, err := recovered.Process(context.Background(), cmd, env, Marshal(cfg)); err != nil || receipt.State != "failed" {
		t.Fatal("interrupted action was retried", receipt, err)
	}
}

func TestControllerFailedMutationRestoresBlobBudget(t *testing.T) {
	for _, failure := range []string{"mutation", "checkpoint"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			c := &Controller{Policy: ControllerPolicy{Data: dir}, state: controllerState{Schema: 1, Nodes: map[string]NodeRecord{}, Blobs: map[string]int64{}}}
			if failure == "checkpoint" {
				if err := os.Mkdir(filepath.Join(dir, "audit.jsonl"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			raw := []byte(`{"fixture":true}`)
			hash := Digest(raw)
			err := c.mutate(func() error {
				if err := c.putBlob(hash, raw); err != nil {
					return err
				}
				if failure == "mutation" {
					return errors.New("injected failure after first target")
				}
				return nil
			})
			if err == nil || len(c.state.Blobs) != 0 {
				t.Fatal("failed publication consumed the committed blob budget", c.state.Blobs, err)
			}
			if err = c.collectOrphanBlobs(); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(dir, "blob-"+hash+".json")); !os.IsNotExist(err) {
				t.Fatal("orphan blob survived collection", err)
			}
		})
	}
}

type stalledBody struct {
	started chan struct{}
	release chan struct{}
}

func (b *stalledBody) Read([]byte) (int, error) {
	close(b.started)
	<-b.release
	return 0, io.EOF
}
func (*stalledBody) Close() error { return nil }

func TestControllerSlowBodyDoesNotBlockOtherNodes(t *testing.T) {
	token := Digest([]byte("synthetic agent"))
	hash := Digest([]byte(token))
	c := &Controller{auth: map[string]ManagementPrincipal{hash: {Hash: hash, Role: "management-agent", Installation: "a"}}, rates: map[string]rate{}, slots: make(chan struct{}, 16)}
	body := &stalledBody{started: make(chan struct{}), release: make(chan struct{})}
	request := httptest.NewRequest("POST", "/management/v1/status", nil)
	request.Body = body
	request.Header.Set("Authorization", "Bearer "+token)
	done := make(chan struct{})
	go func() { defer close(done); c.ServeHTTP(httptest.NewRecorder(), request) }()
	<-body.started
	defer func() { close(body.release); <-done }()
	ready := make(chan int, 1)
	go func() {
		request := httptest.NewRequest("GET", "/management/v1/capabilities", bytes.NewReader(nil))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		c.ServeHTTP(response, request)
		ready <- response.Code
	}()
	select {
	case code := <-ready:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Error("request body held the controller state lock")
	}
}

type stalledResponse struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
}

func (w *stalledResponse) WriteHeader(code int) {
	close(w.started)
	<-w.release
	w.ResponseRecorder.WriteHeader(code)
}

func TestControllerSlowResponseDoesNotBlockOtherNodes(t *testing.T) {
	token := Digest([]byte("synthetic agent"))
	hash := Digest([]byte(token))
	c := &Controller{auth: map[string]ManagementPrincipal{hash: {Hash: hash, Role: "management-agent", Installation: "a"}}, rates: map[string]rate{}, slots: make(chan struct{}, 16)}
	request := func() *http.Request {
		r := httptest.NewRequest("GET", "/management/v1/capabilities", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}
	response := &stalledResponse{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); c.ServeHTTP(response, request()) }()
	<-response.started
	defer func() { close(response.release); <-done }()
	ready := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		c.ServeHTTP(w, request())
		ready <- w.Code
	}()
	select {
	case code := <-ready:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Error("response write held the controller state lock")
	}
}

func TestCancelPreparedDeploymentAfterSessionRotation(t *testing.T) {
	for _, channel := range []string{"config", "update"} {
		t.Run(channel, func(t *testing.T) {
			p, trust, key, cmd := fixture(t)
			cmd.Kind = channel + ".prepare"
			trust.Keys[0].Roles = []string{"config", "update"}
			p.Updates = UpdatePolicy{Accept: true, Session: "new-session"}
			p.Config.Session = "new-session"
			env := signed(t, key, cmd)
			node := NodeRecord{Registration: Registration{ID: cmd.Installation, Project: cmd.Project, Role: cmd.Role, Observer: cmd.Observer}, Sequence: cmd.Sequence, Desired: &env, Received: time.Now(), Status: &NodeStatus{ConfigAccept: true, ConfigSession: p.Config.Session, UpdateAccept: true, UpdateSession: p.Updates.Session}}
			c := &Controller{Policy: ControllerPolicy{Data: t.TempDir()}, key: SigningKey{ID: "one"}, private: key, state: controllerState{Schema: 1, Nodes: map[string]NodeRecord{cmd.Installation: node}, Blobs: map[string]int64{}}}
			principal := ManagementPrincipal{Project: cmd.Project, Installations: []string{cmd.Installation}}
			for _, session := range []string{"new-session", "another-session"} {
				node = c.state.Nodes[cmd.Installation]
				node.Status.ConfigSession, node.Status.UpdateSession = session, session
				p.Config.Session, p.Updates.Session = session, session
				a := Advance{cmd.Deployment, []string{cmd.Installation}}
				response := httptest.NewRecorder()
				request := httptest.NewRequest("POST", "/management/v1/cancel", bytes.NewReader(Marshal(a)))
				if channel == "config" {
					c.advance(response, request, principal, time.Now())
				} else {
					c.advanceUpdate(response, request, principal, time.Now())
				}
				if response.Code != 200 {
					t.Fatal(response.Code, response.Body.String())
				}
				canceled, err := ParseCommand(*c.state.Nodes[cmd.Installation].Desired, p, trust, time.Now())
				if err != nil || canceled.Kind != channel+".cancel" || canceled.Session != session || canceled.Sequence <= cmd.Sequence {
					t.Fatal("cancellation reused revoked authority", canceled, err)
				}
			}
		})
	}
}
