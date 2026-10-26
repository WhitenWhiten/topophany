// Package model defines the versioned wire contract shared by server, executors
// and terminal clients. Identity is never derived from an IP address.
package model

import (
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

const MaxSnapshotBytes = 8 << 20

// MaxObservationFacts is how many facts a collector admits on one
// observation. One over it refuses the observation, and with it the batch
// that carries it, so a probe adding optional facts has to stop here.
const MaxObservationFacts = 32

// SampleClockAhead is how far a sample or heartbeat may sit ahead of the
// reader before it is left out of the current state.
const SampleClockAhead = 5 * time.Second

// SnapshotLifetime is how long a generated snapshot stays current.
const SnapshotLifetime = 15 * time.Second

// Schemas of what is written and sent. A topology file is schema 1. Batches
// and snapshots are schema 2: observations carry the contract of their
// check, not the revision of the whole topology. A collector still accepts
// a schema 1 batch, whose observations carry the revision, while that
// revision is its own.
const (
	ConfigSchema = 1
	// BatchSchema 3 adds optional external reports. A schema 2 batch is
	// native-only and remains readable; a schema 2 observation carrying an
	// external report is refused rather than ignored.
	BatchSchema = 3
	// BatchSchemaContracts is the previous contract-bearing batch schema.
	// It has no external report member.
	BatchSchemaContracts = 2
	// SnapshotSchema 3 carries external binding state. A schema 2 snapshot is
	// native-only and remains readable by this client.
	SnapshotSchema = 3
)

// Version is what this binary reports: a version injected with -ldflags -X,
// the module version the Go toolchain stamped for a tagged build, a short
// VCS revision for a checkout, or "devel" when the build carries no version
// at all. Never guess the last release: a source archive or a test binary
// would then claim to be a release it is not.
var Version string

// pseudoVersion is what the toolchain stamps for a build that is not at a
// tag, in each of its three forms: after a release (1.2.4-0.<time>-<rev>),
// after a pre-release (1.2.4-rc.1.0.<time>-<rev>) and with no tag in reach,
// as in a shallow checkout (0.0.0-<time>-<rev>). It is truthful but too long
// for a banner, so its revision says it better.
var pseudoVersion = regexp.MustCompile(`^\d+\.(?:0\.0-|\d+\.\d+-(?:[^+]*\.)?0\.)\d{14}-([0-9a-f]{12})$`)

func init() {
	// -X sets the initial string before init runs. Only compute a version
	// when the build did not inject one.
	if Version == "" {
		Version = buildVersion(debug.ReadBuildInfo())
	}
}

func buildVersion(info *debug.BuildInfo, ok bool) string {
	revision, modified := "", false
	if ok && info != nil {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
		version := strings.TrimPrefix(info.Main.Version, "v")
		dirty := strings.HasSuffix(version, "+dirty")
		version = strings.TrimSuffix(version, "+dirty")
		if version != "" && version != "(devel)" {
			if match := pseudoVersion.FindStringSubmatch(version); match != nil {
				short := match[1][:7]
				if modified || dirty {
					short += "-dirty"
				}
				return "dev-" + short
			}
			if !dirty {
				if modified {
					version += "-dirty"
				}
				return version
			}
		}
	}
	if revision != "" {
		return "dev-" + shortRevision(revision, modified)
	}
	return "devel"
}

func shortRevision(revision string, modified bool) string {
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}

type Config struct {
	Applications []Application     `json:"applications,omitempty"`
	Addresses    map[string]string `json:"addresses,omitempty"`

	Schema    int        `json:"schema"`
	Project   string     `json:"project"`
	Nodes     []Node     `json:"nodes"`
	Endpoints []Endpoint `json:"endpoints"`
	Services  []Service  `json:"services"`
	Observers []Observer `json:"observers"`
	Links     []Link     `json:"links,omitempty"`
	Paths     []Path     `json:"paths"`
	Checks    []Check    `json:"checks"`
	Sources   []Source   `json:"sources,omitempty"`
}
type Node struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind,omitempty"` // host, vm, container, router, edge, platform, external
	Parent   string    `json:"parent,omitempty"`
	Group    string    `json:"group,omitempty"`
	Provider string    `json:"provider,omitempty"` // who runs it: a cloud or VPS vendor, an edge or platform
	Location *Location `json:"location,omitempty"` // declared placement for maps; never inferred from IP
	// ResourceChecks orders the resource bindings this node uses: primary
	// first, fallbacks after it. Empty preserves native-only behavior and is
	// refused once an external resource makes the choice ambiguous.
	ResourceChecks []string `json:"resource_checks,omitempty"`
	Notes          string   `json:"notes,omitempty"`
}
type Location struct {
	Label string  `json:"label,omitempty"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
}
type Endpoint struct {
	ID       string   `json:"id"`
	Node     string   `json:"node"`
	Address  string   `json:"address"`
	Network  string   `json:"network"`            // public, lan, tailnet, loopback, external
	Observer string   `json:"observer,omitempty"` // mandatory execution scope for loopback
	Via      []string `json:"via,omitempty"`      // edge or proxy nodes that answer first, client side first
	Notes    string   `json:"notes,omitempty"`
}
type Application struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Service struct {
	Application string   `json:"application,omitempty"`
	Environment string   `json:"environment,omitempty"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Node        string   `json:"node"`
	Runtime     string   `json:"runtime,omitempty"` // how it is deployed: workers, pages, functions, container, …
	Endpoints   []string `json:"endpoints,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Runbook     string   `json:"runbook,omitempty"`
	Notes       string   `json:"notes,omitempty"`
}
type Observer struct {
	StorageWeight int `json:"storage_weight,omitempty"` // trusted quota weight; zero means one

	ID               string `json:"id"`
	Name             string `json:"name"`
	Node             string `json:"node,omitempty"`
	Context          string `json:"context"` // describes OS/network namespace, not proof of routing
	RouteExpectation string `json:"route_expectation,omitempty"`
	StaleSeconds     int    `json:"stale_seconds"`
}
type Link struct {
	ID        string `json:"id"`
	From      string `json:"from"` // node IDs; directed initiation, not packet loss direction
	To        string `json:"to"`
	Transport string `json:"transport"`
	Notes     string `json:"notes,omitempty"`
}
type Path struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Observer   string   `json:"observer"`
	Nodes      []string `json:"nodes,omitempty"`
	Links      []string `json:"links,omitempty"`
	Proxies    []Proxy  `json:"proxies,omitempty"`    // explicit wire order, never fallback choices
	Candidates []string `json:"candidates,omitempty"` // other paths; not serial hops
	DependsOn  []string `json:"depends_on,omitempty"`
	Opaque     bool     `json:"opaque,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}
type Proxy struct {
	Kind            string `json:"kind"` // http-connect, https-connect, socks5, ssh
	Address         string `json:"address"`
	Node            string `json:"node,omitempty"`
	Username        string `json:"username,omitempty"`
	PasswordEnv     string `json:"password_env,omitempty"`
	ServerName      string `json:"server_name,omitempty"`
	CAFile          string `json:"ca_file,omitempty"`
	SSHUser         string `json:"ssh_user,omitempty"`
	SSHIdentityFile string `json:"ssh_identity_file,omitempty"`
	SSHKnownHosts   string `json:"ssh_known_hosts,omitempty"`
}
type Check struct {
	AvailabilityScope string         `json:"availability_scope,omitempty"` // remote, local, user_entry; presentation attribution, not a measurement change
	Application       string         `json:"application,omitempty"`
	Environment       string         `json:"environment,omitempty"`
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Observer          string         `json:"observer"`
	Endpoint          string         `json:"endpoint,omitempty"`
	Service           string         `json:"service,omitempty"`
	Path              string         `json:"path,omitempty"`
	Link              string         `json:"link,omitempty"`
	Kind              string         `json:"kind"`             // http,tcp,tls,ssh-banner,dns,udp-echo,icmp,mihomo,tailscale,wireguard,host,cloudflared,cloudflare
	Target            string         `json:"target,omitempty"` // endpoint address unless overridden explicitly
	IntervalSeconds   int            `json:"interval_seconds"`
	TimeoutSeconds    int            `json:"timeout_seconds"`
	StaleSeconds      int            `json:"stale_seconds"`
	FailureThreshold  int            `json:"failure_threshold"`
	RecoveryThreshold int            `json:"recovery_threshold"`
	PolicyThreshold   int            `json:"policy_threshold,omitempty"` // 0/omitted confirms immediately; explicit 1..20
	Disabled          bool           `json:"disabled,omitempty"`
	Maintenance       bool           `json:"maintenance,omitempty"`
	BindIP            string         `json:"bind_ip,omitempty"`
	ResolveIP         string         `json:"resolve_ip,omitempty"` // pin dial address; original Host/SNI preserved
	ServerName        string         `json:"server_name,omitempty"`
	CAFile            string         `json:"ca_file,omitempty"`
	HTTP              HTTPOptions    `json:"http,omitempty"`
	DNS               DNSOptions     `json:"dns,omitempty"`
	Adapter           AdapterOptions `json:"adapter,omitempty"`
	Host              *HostOptions   `json:"host,omitempty"`
	// External is present exactly when Kind is "external". It binds a stable
	// upstream object to this check's target node/service.
	External *ExternalBinding `json:"external,omitempty"`
	Notes    string           `json:"notes,omitempty"`
}

// HostOptions selects a local filesystem and optional resource assertions.
// Zero thresholds collect without asserting; process names never include args.
type HostOptions struct {
	DiskPath         string  `json:"disk_path,omitempty"`
	MaxCPUPercent    float64 `json:"max_cpu_percent,omitempty"`
	MaxMemoryPercent float64 `json:"max_memory_percent,omitempty"`
	MaxDiskPercent   float64 `json:"max_disk_percent,omitempty"`
	ProcessName      string  `json:"process_name,omitempty"`
	MinProcesses     int     `json:"min_processes,omitempty"`
	MaxProcesses     int     `json:"max_processes,omitempty"`
}
type HTTPOptions struct {
	Method         string            `json:"method,omitempty"`
	Status         []int             `json:"status,omitempty"`
	Contains       string            `json:"contains,omitempty"`
	BodyRegex      string            `json:"body_regex,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"` // response assertions, not request secrets
	RequestHeaders map[string]string `json:"request_headers,omitempty"`
	// RequestHeaderEnvs names a header's environment variable, never its
	// value, so a credential need not be written into the topology.
	RequestHeaderEnvs map[string]string `json:"request_header_envs,omitempty"`
	BearerEnv         string            `json:"bearer_env,omitempty"`
	ExpectedExit      []string          `json:"expected_exit,omitempty"` // exact IPs or CIDRs
	// ExpectedPops declares the colos a response from an edge may name, as
	// exactly three uppercase letters each; at most 16, without duplicates.
	// A colo outside the list is a policy violation, never a failed request,
	// and an absent colo (no edge, or withheld on a secret request) asserts
	// nothing.
	ExpectedPops []string `json:"expected_pops,omitempty"`
	ExitFormat   string   `json:"exit_format,omitempty"` // plain, trace, json
	ExitJSONKey  string   `json:"exit_json_key,omitempty"`
	MaxBodyBytes int64    `json:"max_body_bytes,omitempty"`
	// ExpectedTrace requires exit_format trace, the only format whose body
	// this probe parses as a trace.
	ExpectedTrace *TraceExpectations `json:"expected_trace,omitempty"`
}

// TraceExpectations declares what a /cdn-cgi/trace body must report for its
// request to pass. An empty field asserts nothing, and a line the trace did
// not report is not a violation. The vocabularies are Cloudflare's trace
// words, so each value is closed and a colo is three uppercase letters.
type TraceExpectations struct {
	Warp    string   `json:"warp,omitempty"`    // off, on, plus
	Gateway string   `json:"gateway,omitempty"` // on, off
	Colos   []string `json:"colos,omitempty"`   // at most 16 distinct three-letter codes
}
type DNSOptions struct {
	Name        string   `json:"name,omitempty"`
	Type        string   `json:"type,omitempty"`
	ExpectedIPs []string `json:"expected_ips,omitempty"`
	// Transport selects how the question travels: empty or "udp" over UDP
	// with a TCP retry for a truncated answer, "tcp" over TCP, "dot" over
	// verified DNS-over-TLS, and "doh" over DNS-over-HTTPS. It changes the
	// transport, not the answer the parser and assertions accept.
	Transport string `json:"transport,omitempty"`
}

// Observation is a single bounded measurement. ReceivedAt is assigned by the
// collector; SampledAt must not be replaced when replaying an outbox.
// Contract identifies what its check measured (see config.Contract); a
// schema 1 observation carries the revision of the whole topology instead.
type Observation struct {
	ID           string            `json:"id"`
	Project      string            `json:"project"`
	Contract     string            `json:"contract,omitempty"`
	Revision     string            `json:"revision,omitempty"`
	Check        string            `json:"check"`
	Observer     string            `json:"observer"`
	Boot         string            `json:"boot"`
	Sequence     uint64            `json:"sequence"`
	SampledAt    time.Time         `json:"sampled_at"`
	ReceivedAt   time.Time         `json:"received_at"`
	Outcome      string            `json:"outcome"`  // ok,fail,unknown,error,policy
	Evidence     string            `json:"evidence"` // measured,reported
	Stage        string            `json:"stage"`
	Code         string            `json:"code,omitempty"` // bounded diagnostic, never arbitrary raw error
	Detail       string            `json:"detail,omitempty"`
	DurationMS   int64             `json:"duration_ms"`
	Timings      map[string]int64  `json:"timings,omitempty"`
	Connectivity bool              `json:"connectivity"`
	PathEvidence string            `json:"path_evidence,omitempty"`
	Facts        map[string]string `json:"facts,omitempty"`
	// External is present exactly on an external check's collection attempt.
	// The attempt and the upstream report are separate: a failed read has no
	// report, and a report may say the upstream target is down.
	External *ExternalObservation `json:"external,omitempty"`
}

// Heartbeat says an executor is alive. Contracts digests the contracts of
// its observer's checks as it runs them (see config.ObserverContracts).
type Heartbeat struct {
	Delivery []DeliveryStatus `json:"delivery,omitempty"`

	Project    string    `json:"project,omitempty"`
	Revision   string    `json:"revision,omitempty"`
	Contracts  string    `json:"contracts,omitempty"`
	Observer   string    `json:"observer"`
	Boot       string    `json:"boot"`
	SampledAt  time.Time `json:"sampled_at"`
	ReceivedAt time.Time `json:"received_at"`
}

// Batch is what an executor delivers. Revision is the topology revision it
// ran; under schema 2 it only describes the sender, whose observations each
// carry their own contract.
type Batch struct {
	Schema       int           `json:"schema,omitempty"`
	Project      string        `json:"project"`
	Revision     string        `json:"revision"`
	Observations []Observation `json:"observations"`
	Heartbeat    *Heartbeat    `json:"heartbeat,omitempty"`
}
type CheckState struct {
	ID        string       `json:"id"`
	Contract  string       `json:"contract"` // the public contract its samples count under
	Observer  string       `json:"observer"`
	Health    string       `json:"health"`    // up,degraded,down,unknown
	Freshness string       `json:"freshness"` // fresh,stale,never,paused,maintenance
	Evidence  string       `json:"evidence"`
	Reason    string       `json:"reason"`
	Failures  int          `json:"failures"`
	Successes int          `json:"successes"`
	Since     time.Time    `json:"since"`
	Last      *Observation `json:"last,omitempty"`
	// PolicyConfirmed says the latest sample's policy violation has stood
	// for as many consecutive samples as the check's confirmation threshold
	// and, when a provider reference is settling, for the settle window in
	// elapsed time. Ordinary policies confirm immediately unless an explicit
	// policy_threshold requires more samples; provider loading adds its own
	// bounded settle requirement.
	PolicyConfirmed bool `json:"policy_confirmed,omitempty"`
	// Availability is how the check fared over the last day, week and
	// month, from the collector's rollups of its samples.
	Availability []Availability `json:"availability,omitempty"`
}

// Rollup sums what a check's samples said over a span of time. Each
// sample's outcome stands from when it was taken until the next sample or
// until it goes stale, whichever is first: Up while it passed, Degraded
// while it found a policy violated, Down while it failed, and Unknown
// while the monitor could not tell (a monitor-side error). Unmeasured is
// the rest of the span, when no current sample stood for it; it is never
// up or down. Samples and Outcomes count the samples taken in the span,
// and Latency is what the passing ones took.
type Rollup struct {
	Start      time.Time `json:"start"`
	Seconds    int64     `json:"seconds"`
	Up         int64     `json:"up_seconds"`
	Degraded   int64     `json:"degraded_seconds"`
	Down       int64     `json:"down_seconds"`
	Unknown    int64     `json:"unknown_seconds"`
	Unmeasured int64     `json:"unmeasured_seconds"`
	Samples    int64     `json:"samples"`
	// Unreconciled counts samples with unavailable or ambiguous interval
	// boundaries in buckets touching the window. This can exceed Samples
	// when a partial bucket's counts are omitted; time totals are incomplete.
	Unreconciled int64            `json:"unreconciled_samples,omitempty"`
	Outcomes     map[string]int64 `json:"outcomes,omitempty"`
	Latency      *Latency         `json:"latency,omitempty"`
	// FirstByte is what the passing samples' first byte took, split by what
	// the edge said about its cache. Pops counts the colos every sample's
	// response reported, keeping at most sixteen named codes plus other;
	// Cache counts the cache status of every sample an edge answered, in the
	// closed vocabulary, with other for an absent or unknown one; a sample
	// with no edge counts none.
	FirstByte *FirstByteLatency `json:"first_byte,omitempty"`
	Pops      map[string]int64  `json:"pops,omitempty"`
	Cache     map[string]int64  `json:"cache,omitempty"`
}

// Measured is how long a rollup's samples said whether the check passed:
// its up, degraded and down time.
func (r Rollup) Measured() int64 { return r.Up + r.Degraded + r.Down }

// Latency is what passing samples took: the median and the 95th
// percentile, estimated from a histogram whose bins are a quarter of a
// doubling wide, and the slowest.
type Latency struct {
	Samples int64 `json:"samples"`
	P50     int64 `json:"p50_ms"`
	P95     int64 `json:"p95_ms"`
	Max     int64 `json:"max_ms"`
}

// FirstByteLatency is what the first byte of passing samples took, split
// by the cache class the edge reported: Cached for answers the edge served
// itself, Origin for ones it fetched from the origin. A class with no
// passing samples is nil. Samples whose cache status was absent or outside
// both lists are kept unclassified and are not reported.
type FirstByteLatency struct {
	Cached *Latency `json:"cached,omitempty"`
	Origin *Latency `json:"origin,omitempty"`
}

// Availability is a check's rollup over a window ending when its snapshot
// was generated: "24h", "7d" or "30d". Since is the earliest moment the
// collector's rollups hold for the check, when that is inside the window;
// the time before it counts as unmeasured.
type Availability struct {
	Window string `json:"window"`
	Rollup
	Since time.Time `json:"since,omitzero"`
}
type EntityState struct {
	ID       string   `json:"id"`
	Health   string   `json:"health"`
	Reason   string   `json:"reason"`
	Evidence string   `json:"evidence"`
	Affected bool     `json:"affected"`
	Checks   []string `json:"checks,omitempty"`
}
type Incident struct {
	ID              string    `json:"id"`
	Scope           string    `json:"scope"`
	Summary         string    `json:"summary"`
	Hypothesis      string    `json:"hypothesis,omitempty"`
	Checks          []string  `json:"checks"`
	CounterEvidence []string  `json:"counter_evidence,omitempty"`
	Since           time.Time `json:"since"`
}

// IncidentRecord is an incident as a collector's ledger remembers it: when
// its evidence began failing (Since), when the collector first counted it
// (Opened) and, once it is closed, when and how it ended. State is open,
// recovering (its failed checks pass again, short of their recovery
// threshold) or closed; Ended is recovered, unconfirmed (its evidence
// lapsed or no longer confirms it), paused or changed (what its checks
// measure changed, or they were removed). Previous counts the earlier
// records of the same incident that the ledger holds.
type IncidentRecord struct {
	ID       string    `json:"id"`
	Scope    string    `json:"scope"`
	Summary  string    `json:"summary"`
	Checks   []string  `json:"checks"`
	State    string    `json:"state"`
	Since    time.Time `json:"since"`
	Opened   time.Time `json:"opened"`
	Closed   time.Time `json:"closed,omitzero"`
	Ended    string    `json:"ended,omitempty"`
	Previous int       `json:"previous"`
}

// ObserverState is an executor's liveness. Contracts says whether it last
// reported measuring what the collector counts: current, outdated (its
// samples of changed checks are refused until it reloads), or "" when it
// has not said.
type ObserverState struct {
	Delivery []DeliveryStatus `json:"delivery,omitempty"`

	ID        string    `json:"id"`
	Health    string    `json:"health"`
	LastSeen  time.Time `json:"last_seen"`
	Reason    string    `json:"reason"`
	Contracts string    `json:"contracts,omitempty"`
}

// IsColo reports whether s is exactly three ASCII uppercase letters, the
// shape Cloudflare codes a colo in. The rollup, config validation and views
// all use this one predicate, so they cannot disagree about what names a
// colo.
func IsColo(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

type Snapshot struct {
	SourcesStatus *SourcesStatus `json:"sources_status,omitempty"`
	Direct        *DirectInfo    `json:"direct,omitempty"`
	Storage       *StorageUsage  `json:"storage,omitempty"`
	Ingress       *Ingress       `json:"ingress,omitempty"`

	TopologyOmitted bool            `json:"topology_omitted,omitempty"`
	DetailsOmitted  bool            `json:"details_omitted,omitempty"`
	Schema          int             `json:"schema"`
	Version         string          `json:"version"`
	Project         string          `json:"project"`
	Instance        string          `json:"instance"`
	Revision        string          `json:"revision"`
	GeneratedAt     time.Time       `json:"generated_at"`
	ViewObserver    string          `json:"view_observer,omitempty"`
	Topology        Config          `json:"topology,omitzero"`
	Checks          []CheckState    `json:"checks"`
	Nodes           []EntityState   `json:"nodes"`
	Endpoints       []EntityState   `json:"endpoints"`
	Services        []EntityState   `json:"services"`
	Paths           []EntityState   `json:"paths"`
	Links           []EntityState   `json:"links"`
	Observers       []ObserverState `json:"observers"`
	Incidents       []Incident      `json:"incidents"`
	// Ledger is what the collector remembers of the incidents in the view:
	// those open, then those closed in the last day, newest first.
	Ledger []IncidentRecord `json:"ledger,omitempty"`
	// External is the sanitized accepted report state of external bindings in
	// this view. It is absent in schema 2 snapshots.
	External []ExternalBindingView `json:"external,omitempty"`
}
