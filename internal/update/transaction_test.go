package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
)

type testSource struct {
	versions map[string]Resolution
	archives map[int64][]byte
}

type changingReleaseSource struct {
	ReleaseSource
	change func()
}

func (s changingReleaseSource) Resolve(ctx context.Context, root Root, version, osName, arch string, floor uint64, now time.Time) (Resolution, error) {
	r, err := s.ReleaseSource.Resolve(ctx, root, version, osName, arch, floor, now)
	s.change()
	return r, err
}

func TestHelperRechecksLocalAuthorityBeforeStoppingService(t *testing.T) {
	for _, change := range []string{"update-policy", "publisher-trust", "controller-trust"} {
		t.Run(change, func(t *testing.T) {
			h, reg, p, cmd, env, payload, key, effects, original := helperFixture(t)
			defer h.Close()
			prepared, err := h.Handle(context.Background(), HelperRequest{env, payload})
			if err != nil {
				t.Fatal(err)
			}
			changed := false
			h.source = changingReleaseSource{ReleaseSource: original, change: func() {
				if changed {
					return
				}
				changed = true
				switch change {
				case "update-policy":
					policy, err := LoadPolicy(p.Updates.PolicyFile, false)
					if err != nil {
						t.Fatal(err)
					}
					policy.Minimum = "v1.7.2"
					writeJSON(t, p.Updates.PolicyFile, policy)
				case "publisher-trust":
					root, err := LoadRoot(p.Updates.PublisherTrustFile, false)
					if err != nil {
						t.Fatal(err)
					}
					for i := range root.Trust.Keys {
						root.Trust.Keys[i].Roles = []string{"root"}
					}
					writeJSON(t, p.Updates.PublisherTrustFile, root)
				case "controller-trust":
					trust, err := management.LoadTrust(p.Controller.CommandTrustFile, false, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					trust.Keys[0].Roles = []string{"config"}
					writeJSON(t, p.Controller.CommandTrustFile, trust)
				}
			}}
			_, activate := activation(t, cmd, prepared, key)
			receipt, err := h.Handle(context.Background(), HelperRequest{activate, nil})
			if err == nil || receipt.State != "failed" || effects.stopped != 0 {
				t.Fatal("changed local authority did not stop activation", receipt, err, effects.stopped)
			}
			if digest, err := fileDigest(reg.Binary, false); err != nil || digest != original.versions[effects.old].Artifact.BinarySHA256 {
				t.Fatal("revoked activation replaced the binary", digest, err)
			}
		})
	}
}

func (s *testSource) Resolve(_ context.Context, _ Root, v, _, _ string, _ uint64, _ time.Time) (Resolution, error) {
	r, ok := s.versions[v]
	if !ok {
		return Resolution{}, errors.New("missing release")
	}
	return r, nil
}
func (s *testSource) Download(_ context.Context, a Artifact, f *os.File) error {
	_, e := f.Write(s.archives[a.ID])
	return e
}

type testEffects struct {
	beforeHealth                func(string)
	revision                    string
	candidateFails              bool
	old                         string
	stopped, started, preflight int
}

type contextCheckingEffects struct{ *testEffects }

func (e contextCheckingEffects) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.testEffects.Stop(ctx)
}
func (e contextCheckingEffects) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.testEffects.Start(ctx)
}
func (e contextCheckingEffects) Healthy(ctx context.Context, version, digest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.testEffects.Healthy(ctx, version, digest)
}

func TestHelperRecoveryHasIndependentDeadline(t *testing.T) {
	h, reg, _, _, env, payload, _, effects, source := helperFixture(t)
	defer h.Close()
	if _, err := h.Handle(context.Background(), HelperRequest{env, payload}); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(reg.Binary, filepath.Join(reg.State, "previous"), h.state.OldDigest, false); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(filepath.Join(reg.State, "candidate"), reg.Binary, source.versions["v1.7.1"].Artifact.BinarySHA256, false); err != nil {
		t.Fatal(err)
	}
	h.effects = contextCheckingEffects{effects}
	exhausted, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	receipt, err := h.recover(exhausted, func(state, code string) (management.Receipt, error) {
		r := *h.state.Last
		r.State, r.Code = state, code
		h.state.Last = &r
		return r, h.save()
	})
	if err != nil || receipt.State != "rolled_back" || effects.started != 1 {
		t.Fatal("exhausted activation budget prevented recovery", receipt, err)
	}
	if digest, err := fileDigest(reg.Binary, false); err != nil || digest != h.state.OldDigest {
		t.Fatal("old executable was not restored", digest, err)
	}
}

func (e *testEffects) Configuration(context.Context) (string, error) {
	if e.revision != "" {
		return e.revision, nil
	}
	return strings.Repeat("c", 64), nil
}
func (e *testEffects) Stop(context.Context) error  { e.stopped++; return nil }
func (e *testEffects) Start(context.Context) error { e.started++; return nil }
func (e *testEffects) Preflight(context.Context, string, string, string) error {
	e.preflight++
	return nil
}
func (e *testEffects) Healthy(_ context.Context, version, _ string) error {
	if e.beforeHealth != nil {
		e.beforeHealth(version)
	}
	if e.candidateFails && version != e.old {
		return errors.New("candidate not healthy")
	}
	return nil
}

func TestHelperAuditFailureDoesNotPreventBinaryRecovery(t *testing.T) {
	h, reg, _, cmd, env, payload, key, effects, source := helperFixture(t)
	effects.candidateFails = true
	brokenAudit := filepath.Join(reg.State, "audit.jsonl")
	effects.beforeHealth = func(version string) {
		if version != effects.old {
			if err := os.Remove(brokenAudit); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(brokenAudit, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	prepared, err := h.Handle(context.Background(), HelperRequest{env, payload})
	if err != nil {
		t.Fatal(err)
	}
	_, activate := activation(t, cmd, prepared, key)
	receipt, err := h.Handle(context.Background(), HelperRequest{activate, nil})
	if err == nil || !h.storageFailed || receipt.State == "healthy" || effects.started != 2 {
		t.Fatal("persistence fault suppressed recovery or reported success", receipt, err, effects.started)
	}
	digest, err := fileDigest(reg.Binary, false)
	if err != nil || digest != source.versions[effects.old].Artifact.BinarySHA256 {
		t.Fatal("previous binary not restored", err)
	}
	h.Close()
	if err := os.Remove(brokenAudit); err != nil {
		t.Fatal(err)
	}
	effects.beforeHealth = nil
	reopened, err := openHelper(reg, effects, source, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.Recover(context.Background()); err != nil || reopened.state.Phase != "rolled_back" {
		t.Fatal("unconfirmed recovery not reconciled on restart", err)
	}
}

func TestHelperRegistrationRejectsOverlappingMutablePaths(t *testing.T) {
	h, reg, _, _, _, _, _, _, _ := helperFixture(t)
	h.Close()
	for _, name := range []string{"nested-state", "data-in-state", "binary-in-state", "config-in-data", "auth-collision"} {
		t.Run(name, func(t *testing.T) {
			bad := reg
			switch name {
			case "nested-state":
				bad.State = filepath.Join(reg.Data, "updater")
			case "data-in-state":
				bad.Data = filepath.Join(reg.State, "data")
			case "binary-in-state":
				bad.Binary = filepath.Join(reg.State, "candidate")
			case "config-in-data":
				bad.Config = filepath.Join(reg.Data, "local-runtime.json")
			case "auth-collision":
				bad.Role = "collector"
				bad.Auth = reg.Binary
			}
			if bad.Validate() == nil {
				t.Fatal("overlapping registration accepted")
			}
		})
	}
}
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if e := os.WriteFile(path, management.Marshal(v), 0600); e != nil {
		t.Fatal(e)
	}
}
func testArtifact(t *testing.T, version string, id int64, bin []byte) (Artifact, []byte) {
	t.Helper()
	name, e := ArtifactName(version, runtime.GOOS, runtime.GOARCH)
	if e != nil {
		t.Fatal(e)
	}
	root := strings.TrimSuffix(name, ".tar.gz")
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tr := tar.NewWriter(gz)
	for _, member := range []string{"topo", "LICENSE", "README.md", "README.zh.md", "README.ja.md", "CHANGELOG.md"} {
		data := []byte("test metadata")
		if member == "topo" {
			data = bin
		}
		if e = tr.WriteHeader(&tar.Header{Name: root + "/" + member, Mode: 0644, Size: int64(len(data)), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		tr.Write(data)
	}
	tr.Close()
	gz.Close()
	return Artifact{id, name, runtime.GOOS, runtime.GOARCH, int64(b.Len()), management.Digest(b.Bytes()), int64(len(bin)), management.Digest(bin)}, b.Bytes()
}
func helperFixture(t *testing.T) (*Helper, Registration, management.Policy, management.Command, management.Envelope, []byte, ed25519.PrivateKey, *testEffects, *testSource) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native archive transaction fixture")
	}
	dir := t.TempDir()
	root, key := publisher(t)
	now := time.Now().UTC()
	oldVersion, newVersion := "v1.7.0", "v1.7.1"
	oldBytes, newBytes := []byte("old synthetic binary"), []byte("new synthetic binary")
	oldArtifact, oldArchive := testArtifact(t, oldVersion, 10, oldBytes)
	newArtifact, newArchive := testArtifact(t, newVersion, 20, newBytes)
	catalog := Catalog{1, root.RepositoryID, root.Trust.Epoch, 1, now, now.Add(time.Hour), oldVersion, nil, nil}
	source := &testSource{map[string]Resolution{}, map[int64][]byte{10: oldArchive, 20: newArchive}}
	for _, a := range []Artifact{oldArtifact, newArtifact} {
		v := oldVersion
		rollback := []string{}
		if a.ID == 20 {
			v = newVersion
			rollback = []string{oldVersion}
		}
		m := Manifest{Schema: 1, RepositoryID: root.RepositoryID, ReleaseID: a.ID, Version: v, Commit: strings.Repeat("a", 40), Artifacts: []Artifact{a}, ControlProtocol: 1, RollbackFrom: rollback, CompatibleWorkers: []string{oldVersion, newVersion}}
		env := envelope(t, ReleaseDomain, m, key)
		source.versions[v] = Resolution{Release: Release{ID: a.ID, Tag: v, Immutable: true, Published: now}, Manifest: m, Envelope: env, Catalog: catalog, Artifact: a, ResolvedAt: now}
	}
	pub, controlKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	trust := management.Trust{Schema: 1, ID: "control", Epoch: 1, Threshold: 1, Expires: now.Add(time.Hour), Keys: []management.Key{{ID: "control-key", Public: base64.RawURLEncoding.EncodeToString(pub), Roles: []string{"update"}}}}
	p := management.Policy{Schema: 1, Project: "home", Installation: "worker-a", Role: "worker", Observer: "a", Controller: management.ControllerRef{ID: "control", URL: "https://example.invalid", CredentialFile: filepath.Join(dir, "credential.json"), CommandTrustFile: filepath.Join(dir, "control-trust.json")}, Updates: management.UpdatePolicy{Accept: true, Session: "update-session", PolicyFile: filepath.Join(dir, "update-policy.json"), PublisherTrustFile: filepath.Join(dir, "publisher.json")}}
	writeJSON(t, p.Controller.CommandTrustFile, trust)
	writeJSON(t, p.Updates.PublisherTrustFile, root)
	writeJSON(t, p.Updates.PolicyFile, Policy{Schema: 1, Minimum: oldVersion, MaxMajor: 1, AnyTime: true, Socket: filepath.Join(dir, "helper.sock"), HealthSeconds: 30})
	reg := Registration{InstallType: "managed_binary", Schema: 1, Installation: p.Installation, Project: p.Project, Role: p.Role, Observer: p.Observer, PolicyFile: filepath.Join(dir, "management.json"), Binary: filepath.Join(dir, "topo"), Service: "topophany-worker.service", User: "test-service", Data: filepath.Join(dir, "data"), Config: filepath.Join(dir, "topology.json"), State: filepath.Join(dir, "updater"), BootstrapManifest: filepath.Join(dir, "bootstrap.json")}
	writeJSON(t, reg.PolicyFile, p)
	os.WriteFile(reg.PolicyFile+".lock", nil, 0644)
	writeJSON(t, reg.BootstrapManifest, source.versions[oldVersion].Envelope)
	os.WriteFile(reg.Binary, oldBytes, 0755)
	os.Mkdir(reg.Data, 0700)
	writeJSON(t, reg.Config, map[string]int{"schema": 1})
	effects := &testEffects{old: oldVersion}
	h, e := openHelper(reg, effects, source, false)
	if e != nil {
		t.Fatal(e)
	}
	payload := management.Marshal(TargetFor(source.versions[newVersion]))
	cmd := management.Command{Schema: 1, Kind: "update.prepare", Controller: "control", Epoch: 1, Project: p.Project, Installation: p.Installation, Role: p.Role, Observer: p.Observer, Session: p.Updates.Session, ID: "update-1.prepare", Deployment: "update-1", Sequence: 1, Generation: 1, Digest: management.Digest(payload), Length: int64(len(payload)), Issued: now, NotBefore: now, Expires: now.Add(15 * time.Minute), Capabilities: []string{"update.github-release.v1"}}
	env, e := management.Sign(management.ControlDomain, management.Marshal(cmd), "control-key", controlKey)
	if e != nil {
		t.Fatal(e)
	}
	return h, reg, p, cmd, env, payload, controlKey, effects, source
}
func activation(t *testing.T, cmd management.Command, prepared management.Receipt, key ed25519.PrivateKey) (management.Command, management.Envelope) {
	t.Helper()
	cmd.Kind = "update.activate"
	cmd.ID = "update-1.activate"
	cmd.Sequence++
	cmd.Prepared = prepared.Digest()
	e, err := management.Sign(management.ControlDomain, management.Marshal(cmd), "control-key", key)
	if err != nil {
		t.Fatal(err)
	}
	return cmd, e
}
func TestHelperVerifiedUpdateAndIdempotency(t *testing.T) {
	h, reg, _, cmd, env, payload, key, effects, source := helperFixture(t)
	defer h.Close()
	prepared, e := h.Handle(context.Background(), HelperRequest{env, payload})
	if e != nil || prepared.State != "prepared" {
		t.Fatal(prepared, e)
	}
	if effects.stopped != 0 {
		t.Fatal("preparation stopped service")
	}
	_, activate := activation(t, cmd, prepared, key)
	healthy, e := h.Handle(context.Background(), HelperRequest{activate, nil})
	if e != nil || healthy.State != "healthy" {
		t.Fatal(healthy, e)
	}
	hash, e := fileDigest(reg.Binary, false)
	if e != nil || hash != source.versions["v1.7.1"].Artifact.BinarySHA256 {
		t.Fatal("installed wrong binary", e)
	}
	duplicate, e := h.Handle(context.Background(), HelperRequest{activate, nil})
	if e != nil || duplicate.Digest() != healthy.Digest() || effects.stopped != 1 || effects.started != 1 {
		t.Fatal("non-idempotent update", duplicate, e)
	}
	if h.state.Highest != "v1.7.1" || h.state.Generation != 1 {
		t.Fatal("version or generation not committed")
	}
}

func TestHelperCurrentVersionIsVerifiedNoOp(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "tampered"}[tamper], func(t *testing.T) {
			h, reg, _, cmd, _, _, key, effects, source := helperFixture(t)
			defer h.Close()
			payload := management.Marshal(TargetFor(source.versions[effects.old]))
			cmd.Digest, cmd.Length = management.Digest(payload), int64(len(payload))
			env, err := management.Sign(management.ControlDomain, management.Marshal(cmd), "control-key", key)
			if err != nil {
				t.Fatal(err)
			}
			if tamper {
				os.WriteFile(reg.Binary, []byte("modified installed binary"), 0755)
			}
			prepared, err := h.Handle(context.Background(), HelperRequest{env, payload})
			if tamper {
				if err == nil || prepared.Code != "installed_binary_integrity_failed" {
					t.Fatal(prepared, err)
				}
				return
			}
			if err != nil {
				t.Fatal(prepared, err)
			}
			_, activate := activation(t, cmd, prepared, key)
			result, err := h.Handle(context.Background(), HelperRequest{activate, nil})
			if err != nil || result.State != "healthy" || effects.stopped != 0 || effects.started != 0 || effects.preflight != 0 {
				t.Fatal("same-version request restarted service", result, err, effects)
			}
		})
	}
}
func TestHelperFailedHealthRestoresBinaryWithoutRestoringData(t *testing.T) {
	h, reg, _, cmd, env, payload, key, effects, source := helperFixture(t)
	defer h.Close()
	effects.candidateFails = true
	evidence := filepath.Join(reg.Data, "acknowledged")
	os.WriteFile(evidence, []byte("retained evidence"), 0600)
	prepared, e := h.Handle(context.Background(), HelperRequest{env, payload})
	if e != nil {
		t.Fatal(e)
	}
	_, activate := activation(t, cmd, prepared, key)
	receipt, e := h.Handle(context.Background(), HelperRequest{activate, nil})
	if e == nil || receipt.State != "rolled_back" {
		t.Fatal(receipt, e)
	}
	hash, e := fileDigest(reg.Binary, false)
	if e != nil || hash != source.versions["v1.7.0"].Artifact.BinarySHA256 {
		t.Fatal("previous binary not restored", e)
	}
	b, _ := os.ReadFile(evidence)
	if string(b) != "retained evidence" {
		t.Fatal("data restored from old snapshot")
	}
	if effects.stopped != 2 || effects.started != 2 {
		t.Fatal(effects)
	}
}
func TestHelperDisableAndStagingTamper(t *testing.T) {
	for _, disable := range []bool{true, false} {
		t.Run(map[bool]string{true: "disabled", false: "staging-tamper"}[disable], func(t *testing.T) {
			h, reg, p, cmd, env, payload, key, effects, _ := helperFixture(t)
			defer h.Close()
			prepared, e := h.Handle(context.Background(), HelperRequest{env, payload})
			if e != nil {
				t.Fatal(e)
			}
			_, activate := activation(t, cmd, prepared, key)
			if disable {
				p.Updates.Accept = false
				writeJSON(t, reg.PolicyFile, p)
			} else {
				os.WriteFile(filepath.Join(reg.State, "candidate"), []byte("tampered"), 0755)
			}
			if _, e = h.Handle(context.Background(), HelperRequest{activate, nil}); e == nil {
				t.Fatal("unsafe activation accepted")
			}
			if effects.stopped != 0 {
				t.Fatal("stopped service before verifying policy and bytes")
			}
		})
	}
}
func TestHelperCrashRecovery(t *testing.T) {
	h, reg, _, cmd, env, payload, key, effects, source := helperFixture(t)
	prepared, e := h.Handle(context.Background(), HelperRequest{env, payload})
	if e != nil {
		t.Fatal(e)
	}
	_, activate := activation(t, cmd, prepared, key)
	_ = activate
	if e = replaceBinary(reg.Binary, filepath.Join(reg.State, "previous"), h.state.OldDigest, false); e != nil {
		t.Fatal(e)
	}
	if e = replaceBinary(filepath.Join(reg.State, "candidate"), reg.Binary, source.versions["v1.7.1"].Artifact.BinarySHA256, false); e != nil {
		t.Fatal(e)
	}
	h.state.Phase = "checking"
	if e = h.save(); e != nil {
		t.Fatal(e)
	}
	h.Close()
	recovered, e := openHelper(reg, effects, source, false)
	if e != nil {
		t.Fatal(e)
	}
	defer recovered.Close()
	if e = recovered.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	if recovered.state.Phase != "rolled_back" {
		t.Fatal("interrupted installation not recovered")
	}
	digest, e := fileDigest(reg.Binary, false)
	if e != nil || digest != source.versions["v1.7.0"].Artifact.BinarySHA256 {
		t.Fatal("incorrect recovered binary")
	}
}
func TestUpdatePolicyWindowAndDowngrade(t *testing.T) {
	p := Policy{Schema: 1, Minimum: "v1.7.0", MaxMajor: 1, Timezone: "UTC", Start: "23:00", End: "01:00", Socket: filepath.Join(t.TempDir(), "helper.sock"), HealthSeconds: 30}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if !p.Window(time.Date(2026, 11, 12, 23, 30, 0, 0, time.UTC)) || p.Window(time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("window error")
	}
	if p.Allows("v1.6.9", "v1.7.0") == nil || p.Allows("v2.0.0", "v1.7.0") == nil {
		t.Fatal("unauthorized upgrade policy")
	}
	p.Timezone = ""
	if p.Validate() == nil {
		t.Fatal("implicit timezone accepted")
	}
	p.Timezone = "Australia/Lord_Howe"
	p.Start = "01:00"
	p.End = "03:00"
	for _, stamp := range []string{"2026-04-04T14:45:00Z", "2026-04-04T15:15:00Z"} {
		now, _ := time.Parse(time.RFC3339, stamp)
		if p.Window(now) {
			t.Fatal("half-hour DST ambiguity accepted", stamp)
		}
	}
}

func TestUpdateRefusesChangedRuntimeConfiguration(t *testing.T) {
	h, _, _, cmd, env, payload, key, effects, _ := helperFixture(t)
	defer h.Close()
	prepared, e := h.Handle(context.Background(), HelperRequest{env, payload})
	if e != nil {
		t.Fatal(e)
	}
	effects.revision = strings.Repeat("d", 64)
	_, activate := activation(t, cmd, prepared, key)
	r, e := h.Handle(context.Background(), HelperRequest{activate, nil})
	if e == nil || r.Code != "runtime_configuration_changed" || effects.stopped != 0 {
		t.Fatal("configuration changed without re-preflight", r, e)
	}
}
func TestHelperRegistrationCannotBeRetargetedAcrossRestart(t *testing.T) {
	h, reg, _, _, _, _, _, effects, source := helperFixture(t)
	h.Close()
	reg.Service = "topophany-other.service"
	if changed, e := openHelper(reg, effects, source, false); e == nil {
		changed.Close()
		t.Fatal("retargeted registration retained old authority")
	}
}
