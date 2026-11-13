// Package update verifies official release identity independently of deployment
// authority. No controller-selected repository, URL, or executable is trusted.
package update

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
)

const Repository = "WhitenWhiten/topophany"
const ReleaseDomain = "topophany.release.v1"
const CatalogDomain = "topophany.catalog.v1"
const RootDomain = "topophany.publisher-root.v1"

var stable = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
var commit = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func Version(v string) ([3]int64, error) {
	var out [3]int64
	m := stable.FindStringSubmatch(v)
	if m == nil {
		return out, errors.New("release_version_must_be_exact_stable_tag")
	}
	for i := range out {
		out[i], _ = strconv.ParseInt(m[i+1], 10, 64)
	}
	return out, nil
}
func Compare(a, b string) (int, error) {
	x, e := Version(a)
	if e != nil {
		return 0, e
	}
	y, e := Version(b)
	if e != nil {
		return 0, e
	}
	for i := range x {
		if x[i] < y[i] {
			return -1, nil
		}
		if x[i] > y[i] {
			return 1, nil
		}
	}
	return 0, nil
}

type Root struct {
	Schema       int              `json:"schema"`
	Repository   string           `json:"repository"`
	RepositoryID int64            `json:"repository_id"`
	OwnerID      int64            `json:"owner_id"`
	Trust        management.Trust `json:"trust"`
}

func (r Root) Validate(now time.Time) error {
	if r.Schema != 1 || r.Repository != Repository || r.RepositoryID <= 0 || r.OwnerID <= 0 {
		return errors.New("invalid_publisher_identity")
	}
	return r.Trust.Validate(now)
}

type Artifact struct {
	ID           int64  `json:"asset_id"`
	Name         string `json:"name"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	BinarySize   int64  `json:"binary_size"`
	BinarySHA256 string `json:"binary_sha256"`
}
type Manifest struct {
	CompatibleWorkers []string   `json:"compatible_workers"`
	Schema            int        `json:"schema"`
	RepositoryID      int64      `json:"repository_id"`
	ReleaseID         int64      `json:"release_id"`
	Version           string     `json:"version"`
	Commit            string     `json:"commit"`
	Artifacts         []Artifact `json:"artifacts"`
	ControlProtocol   int        `json:"control_protocol"`
	RollbackFrom      []string   `json:"rollback_from"`
}

func ArtifactName(version, osName, arch string) (string, error) {
	if _, e := Version(version); e != nil {
		return "", e
	}
	if !slices.Contains([]string{"linux", "darwin", "windows"}, osName) || !slices.Contains([]string{"amd64", "arm64"}, arch) {
		return "", errors.New("unsupported_release_platform")
	}
	suffix := ".tar.gz"
	if osName == "windows" {
		suffix = ".zip"
	}
	return "topophany_" + strings.TrimPrefix(version, "v") + "_" + osName + "_" + arch + suffix, nil
}
func VerifyManifest(env management.Envelope, root Root, now time.Time) (Manifest, error) {
	var m Manifest
	if e := root.Validate(now); e != nil {
		return m, e
	}
	b, e := management.Verify(ReleaseDomain, "release", env, root.Trust, now, 128<<10)
	if e != nil {
		return m, e
	}
	if e = management.Decode(b, &m, 128<<10); e != nil {
		return m, e
	}
	if _, e = Version(m.Version); e != nil {
		return m, e
	}
	if m.Schema != 1 || m.RepositoryID != root.RepositoryID || m.ReleaseID <= 0 || !commit.MatchString(m.Commit) || m.ControlProtocol != 1 || len(m.Artifacts) < 1 || len(m.Artifacts) > 6 || len(m.RollbackFrom) > 64 {
		return m, errors.New("invalid_release_manifest")
	}
	names := map[string]bool{}
	ids := map[int64]bool{}
	for _, a := range m.Artifacts {
		name, e := ArtifactName(m.Version, a.OS, a.Arch)
		if e != nil || a.Name != name || names[name] || ids[a.ID] || a.ID <= 0 || a.Size < 1 || a.Size > 128<<20 || a.BinarySize < 1 || a.BinarySize > 128<<20 || !management.HashOK(a.SHA256) || !management.HashOK(a.BinarySHA256) {
			return m, errors.New("invalid_release_artifact")
		}
		names[name] = true
		ids[a.ID] = true
	}
	if len(m.CompatibleWorkers) < 1 || len(m.CompatibleWorkers) > 64 || !slices.Contains(m.CompatibleWorkers, m.Version) {
		return m, errors.New("worker_compatibility_declaration_required")
	}
	for _, v := range m.CompatibleWorkers {
		if _, e = Version(v); e != nil {
			return m, e
		}
	}
	for _, v := range m.RollbackFrom {
		if _, e = Version(v); e != nil {
			return m, e
		}
	}
	return m, nil
}

type Catalog struct {
	Schema           int       `json:"schema"`
	RepositoryID     int64     `json:"repository_id"`
	RootEpoch        uint64    `json:"root_epoch"`
	Version          uint64    `json:"version"`
	Issued           time.Time `json:"issued_at"`
	Expires          time.Time `json:"expires_at"`
	Minimum          string    `json:"minimum_version"`
	RevokedArtifacts []string  `json:"revoked_artifacts"`
	RevokedKeys      []string  `json:"revoked_keys"`
}

func VerifyCatalog(env management.Envelope, root Root, floor uint64, now time.Time) (Catalog, error) {
	var c Catalog
	if e := root.Validate(now); e != nil {
		return c, e
	}
	b, e := management.Verify(CatalogDomain, "catalog", env, root.Trust, now, 256<<10)
	if e != nil {
		return c, e
	}
	if e = management.Decode(b, &c, 256<<10); e != nil {
		return c, e
	}
	if c.Schema != 1 || c.RepositoryID != root.RepositoryID || c.RootEpoch != root.Trust.Epoch || c.Version == 0 || c.Version < floor || c.Version > 1<<53-1 || c.Issued.IsZero() || c.Issued.After(now.Add(30*time.Second)) || !c.Expires.After(now) || c.Expires.Sub(c.Issued) > 24*time.Hour || len(c.RevokedArtifacts) > 2048 || len(c.RevokedKeys) > 256 {
		return c, errors.New("invalid_or_expired_security_catalog")
	}
	if _, e = Version(c.Minimum); e != nil {
		return c, e
	}
	for _, h := range c.RevokedArtifacts {
		if !management.HashOK(h) {
			return c, errors.New("invalid_revocation_digest")
		}
	}
	return c, nil
}
func (c Catalog) Permits(m Manifest, a Artifact, env management.Envelope) error {
	n, e := Compare(m.Version, c.Minimum)
	if e != nil || n < 0 {
		return errors.New("release_below_security_floor")
	}
	if slices.Contains(c.RevokedArtifacts, a.SHA256) || slices.Contains(c.RevokedArtifacts, a.BinarySHA256) {
		return errors.New("release_revoked")
	}
	for _, s := range env.Signatures {
		if slices.Contains(c.RevokedKeys, s.KeyID) {
			return errors.New("release_signing_key_revoked")
		}
	}
	return nil
}

// VerifyRootTransition requires both old and new root thresholds. Expired roots
// authenticate only their immediate successor, never ordinary release metadata.
func VerifyRootTransition(env management.Envelope, current Root, now time.Time) (Root, error) {
	verifyTime := now
	if !current.Trust.Expires.After(now) {
		verifyTime = current.Trust.Expires.Add(-time.Nanosecond)
	}
	b, e := management.Verify(RootDomain, "root", env, current.Trust, verifyTime, 64<<10)
	if e != nil {
		return Root{}, e
	}
	var next Root
	if e = management.Decode(b, &next, 64<<10); e != nil {
		return next, e
	}
	if next.Repository != current.Repository || next.RepositoryID != current.RepositoryID || next.OwnerID != current.OwnerID || next.Trust.ID != current.Trust.ID || next.Trust.Epoch != current.Trust.Epoch+1 {
		return next, errors.New("invalid_root_transition")
	}
	verifyTime = now
	if !next.Trust.Expires.After(now) {
		verifyTime = next.Trust.Expires.Add(-time.Nanosecond)
	}
	if e = next.Validate(verifyTime); e != nil {
		return next, e
	}
	_, e = management.Verify(RootDomain, "root", env, next.Trust, verifyTime, 64<<10)
	return next, e
}
