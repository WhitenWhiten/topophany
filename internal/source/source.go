// Package source defines the small static contract between a bounded source
// runtime and an external adapter. Adapters own endpoints, authentication
// protocol, version parsing, pagination and field mapping; the runtime owns
// transport, credentials, page/byte/object budgets and cancellation.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Error is a bounded adapter/runtime failure. Collectors turn it into a
// collection-attempt diagnostic, never into a target outage.
type Error struct {
	Transport string // optional closed transport classification; never upstream error text
	Code      string
	Detail    string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func NewError(code, detail string) error {
	code = safeCode(code)
	if code == "" {
		code = "source_error"
	}
	return &Error{Code: code, Detail: secureText(detail, 256)}
}

// Code returns a bounded error code.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Code != "" {
		return safeCode(e.Code)
	}
	return "source_collection_failed"
}

func safeCode(s string) string {
	if len(s) < 1 || len(s) > 64 || secure.Text(s) != s || strings.TrimSpace(s) != s {
		return ""
	}
	for _, r := range s {
		if r < 0x21 || r == 0x7f || r == '/' || r == '\\' {
			return ""
		}
	}
	return s
}

func secureText(s string, limit int) string {
	if len(s) > limit {
		s = s[:limit]
	}
	if secure.Text(s) != s {
		s = "unprintable diagnostic"
	}
	return s
}

// MaxResultBytes bounds all normalized reports returned by one collection,
// separately from the per-report and upstream-response budgets.
const MaxResultBytes = 16 << 20

// DiscoveredObject is the identity and display name offered for selection.
type DiscoveredObject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Request asks for one stable upstream object's declared report types. One
// object may need a resource report, a status report or both; an adapter
// reads the object once and returns one Reading per requested type.
type Request struct {
	Object  string
	Reports []string
}

// Wants reports whether the request includes a report type.
func (r Request) Wants(report string) bool { return slices.Contains(r.Reports, report) }

// Key is the result identity: an object and one report type.
type Key struct {
	Object string
	Report string
}

// Reading is one upstream object's normalized report. Object is the stable
// key the adapter read; a report may omit its object to be stamped with it.
type Reading struct {
	Object string
	Report model.ExternalReport
}

// Response is a bounded HTTP response supplied by the runtime.
type Response struct {
	RetryAfter  string
	Status      int
	ContentType string
	Body        []byte
}

// IO is the only network and credential access an adapter receives. It reads
// one configured source; it does not allow arbitrary hosts, redirects,
// ambient proxies or unbounded bodies.
type IO interface {
	Credential(context.Context, model.Source) (string, error)
	Get(context.Context, model.Source, string, map[string]string, int64) (Response, error)
	Pages() int
	// Close releases attempt-owned connections, including unfinished dials.
	// Runtime calls it on every exit after NewIO succeeds.
	Close()
}

// Adapter is deliberately static and small. Validate runs before a process
// starts collection; Collect performs one source-level read for the wanted
// stable upstream object IDs.
type Adapter interface {
	Validate(model.Source) error
	Collect(context.Context, IO, model.Source, []Request) ([]Reading, error)
}

// Runtime is an explicit map of adapter types to implementations. There is no
// init-time registration, plugin loading or fallback: a config naming an
// unwired adapter fails before any source is read.
type Runtime struct {
	Adapters map[string]Adapter
	NewIO    func(model.Config, model.Source) (IO, error)
}

// Validate refuses unwired/invalid sources before scheduling begins.
func (r Runtime) Validate(c model.Config) error {
	for _, x := range c.Checks {
		if x.Kind != "external" || x.External == nil {
			continue
		}
		s, ok := config.Source(c, x.External.Source)
		if !ok {
			return fmt.Errorf("source %q is not declared", x.External.Source)
		}
		a, ok := r.Adapters[s.Adapter]
		if !ok || a == nil {
			return fmt.Errorf("source %s: adapter %q is not wired", s.ID, s.Adapter)
		}
		if e := a.Validate(s); e != nil {
			return fmt.Errorf("source %s: %w", s.ID, e)
		}
		if r.NewIO == nil {
			return fmt.Errorf("source %s: no controlled IO is wired", s.ID)
		}
	}
	return nil
}

// Collect executes one adapter read and validates its bounded result. Only
// requested object/report-type pairs leave the runtime; upstream data cannot
// invent bindings, report types or entities. One object may legally supply
// both a resource and a status report, but the same pair twice is refused.
func (r Runtime) Collect(ctx context.Context, c model.Config, s model.Source, requests []Request) ([]Reading, error) {
	a, ok := r.Adapters[s.Adapter]
	if !ok || a == nil {
		return nil, NewError("adapter_unavailable", "the source adapter is not wired")
	}
	if len(requests) == 0 || len(requests) > s.MaxObjects {
		return nil, NewError("object_budget", "requested object count is outside the source budget")
	}
	wanted := make(map[Key]bool)
	objects := make(map[string]bool, len(requests))
	reportCount := 0
	for _, request := range requests {
		if !safeObjectID(request.Object) {
			return nil, NewError("invalid_object_request", "requested object ID is unsafe or too long")
		}
		if objects[request.Object] {
			return nil, NewError("duplicate_object_request", "one object was requested twice")
		}
		objects[request.Object] = true
		if len(request.Reports) == 0 || len(request.Reports) > 2 {
			return nil, NewError("report_budget", "each object requires one or two report types")
		}
		seenType := map[string]bool{}
		for _, kind := range request.Reports {
			if kind != model.ReportTypeResource && kind != model.ReportTypeStatus {
				return nil, NewError("unsupported_report_type", "request names an unsupported report type")
			}
			if seenType[kind] {
				return nil, NewError("duplicate_report_request", "one report type was requested twice")
			}
			seenType[kind] = true
			wanted[Key{Object: request.Object, Report: kind}] = true
			reportCount++
		}
	}
	if reportCount > 2*model.MaxSourceObjects {
		return nil, NewError("report_budget", "requested report count exceeds the source budget")
	}
	io, err := r.NewIO(c, s)
	if err != nil {
		return nil, err
	}
	defer io.Close()
	readings, err := a.Collect(ctx, io, s, cloneRequests(requests))
	if err != nil {
		return nil, err
	}
	if io.Pages() > s.MaxPages {
		return nil, NewError("page_budget", "adapter exceeded max_pages")
	}
	if len(readings) > max(s.MaxObjects, reportCount) {
		return nil, NewError("report_budget", "adapter exceeded the result count budget")
	}
	seen := map[Key]bool{}
	totalBytes := 0
	out := make([]Reading, 0, reportCount)
	for _, reading := range readings {
		if !objects[reading.Object] {
			// An unbound upstream object is ignored, not turned into topology.
			continue
		}
		kind := report.PayloadType(reading.Report)
		if kind == "" {
			return nil, NewError("unsupported_report_type", "adapter returned neither or both report payloads")
		}
		key := Key{Object: reading.Object, Report: kind}
		if !wanted[key] {
			return nil, NewError("unexpected_report_type", "adapter returned a report type that was not requested")
		}
		if seen[key] {
			return nil, NewError("duplicate_report", "adapter returned one object and report type twice")
		}
		seen[key] = true
		if reading.Report.Object == "" {
			reading.Report.Object = reading.Object
		}
		if reading.Report.Object != reading.Object {
			return nil, NewError("report_object_mismatch", "report object does not match the read object")
		}
		normalized, e := report.Normalize(reading.Report, s.MaxReportBytes)
		if e != nil {
			return nil, NewError(report.Code(e), "adapter report failed normalization")
		}
		reading.Report = normalized
		encoded, e := json.Marshal(normalized)
		if e != nil {
			return nil, NewError("report_encoding", "adapter report could not be encoded")
		}
		totalBytes += len(encoded)
		if totalBytes > MaxResultBytes {
			return nil, NewError("report_budget", "adapter results exceeded the encoded byte budget")
		}
		out = append(out, reading)
	}
	return out, nil
}

func cloneRequests(in []Request) []Request {
	out := make([]Request, len(in))
	for i, request := range in {
		out[i] = Request{Object: request.Object, Reports: slices.Clone(request.Reports)}
	}
	return out
}

func safeObjectID(s string) bool {
	if len(s) < 1 || len(s) > 256 || secure.Text(s) != s || strings.TrimSpace(s) != s {
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
