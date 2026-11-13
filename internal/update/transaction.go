package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/management"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Registration struct {
	InstallType       string `json:"install_type"`
	Schema            int    `json:"schema"`
	Installation      string `json:"installation_id"`
	Project           string `json:"project"`
	Role              string `json:"role"`
	Observer          string `json:"observer,omitempty"`
	PolicyFile        string `json:"management_policy"`
	Binary            string `json:"binary"`
	Service           string `json:"service"`
	User              string `json:"user"`
	Data              string `json:"data_dir"`
	Config            string `json:"config_file"`
	Auth              string `json:"auth_file,omitempty"`
	State             string `json:"updater_state_dir"`
	BootstrapManifest string `json:"bootstrap_manifest"`
}

// Effects is the platform boundary. Production uses only the registered Linux
// service implementation; tests inject isolated effects, never shell commands.
type Effects interface {
	Stop(context.Context) error
	Start(context.Context) error
	Preflight(context.Context, string, string, string) error
	Healthy(context.Context, string, string) error
	Configuration(context.Context) (string, error)
}
type ReleaseSource interface {
	Resolve(context.Context, Root, string, string, string, uint64, time.Time) (Resolution, error)
	Download(context.Context, Artifact, *os.File) error
}
type helperState struct {
	RegistrationDigest string              `json:"registration_sha256"`
	PreparedConfig     string              `json:"prepared_config,omitempty"`
	Schema             int                 `json:"schema"`
	Installation       string              `json:"installation_id"`
	Highest            string              `json:"highest_version"`
	ActiveVersion      string              `json:"active_version"`
	ActiveDigest       string              `json:"active_digest"`
	CatalogVersion     uint64              `json:"catalog_version"`
	CatalogDigest      string              `json:"catalog_digest"`
	RootEpoch          uint64              `json:"root_epoch"`
	Sequence           uint64              `json:"sequence"`
	Generation         uint64              `json:"generation"`
	TimeFloor          time.Time           `json:"time_floor"`
	Phase              string              `json:"phase"`
	Target             Target              `json:"target"`
	Candidate          *Resolution         `json:"candidate,omitempty"`
	OldVersion         string              `json:"old_version,omitempty"`
	OldDigest          string              `json:"old_digest,omitempty"`
	Prepared           *management.Receipt `json:"prepared,omitempty"`
	Last               *management.Receipt `json:"last,omitempty"`
}
type Helper struct {
	storageFailed bool
	mu            sync.Mutex
	registration  Registration
	effects       Effects
	source        ReleaseSource
	state         helperState
	unlock        func() error
	system        bool
}
type HelperRequest struct {
	Envelope management.Envelope `json:"command"`
	Payload  []byte              `json:"payload,omitempty"`
}

func (reg Registration) Validate() error {
	if reg.InstallType != "managed_binary" || reg.Schema != 1 || !config.ID(reg.Installation) || !config.ID(reg.Project) || (reg.Role != "worker" && reg.Role != "collector") || reg.User == "" || reg.User == "root" {
		return errors.New("invalid_helper_registration")
	}
	for _, path := range []string{reg.PolicyFile, reg.Binary, reg.Data, reg.Config, reg.State, reg.BootstrapManifest} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errors.New("invalid_helper_path")
		}
	}
	if (reg.Role == "worker" && !config.ID(reg.Observer)) || (reg.Observer != "" && !config.ID(reg.Observer)) {
		return errors.New("invalid_helper_observer")
	}
	files := []string{reg.PolicyFile, reg.Binary, reg.Config, reg.BootstrapManifest}
	if reg.Auth != "" {
		if !filepath.IsAbs(reg.Auth) || filepath.Clean(reg.Auth) != reg.Auth || reg.Role != "collector" {
			return errors.New("invalid_helper_auth_path")
		}
		files = append(files, reg.Auth)
	}
	// Neither mutable tree may contain enrollment authority or an executable.
	// In particular, helper cleanup of candidate/recovery-copy must never overlap
	// monitoring data, a trust file, or the installed executable.
	if within(reg.State, reg.Data) || within(reg.Data, reg.State) || reg.State == string(filepath.Separator) || reg.Data == string(filepath.Separator) {
		return errors.New("helper_path_collision")
	}
	for i, path := range files {
		if within(reg.State, path) || within(reg.Data, path) || within(path, reg.State) || within(path, reg.Data) {
			return errors.New("helper_path_collision")
		}
		for _, other := range files[:i] {
			if within(path, other) || within(other, path) {
				return errors.New("helper_path_collision")
			}
		}
	}
	return nil
}

func within(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func openHelper(reg Registration, effects Effects, source ReleaseSource, system bool) (*Helper, error) {
	if e := reg.Validate(); e != nil {
		return nil, e
	}
	if e := management.MakePrivateDirectory(reg.State, system); e != nil {
		return nil, e
	}
	unlock, e := secure.Lock(filepath.Join(reg.State, "writer.lock"))
	if e != nil {
		return nil, e
	}
	h := &Helper{registration: reg, effects: effects, source: source, unlock: unlock, system: system, state: helperState{RegistrationDigest: management.Digest(management.Marshal(reg)), Schema: 1, Installation: reg.Installation}}
	fail := func(e error) (*Helper, error) { unlock(); return nil, e }
	b, e := management.ReadLocal(filepath.Join(reg.State, "state.json"), 1<<20, system, true)
	if e == nil {
		if e = management.Decode(b, &h.state, 1<<20); e != nil {
			return fail(e)
		}
		if h.state.RegistrationDigest != management.Digest(management.Marshal(reg)) || h.state.Schema != 1 || h.state.Installation != reg.Installation {
			return fail(errors.New("helper_state_identity_mismatch"))
		}
		if h.state.Last != nil && h.state.Last.State == "accepted" && h.state.Phase != "activating" && h.state.Phase != "checking" && h.state.Phase != "recovering" {
			r := *h.state.Last
			r.State = "failed"
			r.Code = "update_interrupted_before_activation"
			r.At = time.Now().UTC()
			h.state.Last = &r
			h.state.Prepared = nil
			h.state.Phase = "failed"
			if e = h.save(); e != nil {
				return fail(e)
			}
		}
		return h, nil
	}
	if !os.IsNotExist(e) {
		return fail(e)
	}
	p, e := management.LoadPolicy(reg.PolicyFile)
	if e != nil {
		return fail(e)
	}
	if system && p.Updates.Accept {
		return fail(errors.New("initialize_helper_with_updates_disabled"))
	}
	root, e := LoadRoot(p.Updates.PublisherTrustFile, system)
	if e != nil {
		return fail(e)
	}
	b, e = management.ReadLocal(reg.BootstrapManifest, 256<<10, system, false)
	if e != nil {
		return fail(e)
	}
	var env management.Envelope
	if e = management.Decode(b, &env, 256<<10); e != nil {
		return fail(e)
	}
	manifest, e := VerifyManifest(env, root, time.Now())
	if e != nil {
		return fail(e)
	}
	digest, e := fileDigest(reg.Binary, system)
	if e != nil {
		return fail(e)
	}
	matched := false
	for _, a := range manifest.Artifacts {
		if a.OS == runtime.GOOS && a.Arch == runtime.GOARCH && a.BinarySHA256 == digest {
			matched = true
		}
	}
	if !matched {
		return fail(errors.New("bootstrap_binary_not_authenticated"))
	}
	h.state.ActiveVersion = manifest.Version
	h.state.ActiveDigest = digest
	h.state.Highest = manifest.Version
	h.state.RootEpoch = root.Trust.Epoch
	if e = h.save(); e != nil {
		return fail(e)
	}
	return h, nil
}
func (h *Helper) Close() error { return h.unlock() }
func (h *Helper) save() error {
	event := management.Audit{Component: "updater", Installation: h.registration.Installation}
	if h.state.Last != nil {
		event.Receipts = []management.Receipt{*h.state.Last}
	}
	e := management.Checkpoint(filepath.Join(h.registration.State, "state.json"), management.Marshal(h.state), event)
	if e != nil {
		h.storageFailed = true
	}
	return e
}
func fileDigest(path string, system bool) (string, error) {
	if e := management.ValidateLocalFile(path, system, false); e != nil {
		return "", e
	}
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || st.Size() > 128<<20 {
		return "", errors.New("binary_size_limit")
	}
	hash := sha256.New()
	if _, e = io.Copy(hash, io.LimitReader(f, 128<<20+1)); e != nil {
		return "", e
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func (h *Helper) policy() (management.Policy, Policy, Root, error) {
	p, e := management.LoadPolicy(h.registration.PolicyFile)
	if e != nil {
		return p, Policy{}, Root{}, e
	}
	if p.Installation != h.registration.Installation || p.Project != h.registration.Project || p.Role != h.registration.Role || p.Observer != h.registration.Observer || h.system && !p.System {
		return p, Policy{}, Root{}, errors.New("helper_policy_identity_mismatch")
	}
	if !p.Updates.Accept {
		return p, Policy{}, Root{}, errors.New("remote_updates_disabled")
	}
	policy, e := LoadPolicy(p.Updates.PolicyFile, h.system)
	if e != nil {
		return p, policy, Root{}, e
	}
	root, e := LoadRoot(p.Updates.PublisherTrustFile, h.system)
	return p, policy, root, e
}
func (h *Helper) Handle(ctx context.Context, request HelperRequest) (management.Receipt, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.storageFailed {
		return management.Receipt{}, errors.New("helper_storage_requires_recovery")
	}
	p, policy, root, e := h.policy()
	if e != nil {
		return management.Receipt{}, e
	}
	trust, e := management.LoadTrust(p.Controller.CommandTrustFile, h.system, time.Now())
	if e != nil {
		return management.Receipt{}, e
	}
	cmd, e := management.ParseCommand(request.Envelope, p, trust, time.Now())
	if e != nil {
		return management.Receipt{}, e
	}
	if cmd.Channel() != "update" {
		return management.Receipt{}, errors.New("helper_operation_denied")
	}
	now := time.Now().UTC()
	hash := management.Digest(management.Marshal(request.Envelope))
	if h.state.Last != nil && cmd.Sequence == h.state.Sequence {
		if h.state.Last.CommandID != cmd.ID || h.state.Last.CommandDigest != hash {
			return management.Receipt{}, errors.New("helper_command_conflict")
		}
		return *h.state.Last, nil
	}
	if cmd.Sequence <= h.state.Sequence || cmd.Base != h.state.Generation || now.Before(h.state.TimeFloor.Add(-30*time.Second)) {
		return management.Receipt{}, errors.New("helper_stale_command")
	}
	if h.state.Phase == "activating" || h.state.Phase == "checking" || h.state.Phase == "manual_recovery_required" {
		return management.Receipt{}, errors.New("helper_recovery_required")
	}
	if cmd.Kind == "update.activate" && (h.state.Prepared == nil || h.state.Prepared.Digest() != cmd.Prepared || h.state.Prepared.Generation != cmd.Generation || h.state.Prepared.PayloadDigest != cmd.Digest || h.state.Prepared.Deployment != cmd.Deployment) {
		return management.Receipt{}, errors.New("helper_prepared_mismatch")
	}
	oldState := h.state
	h.state.Sequence = cmd.Sequence
	h.state.TimeFloor = now
	h.state.Last = &management.Receipt{CommandID: cmd.ID, Deployment: cmd.Deployment, CommandDigest: hash, Sequence: cmd.Sequence, Generation: cmd.Generation, PayloadDigest: cmd.Digest, State: "accepted", At: now}
	if e = h.save(); e != nil {
		h.state = oldState
		return management.Receipt{}, e
	}
	finish := func(state, code string) (management.Receipt, error) {
		r := *h.state.Last
		r.State = state
		r.Code = code
		r.At = time.Now().UTC()
		h.state.Last = &r
		if e := h.save(); e != nil {
			r.State = "accepted"
			r.Code = "helper_storage_requires_recovery"
			h.state.Last = &r
			return r, e
		}
		if code != "" {
			return r, errors.New(code)
		}
		return r, nil
	}
	fail := func(code string) (management.Receipt, error) { h.state.Phase = "failed"; return finish("failed", code) }
	reauthorize := func() error {
		current, currentPolicy, currentRoot, err := h.policy()
		if err != nil || management.Digest(management.Marshal(current)) != management.Digest(management.Marshal(p)) || management.Digest(management.Marshal(currentPolicy)) != management.Digest(management.Marshal(policy)) || management.Digest(management.Marshal(currentRoot)) != management.Digest(management.Marshal(root)) {
			return errors.New("management_policy_changed")
		}
		currentTrust, err := management.LoadTrust(current.Controller.CommandTrustFile, h.system, time.Now())
		if err != nil {
			return errors.New("management_trust_changed")
		}
		if _, err := management.ParseCommand(request.Envelope, current, currentTrust, time.Now()); err != nil {
			return errors.New("management_authorization_changed")
		}
		return nil
	}
	if cmd.Kind == "update.cancel" {
		h.state.Prepared = nil
		h.state.Candidate = nil
		h.state.Phase = "canceled"
		return finish("canceled", "")
	}
	if cmd.Kind == "update.prepare" {
		if int64(len(request.Payload)) != cmd.Length || management.Digest(request.Payload) != cmd.Digest {
			return fail("update_payload_mismatch")
		}
		var target Target
		if management.Decode(request.Payload, &target, 32<<10) != nil || target.Validate() != nil {
			return fail("invalid_update_target")
		}
		if e = policy.Allows(target.Version, h.state.Highest); e != nil {
			return fail(e.Error())
		}
		digest, e := fileDigest(h.registration.Binary, h.system)
		if e != nil || digest != h.state.ActiveDigest {
			return fail("installed_binary_integrity_failed")
		}
		resolved, e := h.source.Resolve(ctx, root, target.Version, runtime.GOOS, runtime.GOARCH, h.state.CatalogVersion, now)
		if e != nil {
			return fail("release_verification_failed")
		}
		if !target.Matches(resolved) {
			return fail("pinned_release_changed")
		}
		sameVersion := target.Version == h.state.ActiveVersion
		if sameVersion && resolved.Artifact.BinarySHA256 != h.state.ActiveDigest {
			return fail("installed_binary_integrity_failed")
		}
		if !sameVersion && !slices.Contains(resolved.Manifest.RollbackFrom, h.state.ActiveVersion) {
			return fail("automatic_recovery_not_supported")
		}
		// A revoked or below-floor previous executable must never be an automatic recovery target.
		previous, e := h.source.Resolve(ctx, root, h.state.ActiveVersion, runtime.GOOS, runtime.GOARCH, h.state.CatalogVersion, now)
		if e != nil || previous.Artifact.BinarySHA256 != h.state.ActiveDigest {
			return fail("recovery_release_not_permitted")
		}
		if e = h.acceptCatalog(previous.Catalog); e != nil {
			return fail(e.Error())
		}
		if resolved.Catalog.Version >= h.state.CatalogVersion {
			if e = h.acceptCatalog(resolved.Catalog); e != nil {
				return fail(e.Error())
			}
		}
		if e = previous.Catalog.Permits(resolved.Manifest, resolved.Artifact, resolved.Envelope); e != nil {
			return fail(e.Error())
		}
		if !sameVersion {
			stage := filepath.Join(h.registration.State, "candidate.archive")
			_ = os.Remove(stage)
			f, e := os.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if e != nil {
				return fail("candidate_staging_failed")
			}
			e = h.source.Download(ctx, resolved.Artifact, f)
			if e != nil {
				f.Close()
				return fail("candidate_download_failed")
			}
			candidate := filepath.Join(h.registration.State, "candidate")
			_ = os.Remove(candidate)
			out, e := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if e != nil {
				f.Close()
				return fail("candidate_staging_failed")
			}
			e = Extract(f, resolved.Artifact, out)
			f.Close()
			out.Close()
			if e != nil {
				return fail("candidate_archive_invalid")
			}
		}
		if e = reauthorize(); e != nil {
			return fail(e.Error())
		}
		configDigest, err := h.effects.Configuration(ctx)
		if err != nil || !management.HashOK(configDigest) {
			return fail("runtime_configuration_unavailable")
		}
		h.state.PreparedConfig = configDigest
		h.state.Candidate = &resolved
		h.state.Target = target
		h.state.OldVersion = h.state.ActiveVersion
		h.state.OldDigest = h.state.ActiveDigest
		h.state.Phase = "prepared"
		r := *h.state.Last
		r.State = "prepared"
		r.At = time.Now().UTC()
		h.state.Last = &r
		h.state.Prepared = &r
		if e = h.save(); e != nil {
			r.State = "accepted"
			r.Code = "helper_storage_requires_recovery"
			h.state.Last = &r
			return r, e
		}
		return r, nil
	}
	if cmd.Kind != "update.activate" || h.state.Candidate == nil {
		return fail("update_not_prepared")
	}
	if !policy.Window(now) {
		return fail("outside_maintenance_window")
	}
	if e = policy.Allows(h.state.Target.Version, h.state.Highest); e != nil {
		return fail(e.Error())
	}
	resolved, e := h.source.Resolve(ctx, root, h.state.Target.Version, runtime.GOOS, runtime.GOARCH, h.state.CatalogVersion, now)
	if e != nil || !h.state.Target.Matches(resolved) {
		return fail("release_revalidation_failed")
	}
	if e = h.acceptCatalog(resolved.Catalog); e != nil {
		return fail(e.Error())
	}
	previous, e := h.source.Resolve(ctx, root, h.state.OldVersion, runtime.GOOS, runtime.GOARCH, h.state.CatalogVersion, now)
	if e != nil || previous.Artifact.BinarySHA256 != h.state.OldDigest {
		return fail("recovery_release_not_permitted")
	}
	if e = h.acceptCatalog(previous.Catalog); e != nil {
		return fail(e.Error())
	}
	if e = previous.Catalog.Permits(resolved.Manifest, resolved.Artifact, resolved.Envelope); e != nil {
		return fail(e.Error())
	}
	configuration, err := h.effects.Configuration(ctx)
	if err != nil || configuration != h.state.PreparedConfig {
		return fail("runtime_configuration_changed")
	}
	healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
	err = h.effects.Healthy(healthCtx, h.state.ActiveVersion, h.state.ActiveDigest)
	healthCancel()
	if err != nil {
		return fail("current_service_not_healthy")
	}
	if err = management.ValidateDirectory(filepath.Dir(h.registration.Binary), h.system); err != nil {
		return fail("installation_directory_untrusted")
	}
	destinationLock, err := secure.Lock(filepath.Join(filepath.Dir(h.registration.Binary), ".topophany-update.lock"))
	if err != nil {
		return fail("installation_update_in_progress")
	}
	defer destinationLock()
	// Once replacement starts, recovery outlives the caller and the replaced service.
	work, cancel := context.WithTimeout(context.Background(), time.Duration(policy.HealthSeconds+120)*time.Second)
	defer cancel()
	digest, e := fileDigest(h.registration.Binary, h.system)
	if e != nil || digest != h.state.OldDigest {
		return fail("installed_binary_integrity_failed")
	}
	candidate := filepath.Join(h.registration.State, "candidate")
	sameVersion := h.state.Target.Version == h.state.ActiveVersion
	if sameVersion {
		candidate = h.registration.Binary
	}
	digest, e = fileDigest(candidate, h.system)
	if e != nil || digest != resolved.Artifact.BinarySHA256 {
		return fail("staged_binary_integrity_failed")
	}
	if !sameVersion {
		backup := filepath.Join(h.registration.State, "previous")
		if e = replaceBinary(h.registration.Binary, backup, h.state.OldDigest, h.system); e != nil {
			return fail("backup_binary_failed")
		}
	}
	release, e := management.PolicyLease(h.registration.PolicyFile)
	if e != nil {
		return fail("management_policy_lock_unavailable")
	}
	// Network verification and staging may outlive a local policy/trust edit or
	// the command/window. Recheck every authority at the service-stop boundary.
	if e = reauthorize(); e != nil {
		release()
		return fail(e.Error())
	}
	if !policy.Window(time.Now()) {
		release()
		return fail("outside_maintenance_window")
	}
	if sameVersion {
		defer release()
		h.state.Phase = "healthy"
		h.state.Generation = cmd.Generation
		h.state.Prepared = nil
		return finish("healthy", "")
	}
	h.state.Phase = "activating"
	if n, _ := Compare(h.state.Target.Version, h.state.Highest); n > 0 {
		h.state.Highest = h.state.Target.Version
	}
	if e = h.save(); e != nil {
		release()
		return *h.state.Last, e
	}
	release()
	if e = h.effects.Stop(work); e != nil {
		h.state.Phase = "manual_recovery_required"
		return finish("manual_recovery_required", "service_stop_failed")
	}
	if e = h.effects.Preflight(work, candidate, h.registration.State, h.state.PreparedConfig); e != nil {
		recovery, stopRecovery := context.WithTimeout(context.WithoutCancel(work), 2*time.Minute)
		defer stopRecovery()
		restartErr := h.effects.Start(recovery)
		if restartErr == nil {
			restartErr = h.effects.Healthy(recovery, h.state.OldVersion, h.state.OldDigest)
		}
		if restartErr != nil {
			h.state.Phase = "manual_recovery_required"
			return finish("manual_recovery_required", "previous_service_restart_failed")
		}
		return fail("candidate_recovery_preflight_failed")
	}
	if e = replaceBinary(candidate, h.registration.Binary, resolved.Artifact.BinarySHA256, h.system); e == nil {
		h.state.Phase = "checking"
		e = h.save()
	}
	if e == nil {
		e = h.effects.Start(work)
	}
	if e == nil {
		health, stop := context.WithTimeout(work, time.Duration(policy.HealthSeconds)*time.Second)
		e = h.effects.Healthy(health, h.state.Target.Version, resolved.Artifact.BinarySHA256)
		stop()
	}
	if e == nil {
		currentConfig, err := h.effects.Configuration(work)
		if err != nil || currentConfig != h.state.PreparedConfig {
			e = errors.New("runtime_configuration_changed")
		}
	}
	if e != nil {
		return h.recover(work, finish)
	}
	h.state.Phase = "healthy"
	h.state.ActiveVersion = h.state.Target.Version
	h.state.ActiveDigest = resolved.Artifact.BinarySHA256
	h.state.Highest = h.state.Target.Version
	h.state.Generation = cmd.Generation
	h.state.Prepared = nil
	return finish("healthy", "")
}
func (h *Helper) acceptCatalog(c Catalog) error {
	hash := management.Digest(management.Marshal(c))
	if h.state.RootEpoch != 0 && c.RootEpoch < h.state.RootEpoch {
		return errors.New("publisher_root_transition_requires_local_review")
	}
	if c.Version < h.state.CatalogVersion || c.Version == h.state.CatalogVersion && h.state.CatalogDigest != "" && hash != h.state.CatalogDigest {
		return errors.New("security_catalog_conflict")
	}
	h.state.RootEpoch = c.RootEpoch
	h.state.CatalogVersion = c.Version
	h.state.CatalogDigest = hash
	return h.save()
}
func (h *Helper) recover(ctx context.Context, finish func(string, string) (management.Receipt, error)) (management.Receipt, error) {
	// A failed activation may have exhausted its entire deadline. Recovery must
	// have its own bounded budget, including service stop/start and health checks.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	h.state.Phase = "recovering"
	// The durable activating/checking intent already authorizes this exact
	// recovery pair. Exhausted journal storage must not prevent restoring the
	// authenticated previous binary. Keep the storage fault latched and report it
	// even if the service recovers; restarting will reconcile the older intent.
	persistErr := h.save()
	e := h.effects.Stop(ctx)
	if e == nil {
		e = replaceBinary(filepath.Join(h.registration.State, "previous"), h.registration.Binary, h.state.OldDigest, h.system)
	}
	if e == nil {
		e = h.effects.Start(ctx)
	}
	if e == nil {
		health, cancel := context.WithTimeout(ctx, 60*time.Second)
		e = h.effects.Healthy(health, h.state.OldVersion, h.state.OldDigest)
		cancel()
	}
	if e != nil {
		h.state.Phase = "manual_recovery_required"
		r, err := finish("manual_recovery_required", "automatic_recovery_failed")
		return r, errors.Join(persistErr, err)
	}
	h.state.Phase = "rolled_back"
	h.state.ActiveVersion = h.state.OldVersion
	h.state.ActiveDigest = h.state.OldDigest
	h.state.Prepared = nil
	r, err := finish("rolled_back", "candidate_health_failed")
	return r, errors.Join(persistErr, err)
}
func replaceBinary(from, to, want string, system bool) error {
	hash, e := fileDigest(from, system)
	if e != nil || hash != want {
		return errors.New("binary_source_mismatch")
	}
	if e = management.ValidateDirectory(filepath.Dir(to), system); e != nil {
		return e
	}
	if _, e = os.Lstat(to); e == nil {
		if e = management.ValidateLocalFile(to, system, false); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	in, e := os.Open(from)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.CreateTemp(filepath.Dir(to), ".topo-update-*")
	if e != nil {
		return e
	}
	defer os.Remove(out.Name())
	digest := sha256.New()
	_, e = io.Copy(io.MultiWriter(out, digest), io.LimitReader(in, 128<<20+1))
	if e == nil && hex.EncodeToString(digest.Sum(nil)) != want {
		e = errors.New("binary_changed_during_copy")
	}
	if e == nil {
		e = out.Chmod(0755)
	}
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(out.Name(), to); e != nil {
		return e
	}
	return secure.SyncDir(filepath.Dir(to))
}

func digestReader(r io.Reader) (string, error) {
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(r, 128<<20+1))
	if e != nil || n > 128<<20 {
		return "", errors.New("binary_size_limit")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Recover completes only the already recorded recovery pair. It never starts a
// new deployment and therefore remains available after local acceptance is off.
func (h *Helper) Recover(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state.Phase != "activating" && h.state.Phase != "checking" && h.state.Phase != "recovering" {
		return nil
	}
	if h.state.Last == nil || h.state.Candidate == nil || !slices.Contains(h.state.Candidate.Manifest.RollbackFrom, h.state.OldVersion) {
		return errors.New("manual_recovery_required")
	}
	p, e := management.LoadPolicy(h.registration.PolicyFile)
	if e != nil {
		return e
	}
	policy, e := LoadPolicy(p.Updates.PolicyFile, h.system)
	if e != nil {
		return e
	}
	n, e := Compare(h.state.OldVersion, policy.Minimum)
	if e != nil || n < 0 {
		return errors.New("recovery_below_local_security_floor")
	}
	destinationLock, err := secure.Lock(filepath.Join(filepath.Dir(h.registration.Binary), ".topophany-update.lock"))
	if err != nil {
		return err
	}
	defer destinationLock()
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	_, e = h.recover(work, func(state, code string) (management.Receipt, error) {
		r := *h.state.Last
		r.State = state
		r.Code = code
		r.At = time.Now().UTC()
		h.state.Last = &r
		return r, h.save()
	})
	return e
}
