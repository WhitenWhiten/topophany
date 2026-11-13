package management

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type SigningKey struct {
	ID      string `json:"id"`
	Private string `json:"private_key"`
}
type ManagementPrincipal struct {
	Installations []string `json:"installations,omitempty"`
	Hash          string   `json:"token_sha256"`
	Role          string   `json:"role"`
	Installation  string   `json:"installation_id,omitempty"`
	Project       string   `json:"project"`
}
type Registration struct {
	ID       string `json:"installation_id"`
	Project  string `json:"project"`
	Role     string `json:"role"`
	Observer string `json:"observer,omitempty"`
}
type ControllerPolicy struct {
	InventoryComplete  bool           `json:"inventory_complete,omitempty"`
	ObservationSeconds int            `json:"observation_seconds,omitempty"`
	PublisherTrust     string         `json:"publisher_trust_file,omitempty"`
	Schema             int            `json:"schema"`
	Enabled            bool           `json:"enabled"`
	ID                 string         `json:"id"`
	Epoch              uint64         `json:"authority_epoch"`
	Listen             string         `json:"listen"`
	Cert               string         `json:"tls_cert_file"`
	Key                string         `json:"tls_key_file"`
	Auth               string         `json:"auth_file"`
	SignKey            string         `json:"signing_key_file"`
	Data               string         `json:"data_dir"`
	Registrations      []Registration `json:"installations"`
}
type Auth struct {
	Schema int                   `json:"schema"`
	Tokens []ManagementPrincipal `json:"tokens"`
}
type NodeStatus struct {
	Contracts     string    `json:"contracts"`
	Collectors    []string  `json:"collectors,omitempty"`
	CollectorID   string    `json:"collector_id,omitempty"`
	QuiesceUntil  time.Time `json:"quiesce_until,omitempty"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	Installation  string    `json:"installation_id"`
	ConfigAccept  bool      `json:"config_accept"`
	UpdateAccept  bool      `json:"update_accept"`
	ConfigSession string    `json:"config_session"`
	UpdateSession string    `json:"update_session"`
	Version       string    `json:"version"`
	At            time.Time `json:"at"`
	Receipt       *Receipt  `json:"receipt,omitempty"`
}
type NodeRecord struct {
	HealthySince     time.Time    `json:"healthy_since,omitempty"`
	TargetVersion    string       `json:"target_version,omitempty"`
	PreparedReceipt  *Receipt     `json:"prepared_receipt,omitempty"`
	UpdateGeneration uint64       `json:"update_generation"`
	Registration     Registration `json:"registration"`
	Sequence         uint64       `json:"sequence"`
	Generation       uint64       `json:"generation"`
	Desired          *Envelope    `json:"desired,omitempty"`
	Status           *NodeStatus  `json:"status,omitempty"`
	Received         time.Time    `json:"received_at"`
}
type controllerState struct {
	Schema int                   `json:"schema"`
	ID     string                `json:"controller_id"`
	Epoch  uint64                `json:"authority_epoch"`
	Nodes  map[string]NodeRecord `json:"nodes"`
	Blobs  map[string]int64      `json:"blobs"`
}
type Controller struct {
	actorInstallation string
	failed            bool
	actor             string
	VerifyUpdate      func(context.Context, []byte, NodeStatus, []NodeRecord) error
	mu                sync.Mutex
	Policy            ControllerPolicy
	state             controllerState
	auth              map[string]ManagementPrincipal
	key               SigningKey
	private           ed25519.PrivateKey
	unlock            func() error
	slots             chan struct{}
	rates             map[string]rate
}
type rate struct {
	At    time.Time
	Count int
}

func LoadControllerPolicy(path string) (ControllerPolicy, error) {
	b, e := ReadLocal(path, 1<<20, false, false)
	if e != nil {
		return ControllerPolicy{}, e
	}
	var p ControllerPolicy
	if e = Decode(b, &p, 1<<20); e != nil {
		return p, e
	}
	if p.Schema != 1 {
		return p, errors.New("invalid_control_plane_policy")
	}
	return p, nil
}
func NewController(p ControllerPolicy) (*Controller, error) {
	if p.ObservationSeconds == 0 {
		p.ObservationSeconds = 300
	}
	if p.ObservationSeconds < 30 || p.ObservationSeconds > 3600 {
		return nil, errors.New("invalid_observation_period")
	}
	if p.Listen == "" {
		p.Listen = "127.0.0.1:7448"
	}
	if !p.Enabled || p.Schema != 1 || !config.ID(p.ID) || p.Epoch == 0 || !filepath.IsAbs(p.Data) || len(p.Registrations) > 256 {
		return nil, errors.New("invalid_control_plane_policy")
	}
	b, e := ReadLocal(p.Auth, 1<<20, false, true)
	if e != nil {
		return nil, e
	}
	var auth Auth
	if e = Decode(b, &auth, 1<<20); e != nil || auth.Schema != 1 || len(auth.Tokens) < 1 || len(auth.Tokens) > 256 {
		return nil, errors.New("invalid_management_auth")
	}
	b, e = ReadLocal(p.SignKey, 16384, false, true)
	if e != nil {
		return nil, e
	}
	var key SigningKey
	if e = Decode(b, &key, 16384); e != nil {
		return nil, e
	}
	priv, e := base64.RawURLEncoding.DecodeString(key.Private)
	if e != nil || len(priv) != ed25519.PrivateKeySize || !config.ID(key.ID) {
		return nil, errors.New("invalid_control_signing_key")
	}
	c := &Controller{Policy: p, auth: map[string]ManagementPrincipal{}, key: key, private: priv, slots: make(chan struct{}, 16), rates: map[string]rate{}, state: controllerState{1, p.ID, p.Epoch, map[string]NodeRecord{}, map[string]int64{}}}
	for _, v := range p.Registrations {
		if !config.ID(v.ID) || !config.ID(v.Project) || (v.Role != "worker" && v.Role != "collector") || (v.Role == "worker" && !config.ID(v.Observer)) {
			return nil, errors.New("invalid_installation_registration")
		}
		if _, ok := c.state.Nodes[v.ID]; ok {
			return nil, errors.New("duplicate_installation")
		}
		c.state.Nodes[v.ID] = NodeRecord{Registration: v}
	}
	for _, v := range auth.Tokens {
		if !HashOK(v.Hash) || !config.ID(v.Project) {
			return nil, errors.New("invalid_management_principal")
		}
		if _, ok := c.auth[v.Hash]; ok {
			return nil, errors.New("duplicate_management_principal")
		}
		switch v.Role {
		case "management-agent":
			r, ok := c.state.Nodes[v.Installation]
			if !ok || r.Registration.Project != v.Project {
				return nil, errors.New("invalid_agent_scope")
			}
		case "config-publish", "update-publish", "management-read":
			if len(v.Installations) == 0 || len(v.Installations) > 256 {
				return nil, errors.New("explicit_installation_scope_required")
			}
			for _, id := range v.Installations {
				r, ok := c.state.Nodes[id]
				if !ok || r.Registration.Project != v.Project {
					return nil, errors.New("invalid_operator_scope")
				}
			}
			if v.Installation != "" {
				return nil, errors.New("invalid_operator_scope")
			}
		default:
			return nil, errors.New("invalid_management_role")
		}
		c.auth[v.Hash] = v
	}
	if e = MakePrivateDirectory(p.Data, false); e != nil {
		return nil, e
	}
	if e = trustedPath(p.Data, false, false, false); e != nil {
		return nil, e
	}
	c.unlock, e = secure.Lock(filepath.Join(p.Data, "control.lock"))
	if e != nil {
		return nil, e
	}
	b, e = ReadLocal(filepath.Join(p.Data, "control.json"), 32<<20, false, true)
	if e == nil {
		var old controllerState
		if e = Decode(b, &old, 32<<20); e == nil {
			if old.Schema != 1 || old.ID != p.ID || old.Epoch != p.Epoch {
				e = errors.New("controller_state_identity_mismatch")
			} else {
				for id, v := range c.state.Nodes {
					if prev, ok := old.Nodes[id]; ok {
						if prev.Registration != v.Registration {
							e = errors.New("installation_identity_changed")
							break
						}
						prev.Received = time.Time{}
						c.state.Nodes[id] = prev
					}
				}
				c.state.Blobs = old.Blobs
			}
		}
	} else if os.IsNotExist(e) {
		e = c.save()
	}
	if e != nil {
		c.unlock()
		return nil, e
	}
	return c, nil
}
func (c *Controller) Close() error { return c.unlock() }
func (c *Controller) save() error {
	b := Marshal(c.state)
	if len(b) > 32<<20 {
		return errors.New("control_storage_budget")
	}
	receipts := []Receipt{}
	for _, node := range c.state.Nodes {
		if c.actorInstallation != "" && c.actorInstallation != node.Registration.ID {
			continue
		}
		if node.Status != nil && node.Status.Receipt != nil {
			receipts = append(receipts, *node.Status.Receipt)
		}
	}
	err := Checkpoint(filepath.Join(c.Policy.Data, "control.json"), b, Audit{Component: "controller", Actor: c.actor, Installation: c.actorInstallation, Receipts: receipts})
	if err != nil {
		c.failed = true
	}
	return err
}
func (c *Controller) mutate(f func() error) error {
	if e := c.collectOrphanBlobs(); e != nil {
		return e
	}
	before := Marshal(c.state)
	restore := func() {
		// Decoding into existing maps retains keys introduced by a failed
		// transaction, incorrectly charging orphan blobs against the budget.
		var previous controllerState
		if err := Decode(before, &previous, 32<<20); err != nil {
			c.failed = true
			return
		}
		c.state = previous
	}
	if e := f(); e != nil {
		restore()
		return e
	}
	if e := c.save(); e != nil {
		if !CheckpointCommitted(e) {
			restore()
		}
		return e
	}
	return nil
}
func managementJSON(w http.ResponseWriter, status int, v any) {
	// Serialize while the caller still owns its state lock. The response writer
	// can release that lock before any potentially blocking socket write.
	b, _ := secure.MarshalJSON(v, "")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

type controllerResponse struct {
	http.ResponseWriter
	release func()
}

func (w controllerResponse) WriteHeader(code int) {
	w.release()
	w.ResponseWriter.WriteHeader(code)
}
func (w controllerResponse) Write(raw []byte) (int, error) {
	w.release()
	return w.ResponseWriter.Write(raw)
}

func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		managementJSON(w, 403, map[string]string{"error": "management_browser_forbidden"})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		managementJSON(w, 401, map[string]string{"error": "management_unauthorized"})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(token) != 64 {
		managementJSON(w, 401, map[string]string{"error": "management_unauthorized"})
		return
	}
	principal, ok := c.auth[secure.Hash(token)]
	if !ok {
		managementJSON(w, 401, map[string]string{"error": "management_unauthorized"})
		return
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		managementJSON(w, 429, map[string]string{"error": "management_busy"})
		return
	}
	c.mu.Lock()
	now := time.Now().UTC()
	v := c.rates[principal.Hash]
	if now.Sub(v.At) >= time.Minute {
		v = rate{At: now}
	}
	v.Count++
	c.rates[principal.Hash] = v
	c.mu.Unlock()
	if v.Count > 60 {
		managementJSON(w, 429, map[string]string{"error": "management_rate_limited"})
		return
	}
	// Never wait for a peer's body while owning the fleet state lock. Admission
	// slots and the HTTP read deadline bound slow readers and buffer allocation.
	if r.Method == "POST" {
		limit := int64(32 << 10)
		switch {
		case principal.Role == "management-agent":
			limit = 16 << 10
		case principal.Role == "config-publish" && r.URL.Path == "/management/v1/config-deployments":
			limit = 4 << 20
		case principal.Role == "update-publish" && r.URL.Path == "/management/v1/update-deployments":
			limit = 1 << 20
		}
		raw, err := readLimit(r.Body, limit)
		if err != nil {
			managementJSON(w, 400, map[string]string{"error": "invalid_management_request_body"})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
	}
	c.mu.Lock()
	release := sync.OnceFunc(c.mu.Unlock)
	defer release()
	w = controllerResponse{ResponseWriter: w, release: release}
	if c.failed {
		managementJSON(w, 503, map[string]string{"error": "control_storage_requires_recovery"})
		return
	}
	c.actor = principal.Role + ":" + principal.Hash[:16]
	c.actorInstallation = principal.Installation
	now = time.Now().UTC()
	if r.Method == "GET" && r.URL.Path == "/management/v1/capabilities" {
		managementJSON(w, 200, map[string]any{"schema": 1, "capabilities": []string{"management.v1", "config.reviewed-contracts.v1"}, "managed_updates": c.VerifyUpdate != nil})
		return
	}
	if principal.Role == "management-agent" {
		c.agent(w, r, principal, now)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/management/v1/nodes" {
		rows := []NodeRecord{}
		for _, n := range c.state.Nodes {
			if n.Registration.Project == principal.Project && slices.Contains(principal.Installations, n.Registration.ID) {
				rows = append(rows, n)
			}
		}
		managementJSON(w, 200, rows)
		return
	}
	if principal.Role == "update-publish" && r.Method == "POST" {
		if r.URL.Path == "/management/v1/update-deployments" {
			c.publishUpdate(w, r, principal, now)
			return
		}
		if r.URL.Path == "/management/v1/update/activate" || r.URL.Path == "/management/v1/update/cancel" {
			c.advanceUpdate(w, r, principal, now)
			return
		}
	}
	if principal.Role != "config-publish" {
		managementJSON(w, 403, map[string]string{"error": "management_scope_denied"})
		return
	}
	if r.Method == "POST" && r.URL.Path == "/management/v1/config-deployments" {
		c.publish(w, r, principal, now)
		return
	}
	if r.Method == "POST" && r.URL.Path == "/management/v1/quiesce" {
		c.quiesce(w, r, principal, now)
		return
	}
	if r.Method == "POST" && (r.URL.Path == "/management/v1/activate" || r.URL.Path == "/management/v1/cancel") {
		c.advance(w, r, principal, now)
		return
	}
	managementJSON(w, 404, map[string]string{"error": "unsupported_management_operation"})
}
func (c *Controller) agent(w http.ResponseWriter, r *http.Request, p ManagementPrincipal, now time.Time) {
	n := c.state.Nodes[p.Installation]
	if r.Method == "GET" && r.URL.Path == "/management/v1/desired" {
		if n.Desired == nil {
			w.WriteHeader(204)
		} else {
			managementJSON(w, 200, n.Desired)
		}
		return
	}
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/management/v1/configs/") {
		hash := strings.TrimPrefix(r.URL.Path, "/management/v1/configs/")
		var cmd Command
		if n.Desired != nil {
			b, _ := payloadBytes(*n.Desired, 32<<10)
			_ = Decode(b, &cmd, 32<<10)
		}
		size, ok := c.state.Blobs[hash]
		if !ok || hash != cmd.Digest || !now.Before(cmd.Expires) {
			managementJSON(w, 403, map[string]string{"error": "configuration_not_assigned"})
			return
		}
		b, err := ReadLocal(filepath.Join(c.Policy.Data, "blob-"+hash+".json"), 4<<20, false, true)
		if err != nil || int64(len(b)) != size || Digest(b) != hash {
			managementJSON(w, 503, map[string]string{"error": "configuration_blob_unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
		return
	}
	if r.Method == "POST" && r.URL.Path == "/management/v1/status" {
		b, e := readLimit(r.Body, 16<<10)
		var status NodeStatus
		if e != nil || Decode(b, &status, 16<<10) != nil || status.Installation != p.Installation || status.At.After(now.Add(30*time.Second)) || status.At.Before(now.Add(-time.Minute)) || len(status.OS) > 16 || len(status.Arch) > 16 || len(status.Version) > 96 || len(status.ConfigSession) > 96 || len(status.UpdateSession) > 96 {
			managementJSON(w, 400, map[string]string{"error": "invalid_management_status"})
			return
		}
		if status.Receipt != nil {
			rr := status.Receipt
			var cmd Command
			if n.Desired == nil {
				managementJSON(w, 409, map[string]string{"error": "unexpected_receipt"})
				return
			}
			raw, _ := payloadBytes(*n.Desired, 32<<10)
			_ = Decode(raw, &cmd, 32<<10)
			if rr.CommandID != cmd.ID || rr.Sequence != cmd.Sequence || rr.Generation != cmd.Generation || rr.PayloadDigest != cmd.Digest || rr.Deployment != cmd.Deployment || rr.CommandDigest != Digest(Marshal(n.Desired)) || len(rr.Code) > 128 {
				managementJSON(w, 409, map[string]string{"error": "receipt_mismatch"})
				return
			}
			switch rr.State {
			case "accepted", "prepared", "quiescing", "quiesced", "active", "healthy", "rolled_back", "manual_recovery_required", "failed", "rejected", "canceled":
			default:
				managementJSON(w, 400, map[string]string{"error": "invalid_receipt_state"})
				return
			}
			if (rr.State == "active" && cmd.Kind != "config.activate") || (rr.State == "healthy" && (cmd.Kind != "update.activate" || "v"+strings.TrimPrefix(status.Version, "v") != n.TargetVersion)) {
				managementJSON(w, 409, map[string]string{"error": "unexpected_active_receipt"})
				return
			}
		}
		if status.Receipt == nil && n.Status != nil && n.Status.Receipt != nil && n.Desired != nil {
			raw, _ := payloadBytes(*n.Desired, 32<<10)
			var pending Command
			_ = Decode(raw, &pending, 32<<10)
			if n.Status.Receipt.CommandID == pending.ID {
				status.Receipt = n.Status.Receipt
			}
		}
		if status.Receipt != nil && status.Receipt.State == "healthy" && "v"+strings.TrimPrefix(status.Version, "v") == n.TargetVersion {
			if n.HealthySince.IsZero() || now.Sub(n.Received) > time.Minute {
				n.HealthySince = now
			}
		} else {
			n.HealthySince = time.Time{}
		}
		unchanged := false
		if n.Status != nil {
			old := *n.Status
			current := status
			old.At = time.Time{}
			current.At = time.Time{}
			unchanged = Digest(Marshal(old)) == Digest(Marshal(current))
		}
		n.Status = &status
		if status.Receipt != nil && status.Receipt.State == "prepared" {
			n.PreparedReceipt = status.Receipt
		}
		n.Received = now
		if status.Receipt != nil && status.Receipt.State == "active" {
			n.Generation = status.Receipt.Generation
		}
		if status.Receipt != nil && status.Receipt.State == "healthy" {
			n.UpdateGeneration = status.Receipt.Generation
		}
		if unchanged {
			c.state.Nodes[p.Installation] = n
			w.WriteHeader(204)
			return
		}
		e = c.mutate(func() error { c.state.Nodes[p.Installation] = n; return nil })
		if e != nil {
			managementJSON(w, 503, map[string]string{"error": "control_storage_failed"})
			return
		}
		w.WriteHeader(204)
		return
	}
	managementJSON(w, 403, map[string]string{"error": "management_scope_denied"})
}

type ConfigDeployment struct {
	ID       string            `json:"deployment_id"`
	Targets  []string          `json:"installations"`
	Bases    map[string]uint64 `json:"base_generations"`
	Topology json.RawMessage   `json:"topology"`
}

func (c *Controller) publish(w http.ResponseWriter, r *http.Request, p ManagementPrincipal, now time.Time) {
	b, e := readLimit(r.Body, 4<<20)
	var plan ConfigDeployment
	if e != nil || Decode(b, &plan, 4<<20) != nil || !config.ID(plan.ID) || len(plan.Targets) < 1 || len(plan.Targets) > 64 {
		managementJSON(w, 400, map[string]string{"error": "invalid_config_deployment"})
		return
	}
	cfg, e := DecodeTopology(plan.Topology)
	if e != nil || cfg.Project != p.Project {
		managementJSON(w, 400, map[string]string{"error": "invalid_remote_configuration"})
		return
	}
	transition, err := c.requiresTransition(plan, cfg, p, now)
	if err != nil {
		managementJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	e = c.mutate(func() error {
		seen := map[string]bool{}
		for _, id := range plan.Targets {
			n, ok := c.state.Nodes[id]
			base, hasBase := plan.Bases[id]
			if !ok || seen[id] || !slices.Contains(p.Installations, id) || n.Registration.Project != p.Project || !hasBase || base != n.Generation {
				return errors.New("deployment_scope_or_generation_conflict")
			}
			seen[id] = true
			if n.Status == nil || now.Sub(n.Received) > time.Minute || !n.Status.ConfigAccept || !config.ID(n.Status.ConfigSession) {
				return errors.New("node_not_ready")
			}
			// Every participant independently validates the prepared measurement and
			// local grants before this cohort can enter its bounded pause.
			projection, e := Project(cfg, n.Registration.Observer, n.Registration.Role)
			if e != nil {
				return e
			}
			raw := Marshal(projection)
			hash := Digest(raw)
			if n.Desired != nil {
				oldraw, _ := payloadBytes(*n.Desired, 32<<10)
				var old Command
				_ = Decode(oldraw, &old, 32<<10)
				if old.Deployment == plan.ID {
					if old.Digest == hash && old.Base == base {
						continue
					}
					return errors.New("deployment_id_conflict")
				}
				if n.Status.Receipt == nil || n.Status.Receipt.CommandID != old.ID || (n.Status.Receipt.State != "active" && n.Status.Receipt.State != "canceled" && n.Status.Receipt.State != "failed" && n.Status.Receipt.State != "rejected" && n.Status.Receipt.State != "healthy" && n.Status.Receipt.State != "rolled_back") {
					return errors.New("deployment_in_progress")
				}
			}
			n.Sequence++
			cmd := Command{RequiresQuiesce: transition, Schema: 1, Kind: "config.prepare", Controller: c.Policy.ID, Epoch: c.Policy.Epoch, Project: p.Project, Installation: id, Role: n.Registration.Role, Observer: n.Registration.Observer, Session: n.Status.ConfigSession, ID: plan.ID + ".prepare", Deployment: plan.ID, Sequence: n.Sequence, Generation: base + 1, Base: base, Digest: hash, Length: int64(len(raw)), Issued: now, NotBefore: now, Expires: now.Add(15 * time.Minute), Capabilities: []string{"management.v1", "config.reviewed-contracts.v1"}}
			if !config.ID(cmd.ID) || n.Sequence > 1<<53-1 {
				return errors.New("command_identifier_limit")
			}
			env, e := Sign(ControlDomain, Marshal(cmd), c.key.ID, c.private)
			if e != nil {
				return e
			}
			n.Desired = &env
			c.state.Nodes[id] = n
			if e := c.putBlob(hash, raw); e != nil {
				return e
			}
		}
		c.pruneBlobs()
		return nil
	})
	if e != nil {
		managementJSON(w, 409, map[string]string{"error": e.Error()})
		return
	}
	managementJSON(w, 201, map[string]string{"deployment_id": plan.ID})
}

type Advance struct {
	ID      string   `json:"deployment_id"`
	Targets []string `json:"installations"`
}

func (c *Controller) advance(w http.ResponseWriter, r *http.Request, p ManagementPrincipal, now time.Time) {
	b, e := readLimit(r.Body, 32<<10)
	var a Advance
	if e != nil || Decode(b, &a, 32<<10) != nil || !config.ID(a.ID) || len(a.Targets) < 1 || len(a.Targets) > 64 {
		managementJSON(w, 400, map[string]string{"error": "invalid_deployment_activation"})
		return
	}
	kind := "config.activate"
	if strings.HasSuffix(r.URL.Path, "cancel") {
		kind = "config.cancel"
	}
	e = c.mutate(func() error {
		seen := map[string]bool{}
		for _, id := range a.Targets {
			n, ok := c.state.Nodes[id]
			if !ok || seen[id] || !slices.Contains(p.Installations, id) || n.Registration.Project != p.Project || n.Desired == nil {
				return errors.New("deployment_scope_denied")
			}
			seen[id] = true
			raw, _ := payloadBytes(*n.Desired, 32<<10)
			var cmd Command
			if Decode(raw, &cmd, 32<<10) != nil || cmd.Deployment != a.ID {
				return errors.New("deployment_not_found")
			}
			if cmd.Kind == kind && (kind != "config.cancel" || n.Status != nil && n.Status.ConfigSession == cmd.Session) {
				continue
			}
			if cmd.Kind != "config.prepare" && cmd.Kind != "config.quiesce" && !(kind == "config.cancel" && cmd.Kind == kind) {
				return errors.New("deployment_already_activated")
			}
			if kind == "config.cancel" {
				if n.Status == nil || !n.Status.ConfigAccept || !config.ID(n.Status.ConfigSession) || now.Sub(n.Received) > time.Minute {
					return errors.New("node_not_ready")
				}
				// Cancel is a new, separately authorized action. Reusing the
				// preparation's revoked session would strand the desired slot.
				cmd.Session = n.Status.ConfigSession
			}
			if kind == "config.activate" {
				if n.Status == nil || !n.Status.ConfigAccept || n.Status.ConfigSession != cmd.Session || now.Sub(n.Received) > time.Minute || n.PreparedReceipt == nil {
					return errors.New("node_not_prepared")
				}
				if cmd.RequiresQuiesce {
					if err := c.activationBarrier(a.ID, n.Registration.Role, p.Project, now); err != nil {
						return err
					}
					if n.Status.Receipt == nil || n.Status.Receipt.State != "quiesced" || !n.Status.QuiesceUntil.After(now.Add(5*time.Second)) {
						return errors.New("node_not_quiesced")
					}
				} else if n.Status.Receipt == nil || n.Status.Receipt.State != "prepared" || n.Status.Receipt.CommandID != cmd.ID {
					return errors.New("node_not_prepared")
				}
				cmd.Prepared = n.PreparedReceipt.Digest()
			}
			n.Sequence++
			cmd.Sequence = n.Sequence
			cmd.Kind = kind
			cmd.ID = a.ID + "." + strings.TrimPrefix(kind, "config.")
			cmd.Issued = now
			cmd.NotBefore = now
			cmd.Expires = now.Add(15 * time.Minute)
			if !config.ID(cmd.ID) || n.Sequence > 1<<53-1 {
				return errors.New("command_identifier_limit")
			}
			env, e := Sign(ControlDomain, Marshal(cmd), c.key.ID, c.private)
			if e != nil {
				return e
			}
			n.Desired = &env
			c.state.Nodes[id] = n
		}
		return nil
	})
	if e != nil {
		managementJSON(w, 409, map[string]string{"error": e.Error()})
		return
	}
	managementJSON(w, 200, map[string]string{"deployment_id": a.ID, "state": kind})
}
func (c *Controller) pruneBlobs() {
	used := map[string]bool{}
	for _, n := range c.state.Nodes {
		if n.Desired != nil {
			b, _ := payloadBytes(*n.Desired, 32<<10)
			var cmd Command
			_ = Decode(b, &cmd, 32<<10)
			used[cmd.Digest] = true
		}
	}
	for h := range c.state.Blobs {
		if !used[h] {
			delete(c.state.Blobs, h)
		}
	}
}
func ServeController(ctx context.Context, c *Controller) error {
	if _, e := ReadLocal(c.Policy.Key, 1<<20, false, true); e != nil {
		return e
	}
	server := &http.Server{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, Addr: c.Policy.Listen, Handler: c, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(stop)
		case <-done:
		}
	}()
	listener, e := net.Listen("tcp", c.Policy.Listen)
	if e != nil {
		return e
	}
	defer listener.Close()
	e = server.ServeTLS(&connectionBudget{Listener: listener, slots: make(chan struct{}, 16)}, c.Policy.Cert, c.Policy.Key)
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}

func (c *Controller) putBlob(hash string, raw []byte) error {
	if !HashOK(hash) || Digest(raw) != hash || len(raw) > 4<<20 {
		return errors.New("invalid_configuration_blob")
	}
	total := int64(len(raw))
	for id, n := range c.state.Blobs {
		if id != hash {
			total += n
		}
	}
	if total > 8<<20 {
		return errors.New("control_configuration_budget")
	}
	if e := WriteLocal(filepath.Join(c.Policy.Data, "blob-"+hash+".json"), raw); e != nil {
		return e
	}
	c.state.Blobs[hash] = int64(len(raw))
	return nil
}

// Garbage collect only before a new transaction: deleting an old blob during
// an uncommitted state mutation would make rollback to the previous snapshot unsafe.
func (c *Controller) collectOrphanBlobs() error {
	entries, e := os.ReadDir(c.Policy.Data)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) != 74 || !strings.HasPrefix(name, "blob-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		hash := name[5:69]
		if !HashOK(hash) {
			continue
		}
		if _, used := c.state.Blobs[hash]; !used {
			if e = os.Remove(filepath.Join(c.Policy.Data, name)); e != nil {
				return e
			}
		}
	}
	return nil
}
