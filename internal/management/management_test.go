//go:build linux || darwin

package management

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) (Policy, Trust, ed25519.PrivateKey, Command) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	p := Policy{Schema: 1, Project: "home", Installation: "worker-a", Role: "worker", Observer: "a", Controller: ControllerRef{ID: "control"}, Config: ConfigPolicy{Accept: true, Session: "session"}}
	tr := Trust{Schema: 1, ID: "control", Epoch: 1, Threshold: 1, Expires: now.Add(time.Hour), Keys: []Key{{"one", base64.RawURLEncoding.EncodeToString(pub), []string{"config"}}}}
	c := Command{Schema: 1, Kind: "config.prepare", Controller: "control", Epoch: 1, Project: "home", Installation: "worker-a", Role: "worker", Observer: "a", Session: "session", ID: "command-1", Deployment: "deploy-1", Sequence: 1, Generation: 1, Digest: Digest([]byte("{}")), Length: 2, Issued: now, NotBefore: now, Expires: now.Add(15 * time.Minute)}
	return p, tr, key, c
}
func signed(t *testing.T, k ed25519.PrivateKey, c Command) Envelope {
	t.Helper()
	e, err := Sign(ControlDomain, Marshal(c), "one", k)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestDefaultOffAndIndependentGates(t *testing.T) {
	for _, raw := range []string{`{"schema":1}`, `{"schema":1,"config":{},"updates":{}}`} {
		var p Policy
		if err := Decode([]byte(raw), &p, 1000); err != nil {
			t.Fatal(err)
		}
		if p.Network() || p.Allows("config.prepare") == nil || p.Allows("update.prepare") == nil {
			t.Fatal("implicit grant")
		}
	}
	for _, cfg := range []bool{false, true} {
		for _, upd := range []bool{false, true} {
			p := Disabled()
			p.Config.Accept = cfg
			p.Updates.Accept = upd
			if (p.Allows("config.prepare") == nil) != cfg || (p.Allows("update.prepare") == nil) != upd {
				t.Fatal("coupled permissions")
			}
		}
	}
	p, e := LoadPolicy("")
	if e != nil || p.Network() {
		t.Fatal(p, e)
	}
}
func TestStrictJSONAndPolicy(t *testing.T) {
	for _, raw := range []string{`{"schema":1,"config":{"accept":true,"accept":false}}`, `{"schema":1,"unknown":0}`, `{"schema":1,"config":{"Accept":true}}`, `{"schema":1,"config":{"accept":false,"Accept":true}}`, `{"schema":1} {}`, `{"schema":1,"updates":{"accept":"true"}}`} {
		var p Policy
		if Decode([]byte(raw), &p, 65536) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if e := os.WriteFile(path, []byte(`{"schema":1}`), 0600); e != nil {
		t.Fatal(e)
	}
	p, e := LoadPolicy(path)
	if e != nil || p.Network() {
		t.Fatal(p, e)
	}
	os.Chmod(path, 0666)
	if p, e = LoadPolicy(path); e == nil || p.Network() {
		t.Fatal("unsafe policy accepted")
	}
}
func TestCommandAuthenticationAndScope(t *testing.T) {
	p, tr, key, c := fixture(t)
	now := time.Now()
	e := signed(t, key, c)
	if _, err := ParseCommand(e, p, tr, now); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Command){"observer": func(c *Command) { c.Observer = "other" }, "session": func(c *Command) { c.Session = "old" }, "tenant": func(c *Command) { c.Project = "other" }, "controller": func(c *Command) { c.Controller = "other" }, "expired": func(c *Command) { c.Expires = c.Issued }, "sequence": func(c *Command) { c.Sequence = 0 }, "kind": func(c *Command) { c.Kind = "exec" }, "future": func(c *Command) { c.NotBefore = now.Add(time.Hour) }, "capability": func(c *Command) { c.Capabilities = []string{"arbitrary"} }}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			next := c
			change(&next)
			if _, err := ParseCommand(signed(t, key, next), p, tr, now); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	p.Config.Accept = false
	if _, err := ParseCommand(e, p, tr, now); err == nil {
		t.Fatal("disabled accepted")
	}
	p.Config.Accept = true
	e.Payload = base64.RawURLEncoding.EncodeToString([]byte(`{}`))
	if _, err := ParseCommand(e, p, tr, now); err == nil {
		t.Fatal("tampering accepted")
	}
}
func TestSignatureDomainAndThreshold(t *testing.T) {
	_, tr, key, c := fixture(t)
	e := signed(t, key, c)
	if _, err := Verify("topophany.release.v1", "config", e, tr, time.Now(), 32768); err == nil {
		t.Fatal("cross-domain signature")
	}
	if _, err := Verify(ControlDomain, "update", e, tr, time.Now(), 32768); err == nil {
		t.Fatal("cross-role signature")
	}
	tr.Threshold = 2
	second := tr.Keys[0]
	second.ID = "two"
	tr.Keys = append(tr.Keys, second)
	e.Signatures = append(e.Signatures, Signature{"two", e.Signatures[0].Value})
	if _, err := Verify(ControlDomain, "config", e, tr, time.Now(), 32768); err == nil {
		t.Fatal("one key satisfied threshold twice")
	}
}
func TestDurableReplayAndPreparedBinding(t *testing.T) {
	p, _, key, c := fixture(t)
	dir := t.TempDir()
	s, err := Open(dir, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	env := signed(t, key, c)
	now := time.Now()
	changedAuthority := c
	changedAuthority.Epoch++
	if _, _, err = s.Begin(changedAuthority, signed(t, key, changedAuthority), now); err == nil {
		t.Fatal("a new authority epoch bypassed persisted enrollment")
	}
	r, dup, err := s.Begin(c, env, now)
	if err != nil || dup || r.State != "accepted" {
		t.Fatal(r, dup, err)
	}
	r, err = s.Finish(c, "prepared", "", now)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, dup, err := s.Begin(c, env, now)
	if err != nil || !dup || got.State != "prepared" {
		t.Fatal(got, dup, err)
	}
	conflict := c
	conflict.ID = "other"
	if _, _, err = s.Begin(conflict, signed(t, key, conflict), now); err == nil {
		t.Fatal("conflicting command accepted")
	}
	activate := c
	activate.Sequence = 2
	activate.ID = "activate"
	activate.Kind = "config.activate"
	activate.Prepared = Digest([]byte("wrong"))
	if _, _, err = s.Begin(activate, signed(t, key, activate), now); err == nil {
		t.Fatal("wrong prepared receipt")
	}
	activate.Prepared = r.Digest()
	if _, _, err = s.Begin(activate, signed(t, key, activate), now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Finish(activate, "active", "", now); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Config.ActiveGeneration != 1 {
		t.Fatal("generation not committed")
	}
	c.Sequence = 3
	c.ID = "old-base"
	if _, _, err = s.Begin(c, signed(t, key, c), now); err == nil {
		t.Fatal("stale base accepted")
	}
	if _, _, err = s.Begin(c, signed(t, key, c), now.Add(-time.Hour)); err == nil {
		t.Fatal("clock rollback")
	}
}
func TestBlobIntegrityAndSymlinks(t *testing.T) {
	p, _, _, _ := fixture(t)
	s, e := Open(t.TempDir(), p, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	b := []byte(`{"schema":1}`)
	h := Digest(b)
	if e = s.PutBlob(b, h); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Blob(h); e != nil {
		t.Fatal(e)
	}
	if e = s.PutBlob(b, Digest([]byte("other"))); e == nil {
		t.Fatal("digest mismatch")
	}
	os.WriteFile(filepath.Join(s.dir, h+".json"), []byte("tampered"), 0600)
	if _, e = s.Blob(h); e == nil {
		t.Fatal("corruption")
	}
	target := filepath.Join(t.TempDir(), "target")
	os.WriteFile(target, b, 0600)
	link := filepath.Join(s.dir, "link")
	if e = os.Symlink(target, link); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadLocal(link, 1024, false, false); e == nil {
		t.Fatal("symlink accepted")
	}
}
func FuzzStrictEnvelope(f *testing.F) {
	f.Add([]byte(`{"payload_b64":"e30","signatures":[]}`))
	f.Fuzz(func(t *testing.T, b []byte) { var e Envelope; _ = Decode(b, &e, 65536) })
}

func TestPublicKeyEncodingCannotDuplicateThresholdIdentity(t *testing.T) {
	_, trust, _, _ := fixture(t)
	trust.Threshold = 2
	alias := trust.Keys[0]
	alias.ID = "alias"
	alias.Public = alias.Public[:10] + "\n" + alias.Public[10:]
	trust.Keys = append(trust.Keys, alias)
	if trust.Validate(time.Now()) == nil {
		t.Fatal("noncanonical encoding counted one key twice")
	}
}
