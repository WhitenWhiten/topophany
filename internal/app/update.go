package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/update"
)

func configureUpdateController(c *management.Controller) {
	if c.Policy.PublisherTrust == "" {
		return
	}
	c.VerifyUpdate = func(ctx context.Context, raw []byte, status management.NodeStatus, inventory []management.NodeRecord) error {
		if status.OS != "linux" {
			return errors.New("managed_updates_require_linux_systemd")
		}
		root, e := update.LoadRoot(c.Policy.PublisherTrust, false)
		if e != nil {
			return e
		}
		var target update.Target
		if e = management.Decode(raw, &target, 32<<10); e != nil {
			return e
		}
		if e = target.Validate(); e != nil {
			return e
		}
		source := update.NewGitHub()
		defer source.Close()
		resolved, e := source.Resolve(ctx, root, target.Version, status.OS, status.Arch, 0, time.Now())
		if e != nil {
			return e
		}
		if !target.Matches(resolved) {
			return errors.New("pinned_release_changed")
		}
		return checkUpdatePeers(resolved.Manifest, status, inventory, time.Now())
	}
}
func (a *App) releaseManagement(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("use release verify or sign")
	}
	switch args[0] {
	case "rotate-root":
		f := a.flags("management release rotate-root")
		rootPath := f.String("trust-file", "", "previous locally trusted publisher root")
		transition := f.String("transition", "", "root transition signed by both old and new root thresholds")
		output := f.String("output", "", "new verified public root file")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		raw, e := management.ReadLocal(*rootPath, 64<<10, false, false)
		if e != nil {
			return e
		}
		var previous update.Root
		if e = management.Decode(raw, &previous, 64<<10); e != nil {
			return e
		}
		raw, e = management.ReadLocal(*transition, 128<<10, false, false)
		if e != nil {
			return e
		}
		var env management.Envelope
		if e = management.Decode(raw, &env, 128<<10); e != nil {
			return e
		}
		next, e := update.VerifyRootTransition(env, previous, time.Now())
		if e != nil {
			return e
		}
		if _, e = os.Lstat(*output); !os.IsNotExist(e) {
			return errors.New("root output already exists")
		}
		if e = management.WriteLocal(*output, management.Marshal(next)); e != nil {
			return e
		}
		return printJSON(a.Out, map[string]any{"output": *output, "epoch": next.Trust.Epoch, "current": next.Trust.Expires.After(time.Now())})
	case "catalog", "refresh-catalog":
		f := a.flags("management release " + args[0])
		rootPath := f.String("trust-file", "", "publisher trust root")
		version := f.Uint64("catalog-version", 0, "monotonically increasing catalog version")
		minimum := f.String("minimum-version", "", "minimum permitted stable tag")
		revoked := f.String("revoke-artifacts", "", "comma-separated SHA-256 digests")
		keys := f.String("revoke-keys", "", "comma-separated key IDs")
		previous := f.String("previous", "", "previous signed catalog for refresh")
		signer := f.String("key-file", "", "private catalog signing key for refresh")
		output := f.String("output", "", "new metadata output file")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		root, e := update.LoadRoot(*rootPath, false)
		if e != nil {
			return e
		}
		var result any
		if args[0] == "catalog" {
			var digests, ids []string
			if *revoked != "" {
				digests = strings.Split(*revoked, ",")
			}
			if *keys != "" {
				ids = strings.Split(*keys, ",")
			}
			result, e = update.NewCatalog(root, *version, *minimum, digests, ids, time.Now())
		} else {
			raw, err := management.ReadLocal(*previous, 512<<10, false, false)
			if err != nil {
				return err
			}
			var env management.Envelope
			if err = management.Decode(raw, &env, 512<<10); err != nil {
				return err
			}
			raw, err = management.ReadLocal(*signer, 16384, false, true)
			if err != nil {
				return err
			}
			var key management.SigningKey
			if err = management.Decode(raw, &key, 16384); err != nil {
				return err
			}
			result, e = update.RefreshCatalog(root, env, key, time.Now())
		}
		if e != nil {
			return e
		}
		if _, e = os.Lstat(*output); !os.IsNotExist(e) {
			return errors.New("catalog output already exists")
		}
		if e = management.WriteLocal(*output, management.Marshal(result)); e != nil {
			return e
		}
		return printJSON(a.Out, map[string]string{"output": *output})
	case "keygen":
		return a.publisherKeys(args[1:])
	case "manifest", "verify-local":
		f := a.flags("management release " + args[0])
		trustPath := f.String("trust-file", "", "local publisher root")
		releasePath := f.String("release-json", "", "saved GitHub draft release metadata")
		dir := f.String("artifacts-dir", "", "absolute archive directory")
		commit := f.String("commit", "", "verified exact tag commit")
		rollback := f.String("rollback-from", "", "comma-separated tested prior versions; omitted means no unattended recovery")
		out := f.String("output", "", "new manifest or publication receipt path")
		compatible := f.String("compatible-workers", "", "comma-separated worker versions tested with the new collector")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		root, e := update.LoadRoot(*trustPath, false)
		if e != nil {
			return e
		}
		raw, e := management.ReadLocal(*releasePath, 1<<20, false, false)
		if e != nil {
			return e
		}
		release, e := update.DecodeRelease(raw)
		if e != nil {
			return e
		}
		var result any
		if args[0] == "manifest" {
			var previous []string
			if *rollback != "" {
				previous = strings.Split(*rollback, ",")
			}
			var workers []string
			if *compatible != "" {
				workers = strings.Split(*compatible, ",")
			}
			result, e = update.BuildManifest(root, release, *commit, *dir, previous, workers)
		} else {
			result, e = update.VerifyDraft(root, release, *commit, *dir)
		}
		if e != nil {
			return e
		}
		if _, e = os.Lstat(*out); !os.IsNotExist(e) {
			return errors.New("output already exists")
		}
		if e = management.WriteLocal(*out, management.Marshal(result)); e != nil {
			return e
		}
		return printJSON(a.Out, result)

	case "verify":
		f := a.flags("management release verify")
		path := f.String("trust-file", "", "locally installed publisher root")
		version := f.String("version", "", "exact stable release tag")
		osName := f.String("os", runtime.GOOS, "target OS")
		arch := f.String("arch", runtime.GOARCH, "target architecture")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		root, e := update.LoadRoot(*path, false)
		if e != nil {
			return e
		}
		source := update.NewGitHub()
		defer source.Close()
		r, e := source.Resolve(ctx, root, *version, *osName, *arch, 0, time.Now())
		if e != nil {
			return e
		}
		return printJSON(a.Out, map[string]any{"target": update.TargetFor(r), "manifest": r.Manifest, "catalog_version": r.Catalog.Version, "resolved_at": r.ResolvedAt})
	case "sign":
		f := a.flags("management release sign")
		path := f.String("input", "", "reviewed metadata JSON")
		keyPaths := f.String("key-files", "", "comma-separated local signing key files")
		kind := f.String("kind", "", "manifest, catalog or root")
		out := f.String("output", "", "new signed envelope path")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		domain := ""
		switch *kind {
		case "manifest":
			domain = update.ReleaseDomain
		case "catalog":
			domain = update.CatalogDomain
		case "root":
			domain = update.RootDomain
		default:
			return errors.New("choose metadata kind explicitly")
		}
		raw, e := management.ReadLocal(*path, 256<<10, false, true)
		if e != nil {
			return e
		}
		var object map[string]any
		if e = management.Decode(raw, &object, 256<<10); e != nil {
			return e
		}
		keys := strings.Split(*keyPaths, ",")
		if len(keys) < 1 || len(keys) > 4 {
			return errors.New("choose one to four signing keys")
		}
		var combined management.Envelope
		seen := map[string]bool{}
		for _, path := range keys {
			b, e := management.ReadLocal(path, 16384, false, true)
			if e != nil {
				return e
			}
			var key management.SigningKey
			if e = management.Decode(b, &key, 16384); e != nil {
				return e
			}
			private, e := base64.RawURLEncoding.DecodeString(key.Private)
			if e != nil || len(private) != ed25519.PrivateKeySize || seen[key.ID] {
				return errors.New("invalid_or_repeated_signing_key")
			}
			seen[key.ID] = true
			env, e := management.Sign(domain, raw, key.ID, private)
			if e != nil {
				return e
			}
			combined.Payload = env.Payload
			combined.Signatures = append(combined.Signatures, env.Signatures...)
		}
		if _, e = os.Lstat(*out); !os.IsNotExist(e) {
			return errors.New("signature output already exists")
		}
		if e = management.WriteLocal(*out, management.Marshal(combined)); e != nil {
			return e
		}
		return printJSON(a.Out, map[string]any{"output": *out, "payload_sha256": management.Digest(raw), "signatures": len(combined.Signatures)})
	default:
		return errors.New("unknown release operation")
	}
}

func (a *App) publisherKeys(args []string) error {
	f := a.flags("management release keygen")
	dir := f.String("dir", "", "new private offline publisher key directory")
	if e := parsed(f, args); e != nil {
		return e
	}
	if !filepath.IsAbs(*dir) {
		return errors.New("absolute key directory required")
	}
	if _, e := os.Lstat(*dir); !os.IsNotExist(e) {
		return errors.New("publisher key directory already exists")
	}
	if e := management.MakePrivateDirectory(*dir, false); e != nil {
		return e
	}
	trust := management.Trust{Schema: 1, ID: "topophany-publisher", Epoch: 1, Threshold: 2, RoleThresholds: map[string]int{"root": 2, "release": 1, "catalog": 1}, Expires: time.Now().UTC().AddDate(2, 0, 0)}
	for i, role := range []string{"root", "root", "root", "release", "catalog"} {
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		nonce, e := secure.Token()
		if e != nil {
			return e
		}
		id := role + "-" + nonce[:24]
		trust.Keys = append(trust.Keys, management.Key{ID: id, Public: base64.RawURLEncoding.EncodeToString(pub), Roles: []string{role}})
		path := filepath.Join(*dir, fmt.Sprintf("%s-%d.key.json", role, i+1))
		if e = management.WriteLocal(path, management.Marshal(management.SigningKey{ID: id, Private: base64.RawURLEncoding.EncodeToString(key)})); e != nil {
			return e
		}
	}
	root := update.Root{Schema: 1, Repository: update.Repository, RepositoryID: update.OfficialRepositoryID, OwnerID: update.OfficialOwnerID, Trust: trust}
	path := filepath.Join(*dir, "publisher-root.json")
	if e := management.WriteLocal(path, management.Marshal(root)); e != nil {
		return e
	}
	return printJSON(a.Out, map[string]string{"publisher_root": path, "root_sha256": management.Digest(management.Marshal(root)), "custody": "keep the three root keys offline and separate; install only the public root on nodes"})
}

func checkUpdatePeers(manifest update.Manifest, target management.NodeStatus, inventory []management.NodeRecord, now time.Time) error {
	var registration management.Registration
	collectors := map[string]management.NodeRecord{}
	for _, node := range inventory {
		if node.Registration.ID == target.Installation {
			registration = node.Registration
		}
		if node.Registration.Role == "collector" && node.Status != nil {
			collectors[node.Status.CollectorID] = node
		}
	}
	if registration.ID == "" {
		return errors.New("update_inventory_incomplete")
	}
	if registration.Role == "worker" {
		if len(target.Collectors) == 0 {
			return errors.New("worker_receiver_inventory_missing")
		}
		for _, id := range target.Collectors {
			peer, ok := collectors[id]
			if !ok || peer.Status == nil || now.Sub(peer.Received) > time.Minute || update.NormalizeInstalledVersion(peer.Status.Version) != manifest.Version {
				return errors.New("receiving_collectors_must_be_updated_first")
			}
		}
	} else {
		if target.CollectorID == "" {
			return errors.New("collector_identity_missing")
		}
		for _, node := range inventory {
			if node.Registration.Role != "worker" {
				continue
			}
			if node.Status == nil || now.Sub(node.Received) > time.Minute || len(node.Status.Collectors) == 0 {
				return errors.New("worker_compatibility_inventory_stale")
			}
			if slices.Contains(node.Status.Collectors, target.CollectorID) && !slices.Contains(manifest.CompatibleWorkers, update.NormalizeInstalledVersion(node.Status.Version)) {
				return errors.New("collector_cannot_accept_existing_worker")
			}
		}
	}
	return nil
}
