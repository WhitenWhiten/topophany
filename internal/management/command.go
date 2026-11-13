package management

import (
	"errors"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
)

type Command struct {
	RequiresQuiesce bool      `json:"requires_quiesce,omitempty"`
	LeaseSeconds    int       `json:"lease_seconds,omitempty"`
	Schema          int       `json:"schema"`
	Kind            string    `json:"kind"`
	Controller      string    `json:"controller_id"`
	Epoch           uint64    `json:"authority_epoch"`
	Project         string    `json:"project"`
	Installation    string    `json:"installation_id"`
	Role            string    `json:"role"`
	Observer        string    `json:"observer,omitempty"`
	Session         string    `json:"acceptance_session"`
	ID              string    `json:"command_id"`
	Deployment      string    `json:"deployment_id"`
	Sequence        uint64    `json:"sequence"`
	Generation      uint64    `json:"desired_generation"`
	Base            uint64    `json:"base_generation"`
	Digest          string    `json:"payload_sha256"`
	Length          int64     `json:"payload_length"`
	Prepared        string    `json:"prepared_receipt,omitempty"`
	Issued          time.Time `json:"issued_at"`
	NotBefore       time.Time `json:"not_before"`
	Expires         time.Time `json:"expires_at"`
	Capabilities    []string  `json:"required_capabilities,omitempty"`
}

func (c Command) Channel() string { return strings.Split(c.Kind, ".")[0] }
func ParseCommand(e Envelope, p Policy, t Trust, now time.Time) (Command, error) {
	// Envelope payload is inspected only to select one closed capability, then
	// independently authenticated before any command fields can affect state.
	raw, err := payloadBytes(e, 32<<10)
	if err != nil {
		return Command{}, err
	}
	var c Command
	if err = Decode(raw, &c, 32<<10); err != nil {
		return c, err
	}
	switch c.Kind {
	case "config.prepare", "config.quiesce", "config.activate", "config.cancel", "update.prepare", "update.activate", "update.cancel":
	default:
		return c, errors.New("unsupported_management_command")
	}
	if err = p.Allows(c.Kind); err != nil {
		return c, err
	}
	if _, err = Verify(ControlDomain, c.Channel(), e, t, now, 32<<10); err != nil {
		return c, err
	}
	if c.Schema != 1 || c.Controller != p.Controller.ID || t.ID != c.Controller || t.Epoch != c.Epoch || c.Project != p.Project || c.Installation != p.Installation || c.Role != p.Role || c.Observer != p.Observer || c.Session != p.Session(c.Channel()) {
		return c, errors.New("command_scope_mismatch")
	}
	if !config.ID(c.ID) || !config.ID(c.Deployment) || c.Sequence == 0 || c.Sequence > 1<<53-1 || c.Generation == 0 || c.Generation > 1<<53-1 || c.Base >= c.Generation || !HashOK(c.Digest) || c.Length < 1 || c.Length > 4<<20 {
		return c, errors.New("invalid_management_command")
	}
	if c.Issued.IsZero() || c.NotBefore.Before(c.Issued) || c.Issued.After(now.Add(30*time.Second)) || now.Before(c.NotBefore) || !now.Before(c.Expires) || c.Expires.Sub(c.Issued) > 24*time.Hour || !c.Expires.After(c.NotBefore) {
		return c, errors.New("stale_command")
	}
	for _, cap := range c.Capabilities {
		if cap != "management.v1" && cap != "config.reviewed-contracts.v1" && cap != "update.github-release.v1" {
			return c, errors.New("unsupported_capability")
		}
	}
	if (strings.HasSuffix(c.Kind, ".activate") || c.Kind == "config.quiesce") && !HashOK(c.Prepared) {
		return c, errors.New("prepared_receipt_required")
	}
	if c.Kind == "config.quiesce" && (c.LeaseSeconds < 30 || c.LeaseSeconds > 600 || !c.RequiresQuiesce) {
		return c, errors.New("invalid_quiesce_lease")
	}
	return c, nil
}
