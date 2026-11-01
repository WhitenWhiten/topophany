// Package local owns this machine's own runtime checks: a private store of at
// most four loopback engine reads, and the session a local stream runs them
// in. It reads only loopback addresses, never changes an engine, and nothing
// it reads leaves the machine.
package local

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

const (
	MaxChecks    = 4
	MaxInput     = 256 << 10
	MaxOutput    = 1 << 20
	MaxGroups    = 64
	Project      = "local"
	Observer     = "local"
	ObserverName = "This machine"
	// A local engine that does not answer is degraded on the first failed
	// read and down on the second in a row; one good read recovers it.
	FailureThreshold  = 2
	RecoveryThreshold = 1
)

// Error is a closed code with a fixed message. It never carries upstream
// text, addresses or secrets.
type Error struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	Message   string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func fail(code string) *Error {
	return &Error{Code: code, Message: messages[code], Retryable: code == "connection_failed"}
}

var messages = map[string]string{
	"invalid_request":            "Invalid local check request.",
	"not_local_address":          "The engine address must be an http(s) loopback address (127.0.0.0/8, ::1 or localhost) without credentials, path, query or fragment.",
	"saved_auth_mismatch":        "No saved secret exists for this check at this address.",
	"connection_failed":          "The engine could not be reached.",
	"authentication_failed":      "The engine did not accept this secret.",
	"tls_error":                  "The engine's TLS certificate could not be verified.",
	"unexpected_status":          "The engine answered with an unexpected HTTP status.",
	"unsupported_response_shape": "The engine's response is not supported by this adapter.",
	"response_too_large":         "The engine's response exceeds the adapter's bounds.",
	"profile_conflict":           "Local checks have changed. List them before retrying.",
	"check_limit":                "At most four local checks can be saved.",
	"not_found":                  "Local check not found.",
	"storage_error":              "Local check storage is unavailable.",
	"credential_storage_error":   "Private secret storage is unavailable.",
	"state_budget":               "The local response exceeds its budget.",
}

// AsError returns err's closed code, or storage_error for anything else.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return fail("storage_error")
}

// Check is one local engine read. Omitted durations default to a 30-second
// interval, a 5-second timeout and three intervals of staleness.
type Check struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Adapter      string `json:"adapter"`
	URL          string `json:"url"`
	Interval     int    `json:"interval_seconds"`
	Timeout      int    `json:"timeout_seconds"`
	Stale        int    `json:"stale_seconds"`
	RootProxy    string `json:"root_proxy"`
	ExpectedMode string `json:"expected_mode"`
	ExpectedTUN  *bool  `json:"expected_tun"`
}

func (c *Check) UnmarshalJSON(b []byte) error {
	type plain Check
	p := plain{Interval: 30, Timeout: 5}
	if err := exact(b, &p, "expected_tun"); err != nil {
		return err
	}
	if p.Stale == 0 {
		p.Stale = 3 * p.Interval
	}
	*c = Check(p)
	return nil
}

// Validate requires the fields' documented bounds. The address is checked
// first-class: anything but a loopback controller is not_local_address.
func (c Check) Validate() error {
	if !config.ID(c.ID) || len(c.ID) > 64 || strings.TrimSpace(c.Name) == "" || len(c.Name) > 128 || secure.Text(c.Name) != c.Name || c.Adapter != "mihomo" {
		return fail("invalid_request")
	}
	if _, err := NormalizeURL(c.URL); err != nil {
		return err
	}
	if c.Interval < 5 || c.Interval > 3600 || c.Timeout < 1 || c.Timeout > 30 || c.Timeout >= c.Interval || c.Stale < 2*c.Interval || c.Stale > 86400 {
		return fail("invalid_request")
	}
	if c.RootProxy != "" && (strings.TrimSpace(c.RootProxy) == "" || len(c.RootProxy) > 128 || secure.Text(c.RootProxy) != c.RootProxy) {
		return fail("invalid_request")
	}
	switch c.ExpectedMode {
	case "", "rule", "global", "direct":
	default:
		return fail("invalid_request")
	}
	return nil
}

// Normalized returns the check with its address in canonical form.
func (c Check) Normalized() Check {
	if u, err := NormalizeURL(c.URL); err == nil {
		c.URL = u
	}
	if c.ExpectedTUN != nil {
		tun := *c.ExpectedTUN
		c.ExpectedTUN = &tun
	}
	return c
}

// NormalizeURL accepts an http(s) controller address on a loopback literal
// (127.0.0.0/8 or ::1) or localhost, with an optional port and no userinfo,
// path, query or fragment. Its canonical form has a lowercase scheme and
// host, a canonical IP and no trailing slash.
func NormalizeURL(raw string) (string, error) {
	bad := fail("not_local_address")
	if raw == "" || len(raw) > 2048 || secure.Text(raw) != raw || strings.TrimSpace(raw) != raw {
		return "", bad
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Path != "" && u.Path != "/" || u.RawPath != "" {
		return "", bad
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", bad
	}
	host := strings.ToLower(u.Hostname())
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() || strings.Contains(host, "%") {
			return "", bad
		}
		host = ip.String()
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", bad
		}
		port = strconv.Itoa(n)
		return scheme + "://" + net.JoinHostPort(host, port), nil
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host, nil
}

// target is the address the probe dials: localhost is read as 127.0.0.1, so
// no resolver or hosts file can route a local check off this machine.
func (c Check) target() string {
	u, err := NormalizeURL(c.URL)
	if err != nil {
		return ""
	}
	parsed, _ := url.Parse(u)
	if parsed.Hostname() == "localhost" {
		port := parsed.Port()
		parsed.Host = "127.0.0.1"
		if port != "" {
			parsed.Host = net.JoinHostPort("127.0.0.1", port)
		}
	}
	return parsed.String()
}

// tokenEnv names a check's stored secret for the probe. It is resolved only
// by the local session, never looked up in the process environment.
func tokenEnv(id string) string { return "TOPO_LOCAL_TOKEN_" + id }

func (c Check) model(token bool) model.Check {
	x := model.Check{ID: c.ID, Name: c.Name, Kind: "mihomo", Observer: Observer, Target: c.target(), IntervalSeconds: c.Interval, TimeoutSeconds: c.Timeout, StaleSeconds: c.Stale, FailureThreshold: FailureThreshold, RecoveryThreshold: RecoveryThreshold}
	x.Adapter.RootProxy = c.RootProxy
	x.Adapter.ExpectedMode = c.ExpectedMode
	if c.ExpectedTUN != nil {
		tun := *c.ExpectedTUN
		x.Adapter.ExpectedTUN = &tun
	}
	if token {
		x.Adapter.TokenEnv = tokenEnv(c.ID)
	}
	return x
}

// Config is the topology a local session runs: one observer, this machine,
// and one Mihomo check per saved check. withToken says which checks send a
// stored secret.
func Config(checks []Check, withToken func(id string) bool) (model.Config, error) {
	c := model.Config{Schema: model.ConfigSchema, Project: Project, Observers: []model.Observer{{ID: Observer, Name: ObserverName, Context: "this machine's own runtime checks, read over loopback", StaleSeconds: 90}}}
	if len(checks) > MaxChecks {
		return c, fail("check_limit")
	}
	seen := map[string]bool{}
	for _, x := range checks {
		if x.Validate() != nil || seen[x.ID] {
			return c, fail("invalid_request")
		}
		seen[x.ID] = true
		c.Checks = append(c.Checks, x.model(withToken != nil && withToken(x.ID)))
	}
	config.Defaults(&c)
	if config.Validate(c) != nil {
		return c, fail("invalid_request")
	}
	return c, nil
}

// Auth is a request's secret union: a new token, none, or the saved one.
type Auth struct {
	Mode  string `json:"mode"`
	Token string `json:"token,omitempty"`
}

func (a *Auth) UnmarshalJSON(b []byte) error {
	type plain Auth
	var p plain
	if err := exact(b, &p); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(b, &keys)
	_, hasToken := keys["token"]
	switch p.Mode {
	case "token":
		if !validToken(p.Token) {
			return fail("invalid_request")
		}
	case "none", "saved":
		if hasToken {
			return fail("invalid_request")
		}
	default:
		return fail("invalid_request")
	}
	*a = Auth(p)
	return nil
}

func validToken(s string) bool {
	return s != "" && len(s) <= 1024 && utf8.ValidString(s) && secure.Text(s) == s && strings.TrimSpace(s) == s
}

// Request is one bounded configuration request.
type Request struct {
	Schema       int    `json:"schema"`
	RequestID    string `json:"request_id"`
	BaseRevision string `json:"base_revision"`
	Check        Check  `json:"check"`
	Auth         Auth   `json:"auth"`
	CheckID      string `json:"check_id"`
}

// Decode reads one JSON object through EOF, at most 256 KiB.
func Decode(r io.Reader) (Request, error) {
	var req Request
	var buf bytes.Buffer
	n, err := buf.ReadFrom(io.LimitReader(r, MaxInput+1))
	if err != nil || n > MaxInput {
		return req, fail("invalid_request")
	}
	if e := exact(buf.Bytes(), &req); e != nil {
		// Keep a well-formed request ID for the envelope.
		var id struct {
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(buf.Bytes(), &id)
		req.RequestID = id.RequestID
		return req, e
	}
	if req.Schema != 1 || len(req.RequestID) > 64 || len(req.BaseRevision) > 128 {
		return req, fail("invalid_request")
	}
	return req, nil
}

// exact ignores unknown extension keys but rejects wrong known types, nulls
// (except named nullable keys), case aliases and duplicate keys. It never
// embeds raw parser errors in output.
func exact(b []byte, v any, nullable ...string) error {
	if !utf8.Valid(b) || secure.JSONBudget(b) != nil {
		return fail("invalid_request")
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil || m == nil {
		return fail("invalid_request")
	}
	t := reflect.TypeOf(v).Elem()
	filtered := map[string]json.RawMessage{}
	for i := 0; i < t.NumField(); i++ {
		key := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		raw, ok := m[key]
		if key == "" || !ok {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			allowed := false
			for _, n := range nullable {
				allowed = allowed || n == key
			}
			if !allowed {
				return fail("invalid_request")
			}
		}
		filtered[key] = raw
	}
	clean, _ := json.Marshal(filtered)
	var nested *Error
	if err := json.Unmarshal(clean, v); err != nil {
		if errors.As(err, &nested) {
			return nested
		}
		return fail("invalid_request")
	}
	return nil
}
