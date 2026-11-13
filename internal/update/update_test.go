package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
)

func publisher(t *testing.T) (Root, ed25519.PrivateKey) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return Root{1, Repository, 123, 456, management.Trust{Schema: 1, ID: "official-publisher", Epoch: 1, Threshold: 1, Expires: time.Now().Add(time.Hour), Keys: []management.Key{{ID: "release", Public: base64.RawURLEncoding.EncodeToString(pub), Roles: []string{"release", "catalog", "root"}}}}}, key
}
func envelope(t *testing.T, domain string, v any, key ed25519.PrivateKey) management.Envelope {
	t.Helper()
	e, err := management.Sign(domain, management.Marshal(v), "release", key)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func archive(t *testing.T, alter string) ([]byte, Artifact) {
	t.Helper()
	version := "v1.7.0"
	name, _ := ArtifactName(version, "linux", "amd64")
	root := strings.TrimSuffix(name, ".tar.gz")
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	tr := tar.NewWriter(z)
	bin := []byte("synthetic binary; never executed")
	for _, n := range []string{"topo", "LICENSE", "README.md", "README.zh.md", "README.ja.md", "CHANGELOG.md"} {
		data := []byte("metadata")
		if n == "topo" {
			data = bin
		}
		path := root + "/" + n
		kind := byte(tar.TypeReg)
		link := ""
		if n == "topo" {
			switch alter {
			case "traversal":
				path = "../topo"
			case "symlink":
				kind = tar.TypeSymlink
				link = "/tmp/elsewhere"
				data = nil
			case "duplicate":
				path = root + "/LICENSE"
			}
		}
		if e := tr.WriteHeader(&tar.Header{Name: path, Mode: 0755, Size: int64(len(data)), Typeflag: kind, Linkname: link}); e != nil {
			t.Fatal(e)
		}
		tr.Write(data)
	}
	tr.Close()
	z.Close()
	a := Artifact{1, name, "linux", "amd64", int64(b.Len()), management.Digest(b.Bytes()), int64(len(bin)), management.Digest(bin)}
	return b.Bytes(), a
}
func TestStableVersions(t *testing.T) {
	for _, v := range []string{"latest", "main", "v1.0.0-rc.1", "v01.0.0", "v1.0.0+build", "../v1.0.0", "1.0.0"} {
		if _, e := Version(v); e == nil {
			t.Fatal(v)
		}
	}
	if n, e := Compare("v1.10.0", "v1.9.9"); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
func TestManifestAndCatalogTrust(t *testing.T) {
	root, key := publisher(t)
	_, a := archive(t, "")
	now := time.Now().UTC()
	m := Manifest{Schema: 1, RepositoryID: root.RepositoryID, ReleaseID: 789, Version: "v1.7.0", Commit: strings.Repeat("a", 40), Artifacts: []Artifact{a}, ControlProtocol: 1, RollbackFrom: []string{"v1.6.1"}, CompatibleWorkers: []string{"v1.7.0", "v1.6.1"}}
	env := envelope(t, ReleaseDomain, m, key)
	if _, e := VerifyManifest(env, root, now); e != nil {
		t.Fatal(e)
	}
	c := Catalog{1, root.RepositoryID, 1, 5, now, now.Add(time.Hour), "v1.6.1", nil, nil}
	ce := envelope(t, CatalogDomain, c, key)
	verified, e := VerifyCatalog(ce, root, 4, now)
	if e != nil {
		t.Fatal(e)
	}
	if e = verified.Permits(m, a, env); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyCatalog(ce, root, 6, now); e == nil {
		t.Fatal("catalog rollback")
	}
	if _, e = VerifyCatalog(ce, root, 4, now.Add(2*time.Hour)); e == nil {
		t.Fatal("expired catalog")
	}
	verified.RevokedArtifacts = []string{a.BinarySHA256}
	if verified.Permits(m, a, env) == nil {
		t.Fatal("revoked binary")
	}
	if _, e = VerifyManifest(envelope(t, CatalogDomain, m, key), root, now); e == nil {
		t.Fatal("cross-domain proof")
	}
	m.RepositoryID++
	if _, e = VerifyManifest(envelope(t, ReleaseDomain, m, key), root, now); e == nil {
		t.Fatal("foreign repository")
	}
}
func TestRootRotationRequiresBothKeys(t *testing.T) {
	root, old := publisher(t)
	next, newKey := publisher(t)
	next.Trust.Epoch = 2
	next.Trust.Keys[0].ID = "next"
	raw := management.Marshal(next)
	a, e := management.Sign(RootDomain, raw, "release", old)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyRootTransition(a, root, time.Now()); e == nil {
		t.Fatal("new root not self-authenticated")
	}
	b, e := management.Sign(RootDomain, raw, "next", newKey)
	if e != nil {
		t.Fatal(e)
	}
	a.Signatures = append(a.Signatures, b.Signatures...)
	if _, e = VerifyRootTransition(a, root, time.Now()); e != nil {
		t.Fatal(e)
	}
	next.Trust.Epoch = 3
	if _, e = VerifyRootTransition(envelope(t, RootDomain, next, old), root, time.Now()); e == nil {
		t.Fatal("skipped root")
	}
}
func TestArchiveClosedMembersAndHashes(t *testing.T) {
	for _, kind := range []string{"", "traversal", "symlink", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			b, a := archive(t, kind)
			dir := t.TempDir()
			path := filepath.Join(dir, "archive")
			os.WriteFile(path, b, 0600)
			f, e := os.Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			out, e := os.Create(filepath.Join(dir, "candidate"))
			if e != nil {
				t.Fatal(e)
			}
			defer out.Close()
			e = Extract(f, a, out)
			if kind == "" && e != nil {
				t.Fatal(e)
			}
			if kind != "" && e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestDownloadAddressPolicy(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::ffff:127.0.0.1", "::1", "fc00::1", "2001:db8::1"} {
		if publicAddress(netip.MustParseAddr(s)) {
			t.Fatal(s)
		}
	}
	if !publicAddress(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOfficialReleaseResolutionAndPrereleaseRejection(t *testing.T) {
	root, key := publisher(t)
	rawArchive, a := archive(t, "")
	now := time.Now().UTC()
	m := Manifest{Schema: 1, RepositoryID: root.RepositoryID, ReleaseID: 789, Version: "v1.7.0", Commit: strings.Repeat("a", 40), Artifacts: []Artifact{a}, ControlProtocol: 1, CompatibleWorkers: []string{"v1.7.0"}}
	env := envelope(t, ReleaseDomain, m, key)
	manifestRaw := management.Marshal(env)
	cat := Catalog{1, root.RepositoryID, 1, 1, now, now.Add(time.Hour), "v1.7.0", nil, nil}
	catalogRaw := management.Marshal(envelope(t, CatalogDomain, cat, key))
	release := Release{ID: 789, Tag: m.Version, Immutable: true, Published: now, Assets: []GitHubAsset{{ID: a.ID, Name: a.Name, Size: a.Size, Digest: "sha256:" + a.SHA256, State: "uploaded"}, {ID: 2, Name: "topophany-update.json", Size: int64(len(manifestRaw)), Digest: "sha256:" + management.Digest(manifestRaw), State: "uploaded"}}}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected endpoint or credentials")
		}
		path := strings.TrimPrefix(r.URL.Path, "/repos/"+Repository)
		var b []byte
		switch path {
		case "":
			b = management.Marshal(map[string]any{"id": 123, "full_name": Repository, "owner": map[string]int{"id": 456}})
		case "/releases/tags/v1.7.0":
			b = management.Marshal(release)
		case "/git/ref/tags/v1.7.0":
			b = management.Marshal(map[string]any{"object": map[string]string{"type": "commit", "sha": m.Commit}})
		case "/releases/assets/2":
			b = manifestRaw
		case "/releases/assets/1":
			b = rawArchive
		case "/contents/update-metadata/security.json":
			b = management.Marshal(map[string]any{"type": "file", "path": "update-metadata/security.json", "encoding": "base64", "size": len(catalogRaw), "content": base64.StdEncoding.EncodeToString(catalogRaw)})
		default:
			t.Fatal("unexpected API path", path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: http.Header{}}, nil
	})}
	g := &GitHub{client, client}
	resolved, e := g.Resolve(context.Background(), root, "v1.7.0", "linux", "amd64", 0, now)
	if e != nil {
		t.Fatal(e)
	}
	if resolved.Artifact.SHA256 != a.SHA256 {
		t.Fatal("wrong artifact")
	}
	release.Prerelease = true
	if _, e = g.Resolve(context.Background(), root, "v1.7.0", "linux", "amd64", 0, now); e == nil {
		t.Fatal("prerelease accepted")
	}
	release.Prerelease = false
	release.Immutable = false
	if _, e = g.Resolve(context.Background(), root, "v1.7.0", "linux", "amd64", 0, now); e == nil {
		t.Fatal("mutable release accepted")
	}
	var generic map[string]json.RawMessage
	if management.Decode([]byte(`{"id":1,"id":2}`), &generic, 100) == nil {
		t.Fatal("duplicate GitHub fields")
	}
}

func TestCatalogRefreshPreservesExpiredPolicyWithoutInstallingFromIt(t *testing.T) {
	root, key := publisher(t)
	now := time.Now().UTC()
	old := Catalog{Schema: 1, RepositoryID: root.RepositoryID, RootEpoch: 1, Version: 5, Issued: now.Add(-48 * time.Hour), Expires: now.Add(-24 * time.Hour), Minimum: "v1.7.0", RevokedArtifacts: []string{strings.Repeat("b", 64)}, RevokedKeys: []string{"revoked-key"}}
	oldEnvelope := envelope(t, CatalogDomain, old, key)
	if _, e := VerifyCatalog(oldEnvelope, root, 5, now); e == nil {
		t.Fatal("expired policy authorized installation")
	}
	refreshed, e := RefreshCatalog(root, oldEnvelope, management.SigningKey{ID: "release", Private: base64.RawURLEncoding.EncodeToString(key)}, now)
	if e != nil {
		t.Fatal(e)
	}
	current, e := VerifyCatalog(refreshed, root, 6, now)
	if e != nil {
		t.Fatal(e)
	}
	if current.Version != 6 || current.Minimum != old.Minimum || len(current.RevokedArtifacts) != 1 || current.RevokedArtifacts[0] != old.RevokedArtifacts[0] || len(current.RevokedKeys) != 1 {
		t.Fatal("refresh dropped security restrictions")
	}
}
