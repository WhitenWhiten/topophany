package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
)

type UpdateDeployment struct {
	ID       string                     `json:"deployment_id"`
	Targets  []string                   `json:"installations"`
	Bases    map[string]uint64          `json:"base_generations"`
	Payloads map[string]json.RawMessage `json:"targets"`
}

func (c *Controller) publishUpdate(w http.ResponseWriter, r *http.Request, p ManagementPrincipal, now time.Time) {
	if c.VerifyUpdate == nil || !c.Policy.InventoryComplete {
		managementJSON(w, 409, map[string]string{"error": "publisher_trust_not_configured"})
		return
	}
	b, e := readLimit(r.Body, 1<<20)
	var plan UpdateDeployment
	if e != nil || Decode(b, &plan, 1<<20) != nil || !config.ID(plan.ID) || len(plan.Targets) < 1 || len(plan.Targets) > 16 {
		managementJSON(w, 400, map[string]string{"error": "invalid_update_deployment"})
		return
	}
	verifyCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	for _, id := range plan.Targets {
		n, ok := c.state.Nodes[id]
		if !ok || !slices.Contains(p.Installations, id) || n.Registration.Project != p.Project || n.Status == nil || len(plan.Payloads[id]) == 0 || len(plan.Payloads[id]) > 32<<10 {
			managementJSON(w, 409, map[string]string{"error": "update_scope_denied"})
			return
		}
		if err := c.verifyOutsideLock(r, verifyCtx, plan.Payloads[id], *n.Status, p.Project); err != nil {
			managementJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
	}
	now = time.Now().UTC()
	e = c.mutate(func() error {
		seen := map[string]bool{}
		for _, id := range plan.Targets {
			n, ok := c.state.Nodes[id]
			base, hasBase := plan.Bases[id]
			raw := plan.Payloads[id]
			if !ok || seen[id] || !slices.Contains(p.Installations, id) || n.Registration.Project != p.Project || !hasBase || base != n.UpdateGeneration || len(raw) == 0 || len(raw) > 32<<10 {
				return errors.New("update_scope_or_generation_conflict")
			}
			seen[id] = true
			if n.Status == nil || now.Sub(n.Received) > time.Minute || !n.Status.UpdateAccept || !config.ID(n.Status.UpdateSession) {
				return errors.New("node_not_ready")
			}
			hash := Digest(raw)
			if n.Desired != nil {
				before, _ := payloadBytes(*n.Desired, 32<<10)
				var previous Command
				_ = Decode(before, &previous, 32<<10)
				if previous.Deployment == plan.ID {
					if previous.Digest == hash && previous.Base == base {
						continue
					}
					return errors.New("deployment_id_conflict")
				}
				if n.Status.Receipt == nil || n.Status.Receipt.CommandID != previous.ID || n.Status.Receipt.CommandDigest != Digest(Marshal(n.Desired)) || !slices.Contains([]string{"active", "healthy", "failed", "rejected", "canceled", "rolled_back"}, n.Status.Receipt.State) {
					return errors.New("deployment_in_progress")
				}
			}
			var targetVersion struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(raw, &targetVersion) != nil {
				return errors.New("invalid_update_target")
			}
			n.TargetVersion = targetVersion.Version
			n.HealthySince = time.Time{}
			n.Sequence++
			cmd := Command{Schema: 1, Kind: "update.prepare", Controller: c.Policy.ID, Epoch: c.Policy.Epoch, Project: p.Project, Installation: id, Role: n.Registration.Role, Observer: n.Registration.Observer, Session: n.Status.UpdateSession, ID: plan.ID + ".prepare", Deployment: plan.ID, Sequence: n.Sequence, Generation: base + 1, Base: base, Digest: hash, Length: int64(len(raw)), Issued: now, NotBefore: now, Expires: now.Add(15 * time.Minute), Capabilities: []string{"management.v1", "update.github-release.v1"}}
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
func (c *Controller) advanceUpdate(w http.ResponseWriter, r *http.Request, p ManagementPrincipal, now time.Time) {
	b, e := readLimit(r.Body, 32<<10)
	var a Advance
	if e != nil || Decode(b, &a, 32<<10) != nil || !config.ID(a.ID) || len(a.Targets) < 1 || len(a.Targets) > 16 {
		managementJSON(w, 400, map[string]string{"error": "invalid_update_activation"})
		return
	}
	kind := "update.activate"
	if strings.HasSuffix(r.URL.Path, "cancel") {
		kind = "update.cancel"
	}
	// Activation is intentionally one node at a time until its authenticated
	// helper completion is observed; callers can stage a fleet and advance canaries.
	if kind == "update.activate" && len(a.Targets) != 1 {
		managementJSON(w, 409, map[string]string{"error": "activate_one_update_at_a_time"})
		return
	}
	if kind == "update.activate" {
		n, ok := c.state.Nodes[a.Targets[0]]
		cmd, err := commandFor(n)
		if !ok || err != nil || !slices.Contains(p.Installations, a.Targets[0]) || n.Registration.Project != p.Project || n.Status == nil || cmd.Deployment != a.ID || cmd.Channel() != "update" || c.VerifyUpdate == nil || !c.Policy.InventoryComplete {
			managementJSON(w, 409, map[string]string{"error": "update_scope_denied"})
			return
		}
		if cmd.Kind == kind {
			managementJSON(w, 200, map[string]string{"deployment_id": a.ID, "state": kind})
			return
		}
		raw, err := ReadLocal(filepath.Join(c.Policy.Data, "blob-"+cmd.Digest+".json"), 32<<10, false, true)
		if err != nil || Digest(raw) != cmd.Digest {
			managementJSON(w, 409, map[string]string{"error": "update_preflight_unavailable"})
			return
		}
		check, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		err = c.verifyOutsideLock(r, check, raw, *n.Status, p.Project)
		cancel()
		if err != nil {
			managementJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		now = time.Now().UTC()
	}
	e = c.mutate(func() error {
		for _, id := range a.Targets {
			n, ok := c.state.Nodes[id]
			if !ok || !slices.Contains(p.Installations, id) || n.Registration.Project != p.Project || n.Desired == nil {
				return errors.New("update_scope_denied")
			}
			raw, _ := payloadBytes(*n.Desired, 32<<10)
			var cmd Command
			if Decode(raw, &cmd, 32<<10) != nil || cmd.Deployment != a.ID || cmd.Channel() != "update" {
				return errors.New("update_not_found")
			}
			if cmd.Kind == kind && (kind != "update.cancel" || n.Status != nil && n.Status.UpdateSession == cmd.Session) {
				continue
			}
			if cmd.Kind != "update.prepare" && !(kind == "update.cancel" && cmd.Kind == kind) {
				return errors.New("update_already_activated")
			}
			if kind == "update.cancel" {
				if n.Status == nil || !n.Status.UpdateAccept || !config.ID(n.Status.UpdateSession) || now.Sub(n.Received) > time.Minute {
					return errors.New("node_not_ready")
				}
				cmd.Session = n.Status.UpdateSession
			}
			if kind == "update.activate" {
				for otherID, other := range c.state.Nodes {
					if otherID == id || other.Desired == nil {
						continue
					}
					b, _ := payloadBytes(*other.Desired, 32<<10)
					var pending Command
					_ = Decode(b, &pending, 32<<10)
					if pending.Kind == "update.activate" && (other.Status == nil || other.Status.Receipt == nil || !slices.Contains([]string{"healthy", "failed", "rejected", "rolled_back", "manual_recovery_required"}, other.Status.Receipt.State)) {
						return errors.New("another_update_is_active")
					}
				}
				if n.Status == nil || !n.Status.UpdateAccept || n.Status.UpdateSession != cmd.Session || now.Sub(n.Received) > time.Minute || n.Status.Receipt == nil || n.Status.Receipt.State != "prepared" || n.Status.Receipt.CommandID != cmd.ID {
					return errors.New("node_not_prepared")
				}
				for otherID, other := range c.state.Nodes {
					if otherID == id || other.Registration.Project != p.Project || other.Desired == nil {
						continue
					}
					pending, _ := commandFor(other)
					if pending.Deployment != a.ID || pending.Kind != "update.activate" {
						continue
					}
					if other.Status == nil || other.Status.Receipt == nil {
						return errors.New("deployment_status_unknown")
					}
					if other.Status.Receipt.State != "healthy" {
						return errors.New("deployment_paused_after_failure")
					}
					if now.Sub(other.Received) > time.Minute || other.HealthySince.IsZero() || now.Sub(other.HealthySince) < time.Duration(c.Policy.ObservationSeconds)*time.Second {
						return errors.New("canary_observation_in_progress")
					}
				}
				cmd.Prepared = n.Status.Receipt.Digest()
			}
			n.Sequence++
			cmd.Sequence = n.Sequence
			cmd.Kind = kind
			cmd.ID = a.ID + "." + strings.TrimPrefix(kind, "update.")
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

func (c *Controller) inventory(project string) []NodeRecord {
	out := []NodeRecord{}
	for _, n := range c.state.Nodes {
		if n.Registration.Project == project {
			out = append(out, n)
		}
	}
	return out
}

// Only the HTTP dispatcher owns the state mutex; direct unit callers are serial.
type controllerLockKey struct{}

func (c *Controller) verificationState() string {
	var state controllerState
	_ = Decode(Marshal(c.state), &state, 32<<20)
	for id, n := range state.Nodes {
		n.Received = time.Time{}
		if n.Status != nil {
			n.Status.At = time.Time{}
		}
		state.Nodes[id] = n
	}
	return Digest(Marshal(state))
}
func (c *Controller) verifyOutsideLock(r *http.Request, ctx context.Context, raw []byte, status NodeStatus, project string) error {
	before := c.verificationState()
	actor, installation := c.actor, c.actorInstallation
	var inventory []NodeRecord
	if err := Decode(Marshal(c.inventory(project)), &inventory, 32<<20); err != nil {
		return err
	}
	locked, _ := r.Context().Value(controllerLockKey{}).(bool)
	err := func() error {
		if locked {
			c.mu.Unlock()
			defer c.mu.Lock()
		}
		return c.VerifyUpdate(ctx, raw, status, inventory)
	}()
	c.actor, c.actorInstallation = actor, installation
	if c.failed || before != c.verificationState() {
		return errors.New("update_state_changed_retry")
	}
	if err != nil {
		return errors.New("release_preflight_failed")
	}
	return ctx.Err()
}
