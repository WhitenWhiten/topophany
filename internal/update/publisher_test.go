//go:build linux || darwin

package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/management"
)

func TestPublisherDraftRequiresAllArchivesAndMatchingSignature(t *testing.T) {
	root, key := publisher(t)
	dir := t.TempDir()
	release := Release{ID: 55, Tag: "v1.7.1", Draft: true}
	for _, osName := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, _ := ArtifactName(release.Tag, osName, arch)
			prefix := strings.TrimSuffix(strings.TrimSuffix(name, ".tar.gz"), ".zip") + "/"
			binary := "topo"
			if osName == "windows" {
				binary += ".exe"
			}
			members := []string{binary, "LICENSE", "README.md", "README.zh.md", "README.ja.md", "CHANGELOG.md"}
			var data bytes.Buffer
			if osName == "windows" {
				z := zip.NewWriter(&data)
				for _, member := range members {
					h := &zip.FileHeader{Name: prefix + member, Method: zip.Deflate}
					h.SetMode(0644)
					w, err := z.CreateHeader(h)
					if err != nil {
						t.Fatal(err)
					}
					w.Write([]byte("inert " + osName + arch + member))
				}
				if err := z.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				gz := gzip.NewWriter(&data)
				tr := tar.NewWriter(gz)
				for _, member := range members {
					body := []byte("inert " + osName + arch + member)
					if err := tr.WriteHeader(&tar.Header{Name: prefix + member, Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
						t.Fatal(err)
					}
					tr.Write(body)
				}
				tr.Close()
				gz.Close()
			}
			if err := os.WriteFile(filepath.Join(dir, name), data.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			release.Assets = append(release.Assets, GitHubAsset{ID: int64(len(release.Assets) + 1), Name: name, Size: int64(data.Len()), Digest: "sha256:" + management.Digest(data.Bytes()), State: "uploaded"})
		}
	}
	commit := strings.Repeat("a", 40)
	manifest, err := BuildManifest(root, release, commit, dir, []string{"v1.7.0"}, []string{"v1.7.0", "v1.7.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Artifacts) != 6 {
		t.Fatal("platform archive omitted")
	}
	env := envelope(t, ReleaseDomain, manifest, key)
	raw := management.Marshal(env)
	if err := os.WriteFile(filepath.Join(dir, "topophany-update.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	release.Assets = append(release.Assets, GitHubAsset{ID: 99, Name: "topophany-update.json", Size: int64(len(raw)), Digest: "sha256:" + management.Digest(raw), State: "uploaded"})
	if _, err = VerifyDraft(root, release, commit, dir); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unsigned", "asset-changed", "missing-platform", "foreign-member", "prerelease", "different-commit"} {
		t.Run(kind, func(t *testing.T) {
			r := release
			r.Assets = append([]GitHubAsset{}, release.Assets...)
			sourceCommit := commit
			defer os.WriteFile(filepath.Join(dir, "topophany-update.json"), raw, 0600)
			switch kind {
			case "unsigned":
				unsigned := env
				unsigned.Signatures = nil
				os.WriteFile(filepath.Join(dir, "topophany-update.json"), management.Marshal(unsigned), 0600)
			case "asset-changed":
				r.Assets[0].ID++
			case "missing-platform":
				r.Assets = r.Assets[1:]
			case "foreign-member":
				r.Assets = append(r.Assets, GitHubAsset{ID: 100, Name: "install.sh", State: "uploaded"})
			case "prerelease":
				r.Prerelease = true
			case "different-commit":
				sourceCommit = strings.Repeat("b", 40)
			}
			if _, err := VerifyDraft(root, r, sourceCommit, dir); err == nil {
				t.Fatal("unverified draft admitted")
			}
		})
	}
}
