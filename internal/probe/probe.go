// Package probe executes bounded checks without changing routing, policy groups,
// TUN settings or services. All diagnostic output is structured and secret-free.
package probe

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflared"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Command = adapters.Command
type Runner struct {
	Command      Command
	ListenPacket func(string, string) (net.PacketConn, error)
	// HostSample is injectable for hermetic resource tests. Production reads OS state.
	HostSample func(context.Context, model.HostOptions) (HostSample, error)
	// since defaults to time.Since; tests model wall-clock steps independently
	// of the monotonic clock without changing the system clock.
	since func(time.Time) time.Duration
}

func (r Runner) Run(ctx context.Context, c model.Config, x model.Check) model.Observation {
	// Keep the monotonic reading for elapsed time across wall-clock changes.
	start := time.Now()
	o := model.Observation{Project: c.Project, Contract: config.Contract(c, x), Check: x.ID, Observer: x.Observer, SampledAt: start.UTC(), Outcome: "unknown", Evidence: "measured", Stage: "setup", Facts: map[string]string{}, Timings: map[string]int64{}, PathEvidence: "requested_not_route_verified"}
	resolved, err := config.ResolveCheck(c, x)
	if err != nil {
		o.Outcome = "error"
		o.Code = "invalid_address_reference"
		o.Detail = "network address reference could not be resolved"
		return o
	}
	x = resolved
	if config.ValidateProbePolicies(c, x) != nil {
		o.Outcome, o.Code, o.Detail = "error", "invalid_probe_policy", "invalid local probe policy"
		return o
	}
	if x.Disabled || x.Maintenance {
		o.Detail = "check not executed"
		return o
	}
	if (x.Kind == "http" || x.Kind == "mihomo") && config.HTTPHeaders(x.HTTP.RequestHeaders) != nil {
		o.Outcome, o.Code, o.Detail = "error", "invalid_http_headers", "invalid local HTTP header configuration"
		return o
	}
	if x.Kind == "http" && config.HTTPHeaderEnvs(x.HTTP.RequestHeaders, x.HTTP.RequestHeaderEnvs) != nil {
		o.Outcome, o.Code, o.Detail = "error", "invalid_http_headers", "invalid local HTTP header configuration"
		return o
	}
	if (x.Kind == "http" || x.Kind == "mihomo" || x.Kind == "cloudflare") && config.HTTPCredentials(c, x) != nil {
		o.Outcome, o.Code, o.Detail = "error", "insecure_http_credentials", "credentials require encrypted transport"
		return o
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(x.TimeoutSeconds)*time.Second)
	defer cancel()
	d := Dialer{Proxies: config.Path(c, x.Path).Proxies, BindIP: x.BindIP}
	target := config.Target(c, x)
	if len(d.Proxies) > 0 {
		o.Facts["transport_hops"] = strconv.Itoa(len(d.Proxies))
		o.PathEvidence = "explicit_proxy_transport"
	}
	o.Facts["connection_semantics"] = "new_client_stream; upstream_core_multiplexing_unknown"
	if x.BindIP != "" {
		o.Facts["bind_ip"] = x.BindIP
	}
	var e error
	switch x.Kind {
	case "http":
		e = r.http(ctx, d, x, target, &o)
	case "tcp", "tls", "ssh-banner":
		e = stream(ctx, d, x, target, &o)
	case "dns":
		e = dns(ctx, x, target, &o)
	case "udp-echo":
		e = udpEcho(ctx, x, target, &o)
	case "icmp":
		e = icmp(ctx, x, target, &o, r.ListenPacket)
	case "mihomo":
		o.Evidence = "reported"
		e = r.mihomo(ctx, d, x, target, &o)
	case "tailscale":
		o.Evidence = "reported"
		e = r.tailscale(ctx, x, &o)
	case "wireguard":
		o.Evidence = "reported"
		e = r.wireguard(ctx, x, &o)
	case "cloudflare":
		o.Evidence = "reported"
		o.PathEvidence = cloudflare.PathEvidence
		o.Stage = "adapter_api"
		// The API read is an HTTP client request, not a core stream against a
		// target; the generic request-stream fact does not describe it.
		delete(o.Facts, "connection_semantics")
		e = r.cloudflare(ctx, d, x, target, &o)
	case "cloudflared":
		o.Evidence = "reported"
		o.PathEvidence = cloudflared.PathEvidence
		// The listener is a local HTTP read, not a client stream to a core.
		delete(o.Facts, "connection_semantics")
		e = r.cloudflared(ctx, x, target, &o)
	case "host":
		o.Evidence = "reported"
		o.PathEvidence = "local_host_runtime_reported"
		delete(o.Facts, "connection_semantics")
		e = r.host(ctx, x, &o)
	case "external":
		// The source runtime is wired through the runner. Reaching this
		// native check path means no source adapter was installed; refuse
		// explicitly rather than pretend a report was collected.
		o.Evidence = "reported"
		o.PathEvidence = "external_source_runtime"
		delete(o.Facts, "connection_semantics")
		e = local("external", "external_runtime_unavailable")
	default:
		e = local("setup", "unsupported_check")
	}
	if e != nil {
		o.Outcome, o.Stage, o.Code = classify(e)
		o.Detail = o.Stage + ": " + o.Code
	}
	since := r.since
	if since == nil {
		since = time.Since
	}
	o.DurationMS = since(start).Milliseconds()
	return o
}
func pinned(address, ip string) string {
	if ip == "" {
		return address
	}
	_, p, e := net.SplitHostPort(address)
	if e != nil {
		return address
	}
	return net.JoinHostPort(ip, p)
}
func stream(ctx context.Context, d Dialer, x model.Check, target string, o *model.Observation) error {
	began := time.Now()
	conn, e := d.DialContext(ctx, "tcp", pinned(target, x.ResolveIP))
	if e != nil {
		return e
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	o.Timings["connect_ms"] = time.Since(began).Milliseconds()
	switch x.Kind {
	case "tcp":
		if len(d.Proxies) == 1 && d.Proxies[0].Kind == "ssh" {
			// Starting ssh and opening its stdio pipes does not acknowledge
			// authentication or the remote -W connection. Use a response-bearing
			// probe to obtain evidence over this transport.
			o.Outcome = "unknown"
			o.Stage = "connect"
			o.Code = "ssh_forward_unverified"
			o.Detail = "SSH forwarding requires a TLS, HTTP or SSH-banner response to verify connection establishment"
			return nil
		}
		o.Detail = "client-visible TCP connect succeeded; remote application not verified"
		o.Facts["response_proof"] = "none"
	case "tls":
		h, _, _ := net.SplitHostPort(target)
		if x.ServerName != "" {
			h = x.ServerName
		}
		tc, e := TLSConfig(h, x.CAFile)
		if e != nil {
			return e
		}
		t := tls.Client(conn, tc)
		b := time.Now()
		if e = t.HandshakeContext(ctx); e != nil {
			return fail("tls", e)
		}
		o.Timings["tls_ms"] = time.Since(b).Milliseconds()
		o.Facts["tls_version"] = fmt.Sprintf("0x%x", t.ConnectionState().Version)
		if x.TLSMinDays > 0 {
			defer func() { certificatePolicy(t.ConnectionState(), x.TLSMinDays, o, time.Now()) }()
		}
		o.Detail = "verified TLS handshake; application and proxy authentication not verified"
		o.Facts["response_proof"] = "verified_tls"
	case "ssh-banner":
		br := bufio.NewReader(io.LimitReader(conn, 8193))
		found := false
		for i := 0; i < 20; i++ {
			line, e := br.ReadString('\n')
			if e != nil {
				return fail("ssh_banner", e)
			}
			if len(line) > 255 {
				return &failure{Stage: "ssh_banner", Code: "line_too_long"}
			}
			if strings.HasPrefix(line, "SSH-2.0-") || strings.HasPrefix(line, "SSH-1.99-") {
				found = true
				break
			}
		}
		if !found {
			return &failure{Stage: "ssh_banner", Code: "invalid_banner"}
		}
		o.Detail = "SSH protocol banner received; host key and authentication not verified"
		o.Facts["response_proof"] = "ssh_banner"
	}
	o.Outcome = "ok"
	o.Connectivity = true
	o.Stage = x.Kind
	return nil
}
func (r Runner) http(ctx context.Context, d Dialer, x model.Check, target string, o *model.Observation) error {
	u, e := url.Parse(target)
	if e != nil {
		return local("setup", "invalid_url")
	}
	name := u.Hostname()
	if x.ServerName != "" {
		name = x.ServerName
	}
	tc, e := TLSConfig(name, x.CAFile)
	if e != nil {
		return e
	}
	// Compression stays off: a body the transport decoded would be counted
	// after decoding, so a peer could stream compressed nothing, empty
	// gzip members without end, for as long as the check may take while
	// the body budget never filled. Undecoded, the budget counts what the
	// peer sends.
	tr := restrictedTransport(tc, time.Duration(x.TimeoutSeconds)*time.Second)
	tr.DialContext = func(ctx context.Context, n, a string) (net.Conn, error) {
		return d.DialContext(ctx, n, pinned(a, x.ResolveIP))
	}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var mu sync.Mutex
	traceTimings := map[string]int64{}
	traceFacts := map[string]string{}
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for k, v := range traceTimings {
			o.Timings[k] = v
		}
		for k, v := range traceFacts {
			o.Facts[k] = v
		}
	}()
	phase := "http"
	var dnsStart, tlsStart time.Time
	requestStart := time.Now()
	trace := &httptrace.ClientTrace{DNSStart: func(httptrace.DNSStartInfo) { mu.Lock(); defer mu.Unlock(); dnsStart = time.Now(); phase = "dns" }, DNSDone: func(info httptrace.DNSDoneInfo) {
		mu.Lock()
		defer mu.Unlock()
		traceTimings["dns_ms"] = time.Since(dnsStart).Milliseconds()
		if info.Err == nil {
			phase = "connect"
		}
	}, TLSHandshakeStart: func() { mu.Lock(); defer mu.Unlock(); tlsStart = time.Now(); phase = "tls" }, TLSHandshakeDone: func(_ tls.ConnectionState, handshakeErr error) {
		mu.Lock()
		defer mu.Unlock()
		traceTimings["tls_ms"] = time.Since(tlsStart).Milliseconds()
		if handshakeErr == nil {
			phase = "http"
		}
	}, GotConn: func(i httptrace.GotConnInfo) {
		mu.Lock()
		defer mu.Unlock()
		traceFacts["client_connection_reused"] = strconv.FormatBool(i.Reused)
	}, GotFirstResponseByte: func() {
		mu.Lock()
		defer mu.Unlock()
		traceTimings["first_byte_ms"] = time.Since(requestStart).Milliseconds()
	}}
	req, e := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), x.HTTP.Method, target, nil)
	if e != nil {
		return local("setup", "invalid_request")
	}
	// The default User-Agent yields to a configured one, literal or
	// environment-backed.
	req.Header.Set("User-Agent", "Topophany/"+model.Version)
	for k, v := range x.HTTP.RequestHeaders {
		// The client sends Request.Host and ignores a Host header.
		if strings.EqualFold(k, "Host") {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	// A secret request header is named in the topology but read from the
	// executor's environment, so its value never reaches a config, a public
	// view or a sample. Resolve and validate every value before sending: a
	// missing variable is a monitor error, and a control-bearing value or bad
	// Host would otherwise become a connect failure that looks like the
	// target's.
	for name, env := range x.HTTP.RequestHeaderEnvs {
		value, e := secure.SecretEnv(env)
		if e != nil || value == "" {
			return local("setup", "http_secret_missing")
		}
		if !config.HTTPHeaderValue(value) {
			return local("setup", "invalid_http_headers")
		}
		if strings.EqualFold(name, "Host") {
			if !config.HostHeader(value) {
				return local("setup", "invalid_http_headers")
			}
			req.Host = value
			continue
		}
		req.Header.Set(name, value)
	}
	token, e := secure.SecretEnv(x.HTTP.BearerEnv)
	if e != nil {
		return local("setup", "http_secret_missing")
	}
	if token != "" {
		if !config.HTTPHeaderValue(token) {
			return local("setup", "invalid_http_headers")
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// Any configured header, including a Host override, may be private.
	// Count the topology's headers because Host lives in Request.Host,
	// outside Request.Header; an environment-backed header, a bearer or a
	// query is private too.
	secret := len(x.HTTP.RequestHeaders) > 0 || len(x.HTTP.RequestHeaderEnvs) > 0 || token != "" || req.URL.RawQuery != "" || u.Scheme != "https" && proxyCredentials(d.Proxies)
	resp, e := client.Do(req)
	if e != nil {
		mu.Lock()
		p := phase
		mu.Unlock()
		out, st, code := classify(e)
		if st == "connect" {
			st = p
		}
		return &failure{Stage: st, Code: code, Local: out == "error"}
	}
	defer resp.Body.Close()
	o.Connectivity = true
	if !secret {
		o.Facts["http_status"] = strconv.Itoa(resp.StatusCode)
	}
	o.Facts["response_proof"] = "http_response"
	if resp.TLS != nil {
		if x.TLSMinDays > 0 {
			defer func() { certificatePolicy(*resp.TLS, x.TLSMinDays, o, time.Now()) }()
		}
		o.Facts["response_proof"] = "verified_https"
	}
	mergeEdgeFacts(o.Facts, EdgeFacts(resp.Header), secret)
	if o.Facts["edge_provider"] == "cloudflare" {
		// The cfL4 Server-Timing entry is the colo's own reading of the
		// connection (TCP or QUIC) that carried the response:
		// edge_rtt_ms, edge_min_rtt_ms, edge_retrans and edge_lost are
		// numbers, and edge_proto is a closed word, so all five cross the
		// secret boundary. They add at most five facts, and only when the
		// entry carries them. The parser returns no header text and never
		// cid.
		mergeEdgeFacts(o.Facts, cloudflare.ServerTimingFacts(resp.Header), secret)
	}
	// A login redirect is evidence by itself, not a provider label: a browser
	// request without an Access session is answered with a redirect to the
	// team's cloudflareaccess.com host. login_redirect is a closed word read
	// from the status and that host; the location's key ID and redirect
	// target are never kept, so it can cross the secret boundary.
	for k, v := range cloudflare.AccessFacts(resp.StatusCode, resp.Header.Get("Location")) {
		o.Facts[k] = v
	}
	if secret {
		o.Facts["peer_text"] = withheld
	}
	limit := x.HTTP.MaxBodyBytes
	if limit <= 0 {
		limit = 65536
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return fail("http_body", e)
	}
	if int64(len(body)) > limit {
		return local("http_body", "body_budget_exceeded")
	}
	// A trace body is the edge's own account of this request's vantage
	// point: the colo it reached, whether WARP and Gateway were on, the
	// HTTP and TLS versions it negotiated and the client country it saw.
	// TraceFacts keeps only closed words and two shape-checked letter pairs;
	// the raw map stays here because a declared colo list must still compare
	// on a secret request, whose observation withholds the colo.
	var traceReport map[string]string
	var exitAddr netip.Addr
	var exitErr error
	parsedExit := false
	if x.HTTP.ExitFormat == "trace" {
		if facts, ok := cloudflare.TraceFacts(body); ok {
			traceReport = facts
			mergeEdgeFacts(o.Facts, facts, secret)
			// Parse the address once for both this fact and expected_exit. Only
			// a recognized trace body yields an unconditional exit_ip: the same
			// 4 KiB cap governs every trace reading. The assertion below keeps
			// its own parser when the body was not recognized.
			exitAddr, exitErr = exitIP(body, x.HTTP)
			parsedExit = true
			if !secret && exitErr == nil {
				o.Facts["exit_ip"] = exitAddr.String()
			}
		}
	}
	accepted := resp.StatusCode >= 200 && resp.StatusCode < 300
	if len(x.HTTP.Status) > 0 {
		accepted = false
		for _, v := range x.HTTP.Status {
			if resp.StatusCode == v {
				accepted = true
			}
		}
	}
	if !accepted {
		return &failure{Stage: "http_assert", Code: "unexpected_status"}
	}
	if x.HTTP.Contains != "" && !strings.Contains(string(body), x.HTTP.Contains) {
		return &failure{Stage: "http_assert", Code: "body_mismatch"}
	}
	if x.HTTP.BodyRegex != "" {
		re, e := config.CompileBodyRegex(x.HTTP.BodyRegex)
		if e != nil {
			return local("setup", "invalid_regex")
		}
		if !re.Match(body) {
			return &failure{Stage: "http_assert", Code: "regex_mismatch"}
		}
	}
	for k, v := range x.HTTP.Headers {
		if resp.Header.Get(k) != v {
			return &failure{Stage: "http_assert", Code: "header_mismatch"}
		}
	}
	if len(x.HTTP.ExpectedExit) > 0 {
		ip, e := exitAddr, exitErr
		if !parsedExit {
			ip, e = exitIP(body, x.HTTP)
		}
		if e != nil {
			return &failure{Stage: "exit_assert", Code: "exit_not_observed"}
		}
		if !secret {
			o.Facts["exit_ip"] = ip.String()
		}
		ok := false
		for _, v := range x.HTTP.ExpectedExit {
			if addr, e := netip.ParseAddr(v); e == nil {
				ok = ok || addr.Unmap() == ip.Unmap()
			} else if prefix, e := netip.ParsePrefix(v); e == nil {
				ok = ok || prefix.Contains(ip) || prefix.Contains(ip.Unmap())
			}
		}
		if !ok {
			o.Outcome = "policy"
			o.Stage = "exit_assert"
			o.Code = "exit_mismatch"
			o.Detail = "request succeeded but observed exit violates the declared policy"
			return nil
		}
	}
	o.Outcome = "ok"
	o.Stage = "http_assert"
	o.Detail = "HTTP assertions passed; result applies only to this request"
	// Policy order: the ordinary assertions and expected_exit decide first,
	// then expected_pops, then expected_trace. A colo the response did not
	// report is no violation: the request may be direct, or the colo may
	// have been withheld from a secret request, and the stored fact is what
	// decides, never the raw header. Vercel's reported region is not a
	// physical colo, so it never satisfies or violates the list.
	if popOutside(o.Facts, x.HTTP.ExpectedPops) {
		o.Outcome, o.Stage, o.Code = "policy", "edge_assert", "pop_mismatch"
		o.Detail = "request succeeded but the edge answered from a colo outside the declared list"
		return nil
	}
	if t := x.HTTP.ExpectedTrace; t != nil {
		// A body TraceFacts did not recognize is not a trace at all. Every
		// declared field would read as unreported, which is true but misleading;
		// the request still passed, as a missing line never violates the policy.
		if traceReport == nil {
			o.Detail = "HTTP assertions passed; the body was not a trace"
			return nil
		}
		var missing []string
		// tracePolicy records a policy sample when a declared value was
		// reported differently. Absent says the trace did not report the line,
		// which is never a violation.
		tracePolicy := func(fact string, declared func(string) bool, code, detail string) (caught, absent bool) {
			got, ok := traceReport[fact]
			if !ok {
				return false, true
			}
			if declared(got) {
				return false, false
			}
			o.Outcome, o.Stage, o.Code, o.Detail = "policy", "trace_assert", code, detail
			return true, false
		}
		if want := t.Warp; want != "" {
			caught, absent := tracePolicy("trace_warp", func(got string) bool { return got == want }, "warp_mismatch",
				"request succeeded but the trace reports WARP outside the declared policy")
			if caught {
				return nil
			}
			if absent {
				missing = append(missing, "warp")
			}
		}
		if want := t.Gateway; want != "" {
			caught, absent := tracePolicy("trace_gateway", func(got string) bool { return got == want }, "gateway_mismatch",
				"request succeeded but the trace reports Gateway outside the declared policy")
			if caught {
				return nil
			}
			if absent {
				missing = append(missing, "gateway")
			}
		}
		if len(t.Colos) > 0 {
			// Compare the raw parse, never the observation's facts: a secret
			// request withholds trace_colo but its declared list still decides
			// the sample, without the observed colo being stored.
			caught, absent := tracePolicy("trace_colo", func(got string) bool { return slices.Contains(t.Colos, got) }, "colo_mismatch",
				"request succeeded but the edge answered from a colo outside the declared list")
			if caught {
				return nil
			}
			if absent {
				missing = append(missing, "colo")
			}
		}
		// A line the trace did not report is not a violation: the request
		// passed, and the detail names what could not be compared, at most
		// the three declared fields.
		if len(missing) > 0 {
			o.Detail = "HTTP assertions passed; the trace did not report " + strings.Join(missing, ", ")
		}
	}
	return nil
}

// popOutside reports whether a passing response named a colo outside the
// declared list. An empty list asserts nothing, an absent colo (no edge, or
// one withheld from a secret request) is no violation, and a Vercel region
// is not a physical colo, so its report never decides this policy. The
// decision reads the stored facts, never a header, so a secret request
// cannot have its withheld colo compared.
func popOutside(facts map[string]string, declared []string) bool {
	return len(declared) > 0 && facts["edge_pop"] != "" && facts["edge_provider"] != "vercel" && !slices.Contains(declared, facts["edge_pop"])
}

// withheld says that what a peer answered is kept only as closed words,
// bounded numbers and comparisons with the topology, as the request carried a
// secret. A peer sent a secret could otherwise hand it back, in a header or a
// name, for its observation to carry to every viewer of the collector, though
// viewers are never given the secret itself. Numbers cross by design: a
// purely numeric secret can appear verbatim in edge_retrans or edge_lost and
// rounded in the RTT readings, the documented numeric exception.
const withheld = adapters.Withheld

// edgeFactSurvivesSecret says whether an edge label may be kept after a
// request that carried a secret. Closed words (the provider, cache status,
// mitigation, protocol and the trace's WARP, Gateway, HTTP and TLS words)
// and bounded numbers (Cloudflare's cfL4 round-trip readings and counts)
// survive; free tokens such as the POP, the Vercel region and the trace's
// colo and client country do not. Access is a closed word merged
// unconditionally below.
func edgeFactSurvivesSecret(k string) bool {
	switch k {
	case "edge_provider", "edge_cache", "edge_mitigation",
		"edge_rtt_ms", "edge_min_rtt_ms", "edge_retrans", "edge_lost", "edge_proto",
		"trace_warp", "trace_gateway", "trace_http", "trace_tls":
		return true
	}
	return false
}

// mergeEdgeFacts copies edge labels into dst under the secret boundary. The
// allowlist is name-only; the adapters own the value contract, so every key
// admitted here is a closed word, a bounded number, or a free label the
// allowlist withholds on a secret request, and a new key must be classified
// there and here alike. The Access constant is merged separately, because its
// fact is evidence even without a provider label.
func mergeEdgeFacts(dst, src map[string]string, secret bool) {
	for k, v := range src {
		if secret && !edgeFactSurvivesSecret(k) {
			continue
		}
		dst[k] = v
	}
}

// proxyCredentials reports whether any hop receives a private login or secret.
// Such a hop can rewrite downstream plain HTTP responses to reflect those values.
func proxyCredentials(hops []model.Proxy) bool {
	for _, p := range hops {
		if p.Username != "" || p.PasswordEnv != "" || p.SSHUser != "" {
			return true
		}
	}
	return false
}

func exitIP(b []byte, h model.HTTPOptions) (netip.Addr, error) {
	s := strings.TrimSpace(string(b))
	switch h.ExitFormat {
	case "trace":
		s = ""
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "ip=") {
				s = strings.TrimSpace(strings.TrimPrefix(line, "ip="))
				break
			}
		}
	case "json":
		if err := secure.JSONResponseBudget(b, 16, 16, 32); err != nil {
			return netip.Addr{}, err
		}
		var v map[string]json.RawMessage
		if e := json.Unmarshal(b, &v); e != nil {
			return netip.Addr{}, e
		}
		key := h.ExitJSONKey
		if key == "" {
			key = "ip"
		}
		if e := json.Unmarshal(v[key], &s); e != nil {
			return netip.Addr{}, e
		}
	}
	return netip.ParseAddr(s)
}
