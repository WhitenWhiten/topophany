// Package importer builds inert review candidates from local inventory files.
// It never executes source directives, resolves names, or opens network sockets.
package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Result struct {
	Format       string       `json:"format"`
	SourceSHA256 string       `json:"source_sha256"`
	Warnings     []string     `json:"warnings"`
	Config       model.Config `json:"topology"`
}

func identity(kind, name string) string {
	h := sha256.Sum256([]byte(kind + "\x00" + name))
	return kind + "-" + hex.EncodeToString(h[:8])
}
func safeLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 32 && r != 127 {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 120 {
		return "Imported resource"
	}
	return out
}
func Build(format string, raw []byte, host string) (Result, error) {
	var r Result
	if len(raw) > 4<<20 {
		return r, errors.New("import source exceeds 4 MiB")
	}
	digest := sha256.Sum256(raw)
	r.Format = format
	r.SourceSHA256 = hex.EncodeToString(digest[:])
	r.Config = model.Config{Schema: 1, Project: "import-" + format, Observers: []model.Observer{{ID: "review", Name: "Review execution context", Context: "Must be assigned to the intended executor before enabling checks"}}}
	r.Warnings = []string{"offline_candidate_not_observed", "all_checks_paused_review_before_enabling"}
	var err error
	switch format {
	case "ssh":
		err = r.ssh(raw)
	case "compose":
		err = r.compose(raw, host)
	case "tailscale":
		err = r.tailscale(raw)
	default:
		err = errors.New("format must be ssh, compose or tailscale")
	}
	if err != nil {
		return Result{}, err
	}
	if len(r.Config.Nodes) == 0 {
		return Result{}, errors.New("no supported inventory found")
	}
	for i := range r.Config.Nodes {
		r.Config.Nodes[i].Notes += " Source " + format + " SHA-256 " + r.SourceSHA256 + "; reviewed candidate, not measured."
	}
	sort.Slice(r.Config.Nodes, func(i, j int) bool { return r.Config.Nodes[i].ID < r.Config.Nodes[j].ID })
	config.Defaults(&r.Config)
	if err = config.Validate(r.Config); err != nil {
		return Result{}, fmt.Errorf("import candidate validation: %w", err)
	}
	return r, nil
}
func (r *Result) node(kind, name string) string {
	id := identity(kind, name)
	r.Config.Nodes = append(r.Config.Nodes, model.Node{ID: id, Name: safeLabel(name), Kind: "host"})
	return id
}
func (r *Result) check(node, name, address, kind string) {
	id := identity("check", node+"/"+name)
	r.Config.Checks = append(r.Config.Checks, model.Check{ID: id, Name: safeLabel(name), Observer: "review", Kind: kind, Target: address, Service: identity("service", node), Disabled: true, Notes: "Imported target declaration; execution context and route require review."})
}
func (r *Result) service(node, name string) {
	r.Config.Services = append(r.Config.Services, model.Service{ID: identity("service", node), Name: safeLabel(name), Node: node, Notes: "Inventory does not establish service availability."})
}
func hostOK(s string) bool {
	if s == "" || len(s) > 253 || strings.ContainsAny(s, " \t\r\n/%$*?!@\\\"'") {
		return false
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func (r *Result) ssh(raw []byte) error {
	type block struct {
		aliases    []string
		host, port string
		blocked    bool
	}
	var current block
	// OpenSSH takes the first value of each option across every matching
	// block. Collect declarations before emitting a target so later blocks
	// can supply a missing port or prohibit an otherwise direct route.
	hosts := map[string]block{}
	order := []string{}
	flush := func() error {
		for _, alias := range current.aliases {
			if !hostOK(alias) {
				continue
			}
			previous, exists := hosts[alias]
			if !exists {
				if len(hosts) >= 1024 {
					return errors.New("SSH host budget exceeded")
				}
				order = append(order, alias)
			}
			if previous.host == "" {
				previous.host = current.host
			}
			if previous.port == "" {
				previous.port = current.port
			}
			previous.blocked = previous.blocked || current.blocked
			hosts[alias] = previous
		}
		return nil
	}
	if len(strings.Split(string(raw), "\n")) > 10000 {
		return errors.New("SSH line budget exceeded")
	}
	globalBlocked := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		words := strings.Fields(strings.Replace(line, "=", " ", 1))
		if len(words) < 2 {
			continue
		}
		key := strings.ToLower(words[0])
		switch key {
		case "host":
			if err := flush(); err != nil {
				return err
			}
			current = block{aliases: words[1:], blocked: globalBlocked}
			for _, alias := range current.aliases {
				if !hostOK(alias) {
					globalBlocked = true
					current.blocked = true
					r.Warnings = append(r.Warnings, "ssh_patterns_not_expanded")
				}
			}
		case "match", "include":
			globalBlocked = true
			current.blocked = true
			r.Warnings = append(r.Warnings, "ssh_conditional_or_include_not_executed")
		case "hostname":
			if len(current.aliases) == 0 {
				globalBlocked = true
			}
			if current.host == "" {
				current.host = words[1]
			}
		case "port":
			if len(current.aliases) == 0 {
				globalBlocked = true
			}
			if current.port == "" {
				current.port = words[1]
			}
		case "proxycommand", "proxyjump":
			current.blocked = true
			if len(current.aliases) == 0 {
				globalBlocked = true
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	for _, alias := range order {
		host := hosts[alias]
		node := r.node("ssh", alias)
		r.service(node, alias)
		if globalBlocked {
			continue
		}
		target, port := host.host, host.port
		if target == "" {
			target = alias
		}
		if port == "" {
			port = "22"
		}
		n, err := strconv.Atoi(port)
		if host.blocked || !hostOK(target) || err != nil || n < 1 || n > 65535 {
			r.Warnings = append(r.Warnings, "ssh_target_requires_manual_route_review")
			continue
		}
		r.check(node, "SSH banner "+alias, net.JoinHostPort(target, port), "ssh-banner")
	}
	// Unsupported global/conditional sections may affect earlier blocks too.
	if globalBlocked {
		r.Config.Checks = nil
		r.Warnings = append(r.Warnings, "ssh_ambiguous_context_inventory_only")
	}
	return nil
}
func (r *Result) compose(raw []byte, host string) error {
	if !hostOK(host) {
		return errors.New("Compose requires --host with the reviewed deployment host")
	}
	if secure.JSONBudget(raw) != nil {
		return errors.New("Compose input must be bounded rendered JSON")
	}
	var data struct {
		Services map[string]struct {
			Ports []json.RawMessage `json:"ports"`
		} `json:"services"`
	}
	if json.Unmarshal(raw, &data) != nil || len(data.Services) > 1024 {
		return errors.New("invalid Compose service inventory")
	}
	names := make([]string, 0, len(data.Services))
	for name := range data.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		node := r.node("compose", name)
		r.Config.Nodes[len(r.Config.Nodes)-1].Kind = "container"
		r.service(node, name)
		ports := data.Services[name].Ports
		if len(ports) > 128 {
			return errors.New("Compose port budget exceeded")
		}
		seen := map[string]bool{}
		for _, rawPort := range ports {
			var p struct {
				Published json.RawMessage `json:"published"`
				Protocol  string          `json:"protocol"`
				HostIP    string          `json:"host_ip"`
			}
			var text string
			port, proto, bind := "", "tcp", ""
			if json.Unmarshal(rawPort, &text) == nil {
				parts := strings.Split(text, "/")
				if len(parts) == 2 {
					proto = parts[1]
				} else if len(parts) > 2 {
					continue
				}
				mapping := strings.Split(parts[0], ":")
				if len(mapping) == 2 {
					port = mapping[0]
				} else if len(mapping) == 3 {
					bind, port = mapping[0], mapping[1]
				} else {
					r.Warnings = append(r.Warnings, "compose_unpublished_or_complex_port_skipped")
					continue
				}
			} else if json.Unmarshal(rawPort, &p) == nil {
				if json.Unmarshal(p.Published, &port) != nil {
					port = string(p.Published)
				}
				if p.Protocol != "" {
					proto = p.Protocol
				}
				bind = p.HostIP
			}
			n, err := strconv.Atoi(port)
			if proto != "tcp" || err != nil || n < 1 || n > 65535 || bind != "" && bind != "0.0.0.0" && bind != "::" && bind != host {
				r.Warnings = append(r.Warnings, "compose_port_requires_manual_review")
				continue
			}
			if seen[port] {
				continue
			}
			seen[port] = true
			r.check(node, name+" published TCP "+port, net.JoinHostPort(host, port), "tcp")
		}
	}
	r.Warnings = append(r.Warnings, "compose_host_declared_not_discovered", "compose_environment_and_healthcheck_not_imported")
	return nil
}
func (r *Result) tailscale(raw []byte) error {
	if secure.JSONBudget(raw) != nil {
		return errors.New("invalid bounded Tailscale JSON")
	}
	type peer struct {
		HostName     string   `json:"HostName"`
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	}
	var data struct {
		Peer map[string]peer `json:"Peer"`
		Self *peer           `json:"Self"`
	}
	if json.Unmarshal(raw, &data) != nil || len(data.Peer) > 1024 {
		return errors.New("invalid Tailscale inventory")
	}
	keys := make([]string, 0, len(data.Peer))
	for key := range data.Peer {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p := data.Peer[key]
		node := r.node("tailnet", key)
		name := p.HostName
		if name == "" {
			name = p.DNSName
		}
		if name == "" {
			name = "Tailnet peer"
		}
		r.Config.Nodes[len(r.Config.Nodes)-1].Name = safeLabel(name)
		seen := map[string]bool{}
		for _, address := range p.TailscaleIPs {
			ip, err := netip.ParseAddr(address)
			if err != nil || seen[address] {
				continue
			}
			seen[address] = true
			r.Config.Endpoints = append(r.Config.Endpoints, model.Endpoint{ID: identity("endpoint", node+ip.String()), Node: node, Address: ip.String(), Network: "tailnet", Notes: "Reported inventory address only; no service port or reachability inferred."})
		}
	}
	r.Warnings = append(r.Warnings, "tailscale_online_is_not_service_health", "tailscale_no_service_ports_inferred")
	return nil
}
