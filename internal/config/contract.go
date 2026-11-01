package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// A check's contract identifies what it measures: the probe it runs, from
// which observer, against which target, through which proxies, and the
// assertions that decide its outcome. A sample counts only under the
// contract it was taken with. Everything else (names, notes, places,
// thresholds, schedules, what the check is attributed to, other checks)
// can change without touching the contract, so the check keeps its
// history and in-flight samples stay valid.
type contract struct {
	TLSMinDays int                  `json:"tls_min_days,omitempty"`
	Schema     int                  `json:"schema"`
	Project    string               `json:"project"`
	Check      string               `json:"check"`
	Observer   string               `json:"observer"`
	Kind       string               `json:"kind"`
	Target     string               `json:"target"`
	Timeout    int                  `json:"timeout_seconds"`
	BindIP     string               `json:"bind_ip,omitempty"`
	Resolve    string               `json:"resolve_ip,omitempty"`
	Server     string               `json:"server_name,omitempty"`
	CAFile     string               `json:"ca_file,omitempty"`
	HTTP       model.HTTPOptions    `json:"http"`
	DNS        model.DNSOptions     `json:"dns"`
	Adapter    model.AdapterOptions `json:"adapter"`
	Host       *model.HostOptions   `json:"host,omitempty"`
	Proxies    []model.Proxy        `json:"proxies,omitempty"`
}

// Contract is a check's contract in c. Executors stamp it on their samples
// and collectors store and count samples by it; like Revision, it is never
// shown to viewers, who see PublicContract.
func Contract(c model.Config, x model.Check) string {
	resolved, _ := ResolveCheck(c, x)
	if resolved.Kind == "external" && resolved.External != nil {
		return externalMeasurement(c.Project, c, resolved)
	}
	return measurement(c.Project, resolved, Target(c, resolved), Path(c, resolved.Path).Proxies)
}

func measurement(project string, x model.Check, target string, proxies []model.Proxy) string {
	httpOpt, dnsOpt, adapter, host := measuredOptions(x)
	b, _ := json.Marshal(contract{TLSMinDays: x.TLSMinDays, Schema: 2, Project: project, Check: x.ID, Observer: x.Observer, Kind: x.Kind, Target: target, Timeout: x.TimeoutSeconds, BindIP: x.BindIP, Resolve: x.ResolveIP, Server: x.ServerName, CAFile: x.CAFile, HTTP: httpOpt, DNS: dnsOpt, Adapter: adapter, Host: host, Proxies: proxies})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// measuredOptions keeps the option blocks the kind actually executes.
// Defaults fills every check with an HTTP method and a DNS type; leaving
// those in the hash would drop a TCP check's history when someone later set
// expected_pops, which this probe never reads.
func measuredOptions(x model.Check) (model.HTTPOptions, model.DNSOptions, model.AdapterOptions, *model.HostOptions) {
	var httpOpt model.HTTPOptions
	var dnsOpt model.DNSOptions
	var adapter model.AdapterOptions
	var host *model.HostOptions
	kind, _ := checkKindOf(x.Kind)
	if kind.options&optionHTTP != 0 {
		httpOpt = x.HTTP
	}
	if kind.options&optionDNS != 0 {
		dnsOpt = x.DNS
	}
	if kind.options&optionAdapter != 0 {
		adapter = x.Adapter
	}
	if kind.options&optionHost != 0 {
		host = x.Host
	}
	// Settle is a confirmation threshold, not a measurement: it says when a
	// difference may be treated as confirmed, like failure_threshold, and
	// must not reset the history it only delays.
	if ref := adapter.ProviderReference; ref != nil {
		settled := *ref
		settled.SettleSeconds = nil
		adapter.ProviderReference = &settled
	}
	return httpOpt, dnsOpt, adapter, host
}

// Contracts are the contracts of every check in c, by check ID.
func Contracts(c model.Config) map[string]string {
	resolved, err := ResolveAddresses(c)
	if err != nil {
		return map[string]string{}
	}
	c = resolved
	out := make(map[string]string, len(c.Checks))
	paths := map[string][]model.Proxy{}
	for _, p := range c.Paths {
		paths[p.ID] = p.Proxies
	}
	for _, x := range c.Checks {
		if x.Kind == "external" && x.External != nil {
			out[x.ID] = externalMeasurement(c.Project, c, x)
			continue
		}
		// Target is the one place a kind's default target is defined, so the
		// batch contract must go through it too; the map of endpoints this
		// used to read would silently disagree with Contract for a kind such
		// as cloudflare, whose target defaults when empty.
		out[x.ID] = measurement(c.Project, x, Target(c, x), paths[x.Path])
	}
	return out
}

// Stale is how long a sample of each check in c stays current, by check
// ID: its stale_seconds.
func Stale(c model.Config) map[string]time.Duration {
	out := make(map[string]time.Duration, len(c.Checks))
	for _, x := range c.Checks {
		out[x.ID] = time.Duration(x.StaleSeconds) * time.Second
	}
	return out
}

// PublicContracts are the contracts of the redacted topology, which viewers
// may see: like PublicRevision, they permit no guess at hidden fields.
func PublicContracts(c model.Config) map[string]string { return Contracts(Public(c)) }

// ObserverContracts digests the contracts of an observer's checks, so a
// collector can tell whether an executor measures what it would count.
func ObserverContracts(c model.Config, observer string) string {
	if digest, ok := ObserverDigests(c, Contracts(c))[observer]; ok {
		return digest
	}
	return observerDigest([]string{})
}

// ObserverDigests reuses a generation's contracts to identify each observer's
// measurements, including observers with no checks.
func ObserverDigests(c model.Config, contracts map[string]string) map[string]string {
	byObserver := make(map[string][]string, len(c.Observers))
	for _, o := range c.Observers {
		byObserver[o.ID] = []string{}
	}
	for _, x := range c.Checks {
		byObserver[x.Observer] = append(byObserver[x.Observer], x.ID+"="+contracts[x.ID])
	}
	out := make(map[string]string, len(byObserver))
	for id, pairs := range byObserver {
		out[id] = observerDigest(pairs)
	}
	return out
}

func observerDigest(pairs []string) string {
	sort.Strings(pairs)
	b, _ := json.Marshal(pairs)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Counted reports whether an observation counts in c under contracts, the
// contracts of c: it carries its check's contract, or it is a schema 1
// observation taken under exactly this topology.
func Counted(v model.Observation, contracts map[string]string, revision string) bool {
	if v.Contract != "" {
		return v.Contract == contracts[v.Check]
	}
	return v.Revision != "" && v.Revision == revision
}

// externalContract identifies what an external binding measures without
// coupling it to presentation or credential values. A change to the source
// instance, route/TLS, budgets, object, report type, subject, age or
// assertions starts a new report state; a name, note or credential variable
// name does not.
type externalContract struct {
	Schema   int                     `json:"schema"`
	Project  string                  `json:"project"`
	Check    string                  `json:"check"`
	Observer string                  `json:"observer"`
	Kind     string                  `json:"kind"`
	Service  string                  `json:"service,omitempty"`
	Source   externalSourceContract  `json:"source"`
	Binding  externalBindingContract `json:"binding"`
}

type externalSourceContract struct {
	ID               string        `json:"id"`
	Adapter          string        `json:"adapter"`
	Target           string        `json:"target"`
	Proxies          []model.Proxy `json:"proxies,omitempty"`
	BindIP           string        `json:"bind_ip,omitempty"`
	ResolveIP        string        `json:"resolve_ip,omitempty"`
	ServerName       string        `json:"server_name,omitempty"`
	CAFile           string        `json:"ca_file,omitempty"`
	TimeoutSeconds   int           `json:"timeout_seconds"`
	MaxResponseBytes int64         `json:"max_response_bytes"`
	MaxPages         int           `json:"max_pages"`
	MaxObjects       int           `json:"max_objects"`
	MaxReportBytes   int           `json:"max_report_bytes"`
}

type externalBindingContract struct {
	Source        string                    `json:"source"`
	Object        string                    `json:"object"`
	Report        string                    `json:"report"`
	Node          string                    `json:"node"`
	MaxAgeSeconds int                       `json:"max_age_seconds"`
	PrimaryDisk   string                    `json:"primary_disk,omitempty"`
	Assertions    *model.ResourceAssertions `json:"assertions,omitempty"`
}

func externalMeasurement(project string, c model.Config, x model.Check) string {
	s, _ := Source(c, x.External.Source)
	b := *x.External
	// The resolved ordered route is the measurement, not the path's name or
	// ID. A path rename with the same hops, or a source/check name change,
	// keeps the contract; changing a proxy address, kind, order or TLS field
	// starts a new one. CredentialEnv, names and notes are absent.
	out := externalContract{
		Schema: 3, Project: project, Check: x.ID, Observer: x.Observer,
		Kind: "external", Service: x.Service,
		Source:  externalSourceContract{ID: s.ID, Adapter: s.Adapter, Target: s.Target, Proxies: Path(c, s.Path).Proxies, BindIP: s.BindIP, ResolveIP: s.ResolveIP, ServerName: s.ServerName, CAFile: s.CAFile, TimeoutSeconds: s.TimeoutSeconds, MaxResponseBytes: s.MaxResponseBytes, MaxPages: s.MaxPages, MaxObjects: s.MaxObjects, MaxReportBytes: s.MaxReportBytes},
		Binding: externalBindingContract{Source: b.Source, Object: b.Object, Report: b.Report, Node: b.Node, MaxAgeSeconds: b.MaxAgeSeconds, PrimaryDisk: b.PrimaryDisk, Assertions: b.Assertions},
	}
	raw, _ := json.Marshal(out)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
