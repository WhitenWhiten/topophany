//go:build linux || darwin

package management

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func topology() model.Config {
	c := model.Config{Schema: 1, Project: "home", Nodes: []model.Node{{ID: "server", Name: "Server"}}, Observers: []model.Observer{{ID: "a", Name: "A", Context: "test", StaleSeconds: 90}}, Endpoints: []model.Endpoint{{ID: "endpoint", Node: "server", Address: "127.0.0.1:1234", Network: "loopback", Observer: "a"}}, Checks: []model.Check{{ID: "tcp", Name: "TCP", Kind: "tcp", Observer: "a", Endpoint: "endpoint"}}}
	config.Defaults(&c)
	return c
}
func put(t *testing.T, dir, name string, v any) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if e := os.WriteFile(path, Marshal(v), 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func TestControllerAgentConfigurationTransaction(t *testing.T) {
	p, trust, key, _ := fixture(t)
	root := t.TempDir()
	cfg := topology()
	if e := config.Validate(cfg); e != nil {
		t.Fatal(e)
	}
	agentToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	adminToken := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cp := ControllerPolicy{Schema: 1, Enabled: true, ID: trust.ID, Epoch: trust.Epoch, Data: filepath.Join(root, "control"), Registrations: []Registration{{p.Installation, p.Project, p.Role, p.Observer}}}
	cp.SignKey = put(t, root, "signer.json", SigningKey{"one", base64.RawURLEncoding.EncodeToString(key)})
	cp.Auth = put(t, root, "auth.json", Auth{1, []ManagementPrincipal{{Hash: Digest([]byte(agentToken)), Role: "management-agent", Installation: p.Installation, Project: p.Project}, {Hash: Digest([]byte(adminToken)), Role: "config-publish", Project: p.Project, Installations: []string{p.Installation}}}})
	ctl, e := NewController(cp)
	if e != nil {
		t.Fatal(e)
	}
	defer ctl.Close()
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); ctl.ServeHTTP(w, r) }))
	defer server.Close()
	caPath := filepath.Join(root, "ca.pem")
	os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	p.Controller.URL = server.URL
	p.Controller.CAFile = caPath
	p.Controller.CredentialFile = put(t, root, "credential.json", Credential{agentToken})
	p.Controller.CommandTrustFile = put(t, root, "trust.json", trust)
	limits, e := ConstraintsFor(cfg, "a")
	if e != nil {
		t.Fatal(e)
	}
	p.Config.ConstraintsFile = put(t, root, "constraints.json", limits)
	p.Config.Accept = false
	path := put(t, root, "policy.json", p)
	os.WriteFile(path+".lock", nil, 0644)
	agent, _, e := NewAgent(path, root, cfg, "worker", "a", 4)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if agent != nil {
			agent.Close()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var applied atomic.Pointer[model.Config]
	changes := agent.Changes
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-changes:
				err := req.Context.Err()
				if err == nil {
					err = req.Check()
				}
				if err == nil {
					c := req.Config
					applied.Store(&c)
				}
				req.Done <- err
			}
		}
	}()
	if e = agent.Poll(ctx); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 0 {
		t.Fatal("disabled agent made requests")
	}
	p.Config.Accept = true
	put(t, root, "policy.json", p)
	if e = agent.Poll(ctx); e != nil {
		t.Fatal(e)
	}
	next := config.Clone(cfg)
	next.Checks[0].IntervalSeconds = 120
	plan := ConfigDeployment{"deployment-1", []string{p.Installation}, map[string]uint64{p.Installation: 0}, Marshal(next)}
	if _, _, e = Exchange(ctx, server.Client(), adminToken, "POST", server.URL+"/management/v1/config-deployments", Marshal(plan), 1<<20); e != nil {
		t.Fatal(e)
	}
	if e = agent.Poll(ctx); e != nil {
		t.Fatal(e)
	}
	if applied.Load() != nil || agent.store.Snapshot().Config.Prepared == nil {
		t.Fatal("preparation executed config or lacked receipt")
	}
	if _, _, e = Exchange(ctx, server.Client(), adminToken, "POST", server.URL+"/management/v1/activate", Marshal(Advance{"deployment-1", []string{p.Installation}}), 1<<20); e != nil {
		t.Fatal(e)
	}
	if e = agent.Poll(ctx); e != nil {
		t.Fatal(e)
	}
	if c := applied.Load(); c == nil || c.Checks[0].IntervalSeconds != 120 {
		t.Fatal("config did not activate")
	}
	if e = agent.Poll(ctx); e != nil {
		t.Fatal(e)
	} // duplicate activation is a status retry
	if agent.store.Snapshot().Config.ActiveGeneration != 1 {
		t.Fatal("active generation missing")
	}
	p.Config.Accept = false
	p.Config.Session = "new-session"
	put(t, root, "policy.json", p)
	before := requests.Load()
	if e = agent.Poll(ctx); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != before {
		t.Fatal("disabled poller contacted controller")
	}
	cancel()
	agent.Close()
	agent = nil
	recovered, active, e := NewAgent(path, root, cfg, "worker", "a", 4)
	if e != nil {
		t.Fatal(e)
	}
	if !recovered.Managed() || active.Checks[0].IntervalSeconds != 120 {
		recovered.Close()
		t.Fatal("disabled restart lost committed configuration")
	}
	recovered.Close()
	os.WriteFile(path, []byte(`{"schema":1,"config":{"accept":true,"accept":false}}`), 0600)
	readonly, active, e := NewAgent(path, root, cfg, "worker", "a", 4)
	if e != nil {
		t.Fatal(e)
	}
	if !readonly.ReadOnly || !readonly.Managed() || active.Checks[0].IntervalSeconds != 120 {
		t.Fatal("invalid policy lost committed config or enabled management")
	}
	before = requests.Load()
	if e = readonly.Poll(context.Background()); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != before {
		t.Fatal("invalid policy contacted controller")
	}
	readonly.Close()
	removed, active, e := NewAgent("", root, cfg, "worker", "a", 4)
	if e != nil {
		t.Fatal(e)
	}
	defer removed.Close()
	if !removed.Managed() || active.Checks[0].IntervalSeconds != 120 {
		t.Fatal("removing startup flag silently reverted configuration")
	}
}
func TestConstraintsRejectDestinationAndSecretRebinding(t *testing.T) {
	c := topology()
	limits, e := ConstraintsFor(c, "a")
	if e != nil {
		t.Fatal(e)
	}
	cases := map[string]func(*model.Config){"target": func(c *model.Config) { c.Endpoints[0].Address = "127.0.0.2:1234" }, "interval": func(c *model.Config) { c.Checks[0].IntervalSeconds = 1 }, "disable": func(c *model.Config) { c.Checks[0].Disabled = true }, "remove": func(c *model.Config) { c.Checks = nil }}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			next := config.Clone(c)
			edit(&next)
			if limits.Check(next, "a", 4) == nil {
				t.Fatal("policy accepted change")
			}
		})
	}
	dns := config.Clone(c)
	dns.Endpoints[0].Address = "example.invalid:1234"
	if _, e = ConstraintsFor(dns, "a"); e == nil {
		t.Fatal("unpinned DNS accepted")
	}
}
func TestProjectionPreservesOwnContractAndRemovesOtherSecrets(t *testing.T) {
	c := topology()
	c.Observers = append(c.Observers, model.Observer{ID: "b", Name: "B", Context: "test", StaleSeconds: 90})
	c.Checks = append(c.Checks, model.Check{ID: "secret", Name: "Other", Kind: "http", Observer: "b", Target: "https://example.invalid/", HTTP: model.HTTPOptions{BearerEnv: "OTHER_SECRET"}})
	config.Defaults(&c)
	v, e := Project(c, "a", "worker")
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Checks) != 1 || config.ObserverContracts(v, "a") != config.ObserverContracts(c, "a") {
		t.Fatal("projection changed contract")
	}
	for _, x := range v.Checks {
		if x.HTTP.BearerEnv != "" {
			t.Fatal("unrelated secret reference")
		}
	}
}
func TestControllerRefusesObservationCredentials(t *testing.T) {
	_, trust, key, _ := fixture(t)
	root := t.TempDir()
	p := ControllerPolicy{Schema: 1, Enabled: true, ID: trust.ID, Epoch: 1, Data: filepath.Join(root, "data")}
	p.SignKey = put(t, root, "key.json", SigningKey{"one", base64.RawURLEncoding.EncodeToString(key)})
	for _, role := range []string{"viewer", "worker"} {
		p.Auth = put(t, root, "auth.json", Auth{1, []ManagementPrincipal{{Hash: Digest([]byte(role)), Role: role, Project: "home"}}})
		if c, e := NewController(p); e == nil {
			c.Close()
			t.Fatal("observation role gained management")
		}
	}
}
func TestExpiredTrustRefused(t *testing.T) {
	p, tr, key, c := fixture(t)
	tr.Expires = time.Now().Add(-time.Second)
	if _, e := ParseCommand(signed(t, key, c), p, tr, time.Now()); e == nil {
		t.Fatal("expired trust")
	}
}
