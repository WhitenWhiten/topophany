package management

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func (c *Controller) requiresTransition(plan ConfigDeployment, cfg model.Config, p ManagementPrincipal, now time.Time) (bool, error) {
	transition := false
	selected := map[string]bool{}
	for _, id := range plan.Targets {
		n, ok := c.state.Nodes[id]
		if !ok || selected[id] || !slices.Contains(p.Installations, id) || n.Registration.Project != p.Project {
			return false, errors.New("deployment_scope_denied")
		}
		selected[id] = true
		if n.Status == nil || now.Sub(n.Received) > time.Minute || !HashOK(n.Status.Contracts) {
			return false, errors.New("node_contract_inventory_required")
		}
		projection, e := Project(cfg, n.Registration.Observer, n.Registration.Role)
		if e != nil {
			return false, e
		}
		transition = transition || n.Status.Contracts != ContractDigest(projection, n.Registration.Role, n.Registration.Observer)
	}
	if !transition {
		return false, nil
	}
	if !c.Policy.InventoryComplete {
		return false, errors.New("contract_change_requires_complete_inventory")
	}
	collectors := map[string]bool{}
	for id, n := range c.state.Nodes {
		if n.Registration.Project != p.Project {
			continue
		}
		if !selected[id] {
			return false, errors.New("contract_change_requires_complete_project_cohort")
		}
		if n.Status == nil || now.Sub(n.Received) > time.Minute || !n.Status.ConfigAccept {
			return false, errors.New("required_node_not_ready")
		}
		if n.Registration.Role == "collector" && n.Status.CollectorID != "" {
			collectors[n.Status.CollectorID] = true
		}
	}
	if len(collectors) == 0 {
		return false, errors.New("contract_change_requires_managed_collectors")
	}
	for id := range selected {
		n := c.state.Nodes[id]
		if n.Registration.Role != "worker" {
			continue
		}
		if len(n.Status.Collectors) == 0 {
			return false, errors.New("worker_collector_inventory_required")
		}
		for _, receiver := range n.Status.Collectors {
			if !collectors[receiver] {
				return false, errors.New("unmanaged_observation_recipient")
			}
		}
	}
	return true, nil
}
func commandFor(n NodeRecord) (Command, error) {
	if n.Desired == nil {
		return Command{}, errors.New("deployment_not_found")
	}
	raw, e := payloadBytes(*n.Desired, 32<<10)
	if e != nil {
		return Command{}, e
	}
	var cmd Command
	e = Decode(raw, &cmd, 32<<10)
	return cmd, e
}
func (c *Controller) quiesce(w http.ResponseWriter, r *http.Request, p ManagementPrincipal, now time.Time) {
	raw, e := readLimit(r.Body, 32<<10)
	var a Advance
	if e != nil || Decode(raw, &a, 32<<10) != nil || !config.ID(a.ID) || len(a.Targets) == 0 || len(a.Targets) > 64 {
		managementJSON(w, 400, map[string]string{"error": "invalid_quiesce_request"})
		return
	}
	e = c.mutate(func() error {
		// The exact prepared cohort must be named; partial pause is not a barrier.
		for id, n := range c.state.Nodes {
			cmd, err := commandFor(n)
			if err == nil && cmd.Deployment == a.ID && n.Registration.Project == p.Project {
				if !slices.Contains(a.Targets, id) {
					return errors.New("complete_cohort_required")
				}
			}
		}
		seen := map[string]bool{}
		for _, id := range a.Targets {
			n, ok := c.state.Nodes[id]
			if !ok || seen[id] || !slices.Contains(p.Installations, id) || n.Registration.Project != p.Project {
				return errors.New("deployment_scope_denied")
			}
			seen[id] = true
			cmd, e := commandFor(n)
			if e != nil || cmd.Deployment != a.ID || !cmd.RequiresQuiesce {
				return errors.New("deployment_not_prepared_for_transition")
			}
			if cmd.Kind == "config.quiesce" {
				continue
			}
			if cmd.Kind != "config.prepare" || n.Status == nil || !n.Status.ConfigAccept || n.Status.ConfigSession != cmd.Session || n.PreparedReceipt == nil || n.Status.Receipt == nil || n.Status.Receipt.State != "prepared" || now.Sub(n.Received) > time.Minute {
				return errors.New("required_node_not_prepared")
			}
		}
		for _, id := range a.Targets {
			n := c.state.Nodes[id]
			cmd, _ := commandFor(n)
			if cmd.Kind == "config.quiesce" {
				continue
			}
			n.Sequence++
			cmd.Kind = "config.quiesce"
			cmd.ID = a.ID + ".quiesce"
			cmd.Sequence = n.Sequence
			cmd.Prepared = n.PreparedReceipt.Digest()
			cmd.LeaseSeconds = 180
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
	managementJSON(w, 200, map[string]string{"deployment_id": a.ID, "state": "quiescing"})
}
func (c *Controller) activationBarrier(deployment, role, project string, now time.Time) error {
	found := false
	for _, n := range c.state.Nodes {
		cmd, e := commandFor(n)
		if e != nil || cmd.Deployment != deployment || n.Registration.Project != project {
			continue
		}
		found = true
		if n.Status == nil || n.Status.Receipt == nil || now.Sub(n.Received) > time.Minute {
			return errors.New("cohort_status_stale")
		}
		state := n.Status.Receipt.State
		if n.Registration.Role == "collector" && role == "worker" {
			if state != "active" || cmd.Kind != "config.activate" || n.Status.Receipt.CommandID != cmd.ID {
				return errors.New("collectors_must_activate_first")
			}
			continue
		}
		if state == "active" && cmd.Kind == "config.activate" {
			continue
		}
		if state != "quiesced" || !n.Status.QuiesceUntil.After(now.Add(5*time.Second)) {
			return errors.New("cohort_not_quiesced")
		}
	}
	if !found {
		return errors.New("deployment_not_found")
	}
	return nil
}
