package probe

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// dns asks one question over the transport the check declares. A transport
// changes only how the question travels: every one of them parses the same
// wire answer and applies the same expected_ips assertion. An empty
// transport is the historical UDP probe, with one TCP retry for a truncated
// answer; tcp, dot and doh never touch UDP.
func dns(ctx context.Context, x model.Check, target string, o *model.Observation) error {
	transport := x.DNS.Transport
	switch transport {
	case "":
		transport = "udp"
	case "udp", "tcp", "dot", "doh":
	default:
		return local("setup", "invalid_dns_transport")
	}
	var idb [2]byte
	if _, e := rand.Read(idb[:]); e != nil {
		return local("setup", "entropy_failure")
	}
	id := binary.BigEndian.Uint16(idb[:])
	qt := uint16(1)
	if x.DNS.Type == "AAAA" {
		qt = 28
	}
	q, e := dnsQuery(id, x.DNS.Name, qt)
	if e != nil {
		return local("setup", "invalid_dns_question")
	}
	var bind netip.AddrPort
	if x.BindIP != "" {
		if bind, e = bindAddr(x.BindIP); e != nil {
			return e
		}
	}
	var b []byte
	switch transport {
	case "udp":
		b, e = dnsOverUDP(ctx, x, target, q, id, bind, o)
	case "tcp":
		b, e = dnsOverTCP(ctx, x, target, q, bind, o)
	case "dot":
		b, e = dnsOverTLS(ctx, x, target, q, bind, o)
	case "doh":
		b, e = dnsOverHTTPS(ctx, x, target, q, bind, o)
	}
	if e != nil {
		return e
	}
	// A message whose transaction ID is not the question's is not an answer:
	// it may be stale, stray or spoofed. The monitor can say only that it
	// could not attribute what arrived, never that the resolver is down. A
	// matching ID that still does not parse is a failed answer below, as for
	// a negative response.
	if len(b) >= 2 && binary.BigEndian.Uint16(b) != id {
		return &failure{Stage: "dns_answer", Code: "unexpected_transaction_id", Local: true}
	}
	ips, e := parseDNS(b, id, x.DNS.Name, qt)
	if e != nil {
		return &failure{Stage: "dns_answer", Code: "invalid_or_negative_answer"}
	}
	if len(ips) == 0 {
		return &failure{Stage: "dns_answer", Code: "no_address_answer"}
	}
	if len(x.DNS.ExpectedIPs) > 0 {
		allow := map[netip.Addr]bool{}
		for _, s := range x.DNS.ExpectedIPs {
			ip, _ := netip.ParseAddr(s)
			allow[ip.Unmap()] = true
		}
		for _, ip := range ips {
			if !allow[ip.Unmap()] {
				return &failure{Stage: "dns_answer", Code: "address_outside_allowlist"}
			}
		}
	}
	vals := make([]string, 0, len(ips))
	for _, ip := range ips {
		vals = append(vals, ip.String())
	}
	addresses := strings.Join(vals, ",")
	if len(addresses) > 2048 {
		return local("dns_answer", "address_evidence_budget_exceeded")
	}
	o.Facts["addresses"] = addresses
	o.Facts["response_proof"] = "dns_response_not_dnssec"
	o.Outcome = "ok"
	o.Stage = "dns_answer"
	o.Connectivity = true
	o.Detail = "configured resolver returned addresses satisfying assertions; DNSSEC not validated"
	return nil
}

// dnsOverUDP is the historical transport: one UDP question, and one TCP
// retry only when the resolver says the answer was truncated. The fact is
// udp until that retry succeeds, so a failed retry still says how far the
// exchange got.
func dnsOverUDP(ctx context.Context, x model.Check, target string, q []byte, id uint16, bind netip.AddrPort, o *model.Observation) ([]byte, error) {
	target = pinned(target, x.ResolveIP)
	nd := net.Dialer{}
	if bind.IsValid() {
		nd.LocalAddr = net.UDPAddrFromAddrPort(bind)
	}
	conn, e := dialContext(ctx, &nd, "udp", target)
	if e != nil {
		return nil, fail("dns_transport", e)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if _, e = conn.Write(q); e != nil {
		return nil, fail("dns_transport", e)
	}
	buf := make([]byte, 65535)
	n, e := conn.Read(buf)
	if e != nil {
		return nil, fail("dns_transport", e)
	}
	b := buf[:n]
	o.Facts["dns_transport"] = "udp"
	if len(b) >= 4 && binary.BigEndian.Uint16(b) == id && b[2]&2 != 0 {
		td := net.Dialer{}
		if bind.IsValid() {
			td.LocalAddr = net.TCPAddrFromAddrPort(bind)
		}
		tcp, e := dialContext(ctx, &td, "tcp", target)
		if e != nil {
			return nil, fail("dns_tcp", e)
		}
		defer tcp.Close()
		if dl, ok := ctx.Deadline(); ok {
			_ = tcp.SetDeadline(dl)
		}
		stop := context.AfterFunc(ctx, func() { tcp.Close() })
		defer stop()
		b, _, e = dnsOverStream(tcp, q, time.Time{})
		if e != nil {
			return nil, fail("dns_tcp", e)
		}
		o.Facts["dns_transport"] = "tcp_fallback"
	}
	return b, nil
}

// dnsOverTCP asks over TCP directly, without trying UDP first.
func dnsOverTCP(ctx context.Context, x model.Check, target string, q []byte, bind netip.AddrPort, o *model.Observation) ([]byte, error) {
	target = pinned(target, x.ResolveIP)
	d := net.Dialer{}
	if bind.IsValid() {
		d.LocalAddr = net.TCPAddrFromAddrPort(bind)
	}
	conn, e := dialContext(ctx, &d, "tcp", target)
	if e != nil {
		return nil, fail("dns_transport", e)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	b, _, e := dnsOverStream(conn, q, time.Time{})
	if e != nil {
		return nil, fail("dns_transport", e)
	}
	o.Facts["dns_transport"] = "tcp"
	return b, nil
}

// dnsOverTLS asks the resolver over DNS-over-TLS. The certificate name is
// server_name, or the target host when server_name is empty; a resolver's
// certificate is normally issued for a name, so an IP target needs
// server_name (for 1.1.1.1, one.one.one.one or cloudflare-dns.com). resolve_ip
// pins the dial address without changing that TLS name, as everywhere else.
// first_byte_ms runs from before the TCP connect through the handshake to the
// first byte of the framed answer, so it includes the handshake; tls_ms
// isolates the handshake.
func dnsOverTLS(ctx context.Context, x model.Check, target string, q []byte, bind netip.AddrPort, o *model.Observation) ([]byte, error) {
	host, _, e := net.SplitHostPort(target)
	if e != nil {
		return nil, local("setup", "invalid_dns_target")
	}
	name := host
	if x.ServerName != "" {
		name = x.ServerName
	}
	tc, e := TLSConfig(name, x.CAFile)
	if e != nil {
		return nil, e
	}
	d := net.Dialer{}
	if bind.IsValid() {
		d.LocalAddr = net.TCPAddrFromAddrPort(bind)
	}
	began := time.Now()
	raw, e := dialContext(ctx, &d, "tcp", pinned(target, x.ResolveIP))
	if e != nil {
		return nil, fail("dns_transport", e)
	}
	defer raw.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	conn := tls.Client(raw, tc)
	handshake := time.Now()
	if e = conn.HandshakeContext(ctx); e != nil {
		return nil, fail("dns_transport", e)
	}
	o.Timings["tls_ms"] = time.Since(handshake).Milliseconds()
	b, first, e := dnsOverStream(conn, q, began)
	if e != nil {
		return nil, fail("dns_transport", e)
	}
	o.Timings["first_byte_ms"] = first.Milliseconds()
	o.Facts["dns_transport"] = "dot"
	return b, nil
}

// maxDoHBody is one DNS message in TCP framing's largest size plus a byte,
// so a body over it cannot be a DNS answer and is refused without reading an
// unbounded response.
const maxDoHBody = 65536

// dnsOverHTTPS asks over DNS-over-HTTPS (RFC 8484) with POST. The wire query
// is the body, so the URL carries no query string and no credential; POST is
// the same question as the GET form and asks the resolver to answer it, with
// no other side effect. This is the probe's only non-GET request. The
// exchange is read directly, not through the check's proxy path: config
// validation refuses a proxy chain for the dns kind. The TLS name is
// server_name, or the URL's host; resolve_ip pins the dial address.
func dnsOverHTTPS(ctx context.Context, x model.Check, target string, q []byte, bind netip.AddrPort, o *model.Observation) ([]byte, error) {
	u, e := url.Parse(target)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, local("setup", "invalid_doh_url")
	}
	name := u.Hostname()
	if x.ServerName != "" {
		name = x.ServerName
	}
	tc, e := TLSConfig(name, x.CAFile)
	if e != nil {
		return nil, e
	}
	d := net.Dialer{}
	if bind.IsValid() {
		d.LocalAddr = net.TCPAddrFromAddrPort(bind)
	}
	// The HTTP transport dials and handshakes in another goroutine, so the
	// timings cross back through atomics; requestStart is read there under
	// the trace and written before the request starts.
	var tlsMS, firstMS atomic.Int64
	var gotTLS, gotFirst atomic.Bool
	requestStart := time.Now()
	tr := restrictedTransport(tc, time.Duration(x.TimeoutSeconds)*time.Second)
	tr.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		raw, e := dialContext(ctx, &d, network, pinned(address, x.ResolveIP))
		if e != nil {
			return nil, fail("dns_transport", e)
		}
		conn := tls.Client(raw, tc)
		handshake := time.Now()
		if e = conn.HandshakeContext(ctx); e != nil {
			raw.Close()
			return nil, fail("dns_transport", e)
		}
		tlsMS.Store(time.Since(handshake).Milliseconds())
		gotTLS.Store(true)
		return conn, nil
	}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() {
		firstMS.Store(time.Since(requestStart).Milliseconds())
		gotFirst.Store(true)
	}}
	req, e := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, target, bytes.NewReader(q))
	if e != nil {
		return nil, local("setup", "invalid_doh_url")
	}
	req.Header.Set("User-Agent", "Topophany/"+model.Version)
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, e := client.Do(req)
	if gotTLS.Load() {
		o.Timings["tls_ms"] = tlsMS.Load()
	}
	if gotFirst.Load() {
		o.Timings["first_byte_ms"] = firstMS.Load()
	}
	if e != nil {
		var f *failure
		if errors.As(e, &f) {
			return nil, e
		}
		return nil, fail("dns_transport", e)
	}
	defer resp.Body.Close()
	o.Facts["dns_transport"] = "doh"
	// A status or content type the exchange did not ask for is the peer's
	// answer, so it fails the check; only a body the monitor cannot read
	// within its budget is a monitor error (below).
	if resp.StatusCode != http.StatusOK {
		return nil, &failure{Stage: "dns_transport", Code: "http_" + strconv.Itoa(resp.StatusCode)}
	}
	ct := resp.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	if !strings.EqualFold(strings.TrimSpace(ct), "application/dns-message") {
		return nil, &failure{Stage: "dns_transport", Code: "content_type"}
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, maxDoHBody+1))
	if e != nil {
		return nil, fail("dns_transport", e)
	}
	if len(body) > maxDoHBody {
		return nil, &failure{Stage: "dns_transport", Code: "response_too_large", Local: true}
	}
	return body, nil
}

// dnsOverStream sends one question on a stream with RFC 1035's length prefix
// and reads one length-prefixed answer. since starts the first-byte reading;
// zero measures from the write. The connection's deadline bounds both reads.
func dnsOverStream(conn net.Conn, q []byte, since time.Time) ([]byte, time.Duration, error) {
	if since.IsZero() {
		since = time.Now()
	}
	packet := binary.BigEndian.AppendUint16(nil, uint16(len(q)))
	packet = append(packet, q...)
	if _, e := conn.Write(packet); e != nil {
		return nil, 0, e
	}
	var lb [2]byte
	if _, e := io.ReadFull(conn, lb[:]); e != nil {
		return nil, 0, e
	}
	first := time.Since(since)
	b := make([]byte, int(binary.BigEndian.Uint16(lb[:])))
	if _, e := io.ReadFull(conn, b); e != nil {
		return nil, 0, e
	}
	return b, first, nil
}

func dnsQuery(id uint16, name string, qt uint16) ([]byte, error) {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b, id)
	binary.BigEndian.PutUint16(b[2:], 0x0100)
	binary.BigEndian.PutUint16(b[4:], 1)
	name = strings.TrimSuffix(name, ".")
	if len(name) > 253 {
		return nil, errors.New("name too long")
	}
	for _, p := range strings.Split(name, ".") {
		if len(p) == 0 || len(p) > 63 {
			return nil, errors.New("invalid label")
		}
		b = append(b, byte(len(p)))
		b = append(b, p...)
	}
	b = append(b, 0)
	b = binary.BigEndian.AppendUint16(b, qt)
	b = binary.BigEndian.AppendUint16(b, 1)
	return b, nil
}

// dnsName bounds pointers, labels and expanded name length; a malformed or
// cyclic compression pointer must never hang an executor.
func dnsName(b []byte, off int) (string, int, error) {
	next := -1
	seen := map[int]bool{}
	parts := []string{}
	size := 0
	for steps := 0; steps < 128; steps++ {
		if off < 0 || off >= len(b) || seen[off] {
			return "", 0, errors.New("invalid DNS name pointer")
		}
		seen[off] = true
		l := int(b[off])
		if l&0xc0 == 0xc0 {
			if off+1 >= len(b) {
				return "", 0, io.ErrUnexpectedEOF
			}
			if next < 0 {
				next = off + 2
			}
			off = (l&0x3f)<<8 | int(b[off+1])
			continue
		}
		off++
		if l == 0 {
			if next < 0 {
				next = off
			}
			return strings.ToLower(strings.Join(parts, ".")), next, nil
		}
		if l > 63 || off+l > len(b) {
			return "", 0, errors.New("invalid DNS label")
		}
		size += l + 1
		if size > 254 {
			return "", 0, errors.New("expanded DNS name too long")
		}
		// A literal dot inside a wire label is not a label separator. Reject
		// ambiguous presentation names instead of matching an unrelated owner.
		label := b[off : off+l]
		for _, c := range label {
			if c <= 32 || c >= 127 || c == '.' || c == '\\' {
				return "", 0, errors.New("unsupported DNS label character")
			}
		}
		parts = append(parts, string(label))
		off += l
	}
	return "", 0, errors.New("DNS pointer budget exceeded")
}
func parseDNS(b []byte, id uint16, name string, qt uint16) ([]netip.Addr, error) {
	if len(b) < 12 || binary.BigEndian.Uint16(b) != id || b[2]&0x80 == 0 || b[2]&0x78 != 0 || b[2]&2 != 0 || b[3]&15 != 0 || binary.BigEndian.Uint16(b[4:]) != 1 {
		return nil, errors.New("bad DNS header")
	}
	q, off, e := dnsName(b, 12)
	if e != nil {
		return nil, e
	}
	if off+4 > len(b) || q != strings.ToLower(strings.TrimSuffix(name, ".")) || binary.BigEndian.Uint16(b[off:]) != qt || binary.BigEndian.Uint16(b[off+2:]) != 1 {
		return nil, errors.New("mismatched DNS question")
	}
	off += 4
	an := int(binary.BigEndian.Uint16(b[6:]))
	if an > 1024 {
		return nil, errors.New("too many DNS answers")
	}
	byName := map[string][]netip.Addr{}
	cname := map[string]string{}
	for i := 0; i < an; i++ {
		owner, n, e := dnsName(b, off)
		if e != nil {
			return nil, e
		}
		off = n
		if off+10 > len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		typ := binary.BigEndian.Uint16(b[off:])
		class := binary.BigEndian.Uint16(b[off+2:])
		ln := int(binary.BigEndian.Uint16(b[off+8:]))
		off += 10
		if off+ln > len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		if class == 1 {
			if typ == qt && (typ == 1 && ln == 4 || typ == 28 && ln == 16) {
				ip, ok := netip.AddrFromSlice(b[off : off+ln])
				if ok {
					byName[owner] = append(byName[owner], ip)
				}
			}
			if typ == 5 {
				v, end, e := dnsName(b, off)
				if e != nil || end != off+ln {
					return nil, errors.New("invalid CNAME")
				}
				cname[owner] = v
			}
		}
		off += ln
	}
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		if seen[q] {
			return nil, errors.New("CNAME cycle")
		}
		seen[q] = true
		if ips := byName[q]; len(ips) > 0 {
			return ips, nil
		}
		n, ok := cname[q]
		if !ok {
			return nil, nil
		}
		q = n
	}
	return nil, errors.New("CNAME budget exceeded")
}
func udpEcho(ctx context.Context, x model.Check, target string, o *model.Observation) error {
	target = pinned(target, x.ResolveIP)
	d := net.Dialer{}
	if x.BindIP != "" {
		a, e := bindAddr(x.BindIP)
		if e != nil {
			return e
		}
		d.LocalAddr = net.UDPAddrFromAddrPort(a)
	}
	c, e := dialContext(ctx, &d, "udp", target)
	if e != nil {
		return fail("udp", e)
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	payload := make([]byte, 32)
	copy(payload, []byte("TOPH"))
	if _, e = rand.Read(payload[4:]); e != nil {
		return local("setup", "entropy_failure")
	}
	if _, e = c.Write(payload); e != nil {
		return fail("udp", e)
	}
	buf := make([]byte, 1024)
	n, e := c.Read(buf)
	if e != nil {
		return fail("udp", e)
	}
	if !bytes.Equal(buf[:n], payload) {
		return &failure{Stage: "udp_echo", Code: "payload_mismatch"}
	}
	o.Outcome = "ok"
	o.Connectivity = true
	o.Stage = "udp_echo"
	o.Facts["response_proof"] = "nonce_echo"
	o.Detail = "nonce echoed by the configured UDP endpoint; does not test arbitrary UDP services"
	return nil
}
func checksum(b []byte) uint16 {
	sum := uint32(0)
	for len(b) > 1 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) > 0 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 > 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return ^uint16(sum)
}
func icmp(ctx context.Context, x model.Check, target string, o *model.Observation, listen func(string, string) (net.PacketConn, error)) error {
	if listen == nil {
		listen = net.ListenPacket
	}
	bind := x.BindIP
	if bind == "" {
		bind = "0.0.0.0"
	}
	c, e := listen("ip4:icmp", bind)
	if e != nil {
		if errors.Is(e, os.ErrPermission) {
			return local("icmp", "raw_socket_permission_required")
		}
		return local("icmp", "raw_socket_unavailable")
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	packet := make([]byte, 32)
	if _, e = rand.Read(packet[4:]); e != nil {
		return local("setup", "entropy_failure")
	}
	packet[0] = 8
	binary.BigEndian.PutUint16(packet[2:], checksum(packet))
	start := time.Now()
	if _, e = c.WriteTo(packet, &net.IPAddr{IP: net.ParseIP(target)}); e != nil {
		return fail("icmp", e)
	}
	buf := make([]byte, 1500)
	for i := 0; i < 128; i++ {
		n, src, e := c.ReadFrom(buf)
		if e != nil {
			return fail("icmp", e)
		}
		ip, ok := src.(*net.IPAddr)
		if !ok || !ip.IP.Equal(net.ParseIP(target)) {
			continue
		}
		b := buf[:n]
		if len(b) > 0 && b[0]>>4 == 4 {
			h := int(b[0]&15) * 4
			if h < 20 || h > len(b) {
				continue
			}
			b = b[h:]
		}
		if len(b) != len(packet) || b[0] != 0 || b[1] != 0 || checksum(b) != 0 || !bytes.Equal(b[4:], packet[4:]) {
			continue
		}
		o.Outcome = "ok"
		o.Connectivity = true
		o.Stage = "icmp"
		o.Facts["response_proof"] = "icmp_echo"
		o.Facts["rtt_ms"] = strconv.FormatInt(time.Since(start).Milliseconds(), 10)
		o.Detail = "IPv4 ICMP echo reply; TCP, UDP and services not inferred"
		return nil
	}
	return &failure{Stage: "icmp", Code: "reply_budget_exceeded"}
}
