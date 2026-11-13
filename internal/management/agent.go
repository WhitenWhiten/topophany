package management

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

type ApplyRequest struct {
	Operation string
	Check     func() error
	Config    model.Config
	Done      chan error
	Context   context.Context
}
type Agent struct {
	httpClient      *http.Client
	httpKey         string
	acknowledged    string
	updating        atomic.Bool
	pausedFlag      atomic.Bool
	Pending         func() (int, uint64)
	Collectors      func() []string
	CollectorID     string
	pauseMu         sync.Mutex
	paused          bool
	pauseUntil      time.Time
	pauseSession    string
	quarantineFloor uint64
	ReadOnly        bool
	PolicyFault     string
	Update          func(context.Context, Policy, Envelope, []byte) (Receipt, error)
	path            string
	identity        Policy
	store           *Store
	current         atomic.Pointer[model.Config]
	constraints     atomic.Pointer[Constraints]
	constraintMu    sync.Mutex
	constraintFile  constraintFile
	managed         atomic.Bool
	concurrency     int
	Changes         chan ApplyRequest
	mu              sync.Mutex
}

// NewAgent never enrolls or enables a capability. Existing committed config may
// continue when acceptance is off; no new network access is then started.
func NewAgent(path, dir string, local model.Config, role, observer string, concurrency int) (*Agent, model.Config, error) {
	stateDir := filepath.Join(dir, "management")
	var saved State
	stateBytes, stateErr := ReadLocal(filepath.Join(stateDir, "state.json"), 1<<20, false, true)
	if stateErr != nil && !os.IsNotExist(stateErr) {
		return nil, local, stateErr
	}
	if stateErr == nil {
		if Decode(stateBytes, &saved, 1<<20) != nil || saved.Schema != 1 || saved.Project != local.Project || saved.Role != role || saved.Observer != observer {
			return nil, local, errors.New("management_state_identity_mismatch")
		}
	}
	p, policyErr := LoadPolicy(path)
	readonly := path == "" || policyErr != nil || p.Installation == ""
	fault := ""
	if policyErr != nil {
		fault = "management_policy_invalid"
	}
	var trust Trust
	if !readonly {
		if p.Project != local.Project || p.Role != role || p.Observer != observer {
			return nil, local, errors.New("management_installation_mismatch")
		}
		raw, err := ReadLocal(p.Controller.CommandTrustFile, 64<<10, p.System, false)
		if err != nil || Decode(raw, &trust, 64<<10) != nil || trust.Schema != 1 || trust.ID != p.Controller.ID || trust.Epoch == 0 {
			readonly = true
			fault = "management_trust_unavailable"
		}
		if stateErr == nil && (saved.Epoch != trust.Epoch || saved.Controller != p.Controller.ID || saved.Installation != p.Installation) {
			readonly = true
			fault = "management_reenrollment_required"
		}
	}
	if readonly && stateErr != nil {
		if fault == "" {
			return nil, local, nil
		}
		a := &Agent{path: path, ReadOnly: true, PolicyFault: fault, concurrency: concurrency, Changes: make(chan ApplyRequest)}
		a.current.Store(&local)
		return a, local, nil
	}
	if readonly {
		p = Policy{Schema: 1, Project: saved.Project, Installation: saved.Installation, Role: saved.Role, Observer: saved.Observer, Controller: ControllerRef{ID: saved.Controller}}
		trust.Epoch = saved.Epoch
	}
	st, err := Open(stateDir, p, trust.Epoch)
	if err != nil {
		return nil, local, err
	}
	a := &Agent{path: path, identity: p, store: st, concurrency: concurrency, Changes: make(chan ApplyRequest), ReadOnly: readonly, PolicyFault: fault}
	state := st.Snapshot()
	if state.Config.ActiveDigest != "" {
		raw, err := st.Blob(state.Config.ActiveDigest)
		if err != nil {
			st.Close()
			return nil, local, err
		}
		loaded, err := DecodeTopology(raw)
		if err != nil || loaded.Project != local.Project {
			st.Close()
			return nil, local, errors.New("managed_configuration_recovery_failed")
		}
		raw, err = st.Blob(state.Config.ActiveConstraints)
		if err != nil {
			st.Close()
			return nil, local, err
		}
		var limits Constraints
		if Decode(raw, &limits, 1<<20) != nil || limits.Check(loaded, observer, concurrency) != nil {
			st.Close()
			return nil, local, errors.New("managed_constraints_recovery_failed")
		}
		a.constraints.Store(&limits)
		a.managed.Store(true)
		local = loaded
	}
	if err := st.RecoverConfig(); err != nil {
		st.Close()
		return nil, local, err
	}
	a.current.Store(&local)
	return a, local, nil
}
func (a *Agent) Close() error {
	if a != nil && a.httpClient != nil {
		a.httpClient.CloseIdleConnections()
	}
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.Close()
}
func (a *Agent) Managed() bool { return a != nil && a.managed.Load() }

func (a *Agent) LocalReload(next model.Config) error {
	if a.Managed() {
		return errors.New("managed_configuration_active")
	}
	a.current.Store(&next)
	return nil
}
func (a *Agent) Run(ctx context.Context, log func(string)) {
	if a == nil {
		return
	}
	if a.PolicyFault != "" && log != nil {
		log(a.PolicyFault)
	}
	leaseCtx, stopLease := context.WithCancel(ctx)
	defer stopLease()
	var leases sync.WaitGroup
	leases.Add(1)
	go func() {
		defer leases.Done()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-tick.C:
				a.checkPause(leaseCtx)
			}
		}
	}()
	defer func() { stopLease(); leases.Wait() }()
	delay := time.Millisecond
	backoff := 15 * time.Second
	last := ""
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		e := a.Poll(ctx)
		if e != nil {
			if e.Error() != last && log != nil {
				log(e.Error())
			}
			last = e.Error()
			delay = backoff
			backoff = min(backoff*2, 5*time.Minute)
		} else {
			last = ""
			backoff = 15 * time.Second
			delay = time.Duration(float64(15*time.Second) * (.8 + rand.Float64()*.4))
		}
	}
}
func (a *Agent) Poll(ctx context.Context) error {
	if a.ReadOnly {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, e := LoadPolicy(a.path)
	if e != nil {
		return errors.New("management_policy_invalid")
	}
	if !p.Network() {
		if a.httpClient != nil {
			a.httpClient.CloseIdleConnections()
			a.httpClient = nil
		}
		return nil
	}
	if p.Installation != a.identity.Installation || p.Controller != a.identity.Controller || p.Project != a.identity.Project || p.Role != a.identity.Role || p.Observer != a.identity.Observer {
		return errors.New("management_reenrollment_required")
	}
	h, token, e := HTTP(p)
	if e != nil {
		return errors.New("management_transport_setup_failed")
	}
	key := Digest(append(Marshal(p.Controller), []byte(token)...))
	if a.httpClient != nil && key == a.httpKey && sameRoots(h, a.httpClient) {
		h.CloseIdleConnections()
		h = a.httpClient
	} else {
		if a.httpClient != nil {
			a.httpClient.CloseIdleConnections()
		}
		a.httpClient, a.httpKey = h, key
	}
	origin := strings.TrimRight(p.Controller.URL, "/")
	status := NodeStatus{Contracts: ContractDigest(*a.current.Load(), p.Role, p.Observer), CollectorID: a.CollectorID, OS: runtime.GOOS, Arch: runtime.GOARCH, Installation: p.Installation, ConfigAccept: p.Config.Accept, UpdateAccept: p.Updates.Accept, ConfigSession: p.Config.Session, UpdateSession: p.Updates.Session, Version: model.Version, At: time.Now().UTC()}
	if a.Collectors != nil {
		status.Collectors = a.Collectors()
	}
	status.QuiesceUntil = a.store.Snapshot().Config.QuiesceUntil
	if _, _, e = Exchange(ctx, h, token, "POST", origin+"/management/v1/status", Marshal(status), 16<<10); e != nil {
		return e
	}
	b, code, e := Exchange(ctx, h, token, "GET", origin+"/management/v1/desired", nil, 64<<10)
	if e != nil || code == 204 {
		return e
	}
	var env Envelope
	if e = Decode(b, &env, 64<<10); e != nil {
		return errors.New("invalid_command_envelope")
	}
	sendReceipt := func(receipt Receipt) error {
		status.Receipt = &receipt
		status.Contracts = ContractDigest(*a.current.Load(), p.Role, p.Observer)
		status.QuiesceUntil = a.store.Snapshot().Config.QuiesceUntil
		status.At = time.Now().UTC()
		_, _, err := Exchange(ctx, h, token, "POST", origin+"/management/v1/status", Marshal(status), 16<<10)
		return err
	}
	snapshot := a.store.Snapshot()
	// Reporting an already authenticated result is not authorization to execute
	// again. Retry the exact receipt even after command/trust expiry or a local
	// session rotation, and avoid repeatedly downloading a prepared payload.
	envelopeDigest := Digest(Marshal(env))
	for _, receipt := range []*Receipt{snapshot.Config.Last, snapshot.Updates.Last} {
		if receipt != nil && receipt.CommandDigest == envelopeDigest && receipt.State != "accepted" {
			if a.acknowledged == receipt.Digest()+Digest(Marshal(p)) {
				return nil
			}
			err := sendReceipt(*receipt)
			if err == nil {
				a.acknowledged = receipt.Digest() + Digest(Marshal(p))
			}
			return err
		}
	}
	if !p.Config.Accept && !p.Updates.Accept {
		return nil
	}
	pending := snapshot.Updates.Last
	if pending != nil && pending.State == "accepted" && pending.CommandDigest == Digest(Marshal(env)) && a.Update != nil && p.Updates.Accept {
		var completedCommand Command
		original, _ := payloadBytes(env, 32<<10)
		if Decode(original, &completedCommand, 32<<10) == nil {
			receipt, err := a.Update(ctx, p, env, nil)
			if receipt.CommandID != "" && receipt.State != "accepted" {
				if saveErr := a.store.ImportUpdateReceipt(completedCommand, receipt); saveErr != nil {
					return saveErr
				}
				sendErr := sendReceipt(receipt)
				if err != nil {
					return err
				}
				return sendErr
			}
		}
	}
	trust, e := LoadTrust(p.Controller.CommandTrustFile, p.System, time.Now())
	if e != nil {
		return errors.New("management_trust_invalid")
	}
	cmd, e := ParseCommand(env, p, trust, time.Now())
	if e != nil {
		return e
	}
	var raw []byte
	if strings.HasSuffix(cmd.Kind, ".prepare") {
		raw, _, e = Exchange(ctx, h, token, "GET", origin+"/management/v1/configs/"+cmd.Digest, nil, 4<<20)
		if e != nil {
			return e
		}
	}
	receipt, e := a.Process(ctx, cmd, env, raw)
	if receipt.CommandID != "" {
		sendErr := sendReceipt(receipt)
		if sendErr == nil && receipt.State != "accepted" {
			a.acknowledged = receipt.Digest() + Digest(Marshal(p))
		}
		if e == nil {
			e = sendErr
		}
	}
	return e
}

// Process verifies again at the mutation boundary rather than trusting Poll.
func (a *Agent) Process(ctx context.Context, cmd Command, env Envelope, raw []byte) (Receipt, error) {
	var release func()
	if cmd.Channel() == "config" {
		var err error
		release, err = PolicyLease(a.path)
		if err != nil {
			return Receipt{}, errors.New("management_policy_lock_unavailable")
		}
		defer release()
	}
	p, e := LoadPolicy(a.path)
	if e != nil {
		return Receipt{}, errors.New("management_policy_invalid")
	}
	trust, e := LoadTrust(p.Controller.CommandTrustFile, p.System, time.Now())
	if e != nil {
		return Receipt{}, e
	}
	verified, e := ParseCommand(env, p, trust, time.Now())
	if e != nil {
		return Receipt{}, e
	}
	if Digest(Marshal(cmd)) != Digest(Marshal(verified)) {
		return Receipt{}, errors.New("command_changed")
	}
	r, duplicate, e := a.store.Begin(cmd, env, time.Now())
	if e != nil {
		return r, e
	}
	if duplicate && (cmd.Channel() != "update" || r.State != "accepted") {
		return r, nil
	}
	fail := func(code string) (Receipt, error) {
		r, e := a.store.Finish(cmd, "rejected", code, time.Now())
		if e != nil {
			return r, e
		}
		return r, errors.New(code)
	}
	if cmd.Channel() == "update" {
		if a.Update == nil {
			return fail("helper_upgrade_required")
		}
		if cmd.Kind == "update.activate" {
			a.updating.Store(true)
			defer a.updating.Store(false)
		}
		receipt, err := a.Update(ctx, p, env, raw)
		if receipt.CommandID != "" {
			if saveErr := a.store.ImportUpdateReceipt(cmd, receipt); saveErr != nil {
				return receipt, saveErr
			}
			return receipt, err
		}
		// A lost helper connection may mean the service was replaced. Preserve
		// accepted state and ask the idempotent helper again after restart.
		return r, err
	}
	if cmd.Kind == "config.cancel" {
		if e := a.resume(ctx); e != nil {
			return fail("configuration_resume_failed")
		}
		return a.store.Finish(cmd, "canceled", "", time.Now())
	}
	if cmd.Kind == "config.quiesce" {
		return a.quiesce(ctx, cmd, p)
	}
	if cmd.Kind == "config.prepare" && a.Paused() {
		return fail("configuration_transition_in_progress")
	}
	limits, e := LoadConstraints(p.Config.ConstraintsFile, p.System)
	if e != nil {
		return fail("configuration_policy_invalid")
	}
	if cmd.Kind == "config.activate" {
		raw, e = a.store.Blob(cmd.Digest)
		if e != nil {
			return fail("configuration_cache_invalid")
		}
	}
	if int64(len(raw)) != cmd.Length || Digest(raw) != cmd.Digest {
		return fail("configuration_digest_mismatch")
	}
	next, e := DecodeTopology(raw)
	if e != nil {
		return fail("invalid_remote_configuration")
	}
	if e = limits.Check(next, p.Observer, a.concurrency); e != nil {
		return fail(e.Error())
	}
	if e = SecretsAvailable(next, p.Observer); e != nil {
		return fail(e.Error())
	}
	before := a.current.Load()
	if ContractDigest(*before, p.Role, p.Observer) != ContractDigest(next, p.Role, p.Observer) && !cmd.RequiresQuiesce {
		return fail("configuration_quiesce_required")
	}
	if cmd.Kind == "config.prepare" {
		if e = a.store.PutBlob(raw, cmd.Digest); e != nil {
			return fail("configuration_persist_failed")
		}
		limitBytes := Marshal(limits)
		hash := Digest(limitBytes)
		if e = a.store.PutBlob(limitBytes, hash); e != nil {
			return fail("configuration_persist_failed")
		}
		return a.store.Finish(cmd, "prepared", "", time.Now(), hash)
	}
	fresh, e := LoadPolicy(a.path)
	if e != nil || Digest(Marshal(fresh)) != Digest(Marshal(p)) {
		return fail("management_policy_changed")
	}
	// Fresh constraints must still match the exact preparation policy.
	prepared := a.store.Snapshot().Config.PreparedConstraints
	if Digest(Marshal(limits)) != prepared {
		return fail("configuration_policy_changed")
	}
	if cmd.RequiresQuiesce {
		if e := a.consumePause(); e != nil {
			return fail("configuration_not_quiesced")
		}
	}
	oldLimits := a.constraints.Load()
	wasManaged := a.managed.Load()
	a.constraints.Store(&limits)
	a.managed.Store(true)
	apply := func(applyCtx context.Context, c model.Config) error {
		req := ApplyRequest{Config: c, Done: make(chan error, 1), Context: applyCtx, Check: func() error {
			fresh, e := LoadPolicy(a.path)
			if e != nil || Digest(Marshal(fresh)) != Digest(Marshal(p)) {
				return errors.New("management_policy_changed")
			}
			return nil
		}}
		select {
		case a.Changes <- req:
		case <-applyCtx.Done():
			return applyCtx.Err()
		}
		return <-req.Done
	}
	if e = apply(ctx, next); e != nil {
		a.constraints.Store(oldLimits)
		a.managed.Store(wasManaged)
		_ = a.requestOperation(context.WithoutCancel(ctx), "resume")
		return fail("configuration_activation_failed")
	}
	r, e = a.store.Finish(cmd, "active", "", time.Now())
	if e != nil {
		if CheckpointCommitted(e) {
			a.current.Store(&next)
			return Receipt{}, e
		}
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := apply(recoveryCtx, *before); err != nil {
			_ = a.requestOperation(recoveryCtx, "pause")
			return Receipt{}, errors.New("configuration_runtime_recovery_required")
		}
		a.constraints.Store(oldLimits)
		a.managed.Store(wasManaged)
		return r, e
	}
	a.current.Store(&next)
	return r, nil
}

func (a *Agent) Updating() bool { return a != nil && a.updating.Load() }
