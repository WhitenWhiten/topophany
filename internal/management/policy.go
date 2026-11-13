package management

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/config"
)

type ControllerRef struct {
	ID               string `json:"id"`
	URL              string `json:"url"`
	CAFile           string `json:"ca_file"`
	CredentialFile   string `json:"credential_file"`
	CommandTrustFile string `json:"command_trust_file"`
}
type ConfigPolicy struct {
	Accept          bool   `json:"accept"`
	ConstraintsFile string `json:"constraints_file,omitempty"`
	Session         string `json:"session,omitempty"`
}
type UpdatePolicy struct {
	Accept             bool   `json:"accept"`
	PolicyFile         string `json:"policy_file,omitempty"`
	PublisherTrustFile string `json:"publisher_trust_file,omitempty"`
	Session            string `json:"session,omitempty"`
}
type Policy struct {
	Schema       int           `json:"schema"`
	System       bool          `json:"system,omitempty"`
	Revision     uint64        `json:"revision,omitempty"`
	Project      string        `json:"project,omitempty"`
	Installation string        `json:"installation_id,omitempty"`
	Role         string        `json:"role,omitempty"`
	Observer     string        `json:"observer,omitempty"`
	Controller   ControllerRef `json:"controller,omitempty"`
	Config       ConfigPolicy  `json:"config"`
	Updates      UpdatePolicy  `json:"updates"`
	Report       bool          `json:"report_management_status"`
}

func Disabled() Policy         { return Policy{Schema: 1} }
func (p Policy) Network() bool { return p.Config.Accept || p.Updates.Accept || p.Report }
func (p Policy) Validate() error {
	if p.Schema != 1 {
		return errors.New("unsupported_management_policy")
	}
	if !p.Network() {
		return nil
	}
	if !config.ID(p.Project) || !config.ID(p.Installation) || !config.ID(p.Controller.ID) || (p.Role != "worker" && p.Role != "collector") {
		return errors.New("invalid_management_identity")
	}
	if p.Role == "worker" && !config.ID(p.Observer) {
		return errors.New("invalid_management_observer")
	}
	u, e := url.Parse(p.Controller.URL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("management_requires_https_origin")
	}
	for _, v := range []string{p.Controller.CredentialFile, p.Controller.CommandTrustFile} {
		if !filepath.IsAbs(v) {
			return errors.New("management_trust_path_required")
		}
	}
	if p.Controller.CAFile != "" && !filepath.IsAbs(p.Controller.CAFile) {
		return errors.New("management_ca_path_invalid")
	}
	if p.Config.Accept && (!filepath.IsAbs(p.Config.ConstraintsFile) || !config.ID(p.Config.Session)) {
		return errors.New("configuration_enrollment_required")
	}
	if p.Updates.Accept && (!filepath.IsAbs(p.Updates.PolicyFile) || !filepath.IsAbs(p.Updates.PublisherTrustFile) || !config.ID(p.Updates.Session)) {
		return errors.New("update_enrollment_required")
	}
	return nil
}
func LoadPolicy(path string) (Policy, error) {
	if path == "" {
		return Disabled(), nil
	}
	b, e := ReadLocal(path, 64<<10, false, false)
	if os.IsNotExist(e) {
		return Disabled(), nil
	}
	if e != nil {
		return Disabled(), e
	}
	var p Policy
	if e = Decode(b, &p, 64<<10); e != nil {
		return Disabled(), e
	}
	if p.System {
		if _, e = ReadLocal(path, 64<<10, true, false); e != nil {
			return Disabled(), e
		}
	}
	if e = p.Validate(); e != nil {
		return Disabled(), e
	}
	return p, nil
}
func (p Policy) Session(channel string) string {
	if channel == "config" {
		return p.Config.Session
	}
	if channel == "update" {
		return p.Updates.Session
	}
	return ""
}
func (p Policy) Allows(kind string) error {
	switch strings.Split(kind, ".")[0] {
	case "config":
		if !p.Config.Accept {
			return errors.New("remote_config_disabled")
		}
	case "update":
		if !p.Updates.Accept {
			return errors.New("remote_updates_disabled")
		}
	default:
		return errors.New("unsupported_management_command")
	}
	return nil
}
