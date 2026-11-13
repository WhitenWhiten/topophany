//go:build linux || darwin

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
	"github.com/WhitenWhiten/topophany/internal/update"
)

func TestManagementCLIInitialPolicyAndCredentialArePrivateAndDefaultOff(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "policy.json")
	if _, _, e := invoke("", "management", "init", "--policy", policy); e != nil {
		t.Fatal(e)
	}
	p, e := management.LoadPolicy(policy)
	if e != nil || p.Network() {
		t.Fatal("init granted management", p, e)
	}
	credential := filepath.Join(dir, "credential.json")
	out, _, e := invoke("", "management", "credential", "--output", credential)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(credential)
	if e != nil {
		t.Fatal(e)
	}
	var c management.Credential
	if e = json.Unmarshal(raw, &c); e != nil {
		t.Fatal(e)
	}
	if len(c.Token) != 64 || strings.Contains(out, c.Token) {
		t.Fatal("credential missing or leaked")
	}
	st, _ := os.Stat(credential)
	if st.Mode().Perm() != 0600 {
		t.Fatal("credential not private")
	}
	if _, _, e = invoke("", "management", "enable", "--policy", policy, "--capability", "config"); e == nil {
		t.Fatal("unenrolled config acceptance enabled")
	}
	p, _ = management.LoadPolicy(policy)
	if p.Network() {
		t.Fatal("failed enable mutated defaults")
	}
}
func TestPublisherCLIUsesIndependentThresholdsAndNoPrivateOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "publisher")
	out, _, e := invoke("", "management", "release", "keygen", "--dir", dir)
	if e != nil {
		t.Fatal(e)
	}
	root, e := update.LoadRoot(filepath.Join(dir, "publisher-root.json"), false)
	if e != nil {
		t.Fatal(e)
	}
	if root.Trust.ThresholdFor("root") != 2 || root.Trust.ThresholdFor("release") != 1 || root.Trust.ThresholdFor("catalog") != 1 {
		t.Fatal("incorrect trust separation")
	}
	files, e := filepath.Glob(filepath.Join(dir, "*.key.json"))
	if e != nil || len(files) != 5 {
		t.Fatal("missing isolated signing keys")
	}
	for _, path := range files {
		raw, _ := os.ReadFile(path)
		var key management.SigningKey
		json.Unmarshal(raw, &key)
		if strings.Contains(out, key.Private) {
			t.Fatal("private key leaked to output")
		}
	}
}
func TestUpdatePeerOrdering(t *testing.T) {
	now := time.Now()
	worker := management.NodeStatus{Installation: "worker", Version: "1.7.0", Collectors: []string{"collector-a", "collector-b"}}
	peers := []management.NodeRecord{
		{Registration: management.Registration{ID: "worker", Role: "worker"}, Status: &worker, Received: now},
		{Registration: management.Registration{ID: "ca", Role: "collector"}, Status: &management.NodeStatus{Installation: "ca", CollectorID: "collector-a", Version: "1.7.1"}, Received: now},
		{Registration: management.Registration{ID: "cb", Role: "collector"}, Status: &management.NodeStatus{Installation: "cb", CollectorID: "collector-b", Version: "1.7.0"}, Received: now},
	}
	manifest := update.Manifest{Version: "v1.7.1", CompatibleWorkers: []string{"v1.7.1"}}
	if checkUpdatePeers(manifest, worker, peers, now) == nil {
		t.Fatal("worker advanced before second collector")
	}
	if checkUpdatePeers(manifest, *peers[1].Status, peers, now) == nil {
		t.Fatal("collector ignored older workers")
	}
	manifest.CompatibleWorkers = append(manifest.CompatibleWorkers, "v1.7.0")
	if e := checkUpdatePeers(manifest, *peers[1].Status, peers, now); e != nil {
		t.Fatal(e)
	}
	peers[2].Status.Version = "1.7.1"
	if e := checkUpdatePeers(manifest, worker, peers, now); e != nil {
		t.Fatal(e)
	}
	peers[2].Received = now.Add(-2 * time.Minute)
	if checkUpdatePeers(manifest, worker, peers, now) == nil {
		t.Fatal("stale receiver inventory accepted")
	}
}
