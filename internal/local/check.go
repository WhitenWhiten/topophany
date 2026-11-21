// Package local owns this machine's own checks: a private store of at most
// sixteen, and the session a local stream runs them in. An engine check reads
// a proxy engine's controller on a loopback address and never changes it; a
// network check asks a website, a port, a certificate or a host the user
// named, from this machine. Nothing either reads is sent anywhere else.
package local

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

const (
	MaxChecks    = 16
	MaxInput     = 256 << 10
	MaxOutput    = 1 << 20
	MaxGroups    = 64
	Project      = "local"
	Observer     = "local"
	ObserverName = "This machine"
	// A local check that does not answer is degraded on the first failed
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
	"invalid_target":             "The address is not one this kind of check reads: an http(s) URL without credentials or fragment, a host:port, or an IPv4 address.",
	"saved_auth_mismatch":        "No saved secret exists for this check at this address.",
	"connection_failed":          "The engine could not be reached.",
	"authentication_failed":      "The engine did not accept this secret.",
	"tls_error":                  "The engine's TLS certificate could not be verified.",
	"unexpected_status":          "The engine answered with an unexpected HTTP status.",
	"unsupported_response_shape": "The engine's response is not supported by this adapter.",
	"response_too_large":         "The engine's response exceeds the adapter's bounds.",
	"profile_conflict":           "Local checks have changed. List them before retrying.",
	"check_limit":                "At most sixteen local checks can be saved.",
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

// Adapters are the kinds of local check, in the order a client offers them:
// a proxy engine's controller, then the network checks.
var Adapters = []string{"mihomo", "http", "tcp", "tls", "icmp"}

// answered is what an http check accepts when no status is expected: the
// site answered, with a page or with where the page is. The probe follows no
// redirect, so one is the answer itself.
var answered = []int{200, 201, 202, 203, 204, 205, 206, 301, 302, 303, 304, 307, 308}

// Check is one local check. Omitted durations default to a 30-second
// interval, a 5-second timeout and three intervals of staleness.
//
// A mihomo check reads URL, a loopback controller, and may declare
// RootProxy, ExpectedMode and ExpectedTUN. An http check requests URL and
// may declare ExpectedStatus; tcp and tls dial Target, a host:port; icmp
// echoes Target, an IPv4 address. TLSMinDays warns of a certificate's expiry
// on an https or tls check, and MaxDurationMS of a slow answer on any
// network check.
type Check struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Adapter        string `json:"adapter"`
	URL            string `json:"url"`
	Target         string `json:"target,omitempty"`
	Interval       int    `json:"interval_seconds"`
	Timeout        int    `json:"timeout_seconds"`
	Stale          int    `json:"stale_seconds"`
	RootProxy      string `json:"root_proxy"`
	ExpectedMode   string `json:"expected_mode"`
	ExpectedTUN    *bool  `json:"expected_tun"`
	ExpectedStatus int    `json:"expected_status,omitempty"`
	TLSMinDays     int    `json:"tls_min_days,omitempty"`
	MaxDurationMS  int    `json:"max_duration_ms,omitempty"`
}

// Engine says whether the check reads a proxy engine's controller, which
// may take a secret, rather than the network.
func (c Check) Engine() bool { return c.Adapter == "mihomo" }

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
// first-class: an engine anywhere but a loopback controller is
// not_local_address, and a network check's unusable address invalid_target.
// A field that belongs to another kind of check is refused, never ignored.
func (c Check) Validate() error {
	if !config.ID(c.ID) || len(c.ID) > 64 || strings.TrimSpace(c.Name) == "" || len(c.Name) > 128 || secure.Text(c.Name) != c.Name || !slices.Contains(Adapters, c.Adapter) {
		return fail("invalid_request")
	}
	if c.Engine() {
		if _, err := NormalizeURL(c.URL); err != nil {
			return err
		}
		if c.Target != "" || c.ExpectedStatus != 0 || c.TLSMinDays != 0 || c.MaxDurationMS != 0 {
			return fail("invalid_request")
		}
	} else {
		if _, err := c.address(); err != nil {
			return err
		}
		if c.RootProxy != "" || c.ExpectedMode != "" || c.ExpectedTUN != nil {
			return fail("invalid_request")
		}
		if c.Adapter != "http" && c.ExpectedStatus != 0 || c.ExpectedStatus != 0 && (c.ExpectedStatus < 100 || c.ExpectedStatus > 599) {
			return fail("invalid_request")
		}
		if c.TLSMinDays < 0 || c.TLSMinDays > 3650 || c.TLSMinDays > 0 && !c.verifiesCertificate() {
			return fail("invalid_request")
		}
		if c.MaxDurationMS < 0 || c.MaxDurationMS > 300000 {
			return fail("invalid_request")
		}
		// Whatever is saved must run: the probe's own rules for its target
		// have the last word, so one check can never stop the others' session.
		if !c.runnable() {
			return fail("invalid_target")
		}
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

// runnable says whether the topology a session would build for this check
// alone passes the configuration's own validation.
func (c Check) runnable() bool {
	if c.Interval < 1 || c.Timeout < 1 {
		// Reported as invalid_request by the bounds that follow.
		return true
	}
	cfg := model.Config{Schema: model.ConfigSchema, Project: Project, Observers: []model.Observer{observer()}, Checks: []model.Check{c.model(false)}}
	config.Defaults(&cfg)
	return config.Validate(cfg) == nil
}

// verifiesCertificate says whether the check completes a verified TLS
// handshake, so that it has a certificate whose expiry it can warn of.
func (c Check) verifiesCertificate() bool {
	return c.Adapter == "tls" || c.Adapter == "http" && strings.HasPrefix(strings.ToLower(c.URL), "https://")
}

// address is a network check's address in canonical form: an http(s) URL
// with a host and no userinfo or fragment, a host:port, or an IPv4 literal.
// The other address field must be empty.
func (c Check) address() (string, error) {
	bad := fail("invalid_target")
	raw, other := c.Target, c.URL
	if c.Adapter == "http" {
		raw, other = c.URL, c.Target
	}
	// "${" would name a topology address, which a local check has none of.
	if other != "" || raw == "" || len(raw) > 2048 || secure.Text(raw) != raw || strings.ContainsAny(raw, " \t") || strings.Contains(raw, "${") {
		return "", bad
	}
	switch c.Adapter {
	case "http":
		u, err := url.Parse(raw)
		if err != nil || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.RawFragment != "" || u.Hostname() == "" {
			return "", bad
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "http" && scheme != "https" {
			return "", bad
		}
		u.Scheme, u.Host = scheme, strings.ToLower(u.Host)
		return u.String(), nil
	case "tcp", "tls":
		if config.HostPort(raw) != nil {
			return "", bad
		}
		return strings.ToLower(raw), nil
	case "icmp":
		ip, err := netip.ParseAddr(raw)
		if err != nil || !ip.Is4() {
			return "", bad
		}
		return ip.String(), nil
	}
	return "", bad
}

// Normalized returns the check with its address in canonical form.
func (c Check) Normalized() Check {
	if c.Engine() {
		if u, err := NormalizeURL(c.URL); err == nil {
			c.URL = u
		}
	} else if a, err := c.address(); err == nil {
		if c.Adapter == "http" {
			c.URL = a
		} else {
			c.Target = a
		}
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

// target is the address the probe dials and, when needed, the original TLS
// name. Pin localhost to 127.0.0.1 without changing the name its certificate
// must match, so no resolver or hosts file can route the check elsewhere.
func (c Check) target() (string, string) {
	u, err := NormalizeURL(c.URL)
	if err != nil {
		return "", ""
	}
	parsed, _ := url.Parse(u)
	serverName := ""
	if parsed.Hostname() == "localhost" {
		if parsed.Scheme == "https" {
			serverName = "localhost"
		}
		port := parsed.Port()
		parsed.Host = "127.0.0.1"
		if port != "" {
			parsed.Host = net.JoinHostPort("127.0.0.1", port)
		}
	}
	return parsed.String(), serverName
}

// tokenEnv names a check's stored secret for the probe. It is resolved only
// by the local session, never looked up in the process environment.
func tokenEnv(id string) string { return "TOPO_LOCAL_TOKEN_" + id }

func (c Check) model(token bool) model.Check {
	if !c.Engine() {
		address, _ := c.address()
		x := model.Check{ID: c.ID, Name: c.Name, Kind: c.Adapter, Observer: Observer, Target: address, IntervalSeconds: c.Interval, TimeoutSeconds: c.Timeout, StaleSeconds: c.Stale, FailureThreshold: FailureThreshold, RecoveryThreshold: RecoveryThreshold, TLSMinDays: c.TLSMinDays, MaxDurationMS: c.MaxDurationMS}
		if c.Adapter == "http" {
			x.HTTP.Status = slices.Clone(answered)
			if c.ExpectedStatus != 0 {
				x.HTTP.Status = []int{c.ExpectedStatus}
			}
		}
		return x
	}
	target, serverName := c.target()
	x := model.Check{ID: c.ID, Name: c.Name, Kind: "mihomo", Observer: Observer, Target: target, ServerName: serverName, IntervalSeconds: c.Interval, TimeoutSeconds: c.Timeout, StaleSeconds: c.Stale, FailureThreshold: FailureThreshold, RecoveryThreshold: RecoveryThreshold}
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

// observer is the one vantage point every local check is read from.
func observer() model.Observer {
	return model.Observer{ID: Observer, Name: ObserverName, Context: "this machine's own checks", StaleSeconds: 90}
}

// Config is the topology a local session runs: one observer, this machine,
// and one check per saved check. withToken says which engine checks send a
// stored secret; a network check never sends one.
func Config(checks []Check, withToken func(id string) bool) (model.Config, error) {
	c := model.Config{Schema: model.ConfigSchema, Project: Project, Observers: []model.Observer{observer()}}
	if len(checks) > MaxChecks {
		return c, fail("check_limit")
	}
	seen := map[string]bool{}
	for _, x := range checks {
		if x.Validate() != nil || seen[x.ID] {
			return c, fail("invalid_request")
		}
		seen[x.ID] = true
		c.Checks = append(c.Checks, x.model(x.Engine() && withToken != nil && withToken(x.ID)))
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
