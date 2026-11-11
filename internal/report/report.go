// Package report normalizes external reports and evaluates their time and
// resource semantics without importing the collector, UI or an adapter. It
// owns units, nullable-field validation, deadlines and bounded counter math.
package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Error is a bounded, machine-readable normalization refusal.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func refuse(code, format string, args ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Code returns an Error's code, or "".
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// PayloadType names the one payload a normalized report carries, or "" when
// it has none or both. Callers use it as part of a source result key; it is
// not a second admission decision.
func PayloadType(r model.ExternalReport) string {
	switch {
	case r.Resource != nil && r.Status == nil:
		return model.ReportTypeResource
	case r.Status != nil && r.Resource == nil:
		return model.ReportTypeStatus
	default:
		return ""
	}
}

// SampleAt returns the upstream sample time when the report carries a known
// time basis. A dashboard's read time must be labelled live_read by the
// adapter's versioned contract; it is never inferred here.
func SampleAt(r model.ExternalReport) (time.Time, bool) {
	if r.TimeBasis != model.TimeBasisUpstreamSample && r.TimeBasis != model.TimeBasisLiveRead {
		return time.Time{}, false
	}
	if r.ObservedAt.IsZero() {
		return time.Time{}, false
	}
	return r.ObservedAt, true
}

// Normalize validates one report and returns an owned canonical copy. Units
// are bytes, milliseconds and 0..100 percent. A partial report is allowed;
// missing values stay nil and are never converted to zero. Foreign fields
// are refused rather than round-tripped.
func Normalize(r model.ExternalReport, maxBytes int) (model.ExternalReport, error) {
	if r.Schema != model.ReportSchema {
		return model.ExternalReport{}, refuse("unsupported_report_schema", "report schema is %d", r.Schema)
	}
	if !safeID(r.Object, 256) {
		return model.ExternalReport{}, refuse("invalid_report_object", "stable object ID is missing or unsafe")
	}
	if r.SampleID != "" && !safeToken(r.SampleID, 128) {
		return model.ExternalReport{}, refuse("invalid_sample_id", "sample ID is unsafe or too long")
	}
	if r.Epoch != "" && !safeToken(r.Epoch, 128) {
		return model.ExternalReport{}, refuse("invalid_epoch", "epoch is unsafe or too long")
	}
	switch r.TimeBasis {
	case model.TimeBasisUpstreamSample, model.TimeBasisLiveRead:
		if r.ObservedAt.IsZero() {
			return model.ExternalReport{}, refuse("report_time_missing", "%s requires observed_at", r.TimeBasis)
		}
	case model.TimeBasisUnknown:
		// A time may be carried for diagnostics, but it is not evidence.
	default:
		return model.ExternalReport{}, refuse("invalid_time_basis", "time basis is %q", r.TimeBasis)
	}
	if r.PayloadCount() != 1 {
		return model.ExternalReport{}, refuse("report_payload_count", "exactly one resource or status payload is required")
	}
	if len(r.Fields) > model.MaxReportFields {
		return model.ExternalReport{}, refuse("report_fields_budget", "more than %d field statuses", model.MaxReportFields)
	}
	out := model.CloneExternalReport(r)
	switch {
	case r.Resource != nil:
		if err := normalizeResource(out.Resource); err != nil {
			return model.ExternalReport{}, err
		}
	case r.Status != nil:
		if err := normalizeStatus(out.Status); err != nil {
			return model.ExternalReport{}, err
		}
	}
	if err := normalizeFields(out); err != nil {
		return model.ExternalReport{}, err
	}
	if maxBytes <= 0 || maxBytes > model.MaxReportBytes {
		maxBytes = model.MaxReportBytes
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return model.ExternalReport{}, refuse("report_encoding", "%v", err)
	}
	if len(raw) > maxBytes {
		return model.ExternalReport{}, refuse("report_too_large", "%d encoded bytes exceed %d", len(raw), maxBytes)
	}
	return out, nil
}

func normalizeResource(r *model.ResourceReport) error {
	empty := r.ResourceMetrics == (model.ResourceMetrics{})
	for _, n := range []*float64{
		r.Network.RXBytesPerSecond, r.Network.TXBytesPerSecond,
		r.Load.Avg1m, r.Load.Avg5m, r.Load.Avg15m,
	} {
		if n != nil && (!finite(*n) || math.Signbit(*n)) {
			return refuse("invalid_resource_number", "network rates and load averages must be finite and nonnegative")
		}
	}
	if s := r.Swap; s.TotalBytes != nil && s.UsedBytes != nil && *s.UsedBytes > *s.TotalBytes {
		return refuse("invalid_capacity", "swap.used_bytes exceeds total_bytes")
	}
	if c := r.CPU; c != nil {
		if c.Percent != nil {
			empty = false
			if !finite(*c.Percent) || *c.Percent < 0 || *c.Percent > 100 {
				return refuse("invalid_percent", "cpu.percent must be 0..100")
			}
		}
		if c.LogicalCores != nil {
			empty = false
			if *c.LogicalCores < 1 || *c.LogicalCores > 65536 {
				return refuse("invalid_cpu_cores", "cpu.logical_cores must be 1..65536")
			}
		}
		if c.SampleWindowMS != nil {
			empty = false
			if *c.SampleWindowMS < 1 || *c.SampleWindowMS > 3600000 {
				return refuse("invalid_sample_window", "cpu.sample_window_ms must be 1..3600000")
			}
		}
		if c.Basis != "" {
			empty = false
		}
		if !safeBasis(c.Basis) {
			return refuse("invalid_basis", "cpu.basis is unsafe or too long")
		}
	}
	if m := r.Memory; m != nil {
		for _, p := range []*uint64{m.TotalBytes, m.UsedBytes, m.AvailableBytes} {
			if p != nil {
				empty = false
			}
		}
		if m.Percent != nil {
			empty = false
		}
		if m.Basis != "" {
			empty = false
		}
		if m.TotalBytes != nil && *m.TotalBytes == 0 {
			return refuse("invalid_capacity", "memory.total_bytes must be positive")
		}
		if m.TotalBytes != nil && m.UsedBytes != nil && *m.UsedBytes > *m.TotalBytes {
			return refuse("invalid_capacity", "memory.used_bytes exceeds total_bytes")
		}
		if m.TotalBytes != nil && m.AvailableBytes != nil && *m.AvailableBytes > *m.TotalBytes {
			return refuse("invalid_capacity", "memory.available_bytes exceeds total_bytes")
		}
		if m.TotalBytes != nil && m.UsedBytes != nil && m.AvailableBytes != nil && *m.AvailableBytes > *m.TotalBytes-*m.UsedBytes {
			return refuse("invalid_capacity", "memory.used_bytes + available_bytes exceeds total_bytes")
		}
		if m.Percent != nil && (!finite(*m.Percent) || *m.Percent < 0 || *m.Percent > 100) {
			return refuse("invalid_percent", "memory.percent must be 0..100")
		}
		if !safeBasis(m.Basis) {
			return refuse("invalid_basis", "memory.basis is unsafe or too long")
		}
	}
	if len(r.Disks) > model.MaxReportDisks {
		return refuse("disk_budget", "at most %d disks", model.MaxReportDisks)
	}
	seen := map[string]bool{}
	for i := range r.Disks {
		d := &r.Disks[i]
		empty = false
		if !safeID(d.ID, 128) {
			return refuse("invalid_disk_id", "disk ID is missing or unsafe")
		}
		if seen[d.ID] {
			return refuse("duplicate_disk_id", "disk %q appears twice", d.ID)
		}
		seen[d.ID] = true
		if d.Path != "" && (len(d.Path) > 1024 || secure.Text(d.Path) != d.Path) {
			return refuse("invalid_disk_path", "disk %q path is unsafe", d.ID)
		}
		if d.TotalBytes != nil && *d.TotalBytes == 0 {
			return refuse("invalid_capacity", "disk %q total_bytes must be positive", d.ID)
		}
		if d.TotalBytes != nil && d.UsedBytes != nil && *d.UsedBytes > *d.TotalBytes {
			return refuse("invalid_capacity", "disk %q used_bytes exceeds total_bytes", d.ID)
		}
		if d.TotalBytes != nil && d.FreeBytes != nil && *d.FreeBytes > *d.TotalBytes {
			return refuse("invalid_capacity", "disk %q free_bytes exceeds total_bytes", d.ID)
		}
		if d.TotalBytes != nil && d.UsedBytes != nil && d.FreeBytes != nil && *d.FreeBytes > *d.TotalBytes-*d.UsedBytes {
			return refuse("invalid_capacity", "disk %q used_bytes + free_bytes exceeds total_bytes", d.ID)
		}
		if d.Percent != nil && (!finite(*d.Percent) || *d.Percent < 0 || *d.Percent > 100) {
			return refuse("invalid_percent", "disk %q percent must be 0..100", d.ID)
		}
		if !safeBasis(d.Basis) {
			return refuse("invalid_basis", "disk %q basis is unsafe or too long", d.ID)
		}
	}
	if empty {
		return refuse("empty_resource_report", "resource report carries no values")
	}
	return nil
}

func normalizeStatus(s *model.StatusReport) error {
	switch s.Status {
	case "up", "degraded", "down", "unknown":
	default:
		return refuse("invalid_status", "status must be up, degraded, down or unknown")
	}
	if s.UpstreamLatencyMS != nil && (*s.UpstreamLatencyMS < 0 || *s.UpstreamLatencyMS > 3600000) {
		return refuse("invalid_upstream_latency", "status.latency_ms must be 0..3600000")
	}
	if s.ObservationPoint != "" && (len(s.ObservationPoint) > 128 || secure.Text(s.ObservationPoint) != s.ObservationPoint) {
		return refuse("invalid_observation_point", "status observation point is unsafe or too long")
	}
	return nil
}

// normalizeFields keeps availability vocabulary closed and refuses a status
// that contradicts the payload. Fields absent from the map and payload are
// implicitly missing; present values are implicitly known.
func normalizeFields(r model.ExternalReport) error {
	present := fieldPresence(r)
	for key, status := range r.Fields {
		if len(key) > 160 || secure.Text(key) != key {
			return refuse("invalid_field_key", "field status key is unsafe or too long")
		}
		if !knownFieldKey(key, r) {
			return refuse("unknown_field", "field %q is not part of this report", key)
		}
		switch status {
		case model.FieldKnown, model.FieldMissing, model.FieldUnsupported, model.FieldInvalid:
		default:
			return refuse("invalid_field_status", "field %q has status %q", key, status)
		}
		has := present[key]
		if status == model.FieldKnown && !has {
			return refuse("field_status_conflict", "field %q is known but absent", key)
		}
		if status != model.FieldKnown && has {
			return refuse("field_status_conflict", "field %q is present but marked %s", key, status)
		}
	}
	return nil
}

func fieldPresence(r model.ExternalReport) map[string]bool {
	p := map[string]bool{}
	if res := r.Resource; res != nil {
		if c := res.CPU; c != nil {
			p["cpu.percent"] = c.Percent != nil
			p["cpu.logical_cores"] = c.LogicalCores != nil
			p["cpu.sample_window_ms"] = c.SampleWindowMS != nil
			p["cpu.basis"] = c.Basis != ""
		}
		if m := res.Memory; m != nil {
			p["memory.total_bytes"] = m.TotalBytes != nil
			p["memory.used_bytes"] = m.UsedBytes != nil
			p["memory.available_bytes"] = m.AvailableBytes != nil
			p["memory.percent"] = m.Percent != nil
			p["memory.basis"] = m.Basis != ""
		}
		p["network.rx_bytes_per_second"] = res.Network.RXBytesPerSecond != nil
		p["network.tx_bytes_per_second"] = res.Network.TXBytesPerSecond != nil
		p["swap.total_bytes"] = res.Swap.TotalBytes != nil
		p["swap.used_bytes"] = res.Swap.UsedBytes != nil
		p["load.avg_1m"] = res.Load.Avg1m != nil
		p["load.avg_5m"] = res.Load.Avg5m != nil
		p["load.avg_15m"] = res.Load.Avg15m != nil
		for _, d := range res.Disks {
			prefix := "disk." + d.ID + "."
			p[prefix+"total_bytes"] = d.TotalBytes != nil
			p[prefix+"used_bytes"] = d.UsedBytes != nil
			p[prefix+"free_bytes"] = d.FreeBytes != nil
			p[prefix+"percent"] = d.Percent != nil
			p[prefix+"basis"] = d.Basis != ""
			p[prefix+"path"] = d.Path != ""
		}
	}
	if s := r.Status; s != nil {
		p["status"] = s.Status != ""
		p["status.latency_ms"] = s.UpstreamLatencyMS != nil
		p["status.observation_point"] = s.ObservationPoint != ""
	}
	return p
}

// knownFieldKey is a payload-specific allowlist. A status report cannot
// carry resource fields and a resource report cannot carry status fields,
// even though field_status is a plain map on the shared envelope. Disk
// fields are accepted only for a declared disk ID of this resource payload.
func knownFieldKey(key string, r model.ExternalReport) bool {
	switch {
	case r.Status != nil:
		switch key {
		case "status", "status.latency_ms", "status.observation_point":
			return true
		}
		return false
	case r.Resource != nil:
		switch key {
		case "cpu.percent", "cpu.logical_cores", "cpu.sample_window_ms", "cpu.basis",
			"memory.total_bytes", "memory.used_bytes", "memory.available_bytes", "memory.percent", "memory.basis",
			"network.rx_bytes_per_second", "network.tx_bytes_per_second", "swap.total_bytes", "swap.used_bytes",
			"load.avg_1m", "load.avg_5m", "load.avg_15m":
			return true
		}
		if !strings.HasPrefix(key, "disk.") {
			return false
		}
		// Disk IDs may contain dots; the field suffix is what follows the
		// last one.
		rest := strings.TrimPrefix(key, "disk.")
		i := strings.LastIndexByte(rest, '.')
		if i <= 0 {
			return false
		}
		diskID, field := rest[:i], rest[i+1:]
		switch field {
		case "total_bytes", "used_bytes", "free_bytes", "percent", "basis", "path":
		default:
			return false
		}
		for _, d := range r.Resource.Disks {
			if d.ID == diskID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func safeBasis(s string) bool {
	return len(s) <= 64 && secure.Text(s) == s && strings.TrimSpace(s) == s
}

func safeToken(s string, limit int) bool {
	if len(s) < 1 || len(s) > limit || secure.Text(s) != s || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}

func safeID(s string, limit int) bool {
	if len(s) < 1 || len(s) > limit || secure.Text(s) != s || strings.TrimSpace(s) != s {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:/-", rune(c)) {
			continue
		}
		return false
	}
	return true
}

// DataDeadline is the original sample time plus the binding's maximum age.
// It exists only for a known sample time and never derives from a read or
// receipt time.
func DataDeadline(r model.ExternalReport, maxAge time.Duration) (time.Time, bool) {
	at, ok := SampleAt(r)
	if !ok || maxAge <= 0 {
		return time.Time{}, false
	}
	return at.Add(maxAge), true
}

// ContactDeadline is the last successful source contact plus its stale
// window. A duplicate report may update contact separately; it never changes
// DataDeadline.
func ContactDeadline(lastContact time.Time, stale time.Duration) (time.Time, bool) {
	if lastContact.IsZero() || stale <= 0 {
		return time.Time{}, false
	}
	return lastContact.Add(stale), true
}

// ValidUntil is the earliest of the evidence deadlines, with zero times
// ignored. An empty result means no deadline could be established.
func ValidUntil(times ...time.Time) time.Time {
	var out time.Time
	for _, at := range times {
		if at.IsZero() {
			continue
		}
		if out.IsZero() || at.Before(out) {
			out = at
		}
	}
	return out
}

// Policy result vocabulary.
const (
	PolicyPass      = "ok"
	PolicyViolation = "policy"
	PolicyUnknown   = "unknown"
)

// Policy is the outcome of generic resource assertions. A known violation is
// policy even when another required field is missing. Without a violation, a
// required but unknown field makes the result unknown; missing never passes.
type Policy struct {
	Outcome    string
	Violations []string
	Missing    []string
}

func Evaluate(binding model.ExternalBinding, r model.ExternalReport) Policy {
	out := Policy{Outcome: PolicyPass}
	if r.Resource == nil || binding.Report != model.ReportTypeResource {
		out.Outcome = PolicyUnknown
		out.Missing = []string{"resource"}
		return out
	}
	a := binding.Assertions
	if a == nil {
		return out
	}
	res := r.Resource
	check := func(name string, max *float64, value *float64) {
		if max == nil {
			return
		}
		if value == nil {
			out.Missing = append(out.Missing, name)
			return
		}
		if *value > *max {
			out.Violations = append(out.Violations, name)
		}
	}
	var cpu, memory *float64
	if res.CPU != nil {
		cpu = res.CPU.Percent
	}
	if res.Memory != nil {
		memory = res.Memory.Percent
	}
	check("cpu", a.CPUPercentMax, cpu)
	check("memory", a.MemoryPercentMax, memory)
	if a.DiskPercentMax != nil {
		var disk *model.DiskReport
		for i := range res.Disks {
			if res.Disks[i].ID == binding.PrimaryDisk {
				disk = &res.Disks[i]
				break
			}
		}
		if disk == nil {
			out.Missing = append(out.Missing, "disk")
		} else {
			check("disk", a.DiskPercentMax, disk.Percent)
		}
	}
	switch {
	case len(out.Violations) > 0:
		out.Outcome = PolicyViolation
	case len(out.Missing) > 0:
		out.Outcome = PolicyUnknown
	}
	slices.Sort(out.Violations)
	slices.Sort(out.Missing)
	return out
}

// CounterAnomaly names why a counter delta could not be used.
const (
	CounterNoAnomaly      = ""
	CounterWindowExceeded = "window_exceeded"
	CounterTimeBackwards  = "time_backwards"
	CounterZeroInterval   = "zero_interval"
	CounterSeriesLimit    = "series_limit"
	CounterInvalidWindow  = "invalid_window"
)

// CounterRate is one bounded counter observation. Rate is valid only when
// Valid is true; first samples, resets and anomalies never produce zero.
type CounterRate struct {
	Valid         bool
	Seeded        bool
	Reset         bool
	Delta         uint64
	Seconds       float64
	RatePerSecond float64
	Anomaly       string
}

type counterState struct {
	value uint64
	at    time.Time
}

// CounterSeries derives rates from monotonic counters. Series are isolated
// by key and the map is bounded; a new key over the bound is an explicit
// anomaly, never an eviction that would silently merge histories.
type CounterSeries struct {
	mu     sync.Mutex
	limit  int
	series map[string]counterState
}

func NewCounterSeries(limit int) *CounterSeries {
	if limit < 1 || limit > 4096 {
		limit = 256
	}
	return &CounterSeries{limit: limit, series: map[string]counterState{}}
}

// Observe adds one absolute counter value. window is the longest accepted
// gap; a longer gap reseeds rather than pretending the missing interval had
// that rate. A zero window accepts any positive gap.
func (c *CounterSeries) Observe(key string, value uint64, at time.Time, window time.Duration) CounterRate {
	if c == nil {
		return CounterRate{Anomaly: CounterSeriesLimit}
	}
	if at.IsZero() {
		return CounterRate{Anomaly: CounterZeroInterval}
	}
	if window < 0 {
		return CounterRate{Anomaly: CounterInvalidWindow}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prev, ok := c.series[key]
	if !ok {
		if len(c.series) >= c.limit {
			return CounterRate{Anomaly: CounterSeriesLimit}
		}
		c.series[key] = counterState{value: value, at: at}
		return CounterRate{Seeded: true}
	}
	gap := at.Sub(prev.at)
	switch {
	case gap < 0:
		return CounterRate{Anomaly: CounterTimeBackwards}
	case gap == 0:
		return CounterRate{Anomaly: CounterZeroInterval}
	case value < prev.value:
		c.series[key] = counterState{value: value, at: at}
		return CounterRate{Reset: true, Seeded: true}
	case window > 0 && gap > window:
		c.series[key] = counterState{value: value, at: at}
		return CounterRate{Seeded: true, Anomaly: CounterWindowExceeded}
	}
	c.series[key] = counterState{value: value, at: at}
	seconds := gap.Seconds()
	return CounterRate{Valid: true, Delta: value - prev.value, Seconds: seconds, RatePerSecond: float64(value-prev.value) / seconds}
}

// CounterPercent converts two rates observed over the same interval into a
// bounded percentage. It returns false instead of inventing a value when
// either rate is invalid, the total is zero, the intervals differ or the
// part exceeds the total.
func CounterPercent(part, total CounterRate) (float64, bool) {
	if !part.Valid || !total.Valid || total.RatePerSecond <= 0 || part.RatePerSecond < 0 || part.Seconds != total.Seconds || part.Seconds <= 0 {
		return 0, false
	}
	p := part.RatePerSecond / total.RatePerSecond * 100
	if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 100 {
		return 0, false
	}
	return p, true
}

// Zero is useful to adapters that need to compare a copied float without
// sharing a pointer.
func Zero(v *float64) bool { return v == nil || *v == 0 }

// EqualContent compares canonical reports for duplicate/conflict detection.
// It is equality of normalized content, not sample identity.
func EqualContent(a, b model.ExternalReport) bool { return reflect.DeepEqual(a, b) }

// ValidateExternalObservation checks the collector-independent shape of one
// external attempt. It refuses a native observation that carries external
// data and an external observation without its declared report identity. The
// returned report is normalized and owned; a failed source attempt has none.
func ValidateExternalObservation(v model.Observation, x model.Check, s model.Source) (*model.ExternalReport, error) {
	if x.Kind != "external" || x.External == nil {
		if v.External != nil {
			return nil, refuse("external_binding_mismatch", "native check %q carries external evidence", x.ID)
		}
		return nil, nil
	}
	e := v.External
	if e == nil {
		return nil, refuse("external_attempt_required", "external check %q has no collection attempt", x.ID)
	}
	if e.Source != x.External.Source || e.Source != s.ID {
		return nil, refuse("external_source_mismatch", "source is not the declared binding source")
	}
	if v.Observer != s.Observer {
		return nil, refuse("external_observer_mismatch", "observer is not the source executor")
	}
	if v.Evidence != "reported" {
		return nil, refuse("external_evidence_mismatch", "external evidence must be reported, not %q", v.Evidence)
	}
	if v.Connectivity {
		return nil, refuse("external_connectivity_claim", "an external report cannot claim measured connectivity")
	}
	if e.AttemptedAt.IsZero() {
		return nil, refuse("external_attempt_time_missing", "collection attempt has no time")
	}
	if len(e.SourceGeneration) > 64 || (e.SourceGeneration != "" && !safeToken(e.SourceGeneration, 64)) {
		return nil, refuse("external_generation_invalid", "source generation is unsafe or too long")
	}
	switch v.Outcome {
	case "ok", "error", "unknown":
	default:
		return nil, refuse("external_attempt_outcome", "external attempt outcome must be ok, error or unknown")
	}
	if e.Report == nil {
		if v.Outcome == "ok" {
			return nil, refuse("external_report_missing", "a successful source read must not silently have no report")
		}
		if v.Code == "" {
			return nil, refuse("external_attempt_code_missing", "an attempt without a report needs a bounded diagnostic code")
		}
		return nil, nil
	}
	if !e.Contact {
		return nil, refuse("external_report_without_contact", "a report requires a successful source read")
	}
	if v.Outcome != "ok" {
		return nil, refuse("external_report_outcome", "a source read with a report is a successful collection attempt")
	}
	normalized, err := Normalize(*e.Report, s.MaxReportBytes)
	if err != nil {
		return nil, err
	}
	if normalized.Object != x.External.Object {
		return nil, refuse("external_object_mismatch", "report object is not the bound object")
	}
	switch x.External.Report {
	case model.ReportTypeResource:
		if normalized.Resource == nil || normalized.Status != nil {
			return nil, refuse("external_report_type_mismatch", "binding expects a resource report")
		}
	case model.ReportTypeStatus:
		if normalized.Status == nil || normalized.Resource != nil {
			return nil, refuse("external_report_type_mismatch", "binding expects a status report")
		}
	default:
		return nil, refuse("external_report_type", "binding has an invalid report type")
	}
	return &normalized, nil
}
