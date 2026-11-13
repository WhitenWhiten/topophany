package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
)

const OfficialRepositoryID int64 = 1417739003
const OfficialOwnerID int64 = 31219380

func DecodeRelease(raw []byte) (Release, error) {
	var fields map[string]json.RawMessage
	if e := management.Decode(raw, &fields, 1<<20); e != nil {
		return Release{}, e
	}
	var r Release
	e := json.Unmarshal(raw, &r)
	return r, e
}
func BuildManifest(root Root, release Release, sourceCommit, dir string, rollback []string, compatibleWorkers ...[]string) (Manifest, error) {
	if e := root.Validate(time.Now()); e != nil {
		return Manifest{}, e
	}
	if _, e := Version(release.Tag); e != nil {
		return Manifest{}, e
	}
	if release.ID <= 0 || !release.Draft || release.Prerelease || !commit.MatchString(sourceCommit) || !filepath.IsAbs(dir) {
		return Manifest{}, errors.New("managed_manifest_requires_stable_draft_and_exact_commit")
	}
	if e := management.ValidateDirectory(dir, false); e != nil {
		return Manifest{}, e
	}
	m := Manifest{CompatibleWorkers: []string{release.Tag}, Schema: 1, RepositoryID: root.RepositoryID, ReleaseID: release.ID, Version: release.Tag, Commit: sourceCommit, ControlProtocol: 1, RollbackFrom: rollback}
	if len(compatibleWorkers) > 0 && len(compatibleWorkers[0]) > 0 {
		m.CompatibleWorkers = compatibleWorkers[0]
	}
	for _, v := range m.CompatibleWorkers {
		if _, e := Version(v); e != nil {
			return m, e
		}
	}
	for _, v := range rollback {
		if _, e := Version(v); e != nil {
			return m, e
		}
	}
	scratch, e := os.MkdirTemp("", "topophany-release-inspection-")
	if e != nil {
		return m, e
	}
	defer os.RemoveAll(scratch)
	for _, osName := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, _ := ArtifactName(release.Tag, osName, arch)
			var asset GitHubAsset
			count := 0
			for _, a := range release.Assets {
				if a.Name == name {
					asset = a
					count++
				}
			}
			if count != 1 || asset.ID <= 0 || asset.State != "uploaded" || asset.Size < 1 || asset.Size > 128<<20 {
				return m, errors.New("draft_archive_missing_or_invalid")
			}
			path := filepath.Join(dir, name)
			if e = management.ValidateLocalFile(path, false, false); e != nil {
				return m, e
			}
			archive, e := os.Open(path)
			if e != nil {
				return m, e
			}
			a := Artifact{ID: asset.ID, Name: name, OS: osName, Arch: arch, Size: asset.Size}
			if len(asset.Digest) != 71 || asset.Digest[:7] != "sha256:" {
				archive.Close()
				return m, errors.New("github_asset_digest_required")
			}
			a.SHA256 = asset.Digest[7:]
			binary, e := os.CreateTemp(scratch, "candidate-")
			if e != nil {
				archive.Close()
				return m, e
			}
			e = extractArchive(archive, &a, binary, true)
			archive.Close()
			binary.Close()
			if e != nil {
				return m, e
			}
			if a.BinarySize < 1 {
				return m, errors.New("empty_release_binary")
			}
			m.Artifacts = append(m.Artifacts, a)
		}
	}
	return m, nil
}

type PublicationReceipt struct {
	Schema         int           `json:"schema"`
	Version        string        `json:"version"`
	ReleaseID      int64         `json:"release_id"`
	Commit         string        `json:"commit"`
	Assets         []GitHubAsset `json:"assets"`
	ManifestSHA256 string        `json:"manifest_sha256"`
}

func VerifyDraft(root Root, release Release, sourceCommit, dir string) (PublicationReceipt, error) {
	receipt := PublicationReceipt{}
	raw, e := management.ReadLocal(filepath.Join(dir, "topophany-update.json"), 256<<10, false, false)
	if e != nil {
		return receipt, e
	}
	var env management.Envelope
	if e = management.Decode(raw, &env, 256<<10); e != nil {
		return receipt, e
	}
	m, e := VerifyManifest(env, root, time.Now())
	if e != nil {
		return receipt, e
	}
	inspected, e := BuildManifest(root, release, sourceCommit, dir, m.RollbackFrom, m.CompatibleWorkers)
	if e != nil {
		return receipt, e
	}
	if management.Digest(management.Marshal(inspected)) != management.Digest(management.Marshal(m)) {
		return receipt, errors.New("signed_manifest_differs_from_uploaded_archives")
	}
	allowed := map[string]bool{"topophany-update.json": true, "SHA256SUMS.txt": true, "CHANGELOG.md": true, "RELEASE_NOTES.md": true}
	for _, a := range m.Artifacts {
		allowed[a.Name] = true
	}
	seen := map[string]bool{}
	found := false
	for _, a := range release.Assets {
		if !allowed[a.Name] || seen[a.Name] || a.State != "uploaded" {
			return receipt, errors.New("unexpected_release_asset")
		}
		seen[a.Name] = true
		if a.Name == "topophany-update.json" {
			found = true
			if a.Size != int64(len(raw)) || a.Digest != "sha256:"+management.Digest(raw) {
				return receipt, errors.New("uploaded_signature_differs")
			}
		}
	}
	if !found {
		return receipt, errors.New("upload_signed_manifest_before_publication")
	}
	return PublicationReceipt{1, m.Version, m.ReleaseID, m.Commit, release.Assets, management.Digest(raw)}, nil
}
