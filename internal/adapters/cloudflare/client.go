package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// The API adapter reads a small, fixed set of Cloudflare resources. Each check
// performs exactly one allowlisted GET, and every response is treated as
// credential-bearing: only closed words, numbers, shape-validated tokens and
// comparisons with the topology leave the adapter.

// PathEvidence marks a sample taken from Cloudflare's own API rather than
// from a request the edge answered; the constant lives in model because the
// engine reads the same marker.
const PathEvidence = model.RemotePlatformPathEvidence

// Body budgets. A tunnel or pool response is a few kilobytes; a DNS record
// list can be larger but is still bounded. A body over its budget is a
// monitor error, never a target failure.
const (
	maxTunnelBytes = 64 << 10
	maxDNSBytes    = 128 << 10
	maxPoolBytes   = 64 << 10
)

// JSON complexity budgets: objects, arrays and total containers. They bound
// the work done before a typed decode, including fields this adapter ignores.
const (
	maxJSONObjects    = 256
	maxJSONArrays     = 256
	maxJSONContainers = 1024
)

// The exact paths this adapter may request. Identifiers are 32 hex characters
// or, for a tunnel, a UUID. The DNS query carries only a name and a closed type, so a
// path outside these forms (including one with traversal segments) is refused
// before any request is built. There is no prefix or suffix exception.
var (
	tunnelPath     = regexp.MustCompile(`^/accounts/[0-9a-fA-F]{32}/cfd_tunnel/` + tunnelIDPattern + `$`)
	dnsPath        = regexp.MustCompile(`^/zones/[0-9a-fA-F]{32}/dns_records$`)
	poolHealthPath = regexp.MustCompile(`^/accounts/[0-9a-fA-F]{32}/load_balancers/pools/[0-9a-fA-F]{32}/health$`)
)

// Dependencies are supplied by the probe, preserving its explicit route and
// TLS policy. The client follows no redirects and carries no ambient proxy.
type Dependencies struct {
	Client func(model.Check, string) (*http.Client, func(), error)
}

// readRequest is one allowlisted GET: an exact path and, only for the DNS
// read, the two query parameters the documented list endpoint takes.
type readRequest struct {
	path  string
	query url.Values
}

func (r readRequest) allowed() bool {
	matched := tunnelPath.MatchString(r.path) || poolHealthPath.MatchString(r.path) || dnsPath.MatchString(r.path)
	if !matched {
		return false
	}
	if dnsPath.MatchString(r.path) {
		return validDNSQuery(r.query)
	}
	return len(r.query) == 0
}

// validDNSQuery accepts exactly one name and one closed record type. Nothing
// else may travel in the query, so the request cannot smuggle a filter or a
// second read onto the allowlisted path.
func validDNSQuery(q url.Values) bool {
	if len(q) != 2 || len(q["name"]) != 1 || len(q["type"]) != 1 {
		return false
	}
	if !validDNSName(q["name"][0]) {
		return false
	}
	switch q["type"][0] {
	case "A", "AAAA", "CNAME":
		return true
	}
	return false
}

// Collect performs the one read the options select. Configuration validation
// has already established the identifiers; it is repeated because a check can
// be executed from a hand-built topology in tests and because a bad value must
// fail before a request, not after one.
func Collect(ctx context.Context, deps Dependencies, x model.Check, target string, o *model.Observation) error {
	options := x.Adapter.CloudflareOptions()
	if err := Validate(options); err != nil {
		return adapters.Local("setup", "invalid_cloudflare_options")
	}
	if err := ValidateTarget(target); err != nil {
		return adapters.Local("setup", "invalid_adapter_url")
	}
	if x.Adapter.TokenEnv == "" {
		return adapters.Local("setup", "cloudflare_token_missing")
	}
	token, err := secure.SecretEnv(x.Adapter.TokenEnv)
	if err != nil {
		return adapters.Local("setup", "cloudflare_token_missing")
	}
	request, limit := readFor(options)
	client, closeClient, err := deps.Client(x, target)
	if err != nil {
		return err
	}
	defer closeClient()
	body, err := readOnce(ctx, client, target, request, token, limit)
	if err != nil {
		return err
	}
	if o.Facts == nil {
		o.Facts = map[string]string{}
	}
	// The token is sent with every read, so the peer's answer is withheld to
	// closed words, numbers, shaped tokens and declared comparisons.
	o.PathEvidence = PathEvidence
	o.Facts["peer_text"] = adapters.Withheld
	switch options.Read {
	case ReadTunnel:
		err = collectTunnel(body, options, o)
	case ReadDNSRecord:
		err = collectDNSRecord(body, options, o)
	case ReadPool:
		err = collectPool(body, options, o)
	default:
		err = adapters.Local("setup", "invalid_cloudflare_options")
	}
	if err != nil {
		return err
	}
	// A shaped value that is the token, or a long run of it, is never evidence:
	// drop it from the facts and from any comma-joined list, so even a token
	// with a reportable shape (a version or colo token) cannot echo itself,
	// whole or split across several values.
	scrubToken(o, token)
	o.Stage = "adapter_api"
	if o.Outcome == "ok" && o.Detail == "" {
		o.Detail = "Cloudflare API state reported; no request to the zone's own hostname was made"
	}
	return nil
}

// minTokenFragment is the shortest run of the bearer token that counts as an
// echo of it. A shorter run is indistinguishable from the platform's own
// three-letter colo, and a peer that splits a token into several version
// strings needs each piece long enough to matter.
const minTokenFragment = 8

// echoesToken reports whether a peer string is the bearer token or a run of at
// least minTokenFragment bytes of it.
func echoesToken(value, token string) bool {
	return value == token || len(value) >= minTokenFragment && strings.Contains(token, value)
}

// scrubToken removes any fact value that echoes the bearer token, and any
// member of a comma-joined token list that does, whether it is the whole token
// or a long enough run of it. Only the facts that carry peer strings are
// examined: numbers are the documented numeric exception, and a token that
// happens to be "3" must not erase a connection count. The topology already
// knows the token; a peer echoing it is not evidence about the platform.
func scrubToken(o *model.Observation, token string) {
	for _, key := range []string{"tunnel_colos", "connector_versions", "oldest_connection_at"} {
		value, ok := o.Facts[key]
		if !ok {
			continue
		}
		if echoesToken(value, token) {
			delete(o.Facts, key)
			continue
		}
		if !strings.Contains(value, ",") {
			continue
		}
		parts := strings.Split(value, ",")
		kept := parts[:0]
		for _, part := range parts {
			if !echoesToken(part, token) {
				kept = append(kept, part)
			}
		}
		if len(kept) == 0 {
			delete(o.Facts, key)
		} else if len(kept) != len(parts) {
			o.Facts[key] = strings.Join(kept, ",")
		}
	}
}

// readFor builds the one request and body budget the selected read uses. Every
// segment comes from a value Validate has already constrained: hex identifiers
// or a tunnel UUID, a DNS name and a closed record type.
func readFor(options model.CloudflareOptions) (readRequest, int64) {
	switch options.Read {
	case ReadTunnel:
		return readRequest{path: "/accounts/" + options.AccountID + "/cfd_tunnel/" + options.TunnelID}, maxTunnelBytes
	case ReadDNSRecord:
		return readRequest{
			path:  "/zones/" + options.ZoneID + "/dns_records",
			query: url.Values{"name": {options.RecordName}, "type": {options.RecordType}},
		}, maxDNSBytes
	case ReadPool:
		return readRequest{path: "/accounts/" + options.AccountID + "/load_balancers/pools/" + options.PoolID + "/health"}, maxPoolBytes
	}
	return readRequest{}, 0
}

// readOnce performs exactly one bounded GET. A network error, a status other
// than 200, an oversized body and an unparseable JSON envelope are all local
// monitor errors: the Cloudflare API is a monitor source, so its own failure
// never becomes a target outage. A redirect is refused by the client, and the
// resulting 3xx is mapped like any other unexpected status.
func readOnce(ctx context.Context, client *http.Client, target string, r readRequest, token string, limit int64) ([]byte, error) {
	if !r.allowed() {
		return nil, adapters.Local("adapter_api", "api_path_not_allowed")
	}
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return nil, adapters.Local("setup", "invalid_adapter_url")
	}
	u.Path = strings.TrimRight(u.Path, "/") + r.path
	u.RawPath = ""
	u.RawQuery = r.query.Encode()
	u.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, adapters.Local("setup", "invalid_adapter_url")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Topophany/"+model.Version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, adapters.Local("adapter_api", "api_unavailable")
	}
	defer resp.Body.Close()
	if err := statusFailure(resp.StatusCode); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, adapters.Local("adapter_api", "api_unavailable")
	}
	if int64(len(body)) > limit {
		return nil, adapters.Local("adapter_api", "response_too_large")
	}
	if secure.JSONResponseBudget(body, maxJSONObjects, maxJSONArrays, maxJSONContainers) != nil {
		return nil, adapters.Local("adapter_api", "unsupported_response_shape")
	}
	return body, nil
}

// statusFailure maps the API's HTTP status. 401 and 403 mean the token cannot
// read this resource; every other non-200 keeps its status in the code, so
// 429 and 5xx are visibly an unavailable API rather than a failing target.
func statusFailure(status int) error {
	switch {
	case status == http.StatusOK:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return adapters.Local("adapter_api", "authentication_failed")
	default:
		return adapters.Local("adapter_api", fmt.Sprintf("http_%d", status))
	}
}

// checkEnvelope reads the API's own success flag. A 200 with success:false is
// the API saying the read did not happen: a monitor error, never a target
// failure. The message text beside it is never read, because it is peer-
// controlled text and may echo request material.
func checkEnvelope(success *bool) error {
	if success == nil {
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	if !*success {
		return adapters.Local("adapter_api", "api_unavailable")
	}
	return nil
}

// decodeJSON bounds a typed decode to the already-budgeted body. Unknown
// fields are ignored: Cloudflare adds fields over time, and this adapter
// stores none of them.
func decodeJSON(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	return nil
}
