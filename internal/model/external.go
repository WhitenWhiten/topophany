package model

import "time"

// External observation vocabulary. Report schema 1 is the first external
// report contract; it is independent of the topology schema and of the batch
// and snapshot schemas.
const (
	ReportSchema = 1

	ReportTypeResource = "resource"
	ReportTypeStatus   = "status"

	TimeBasisUpstreamSample = "upstream_sample"
	TimeBasisLiveRead       = "live_read"
	TimeBasisUnknown        = "unknown"

	FieldKnown       = "known"
	FieldMissing     = "missing"
	FieldUnsupported = "unsupported"
	FieldInvalid     = "invalid"

	MaxSources         = 64
	MaxExternalChecks  = 512
	MaxSourcePages     = 20
	MaxSourceObjects   = 10000
	MaxReportDisks     = 64
	MaxReportFields    = 64
	MaxReportBytes     = 64 << 10
	MaxSourceTarget    = 2048
	MaxSourceReportIDs = 256
	MaxRetiredEpochs   = 8
)

// Source is one external instance read from one observer. It owns the
// endpoint, credentials reference, route, schedule and collection budgets.
// Upstream objects are named only by ExternalBinding, never by Source.Target.
type Source struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Adapter string `json:"adapter"`
	// Observer is the sole executor identity allowed to run this source and
	// submit reports collected from it.
	Observer string `json:"observer"`
	// Target is the adapter-specific address. Credentials, query strings and
	// fragments are redacted before viewers see it.
	Target string `json:"target"`
	// CredentialEnv names an environment variable, never a credential value.
	CredentialEnv string `json:"credential_env,omitempty"`
	Path          string `json:"path,omitempty"`
	BindIP        string `json:"bind_ip,omitempty"`
	ResolveIP     string `json:"resolve_ip,omitempty"`
	ServerName    string `json:"server_name,omitempty"`
	CAFile        string `json:"ca_file,omitempty"`
	// AllowInsecure carries a direct profile's explicit HTTP consent at runtime.
	// It cannot be enabled through topology JSON or transmitted to a collector.
	AllowInsecure bool `json:"-"`
	// IntervalSeconds is the source-level cadence. One collection is fanned
	// out to every binding; it is not a per-binding schedule.
	IntervalSeconds int `json:"interval_seconds"`
	TimeoutSeconds  int `json:"timeout_seconds"`
	// StaleSeconds bounds source contact freshness: the last successful read
	// plus this window. A failed read never extends it.
	StaleSeconds int `json:"stale_seconds"`
	// Budgets are enforced by the runtime and the adapter together.
	MaxResponseBytes int64  `json:"max_response_bytes,omitempty"`
	MaxPages         int    `json:"max_pages,omitempty"`
	MaxObjects       int    `json:"max_objects,omitempty"`
	MaxReportBytes   int    `json:"max_report_bytes,omitempty"`
	Notes            string `json:"notes,omitempty"`
}

// ExternalBinding declares the stable upstream object, subject and report
// shape of one external check. It never creates an entity from a response.
type ExternalBinding struct {
	Source        string `json:"source"`
	Object        string `json:"object"`
	Report        string `json:"report"`
	Node          string `json:"node"`
	MaxAgeSeconds int    `json:"max_age_seconds"`
	// PrimaryDisk is a stable upstream disk ID. When empty, no external disk
	// value is selected; the card shows it as unknown rather than guessing.
	PrimaryDisk string              `json:"primary_disk,omitempty"`
	Assertions  *ResourceAssertions `json:"assertions,omitempty"`
}

// ResourceAssertions are generic thresholds over normalized report fields.
// A configured threshold whose field is missing makes the result unknown; it
// never passes silently and never becomes zero.
type ResourceAssertions struct {
	CPUPercentMax    *float64 `json:"cpu_percent_max,omitempty"`
	MemoryPercentMax *float64 `json:"memory_percent_max,omitempty"`
	DiskPercentMax   *float64 `json:"disk_percent_max,omitempty"`
}

// ReportMeta is the identity and time shared by resource and status reports.
// Numeric pointers distinguish a missing value from zero.
type ReportMeta struct {
	Schema     int       `json:"schema"`
	Object     string    `json:"object"`
	SampleID   string    `json:"sample_id,omitempty"`
	Epoch      string    `json:"epoch,omitempty"`
	Sequence   *uint64   `json:"sequence,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
	// TimeBasis is one of upstream_sample, live_read or unknown. A collector
	// clock never silently supplies an upstream sample time.
	TimeBasis string `json:"time_basis"`
	// Fields names the availability of bounded canonical fields. A value
	// absent from this map and from the payload is missing.
	Fields map[string]string `json:"field_status,omitempty"`
}

// ExternalReport is exactly one resource or status payload. Both or neither
// is invalid; it is not a union to be chosen after admission.
type ExternalReport struct {
	ReportMeta
	Resource *ResourceReport `json:"resource,omitempty"`
	Status   *StatusReport   `json:"status,omitempty"`
}

// ResourceReport carries partial resource facts. Every field is optional;
// its availability is stated in ReportMeta.Fields.
type ResourceReport struct {
	CPU    *CPUReport    `json:"cpu,omitempty"`
	Memory *MemoryReport `json:"memory,omitempty"`
	Disks  []DiskReport  `json:"disks,omitempty"`
	ResourceMetrics
}

type CPUReport struct {
	Percent        *float64 `json:"percent,omitempty"`
	LogicalCores   *int     `json:"logical_cores,omitempty"`
	SampleWindowMS *int64   `json:"sample_window_ms,omitempty"`
	Basis          string   `json:"basis,omitempty"`
}

type MemoryReport struct {
	TotalBytes     *uint64  `json:"total_bytes,omitempty"`
	UsedBytes      *uint64  `json:"used_bytes,omitempty"`
	AvailableBytes *uint64  `json:"available_bytes,omitempty"`
	Percent        *float64 `json:"percent,omitempty"`
	Basis          string   `json:"basis,omitempty"`
}

// DiskReport identifies a disk by a stable upstream ID, not by its mount
// point or array position. Path is a display hint only.
type DiskReport struct {
	ID         string   `json:"id"`
	Path       string   `json:"path,omitempty"`
	TotalBytes *uint64  `json:"total_bytes,omitempty"`
	UsedBytes  *uint64  `json:"used_bytes,omitempty"`
	FreeBytes  *uint64  `json:"free_bytes,omitempty"`
	Percent    *float64 `json:"percent,omitempty"`
	Basis      string   `json:"basis,omitempty"`
}

// StatusReport is an upstream statement about the bound object or service.
// It is reported evidence, not a measured connection from the observer.
type StatusReport struct {
	// Status is up, degraded, down or unknown.
	Status string `json:"status"`
	// UpstreamLatencyMS is the upstream's own response time, when reported.
	// It is separate from collection duration and never filled with it.
	UpstreamLatencyMS *int64 `json:"latency_ms,omitempty"`
	// ObservationPoint identifies where the upstream measured or observed.
	ObservationPoint string `json:"observation_point,omitempty"`
}

// PayloadCount says how many of the mutually exclusive payloads are present.
func (r ExternalReport) PayloadCount() int {
	n := 0
	if r.Resource != nil {
		n++
	}
	if r.Status != nil {
		n++
	}
	return n
}

// ExternalObservation separates one source collection attempt from the
// optional upstream report it produced. Contact says the source was read;
// it never says the bound target is healthy.
type ExternalObservation struct {
	Source           string          `json:"source"`
	SourceGeneration string          `json:"source_generation,omitempty"`
	AttemptedAt      time.Time       `json:"attempted_at,omitzero"`
	Contact          bool            `json:"contact"`
	Report           *ExternalReport `json:"report,omitempty"`
}

// CloneExternalReport detaches every mutable member of a report.
func CloneExternalReport(r ExternalReport) ExternalReport {
	out := r
	out.Fields = nil
	if r.Fields != nil {
		out.Fields = make(map[string]string, len(r.Fields))
		for k, v := range r.Fields {
			out.Fields[k] = v
		}
	}
	out.Sequence = nil
	if r.Sequence != nil {
		v := *r.Sequence
		out.Sequence = &v
	}
	if r.Resource != nil {
		res := *r.Resource
		res.ResourceMetrics = res.ResourceMetrics.Clone()
		if r.Resource.CPU != nil {
			cpu := *r.Resource.CPU
			if cpu.Percent != nil {
				v := *cpu.Percent
				cpu.Percent = &v
			}
			if cpu.LogicalCores != nil {
				v := *cpu.LogicalCores
				cpu.LogicalCores = &v
			}
			if cpu.SampleWindowMS != nil {
				v := *cpu.SampleWindowMS
				cpu.SampleWindowMS = &v
			}
			res.CPU = &cpu
		}
		if r.Resource.Memory != nil {
			m := *r.Resource.Memory
			for _, v := range []**uint64{&m.TotalBytes, &m.UsedBytes, &m.AvailableBytes} {
				if *v != nil {
					n := **v
					*v = &n
				}
			}
			if m.Percent != nil {
				v := *m.Percent
				m.Percent = &v
			}
			res.Memory = &m
		}
		res.Disks = append([]DiskReport(nil), r.Resource.Disks...)
		for i := range res.Disks {
			d := &res.Disks[i]
			for _, v := range []**uint64{&d.TotalBytes, &d.UsedBytes, &d.FreeBytes} {
				if *v != nil {
					n := **v
					*v = &n
				}
			}
			if d.Percent != nil {
				v := *d.Percent
				d.Percent = &v
			}
		}
		out.Resource = &res
	}
	if r.Status != nil {
		s := *r.Status
		if s.UpstreamLatencyMS != nil {
			v := *s.UpstreamLatencyMS
			s.UpstreamLatencyMS = &v
		}
		out.Status = &s
	}
	return out
}
