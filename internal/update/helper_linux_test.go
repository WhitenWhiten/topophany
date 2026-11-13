//go:build linux

package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
)

func TestLinuxHelperReadinessBindsRootPeerAndInstallation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-peer success requires disposable Linux root test environment")
	}
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "update.json")
	socket := filepath.Join(dir, "helper.sock")
	writeJSON(t, policyPath, Policy{Schema: 1, Minimum: "v1.7.0", MaxMajor: 1, AnyTime: true, Socket: socket, HealthSeconds: 30})
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(socket, 0660); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/capabilities" {
			t.Error("readiness attempted mutation")
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`{"schema":1,"installation_id":"registered","protocol":1}`))
	})}
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(listener) }()
	defer func() { server.Close(); <-done }()
	p := management.Policy{System: true, Installation: "registered", Updates: management.UpdatePolicy{PolicyFile: policyPath}}
	if err = CheckHelper(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	p.Installation = "another-installation"
	if err = CheckHelper(context.Background(), p); err == nil {
		t.Fatal("helper for another installation accepted")
	}
	if err = os.Chmod(socket, 0666); err != nil {
		t.Fatal(err)
	}
	if err = CheckHelper(context.Background(), p); err == nil {
		t.Fatal("world-accessible socket accepted")
	}
}

func TestLinuxRecoveryHandoffKeepsPrivateGateUntilTraversalCompletes(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("ownership handoff requires disposable Linux root test environment")
	}
	base := t.TempDir()
	tree := filepath.Join(base, "trial")
	if err := os.MkdirAll(filepath.Join(tree, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(tree, "nested", "data")
	outside := filepath.Join(base, "outside")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(tree, "z-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := handoffRecoveryCopy(tree, 65534, 65534); err == nil {
		t.Fatal("untrusted link admitted")
	}
	for _, path := range []string{tree, outside} {
		st, err := os.Lstat(path)
		if err != nil || st.Sys().(*syscall.Stat_t).Uid != 0 {
			t.Fatal("private gate released early or outside owner changed", path, err)
		}
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := handoffRecoveryCopy(tree, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{tree, filepath.Dir(inside), inside} {
		st, err := os.Lstat(path)
		if err != nil || st.Sys().(*syscall.Stat_t).Uid != 65534 {
			t.Fatal("copy not handed over", path, err)
		}
	}
}

func TestLinuxRuntimeRecordRejectsLinksFIFOsAndInvalidLeases(t *testing.T) {
	dir := t.TempDir()
	effects := &linuxEffects{reg: Registration{Data: dir, Project: "home", Role: "worker", Observer: "a"}}
	path := filepath.Join(dir, "local-runtime.json")
	now := time.Now().UTC()
	record := runtimeRecord{Schema: 1, Role: "worker", PID: os.Getpid(), Version: "1.7.0", Project: "home", Observer: "a", Revision: strings.Repeat("a", 64), At: now, Until: now.Add(15 * time.Second)}
	writeJSON(t, path, record)
	if _, err := effects.Configuration(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"symlink", "fifo", "directory", "future", "overlong", "wrong-project"} {
		t.Run(kind, func(t *testing.T) {
			os.RemoveAll(path)
			bad := record
			switch kind {
			case "symlink":
				target := filepath.Join(dir, "outside.json")
				writeJSON(t, target, record)
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "future":
				bad.At = now.Add(time.Hour)
				bad.Until = bad.At.Add(15 * time.Second)
				writeJSON(t, path, bad)
			case "overlong":
				bad.Until = now.Add(time.Hour)
				writeJSON(t, path, bad)
			case "wrong-project":
				bad.Project = "elsewhere"
				writeJSON(t, path, bad)
			}
			if _, err := effects.Configuration(context.Background()); err == nil {
				t.Fatal("untrusted runtime record accepted")
			}
		})
	}
}

type acceptanceEffects struct {
	*linuxEffects
	failCandidate bool
}

func (e *acceptanceEffects) Healthy(ctx context.Context, version, digest string) error {
	if err := e.linuxEffects.Healthy(ctx, version, digest); err != nil {
		return err
	}
	if e.failCandidate && version == "v1.7.1" {
		return errors.New("injected candidate health failure after actual startup")
	}
	return nil
}

// Explicit, disposable-VM acceptance only: the test creates its own service and
// removes it. Ordinary go test never modifies systemd. GitHub is replaced with
// authenticated synthetic releases; lifecycle, recovery preflight, UID boundary,
// executable replacement and /proc health verification use the production code.
func TestLinuxSystemdUpdateAcceptance(t *testing.T) {
	if os.Getenv("TOPO_UPDATE_SYSTEMD_ACCEPTANCE") != "1" {
		t.Skip("requires an explicitly disposable Linux systemd VM")
	}
	if os.Geteuid() != 0 {
		t.Fatal("acceptance requires root in a disposable VM")
	}
	if _, err := os.Stat("/run/topophany-disposable-acceptance"); err != nil {
		t.Fatal("disposable VM sentinel missing")
	}
	fixtureDir := os.Getenv("TOPO_UPDATE_FIXTURES")
	oldBinary, err := os.ReadFile(filepath.Join(fixtureDir, "topo-old"))
	if err != nil {
		t.Fatal(err)
	}
	newBinary, err := os.ReadFile(filepath.Join(fixtureDir, "topo-new"))
	if err != nil {
		t.Fatal(err)
	}
	account, err := user.Lookup("nobody")
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	for _, mode := range []string{"verified-activation", "failed-health-recovery", "configuration-file-changed"} {
		t.Run(mode, func(t *testing.T) {
			dir, err := os.MkdirTemp("/opt", "topophany-update-acceptance-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			if err := os.Chmod(dir, 0755); err != nil {
				t.Fatal(err)
			}
			service := "topophany-acceptance-" + filepath.Base(dir)[len("topophany-update-acceptance-"):] + ".service"
			reg := Registration{InstallType: "managed_binary", Schema: 1, Installation: "acceptance", Project: "home", Role: "collector", User: "nobody", Binary: filepath.Join(dir, "topo"), PolicyFile: filepath.Join(dir, "management.json"), Config: filepath.Join(dir, "topology.json"), Auth: filepath.Join(dir, "auth.json"), Data: filepath.Join(dir, "data"), State: filepath.Join(dir, "updater"), BootstrapManifest: filepath.Join(dir, "bootstrap.json"), Service: service}
			if err := os.Mkdir(reg.Data, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(reg.Data, uid, gid); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(reg.Binary, oldBinary, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(reg.Config, []byte(`{"schema":1,"project":"home","nodes":[{"id":"server","name":"Server"}],"observers":[{"id":"a","name":"A","context":"native acceptance","stale_seconds":90}],"checks":[]}`), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(reg.Auth, []byte(`{"schema":1,"tokens":[{"role":"viewer","token_sha256":"`+strings.Repeat("a", 64)+`"}]}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(reg.Auth, uid, gid); err != nil {
				t.Fatal(err)
			}
			root, publisherKey := publisher(t)
			now := time.Now().UTC()
			source := &testSource{versions: map[string]Resolution{}, archives: map[int64][]byte{}}
			for i, binary := range [][]byte{oldBinary, newBinary} {
				version := []string{"v1.7.0", "v1.7.1"}[i]
				a, archive := testArtifact(t, version, int64(10+i), binary)
				m := Manifest{Schema: 1, RepositoryID: root.RepositoryID, ReleaseID: a.ID, Version: version, Commit: strings.Repeat("b", 40), Artifacts: []Artifact{a}, ControlProtocol: 1, RollbackFrom: []string{"v1.7.0"}, CompatibleWorkers: []string{"v1.7.0", "v1.7.1"}}
				source.versions[version] = Resolution{Manifest: m, Envelope: envelope(t, ReleaseDomain, m, publisherKey), Release: Release{ID: a.ID, Tag: version, Immutable: true, Published: now}, Artifact: a, Catalog: Catalog{1, root.RepositoryID, 1, 1, now, now.Add(time.Hour), "v1.7.0", nil, nil}}
				source.archives[a.ID] = archive
			}
			pub, controlKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			p := management.Policy{Schema: 1, System: true, Revision: 1, Project: "home", Role: "collector", Installation: "acceptance", Controller: management.ControllerRef{ID: "control", URL: "https://unused.invalid", CredentialFile: filepath.Join(dir, "credential.json"), CommandTrustFile: filepath.Join(dir, "control.json")}, Updates: management.UpdatePolicy{Session: "acceptance-session", PolicyFile: filepath.Join(dir, "update.json"), PublisherTrustFile: filepath.Join(dir, "publisher.json")}}
			writeJSON(t, p.Controller.CommandTrustFile, management.Trust{Schema: 1, ID: "control", Epoch: 1, Threshold: 1, Expires: now.Add(time.Hour), Keys: []management.Key{{ID: "control-key", Public: base64.RawURLEncoding.EncodeToString(pub), Roles: []string{"update"}}}})
			writeJSON(t, p.Updates.PublisherTrustFile, root)
			writeJSON(t, p.Updates.PolicyFile, Policy{Schema: 1, Minimum: "v1.7.0", MaxMajor: 1, AnyTime: true, Socket: filepath.Join(dir, "helper.sock"), HealthSeconds: 30})
			writeJSON(t, reg.BootstrapManifest, source.versions["v1.7.0"].Envelope)
			if err := management.WritePolicy(reg.PolicyFile, p); err != nil {
				t.Fatal(err)
			}
			unit := fmt.Sprintf("[Unit]\nDescription=Disposable Topophany update acceptance\n[Service]\nUser=nobody\nGroup=%s\nExecStart=%s serve --config %s --auth %s --data %s --listen 127.0.0.1:0\nNoNewPrivileges=true\nProtectSystem=strict\nReadWritePaths=%s\nPrivateTmp=true\n", account.Gid, reg.Binary, reg.Config, reg.Auth, reg.Data, reg.Data)
			unitPath := filepath.Join("/run/systemd/system", service)
			if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
				t.Fatal(err)
			}
			defer func() {
				exec.Command("/usr/bin/systemctl", "stop", service).Run()
				os.Remove(unitPath)
				exec.Command("/usr/bin/systemctl", "daemon-reload").Run()
			}()
			if out, err := exec.Command("/usr/bin/systemctl", "daemon-reload").CombinedOutput(); err != nil {
				t.Fatalf("daemon reload: %v %s", err, out)
			}
			linux, err := newLinuxEffects(reg)
			if err != nil {
				t.Fatal(err)
			}
			effects := &acceptanceEffects{linuxEffects: linux, failCandidate: mode == "failed-health-recovery"}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := effects.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := effects.Healthy(ctx, "v1.7.0", management.Digest(oldBinary)); err != nil {
				out, _ := exec.Command("/usr/bin/journalctl", "--unit", service, "--no-pager", "-n", "30").CombinedOutput()
				t.Fatalf("initial health: %v\n%s", err, out)
			}
			h, err := openHelper(reg, effects, source, true)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			p.Updates.Accept = true
			p.Revision++
			if err := management.WritePolicy(reg.PolicyFile, p); err != nil {
				t.Fatal(err)
			}
			payload := management.Marshal(TargetFor(source.versions["v1.7.1"]))
			cmd := management.Command{Schema: 1, Kind: "update.prepare", Controller: "control", Epoch: 1, Project: "home", Installation: p.Installation, Role: p.Role, Session: p.Updates.Session, ID: "native.prepare", Deployment: "native", Sequence: 1, Generation: 1, Digest: management.Digest(payload), Length: int64(len(payload)), Issued: now, NotBefore: now, Expires: now.Add(15 * time.Minute), Capabilities: []string{"update.github-release.v1"}}
			env, err := management.Sign(management.ControlDomain, management.Marshal(cmd), "control-key", controlKey)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := h.Handle(ctx, HelperRequest{env, payload})
			if err != nil {
				t.Fatal(prepared, err)
			}
			if mode == "configuration-file-changed" {
				raw, err := os.ReadFile(reg.Config)
				if err != nil {
					t.Fatal(err)
				}
				raw = []byte(strings.ReplaceAll(string(raw), `"Server"`, `"Changed locally"`))
				if err = os.WriteFile(reg.Config, raw, 0644); err != nil {
					t.Fatal(err)
				}
			}
			_, activate := activation(t, cmd, prepared, controlKey)
			result, err := h.Handle(ctx, HelperRequest{activate, nil})
			wantState, wantVersion := "healthy", "v1.7.1"
			if mode == "failed-health-recovery" {
				wantState, wantVersion = "rolled_back", "v1.7.0"
			}
			if mode == "configuration-file-changed" {
				wantState, wantVersion = "failed", "v1.7.0"
				if result.Code != "candidate_recovery_preflight_failed" {
					t.Fatal(result, err)
				}
			}
			if result.State != wantState || (err != nil) != (mode != "verified-activation") {
				t.Fatal(result, err)
			}
			if err := linux.Healthy(ctx, wantVersion, source.versions[wantVersion].Artifact.BinarySHA256); err != nil {
				t.Fatal(err)
			}
		})
	}
}
