// Package config loads a strict, versioned JSON topology. No remote configuration
// is executed: each executor uses its own reviewed local copy.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"unicode"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)
var sshUser = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

func ID(s string) bool { return identifier.MatchString(s) }
func Decode(r io.Reader) (model.Config, error) {
	var c model.Config
	b, err := io.ReadAll(io.LimitReader(r, 4<<20+1))
	if err != nil {
		return c, err
	}
	if len(b) > 4<<20 {
		return c, errors.New("topology exceeds 4 MiB")
	}
	if err := secure.JSONBudget(b); err != nil {
		return c, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid topology JSON: %w", err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return c, errors.New("topology must contain one JSON object")
	}
	Defaults(&c)
	return c, Validate(c)
}
func Load(path string) (model.Config, error) {
	st, e := os.Lstat(path)
	if e != nil {
		return model.Config{}, e
	}
	f, e := os.Open(path)
	if e != nil {
		return model.Config{}, e
	}
	defer f.Close()
	c, e := Decode(f)
	if e != nil {
		return c, e
	}
	if HasLiteralSecret(c) {
		if !st.Mode().IsRegular() {
			return model.Config{}, errors.New("topology with secrets must be a regular non-symlink file")
		}
		if runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
			return model.Config{}, errors.New("topology with secrets must be 0600 or stricter")
		}
	}
	return c, nil
}
func Defaults(c *model.Config) {
	defaultSources(c)
	for i := range c.Checks {
		sourceSchedule(*c, &c.Checks[i])
	}
	for i := range c.Observers {
		if c.Observers[i].StaleSeconds == 0 {
			c.Observers[i].StaleSeconds = 90
		}
	}
	for i := range c.Checks {
		x := &c.Checks[i]
		if x.IntervalSeconds == 0 {
			x.IntervalSeconds = 60
		}
		if x.TimeoutSeconds == 0 {
			x.TimeoutSeconds = 8
		}
		if x.StaleSeconds == 0 {
			x.StaleSeconds = x.IntervalSeconds * 3
		}
		if x.FailureThreshold == 0 {
			x.FailureThreshold = 3
		}
		if x.RecoveryThreshold == 0 {
			x.RecoveryThreshold = 2
		}
		if x.Kind != "external" {
			if x.HTTP.Method == "" {
				x.HTTP.Method = "GET"
			}
			if x.HTTP.MaxBodyBytes == 0 {
				x.HTTP.MaxBodyBytes = 65536
			}
			if x.DNS.Type == "" {
				x.DNS.Type = "A"
			}
		}
	}
}
func Validate(c model.Config) error {
	if err := PresentationBounds(c); err != nil {
		return err
	}
	resolved, err := ResolveAddresses(c)
	if err != nil {
		return err
	}
	return validateResolved(resolved)
}
func validateResolved(c model.Config) error {
	if c.Schema != model.ConfigSchema {
		return fmt.Errorf("unsupported schema: %d", c.Schema)
	}
	if !ID(c.Project) {
		return errors.New("invalid project ID")
	}
	if e := PresentationBounds(c); e != nil {
		return e
	}
	if err := validateApplications(c); err != nil {
		return err
	}
	nodes := map[string]bool{}
	ends := map[string]model.Endpoint{}
	services := map[string]bool{}
	obs := map[string]bool{}
	paths := map[string]model.Path{}
	links := map[string]model.Link{}
	checks := map[string]bool{}
	unique := func(m map[string]bool, id, kind string) error {
		if !ID(id) || m[id] {
			return fmt.Errorf("invalid or duplicate %s ID %q", kind, id)
		}
		m[id] = true
		return nil
	}
	for _, n := range c.Nodes {
		if e := unique(nodes, n.ID, "node"); e != nil {
			return e
		}
	}
	for _, s := range c.Services {
		if e := unique(services, s.ID, "service"); e != nil {
			return e
		}
		if !nodes[s.Node] {
			return fmt.Errorf("service %s: missing node", s.ID)
		}
		if len(s.Runtime) > 32 || secure.Text(s.Runtime) != s.Runtime {
			return fmt.Errorf("service %s: runtime must be a short label", s.ID)
		}
	}
	for _, o := range c.Observers {
		if o.StorageWeight < 0 || o.StorageWeight > 1000 {
			return fmt.Errorf("observer %s: storage_weight must be 0..1000", o.ID)
		}
		if e := unique(obs, o.ID, "observer"); e != nil {
			return e
		}
		if o.Node != "" && !nodes[o.Node] {
			return fmt.Errorf("observer %s: missing node", o.ID)
		}
		if o.Context == "" || o.StaleSeconds < 5 || o.StaleSeconds > 86400 {
			return fmt.Errorf("observer %s: context and valid freshness required", o.ID)
		}
	}
	for _, n := range c.Nodes {
		if n.Parent != "" && !nodes[n.Parent] {
			return fmt.Errorf("node %s: missing parent", n.ID)
		}
		if l := n.Location; l != nil {
			if !(l.Lat >= -90 && l.Lat <= 90 && l.Lon >= -180 && l.Lon <= 180) || len(l.Label) > 64 || secure.Text(l.Label) != l.Label {
				return fmt.Errorf("node %s: location needs lat -90..90, lon -180..180 and a short label", n.ID)
			}
		}
		if len(n.Provider) > 64 || secure.Text(n.Provider) != n.Provider {
			return fmt.Errorf("node %s: provider must be a short label", n.ID)
		}
	}
	for _, ep := range c.Endpoints {
		if !ID(ep.ID) || ends[ep.ID].ID != "" || !nodes[ep.Node] || ep.Address == "" {
			return fmt.Errorf("invalid endpoint %q", ep.ID)
		}
		switch ep.Network {
		case "public", "lan", "tailnet", "external":
		case "loopback":
			if !obs[ep.Observer] {
				return fmt.Errorf("loopback endpoint %s needs an observer scope", ep.ID)
			}
		default:
			return fmt.Errorf("endpoint %s: invalid network", ep.ID)
		}
		if ep.Observer != "" && !obs[ep.Observer] {
			return fmt.Errorf("endpoint %s: unknown observer", ep.ID)
		}
		// via lists the edges and proxies a client reaches before the
		// endpoint's own node, in order; none may repeat or be that node.
		if len(ep.Via) > 8 {
			return fmt.Errorf("endpoint %s: at most 8 via hops", ep.ID)
		}
		for i, v := range ep.Via {
			if !nodes[v] || v == ep.Node || slices.Contains(ep.Via[:i], v) {
				return fmt.Errorf("endpoint %s: via needs distinct known nodes other than its own", ep.ID)
			}
		}
		ends[ep.ID] = ep
	}
	for _, l := range c.Links {
		if !ID(l.ID) || links[l.ID].ID != "" || !nodes[l.From] || !nodes[l.To] {
			return fmt.Errorf("invalid link %q", l.ID)
		}
		links[l.ID] = l
	}
	for _, p := range c.Paths {
		if !ID(p.ID) || paths[p.ID].ID != "" || !obs[p.Observer] {
			return fmt.Errorf("invalid path %q", p.ID)
		}
		paths[p.ID] = p
	}
	sources, err := validateSources(c)
	if err != nil {
		return err
	}
	graph := map[string][]string{}
	for _, p := range c.Paths {
		for _, n := range p.Nodes {
			if !nodes[n] {
				return fmt.Errorf("path %s: missing node %q", p.ID, n)
			}
		}
		for i, id := range p.Links {
			l, ok := links[id]
			if !ok {
				return fmt.Errorf("path %s: missing link", p.ID)
			}
			if i > 0 && links[p.Links[i-1]].To != l.From {
				return fmt.Errorf("path %s: links are not contiguous", p.ID)
			}
		}
		if len(p.Candidates) > 0 && len(p.Proxies) > 0 {
			return fmt.Errorf("path %s: candidates and transport hops are distinct", p.ID)
		}
		for _, id := range append(append([]string{}, p.Candidates...), p.DependsOn...) {
			q, ok := paths[id]
			if !ok || q.Observer != p.Observer {
				return fmt.Errorf("path %s: missing/cross-observer dependency %q", p.ID, id)
			}
			graph[p.ID] = append(graph[p.ID], id)
		}
		if len(p.Proxies) > 12 {
			return fmt.Errorf("path %s: maximum 12 transport hops", p.ID)
		}
		for i, pv := range p.Proxies {
			if pv.Node != "" && !nodes[pv.Node] {
				return fmt.Errorf("path %s: unknown proxy node", p.ID)
			}
			if e := HostPort(pv.Address); e != nil {
				return fmt.Errorf("path %s: %w", p.ID, e)
			}
			switch pv.Kind {
			case "http-connect", "https-connect", "socks5":
			case "ssh":
				if i != 0 || pv.SSHKnownHosts == "" || pv.SSHIdentityFile == "" || pv.SSHUser == "" {
					return fmt.Errorf("path %s: SSH must be first, with identity, user and known_hosts", p.ID)
				}
				if !sshUser.MatchString(pv.SSHUser) {
					return errors.New("unsafe SSH user")
				}
			default:
				return fmt.Errorf("unsupported proxy kind %q", pv.Kind)
			}
			if e := ProxyCredentials(pv, i); e != nil {
				return fmt.Errorf("path %s: %w", p.ID, e)
			}
			if len(pv.Username) > 255 {
				return errors.New("proxy username too long")
			}
		}
	}
	if e := acyclic(graph); e != nil {
		return fmt.Errorf("path %w", e)
	}
	graph = map[string][]string{}
	for _, n := range c.Nodes {
		if n.Parent != "" {
			graph[n.ID] = []string{n.Parent}
		}
	}
	if e := acyclic(graph); e != nil {
		return fmt.Errorf("node parent %w", e)
	}
	graph = map[string][]string{}
	for _, s := range c.Services {
		for _, id := range s.Endpoints {
			if _, ok := ends[id]; !ok {
				return fmt.Errorf("service %s: missing or foreign endpoint", s.ID)
			}
		}
		for _, d := range s.DependsOn {
			if !services[d] {
				return fmt.Errorf("service %s: missing dependency", s.ID)
			}
		}
		graph[s.ID] = s.DependsOn
	}
	if e := acyclic(graph); e != nil {
		return fmt.Errorf("service %w", e)
	}
	for _, x := range c.Checks {
		if e := unique(checks, x.ID, "check"); e != nil {
			return e
		}
		if !obs[x.Observer] {
			return fmt.Errorf("check %s: missing observer", x.ID)
		}
		if err := validateServiceScope(x, c); err != nil {
			return fmt.Errorf("check %s: %w", x.ID, err)
		}
		if err := validateHost(x, c); err != nil {
			return fmt.Errorf("check %s: %w", x.ID, err)
		}
		if err := validateExternal(x, c, sources); err != nil {
			return fmt.Errorf("check %s: %w", x.ID, err)
		}
		if x.Endpoint != "" {
			ep, ok := ends[x.Endpoint]
			if !ok {
				return fmt.Errorf("check %s: missing endpoint", x.ID)
			}
			if ep.Observer != "" && ep.Observer != x.Observer {
				return fmt.Errorf("check %s: endpoint scope violation", x.ID)
			}
		}
		if x.Link != "" {
			if _, ok := links[x.Link]; !ok {
				return fmt.Errorf("check %s: missing link", x.ID)
			}
		}
		if x.Service != "" && !services[x.Service] {
			return fmt.Errorf("check %s: missing service", x.ID)
		}
		if x.Path != "" {
			p, ok := paths[x.Path]
			if !ok || p.Observer != x.Observer {
				return fmt.Errorf("check %s: missing/cross-observer path", x.ID)
			}
			if len(p.Candidates) > 0 {
				return fmt.Errorf("check %s: probe individual candidate or a named production-listener path, not an abstract policy", x.ID)
			}
		}
		if x.IntervalSeconds < 1 || x.IntervalSeconds > 86400 || x.TimeoutSeconds < 1 || x.TimeoutSeconds > MaxTimeoutSeconds || x.StaleSeconds < x.IntervalSeconds || x.StaleSeconds > 604800 || x.FailureThreshold < 1 || x.FailureThreshold > 20 || x.RecoveryThreshold < 1 || x.RecoveryThreshold > 20 || x.PolicyThreshold < 0 || x.PolicyThreshold > 20 {
			return fmt.Errorf("check %s: invalid timing/threshold", x.ID)
		}
		for _, ip := range []string{x.BindIP, x.ResolveIP} {
			if ip != "" {
				if _, e := netip.ParseAddr(ip); e != nil {
					return fmt.Errorf("check %s: invalid dial/bind IP", x.ID)
				}
			}
		}
		if err := validateCheckKind(c, x); err != nil {
			return err
		}
	}
	return validateResourceSelections(c)
}

func acyclic(g map[string][]string) error {
	remaining := map[string]int{}
	parents := map[string][]string{}
	depth := map[string]int{}
	for id, deps := range g {
		remaining[id] = len(deps)
		for _, dep := range deps {
			parents[dep] = append(parents[dep], id)
			if _, ok := remaining[dep]; !ok {
				remaining[dep] = 0
			}
		}
	}
	queue := []string{}
	for id, n := range remaining {
		if n == 0 {
			queue = append(queue, id)
			depth[id] = 1
		}
	}
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		if depth[id] > 32 {
			return errors.New("reference depth exceeds 32")
		}
		for _, parent := range parents[id] {
			if depth[parent] < depth[id]+1 {
				depth[parent] = depth[id] + 1
			}
			remaining[parent]--
			if remaining[parent] == 0 {
				queue = append(queue, parent)
			}
		}
	}
	if len(queue) != len(remaining) {
		return errors.New("reference cycle")
	}
	return nil
}
func HostPort(s string) error {
	h, p, e := net.SplitHostPort(s)
	if e != nil || h == "" || strings.HasPrefix(h, "-") || strings.ContainsAny(h, "/\\") || strings.IndexFunc(h, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return errors.New("expected safe host:port")
	}
	n, e := net.LookupPort("tcp", p)
	if e != nil || n < 1 || n > 65535 {
		return errors.New("invalid port")
	}
	return nil
}

func Target(c model.Config, x model.Check) string {
	if x.Target != "" {
		value, _ := expandAddress(c.Addresses, x.Target)
		return value
	}
	// The Cloudflare API adapter reads one documented v4 resource; naming the
	// public base once keeps the contract and the request in step.
	if x.Kind == "cloudflare" {
		return "https://api.cloudflare.com/client/v4"
	}
	for _, e := range c.Endpoints {
		if e.ID == x.Endpoint {
			value, _ := expandAddress(c.Addresses, e.Address)
			return value
		}
	}
	return ""
}
func Check(c model.Config, id string) (model.Check, bool) {
	for _, x := range c.Checks {
		if x.ID == id {
			return x, true
		}
	}
	return model.Check{}, false
}
func Path(c model.Config, id string) model.Path {
	for _, p := range c.Paths {
		if p.ID == id {
			resolved, _ := resolvePath(c, p)
			return resolved
		}
	}
	return model.Path{}
}
func Revision(c model.Config) string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// PublicURL is s as viewers may see it; see secure.PublicURL.
func PublicURL(s string) string { return secure.PublicURL(s) }

// Public returns a deep copy; redaction never modifies executor configuration.
func Public(c model.Config) model.Config {
	resolved, err := ResolveAddresses(c)
	if err != nil {
		return model.Config{Schema: c.Schema, Project: c.Project}
	}
	out := Clone(resolved)
	for i := range out.Endpoints {
		out.Endpoints[i].Address = PublicURL(out.Endpoints[i].Address)
	}
	for i := range out.Checks {
		out.Checks[i] = publicCheckOptions(out.Checks[i])
	}
	for i := range out.Sources {
		s := &out.Sources[i]
		s.Target = PublicURL(s.Target)
		s.CredentialEnv = ""
		s.CAFile = ""
	}
	for i := range out.Paths {
		for j := range out.Paths[i].Proxies {
			p := &out.Paths[i].Proxies[j]
			p.Username = ""
			p.PasswordEnv = ""
			p.SSHUser = ""
			p.SSHIdentityFile = ""
			p.SSHKnownHosts = ""
			p.CAFile = ""
		}
	}
	return out
}

// StorageWeights returns the trusted allocation, independently of check contracts.
func StorageWeights(c model.Config) map[string]int {
	w := make(map[string]int, len(c.Observers))
	for _, o := range c.Observers {
		w[o.ID] = max(o.StorageWeight, 1)
	}
	return w
}
