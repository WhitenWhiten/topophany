package management

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"slices"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// Constraints deliberately authorizes exact reviewed measurement contracts,
// not arbitrary URLs or environment names. Scheduling and presentation may change.
// Initial managed network probes require literal/pinned IPs and no proxy-side DNS.
type Constraints struct {
	RequiredChecks []string            `json:"required_checks"`
	CIDRs          []string            `json:"allowed_cidrs"`
	Bindings       map[string][]string `json:"execution_bindings"`
	Schema         int                 `json:"schema"`
	Project        string              `json:"project"`
	Observer       string              `json:"observer"`
	Contracts      map[string][]string `json:"contracts"`
	MinInterval    int                 `json:"min_interval_seconds"`
	MaxChecks      int                 `json:"max_checks"`
	MaxConcurrency int                 `json:"max_concurrency"`
	AllowDisable   bool                `json:"allow_disable"`
}

func ConstraintsFor(c model.Config, observer string, options ...Constraints) (Constraints, error) {
	v := Constraints{Schema: 1, Project: c.Project, Observer: observer, Contracts: map[string][]string{}, Bindings: map[string][]string{}, RequiredChecks: []string{}, MinInterval: 60, MaxChecks: 256, MaxConcurrency: 4}
	if len(options) > 0 {
		v.CIDRs = append([]string{}, options[0].CIDRs...)
		v.AllowDisable = options[0].AllowDisable
	}
	for _, x := range c.Checks {
		if observer == "" || x.Observer == observer {
			v.RequiredChecks = append(v.RequiredChecks, x.ID)
			v.Contracts[x.ID] = []string{config.Contract(c, x)}
			v.Bindings[x.ID] = []string{ExecutionBinding(c, x)}
			host, proxies, _ := targetHost(c, x)
			hosts := []string{host}
			for _, proxy := range proxies {
				h, _, _ := net.SplitHostPort(proxy.Address)
				hosts = append(hosts, h)
			}
			for _, host := range hosts {
				if ip, e := netip.ParseAddr(host); e == nil && ip.Zone() == "" {
					prefix := netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()).String()
					if !slices.Contains(v.CIDRs, prefix) {
						v.CIDRs = append(v.CIDRs, prefix)
					}
				}
			}
			v.MinInterval = min(v.MinInterval, x.IntervalSeconds)
		}
	}
	return v, v.Check(c, observer, 4)
}
func LoadConstraints(path string, system bool) (Constraints, error) {
	b, e := ReadLocal(path, 1<<20, system, false)
	if e != nil {
		return Constraints{}, e
	}
	var v Constraints
	if e = Decode(b, &v, 1<<20); e != nil {
		return v, e
	}
	return v, v.Validate()
}
func (v Constraints) Validate() error {
	if v.Schema != 1 || !config.ID(v.Project) || (v.Observer != "" && !config.ID(v.Observer)) || len(v.Contracts) > 4096 || v.MinInterval < 1 || v.MinInterval > 86400 || v.MaxChecks < 1 || v.MaxChecks > 4096 || v.MaxConcurrency < 1 || v.MaxConcurrency > 32 {
		return errors.New("invalid_config_constraints")
	}
	if len(v.RequiredChecks) > 4096 || len(v.CIDRs) > 256 || len(v.Bindings) > 4096 {
		return errors.New("configuration_policy_budget")
	}
	for _, cidr := range v.CIDRs {
		if _, e := netip.ParsePrefix(cidr); e != nil {
			return errors.New("invalid_network_scope")
		}
	}
	for id, hashes := range v.Contracts {
		if !config.ID(id) || len(hashes) == 0 || len(hashes) > 8 {
			return errors.New("invalid_contract_allowlist")
		}
		for _, h := range hashes {
			if !HashOK(h) {
				return errors.New("invalid_contract_allowlist")
			}
		}
	}
	return nil
}
func (v Constraints) Check(c model.Config, observer string, concurrency int) error {
	if e := v.Validate(); e != nil {
		return e
	}
	if c.Project != v.Project || observer != v.Observer || concurrency > v.MaxConcurrency {
		return errors.New("configuration_policy_scope")
	}
	if e := config.Validate(c); e != nil {
		return errors.New("invalid_remote_configuration")
	}
	if config.HasLiteralSecret(c) {
		return errors.New("remote_literal_secrets_denied")
	}
	count := 0
	present := make(map[string]bool, len(c.Checks))
	for _, x := range c.Checks {
		if observer != "" && x.Observer != observer {
			continue
		}
		count++
		present[x.ID] = true
		if !slices.Contains(v.Contracts[x.ID], config.Contract(c, x)) {
			return errors.New("measurement_contract_not_authorized")
		}
		if x.IntervalSeconds < v.MinInterval {
			return errors.New("remote_probe_budget_exceeded")
		}
		if !v.AllowDisable && (x.Disabled || x.Maintenance) {
			return errors.New("remote_disablement_denied")
		}
		if observer != "" {
			if !slices.Contains(v.Bindings[x.ID], ExecutionBinding(c, x)) {
				return errors.New("secret_binding_denied")
			}
			if e := v.Transport(c, x); e != nil {
				return e
			}
		}
	}
	if count > v.MaxChecks {
		return errors.New("remote_probe_budget_exceeded")
	}
	if !v.AllowDisable {
		required := v.RequiredChecks
		if required == nil {
			for id := range v.Contracts {
				required = append(required, id)
			}
		}
		for _, id := range required {
			if !present[id] {
				return errors.New("remote_check_removal_denied")
			}
		}
	}
	return nil
}

func (v *Constraints) Merge(other Constraints) error {
	if v.Project != other.Project || v.Observer != other.Observer {
		return errors.New("reviewed_topology_scope_mismatch")
	}
	for id, hashes := range other.Contracts {
		for _, h := range hashes {
			if !slices.Contains(v.Contracts[id], h) {
				v.Contracts[id] = append(v.Contracts[id], h)
			}
		}
	}
	for id, hashes := range other.Bindings {
		for _, h := range hashes {
			if !slices.Contains(v.Bindings[id], h) {
				v.Bindings[id] = append(v.Bindings[id], h)
			}
		}
	}
	for _, cidr := range other.CIDRs {
		if !slices.Contains(v.CIDRs, cidr) {
			v.CIDRs = append(v.CIDRs, cidr)
		}
	}
	v.MinInterval = min(v.MinInterval, other.MinInterval)
	return v.Validate()
}
func SecretsAvailable(c model.Config, observer string) error {
	if observer == "" {
		return nil
	}
	names := map[string]bool{}
	for _, x := range c.Checks {
		if x.Observer != observer || x.Disabled || x.Maintenance {
			continue
		}
		names[x.HTTP.BearerEnv] = true
		names[x.Adapter.TokenEnv] = true
		for _, name := range x.HTTP.RequestHeaderEnvs {
			names[name] = true
		}
		for _, proxy := range config.Path(c, x.Path).Proxies {
			names[proxy.PasswordEnv] = true
		}
		if x.External != nil {
			if src, ok := config.Source(c, x.External.Source); ok {
				names[src.CredentialEnv] = true
				for _, proxy := range config.Path(c, src.Path).Proxies {
					names[proxy.PasswordEnv] = true
				}
			}
		}
	}
	for name := range names {
		if name != "" && os.Getenv(name) == "" {
			return errors.New("management_secret_unavailable")
		}
	}
	return nil
}
