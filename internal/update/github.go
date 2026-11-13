package update

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
)

const apiOrigin = "https://api.github.com"

var deliveryHosts = map[string]bool{"github.com": true, "release-assets.githubusercontent.com": true, "objects.githubusercontent.com": true}

type GitHub struct {
	metadata *http.Client
	download *http.Client
}

func NewGitHub() *GitHub {
	tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableCompression: true, MaxResponseHeaderBytes: 32768, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DialContext: publicDial, MaxIdleConnsPerHost: 2}
	return &GitHub{metadata: &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, download: &http.Client{Transport: tr, Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Port() != "" && !strings.EqualFold(req.URL.Port(), "443") || !deliveryHosts[req.URL.Hostname()] {
			return errors.New("release_redirect_denied")
		}
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		return nil
	}}}
}
func (g *GitHub) Close() { g.metadata.CloseIdleConnections(); g.download.CloseIdleConnections() }
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if e != nil {
		return nil, errors.New("release_dns_failed")
	}
	for _, ip := range ips {
		if !publicAddress(ip) {
			return nil, errors.New("release_private_address_denied")
		}
	}
	for _, ip := range ips {
		conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
	}
	return nil, errors.New("release_connection_failed")
}
func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	if ip.Is6() {
		return netip.MustParsePrefix("2000::/3").Contains(ip) && !netip.MustParsePrefix("2001:db8::/32").Contains(ip)
	}
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}
func request(ctx context.Context, h *http.Client, path, accept string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", path, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "Topophany-release-verifier")
	r, e := h.Do(req)
	if e != nil {
		return nil, errors.New("github_transport_failed")
	}
	if r.StatusCode != 200 {
		r.Body.Close()
		return nil, fmt.Errorf("github_request_refused_%d", r.StatusCode)
	}
	return r, nil
}
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("release_metadata_too_large")
	}
	return b, nil
}

// GitHub responses may add fields. Duplicate keys and body budgets are still
// enforced, while signed project metadata is always decoded with a closed schema.
func (g *GitHub) get(ctx context.Context, path string, v any) error {
	r, e := request(ctx, g.metadata, apiOrigin+"/repos/"+Repository+path, "application/vnd.github+json")
	if e != nil {
		return e
	}
	defer r.Body.Close()
	b, e := readBounded(r.Body, 1<<20)
	if e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e = management.Decode(b, &fields, 1<<20); e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}

type GitHubAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
	State  string `json:"state"`
	URL    string `json:"browser_download_url"`
}
type Release struct {
	ID         int64         `json:"id"`
	Tag        string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Immutable  bool          `json:"immutable"`
	Published  time.Time     `json:"published_at"`
	Assets     []GitHubAsset `json:"assets"`
}
type Resolution struct {
	Release    Release
	Manifest   Manifest
	Envelope   management.Envelope
	Catalog    Catalog
	Artifact   Artifact
	ResolvedAt time.Time
}

func (g *GitHub) Resolve(ctx context.Context, root Root, version, osName, arch string, catalogFloor uint64, now time.Time) (Resolution, error) {
	var result Resolution
	if e := root.Validate(now); e != nil {
		return result, e
	}
	if _, e := Version(version); e != nil {
		return result, e
	}
	var repo struct {
		ID    int64  `json:"id"`
		Name  string `json:"full_name"`
		Owner struct {
			ID int64 `json:"id"`
		} `json:"owner"`
	}
	if e := g.get(ctx, "", &repo); e != nil {
		return result, e
	}
	if repo.ID != root.RepositoryID || repo.Owner.ID != root.OwnerID || !strings.EqualFold(repo.Name, Repository) {
		return result, errors.New("official_repository_identity_mismatch")
	}
	var release Release
	if e := g.get(ctx, "/releases/tags/"+url.PathEscape(version), &release); e != nil {
		return result, e
	}
	if release.ID <= 0 || release.Tag != version || release.Draft || release.Prerelease || release.Published.IsZero() || release.Published.After(now.Add(30*time.Second)) {
		return result, errors.New("release_not_stable")
	}
	if !release.Immutable {
		return result, errors.New("release_not_immutable")
	}
	if len(release.Assets) > 32 {
		return result, errors.New("release_asset_budget")
	}
	object := struct {
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}{}
	if e := g.get(ctx, "/git/ref/tags/"+url.PathEscape(version), &object); e != nil {
		return result, e
	}
	kind, sha := object.Object.Type, object.Object.SHA
	for depth := 0; kind == "tag" && depth < 16; depth++ {
		if !commit.MatchString(sha) {
			return result, errors.New("invalid_tag_object")
		}
		var tag struct {
			Object struct {
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"object"`
		}
		if e := g.get(ctx, "/git/tags/"+sha, &tag); e != nil {
			return result, e
		}
		kind, sha = tag.Object.Type, tag.Object.SHA
	}
	if kind != "commit" || !commit.MatchString(sha) {
		return result, errors.New("release_commit_unresolved")
	}
	var manifestAsset *GitHubAsset
	for i, a := range release.Assets {
		if a.Name == "topophany-update.json" {
			if manifestAsset != nil {
				return result, errors.New("duplicate_update_manifest")
			}
			manifestAsset = &release.Assets[i]
		}
	}
	if manifestAsset == nil || manifestAsset.Size > 256<<10 {
		return result, errors.New("release_signature_missing")
	}
	raw, e := g.assetBytes(ctx, *manifestAsset, 256<<10)
	if e != nil {
		return result, e
	}
	var env management.Envelope
	if e = management.Decode(raw, &env, 256<<10); e != nil {
		return result, e
	}
	m, e := VerifyManifest(env, root, now)
	if e != nil {
		return result, e
	}
	if m.ReleaseID != release.ID || m.Version != version || m.Commit != sha {
		return result, errors.New("release_manifest_identity_mismatch")
	}
	catalog, e := g.Catalog(ctx, root, catalogFloor, now)
	if e != nil {
		return result, e
	}
	name, e := ArtifactName(version, osName, arch)
	if e != nil {
		return result, e
	}
	var selected Artifact
	for _, a := range m.Artifacts {
		if a.Name == name {
			selected = a
		}
	}
	if selected.ID == 0 {
		return result, errors.New("release_platform_unavailable")
	}
	matches := 0
	for _, a := range release.Assets {
		if a.Name == name {
			matches++
			if a.ID != selected.ID || a.Size != selected.Size || a.State != "uploaded" || a.Digest != "sha256:"+selected.SHA256 {
				return result, errors.New("release_asset_identity_mismatch")
			}
		}
	}
	if matches != 1 {
		return result, errors.New("release_asset_missing_or_duplicate")
	}
	if e = catalog.Permits(m, selected, env); e != nil {
		return result, e
	}
	return Resolution{release, m, env, catalog, selected, now.UTC()}, nil
}
func (g *GitHub) Catalog(ctx context.Context, root Root, floor uint64, now time.Time) (Catalog, error) {
	raw, e := g.repositoryFile(ctx, "update-metadata/security.json")
	if e != nil {
		return Catalog{}, e
	}
	var env management.Envelope
	if e = management.Decode(raw, &env, 512<<10); e != nil {
		return Catalog{}, e
	}
	return VerifyCatalog(env, root, floor, now)
}
func (g *GitHub) repositoryFile(ctx context.Context, path string) ([]byte, error) {
	var content struct {
		Type     string `json:"type"`
		Path     string `json:"path"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		Size     int64  `json:"size"`
	}
	if e := g.get(ctx, "/contents/"+path+"?ref=main", &content); e != nil {
		return nil, e
	}
	if content.Type != "file" || content.Path != path || content.Encoding != "base64" || content.Size < 1 || content.Size > 512<<10 {
		return nil, errors.New("invalid_publisher_metadata_file")
	}
	b, e := base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
	if e != nil || int64(len(b)) != content.Size {
		return nil, errors.New("invalid_publisher_metadata_content")
	}
	return b, nil
}
func (g *GitHub) assetBytes(ctx context.Context, a GitHubAsset, limit int64) ([]byte, error) {
	if a.ID <= 0 || a.Size < 1 || a.Size > limit || !strings.HasPrefix(a.Digest, "sha256:") || !management.HashOK(strings.TrimPrefix(a.Digest, "sha256:")) {
		return nil, errors.New("invalid_release_asset")
	}
	r, e := request(ctx, g.download, apiOrigin+"/repos/"+Repository+"/releases/assets/"+strconv.FormatInt(a.ID, 10), "application/octet-stream")
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	b, e := readBounded(r.Body, a.Size)
	if e != nil || int64(len(b)) != a.Size || "sha256:"+management.Digest(b) != a.Digest {
		return nil, errors.New("release_asset_digest_mismatch")
	}
	return b, nil
}

// Download writes only to a caller-created protected staging file. The API URL
// is constructed from the already verified asset ID, never a command URL.
func (g *GitHub) Download(ctx context.Context, a Artifact, destination *os.File) error {
	if a.ID <= 0 || a.Size < 1 || a.Size > 128<<20 || !management.HashOK(a.SHA256) {
		return errors.New("invalid_release_artifact")
	}
	r, e := request(ctx, g.download, apiOrigin+"/repos/"+Repository+"/releases/assets/"+strconv.FormatInt(a.ID, 10), "application/octet-stream")
	if e != nil {
		return e
	}
	defer r.Body.Close()
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(r.Body, a.Size+1))
	if e != nil || n != a.Size || hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return errors.New("release_archive_digest_mismatch")
	}
	return destination.Sync()
}
