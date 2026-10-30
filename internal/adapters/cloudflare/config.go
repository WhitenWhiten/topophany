package cloudflare

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// The three allowlisted reads. A check selects exactly one; the client builds
// that read's request from validated identifiers only.
const (
	ReadTunnel    = "tunnel"
	ReadDNSRecord = "dns_record"
	ReadPool      = "pool"
)

// Bounds. These are malformed-input guards and small topology limits, not
// platform quotas: a tunnel's connection list and a pool's origin list are
// both small, and a declaration longer than these is more likely a mistake.
const (
	MaxExpectedConnections = 64
	MaxExpectedColos       = 8
	MaxExpectedOrigins     = 16
	MaxRecordNameBytes     = 253
	MaxRecordContentBytes  = 255
	MaxConnectionVersions  = 4
	MaxTunnelColos         = 8
)

// Account, zone and pool identifiers are 32 hex characters. Tunnel IDs use
// UUIDs; keep the previously accepted compact form without normalizing it so
// existing configurations retain their request paths and measurement contracts.
// Share the tunnel shape with the request allowlist.
const tunnelIDPattern = `(?:[0-9a-fA-F]{32}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`

var hexID = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
var tunnelID = regexp.MustCompile(`^` + tunnelIDPattern + `$`)

// A connector version is a short token such as 2026.10.0. Anything that could
// echo an arbitrary response string is not a version.
var versionToken = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,23}$`)

// Validate checks the nested options. The selected read decides which fields
// are required and which must stay empty: a field belonging to another read is
// refused rather than silently ignored, so a mistyped topology cannot change
// the check's contract without asking for the read it belongs to.
func Validate(o model.CloudflareOptions) error {
	switch o.Read {
	case ReadTunnel:
		if !hexID.MatchString(o.AccountID) {
			return errors.New("cloudflare read requires a 32-character hex account_id")
		}
		if !tunnelID.MatchString(o.TunnelID) {
			return errors.New("cloudflare tunnel read requires a UUID tunnel_id (or legacy 32-character hex ID)")
		}
		if o.ZoneID != "" || o.RecordName != "" || o.RecordType != "" || o.ExpectedProxied != nil || o.ExpectedContent != "" || o.PoolID != "" || len(o.ExpectedOrigins) > 0 {
			return errors.New("cloudflare tunnel read accepts only account_id, tunnel_id, expected_connections and expected_colos")
		}
		if value := o.ExpectedConnections; value != nil && (*value < 1 || *value > MaxExpectedConnections) {
			return errors.New("cloudflare expected_connections must be 1..64")
		}
		if len(o.ExpectedColos) > MaxExpectedColos {
			return errors.New("cloudflare expected_colos accepts at most 8 colos")
		}
		seen := make(map[string]bool, len(o.ExpectedColos))
		for _, colo := range o.ExpectedColos {
			if !model.IsColo(colo) || seen[colo] {
				return errors.New("cloudflare expected_colos must be three uppercase letters, without duplicates")
			}
			seen[colo] = true
		}
	case ReadDNSRecord:
		if !hexID.MatchString(o.ZoneID) {
			return errors.New("cloudflare dns_record read requires a 32-character hex zone_id")
		}
		if !validDNSName(o.RecordName) {
			return errors.New("cloudflare dns_record read requires a DNS record_name")
		}
		switch o.RecordType {
		case "A", "AAAA", "CNAME":
		default:
			return errors.New("cloudflare dns_record record_type must be A, AAAA or CNAME")
		}
		if o.AccountID != "" || o.TunnelID != "" || o.PoolID != "" || len(o.ExpectedOrigins) > 0 || o.ExpectedConnections != nil || len(o.ExpectedColos) > 0 {
			return errors.New("cloudflare dns_record read accepts only zone_id, record_name, record_type, expected_proxied and expected_content")
		}
		if o.ExpectedContent != "" {
			if len(o.ExpectedContent) > MaxRecordContentBytes || secure.Text(o.ExpectedContent) != o.ExpectedContent {
				return errors.New("cloudflare expected_content must be at most 255 bytes of plain text")
			}
		}
	case ReadPool:
		if err := requireIDs(o.AccountID, o.PoolID); err != nil {
			return err
		}
		if o.ZoneID != "" || o.TunnelID != "" || o.RecordName != "" || o.RecordType != "" || o.ExpectedProxied != nil || o.ExpectedContent != "" || o.ExpectedConnections != nil || len(o.ExpectedColos) > 0 {
			return errors.New("cloudflare pool read accepts only account_id, pool_id and expected_origins")
		}
		if len(o.ExpectedOrigins) > MaxExpectedOrigins {
			return errors.New("cloudflare expected_origins accepts at most 16 origins")
		}
		seen := make(map[string]bool, len(o.ExpectedOrigins))
		for _, origin := range o.ExpectedOrigins {
			key, ok := originKey(origin)
			if !ok {
				return errors.New("cloudflare expected_origins must be IP addresses or host names of at most 120 bytes, without duplicates")
			}
			if seen[key] {
				return errors.New("cloudflare expected_origins must not contain duplicates")
			}
			seen[key] = true
		}
	default:
		return errors.New("cloudflare read must be tunnel, dns_record or pool")
	}
	return nil
}

// requireIDs checks the pool read's two hex identifiers: an account and a
// pool. The error names both so a misplaced field is
// reported as what is missing.
func requireIDs(account, resource string) error {
	if !hexID.MatchString(account) {
		return errors.New("cloudflare read requires a 32-character hex account_id")
	}
	if !hexID.MatchString(resource) {
		return errors.New("cloudflare read requires a 32-character hex resource id")
	}
	return nil
}

// originKey returns the comparison key for a declared origin: a canonical IP
// or a lowercased host name. The same function is used by the pool parser, so
// a declaration is compared exactly as the report's keys are.
func originKey(origin string) (string, bool) {
	if origin == "" || len(origin) > 120 || secure.Text(origin) != origin {
		return "", false
	}
	if ip, err := netip.ParseAddr(origin); err == nil {
		return ip.String(), true
	}
	if strings.Contains(origin, "*") || !validDNSName(origin) {
		return "", false
	}
	return strings.ToLower(strings.TrimSuffix(origin, ".")), true
}

// validDNSName accepts a record name: dot-separated labels of letters, digits,
// hyphen and underscore, at most 63 bytes each and 253 in all. A whole label
// of `*` is allowed because a wildcard record has one. A trailing dot is
// accepted; a leading, doubled or trailing empty label is not.
func validDNSName(name string) bool {
	if name == "" || len(name) > MaxRecordNameBytes {
		return false
	}
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		if label == "*" {
			continue
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
				continue
			}
			return false
		}
	}
	return true
}

// The one production API base. The bearer token is sent to it, so a
// non-loopback target must be exactly this host and path: a typo, a lookalike
// host or any other path would hand the token to a different service.
const (
	apiHost     = "api.cloudflare.com"
	apiBasePath = "/client/v4"
)

// ValidateTarget checks the API base URL. Production uses
// https://api.cloudflare.com/client/v4; the only other accepted form is a
// literal loopback address, which tests use, optionally over http. A host name
// such as localhost is not accepted over http (a hosts file could point it
// elsewhere), and a non-loopback host must be api.cloudflare.com at
// /client/v4 so the token cannot be sent elsewhere. The base carries no
// dot segments, query, fragment or user information; the client appends one
// allowlisted path to it.
func ValidateTarget(target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Hostname() == "" || secure.Text(target) != target {
		return errors.New("cloudflare target must be an absolute API base URL without userinfo, query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("cloudflare target port must be 1..65535")
		}
	}
	loopback := net.ParseIP(u.Hostname())
	if loopback != nil && loopback.IsLoopback() {
		if u.Scheme != "http" && u.Scheme != "https" {
			return errors.New("cloudflare target must use http or https")
		}
		return validTargetPath(u.Path)
	}
	if u.Scheme != "https" {
		return errors.New("cloudflare target requires https outside a literal loopback test address")
	}
	if !strings.EqualFold(u.Hostname(), apiHost) {
		return errors.New("cloudflare target host must be api.cloudflare.com (or a literal loopback test address)")
	}
	if strings.TrimRight(u.Path, "/") != apiBasePath {
		return errors.New("cloudflare target path must be /client/v4")
	}
	return validTargetPath(u.Path)
}

// validTargetPath rejects traversal segments and backslashes in the base path.
func validTargetPath(path string) error {
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." || strings.Contains(segment, `\`) {
			return errors.New("cloudflare target path must not contain dot segments or backslashes")
		}
	}
	return nil
}
