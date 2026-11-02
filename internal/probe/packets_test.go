package probe

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func dnsAnswer(q []byte, addresses ...string) []byte {
	out := append([]byte(nil), q...)
	binary.BigEndian.PutUint16(out[2:], 0x8180)
	binary.BigEndian.PutUint16(out[6:], uint16(len(addresses)))
	for _, s := range addresses {
		ip := netip.MustParseAddr(s)
		typ := uint16(1)
		data := ip.AsSlice()
		if ip.Is6() {
			typ = 28
		}
		out = append(out, 0xc0, 0x0c)
		out = binary.BigEndian.AppendUint16(out, typ)
		out = binary.BigEndian.AppendUint16(out, 1)
		out = binary.BigEndian.AppendUint32(out, 60)
		out = binary.BigEndian.AppendUint16(out, uint16(len(data)))
		out = append(out, data...)
	}
	return out
}
func dnsFixture(t *testing.T, tcpFallback bool, answer func([]byte) []byte) string {
	t.Helper()
	var udp net.PacketConn
	var tcp net.Listener
	var e error
	// Pick TCP first: Windows can assign UDP ephemeral ports from ranges that
	// are excluded for TCP, so retrying adjacent UDP ports can keep failing.
	// Keep the TCP listener open while binding UDP to the same port; retry if
	// another UDP socket already owns it.
	for attempt := 0; attempt < 20; attempt++ {
		address := "127.0.0.1:0"
		if !tcpFallback {
			udp, e = net.ListenPacket("udp", address)
			break
		}
		tcp, e = net.Listen("tcp", address)
		if e != nil {
			t.Fatal(e)
		}
		if udp, e = net.ListenPacket("udp", tcp.Addr().String()); e == nil {
			break
		}
		tcp.Close()
	}
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, a, e := udp.ReadFrom(buf)
			if e != nil {
				return
			}
			q := append([]byte(nil), buf[:n]...)
			out := answer(q)
			if tcpFallback {
				out = append([]byte(nil), q...)
				binary.BigEndian.PutUint16(out[2:], 0x8380)
			}
			_, _ = udp.WriteTo(out, a)
		}
	}()
	if tcp != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				conn, e := tcp.Accept()
				if e != nil {
					return
				}
				func() {
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(time.Second))
					var length [2]byte
					if _, e := io.ReadFull(conn, length[:]); e != nil {
						return
					}
					q := make([]byte, int(binary.BigEndian.Uint16(length[:])))
					if _, e = io.ReadFull(conn, q); e != nil {
						return
					}
					out := answer(q)
					frame := binary.BigEndian.AppendUint16(nil, uint16(len(out)))
					frame = append(frame, out...)
					_, _ = conn.Write(frame)
				}()
			}
		}()
	}
	t.Cleanup(func() {
		udp.Close()
		if tcp != nil {
			tcp.Close()
		}
		wg.Wait()
	})
	return udp.LocalAddr().String()
}
func TestDNSUDPAndTCPFallback(t *testing.T) {
	for _, tcp := range []bool{false, true} {
		t.Run(map[bool]string{false: "UDP", true: "TCP fallback"}[tcp], func(t *testing.T) {
			server := dnsFixture(t, tcp, func(q []byte) []byte { return dnsAnswer(q, "203.0.113.8") })
			x := httpCheck("")
			x.Kind = "dns"
			x.Target = server
			x.DNS = model.DNSOptions{Name: "test.example", Type: "A", ExpectedIPs: []string{"203.0.113.8"}}
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			want := "udp"
			if tcp {
				want = "tcp_fallback"
			}
			if o.Outcome != "ok" || o.Facts["dns_transport"] != want || o.Facts["addresses"] != "203.0.113.8" {
				t.Fatal(o)
			}
		})
	}
}
func TestDNSAAAAAllowlistMismatchAndNegativeResponses(t *testing.T) {
	for _, tt := range []struct {
		name, typ     string
		ips, expected []string
		mutate        func([]byte)
		outcome, code string
	}{{"IPv6 response", "AAAA", []string{"2001:db8::8"}, []string{"2001:db8::8"}, nil, "ok", ""}, {"allowlist rejects unexpected IP", "A", []string{"192.0.2.9"}, []string{"203.0.113.8"}, nil, "fail", "address_outside_allowlist"}, {"wrong transaction ID", "A", []string{"203.0.113.8"}, nil, func(b []byte) { b[0] ^= 0xff }, "error", "unexpected_transaction_id"}, {"NXDOMAIN", "A", nil, nil, func(b []byte) { b[3] = 0x83 }, "fail", "invalid_or_negative_answer"}, {"no address answer", "A", nil, nil, nil, "fail", "no_address_answer"}} {
		t.Run(tt.name, func(t *testing.T) {
			server := dnsFixture(t, false, func(q []byte) []byte {
				b := dnsAnswer(q, tt.ips...)
				if tt.mutate != nil {
					tt.mutate(b)
				}
				return b
			})
			x := httpCheck("")
			x.Kind = "dns"
			x.Target = server
			x.DNS = model.DNSOptions{Name: "test.example", Type: tt.typ, ExpectedIPs: tt.expected}
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tt.outcome || o.Code != tt.code {
				t.Fatal(o)
			}
		})
	}
}
func TestDNSParserCNAMEAndCompressionBudgets(t *testing.T) {
	q, _ := dnsQuery(42, "test.example", 1)
	b := dnsAnswer(q)
	binary.BigEndian.PutUint16(b[6:], 2)
	alias, _ := dnsQuery(0, "alias.example", 1)
	name := alias[12 : len(alias)-4]
	b = append(b, 0xc0, 0x0c, 0, 5, 0, 1, 0, 0, 0, 60)
	b = binary.BigEndian.AppendUint16(b, uint16(len(name)))
	offset := len(b)
	b = append(b, name...)
	b = binary.BigEndian.AppendUint16(b, 0xc000|uint16(offset))
	b = append(b, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 203, 0, 113, 8)
	ips, e := parseDNS(b, 42, "test.example", 1)
	if e != nil || len(ips) != 1 || ips[0].String() != "203.0.113.8" {
		t.Fatal("valid compressed CNAME failed", ips, e)
	}
	for _, packet := range [][]byte{{0xc0, 0x00}, {0xc0}, {0xff, 0xff}, {64, 0}, {3, 'x'}, {0xc0, 2, 0xc0, 0}} {
		if _, _, e := dnsName(packet, 0); e == nil {
			t.Fatal("invalid compression accepted", packet)
		}
	}
	if _, e = dnsQuery(1, strings.Repeat("x", 64)+".test", 1); e == nil {
		t.Fatal("overlong label accepted")
	}
	if _, e = parseDNS(b, 99, "test.example", 1); e == nil {
		t.Fatal("wrong transaction accepted")
	}
	for n := 0; n < len(b); n++ {
		_, _ = parseDNS(b[:n], 42, "test.example", 1)
	}
}
func TestUDPEchoRequiresResponseNonce(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonce returned", true: "wrong nonce"}[bad], func(t *testing.T) {
			s, e := net.ListenPacket("udp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				buf := make([]byte, 1024)
				n, a, e := s.ReadFrom(buf)
				if e == nil {
					if bad {
						buf[0] ^= 1
					}
					_, _ = s.WriteTo(buf[:n], a)
				}
			}()
			x := httpCheck("")
			x.Kind = "udp-echo"
			x.Target = s.LocalAddr().String()
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			<-done
			if bad {
				if o.Outcome != "fail" || o.Code != "payload_mismatch" {
					t.Fatal(o)
				}
			} else if o.Outcome != "ok" || o.Facts["response_proof"] != "nonce_echo" {
				t.Fatal(o)
			}
		})
	}
}
func TestLocalBindFailureIsMonitorError(t *testing.T) {
	x := httpCheck("http://127.0.0.1:1/")
	x.BindIP = "192.0.2.254"
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" || o.Code != "local_bind_failed" {
		t.Fatal("unconfigured local bind called target outage", o)
	}
}

func TestBindToAMissingInterfaceNeverProbesFromAnother(t *testing.T) {
	tcp, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer tcp.Close()
	udp, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer udp.Close()
	for kind, target := range map[string]string{"http": "http://" + tcp.Addr().String() + "/", "tcp": tcp.Addr().String(), "udp-echo": udp.LocalAddr().String(), "dns": udp.LocalAddr().String()} {
		x := httpCheck(target)
		x.Kind = kind
		x.DNS = model.DNSOptions{Name: "example.invalid", Type: "A"}
		x.BindIP = "::1%topo-missing0"
		o := (Runner{}).Run(context.Background(), testkit.Config(), x)
		if o.Outcome != "error" || o.Code != "local_bind_interface_unavailable" {
			t.Fatal(kind, "bound to a missing interface", o)
		}
	}
	_ = tcp.(*net.TCPListener).SetDeadline(time.Now().Add(50 * time.Millisecond))
	if c, e := tcp.Accept(); e == nil {
		c.Close()
		t.Fatal("a check bound to a missing interface connected from another")
	}
	_ = udp.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, _, e := udp.ReadFrom(make([]byte, 512)); e == nil {
		t.Fatal("a check bound to a missing interface sent from another")
	}
}
func TestBindKeepsTheZone(t *testing.T) {
	ifs, e := net.Interfaces()
	if e != nil || len(ifs) == 0 {
		t.Skip("no interfaces to bind to")
	}
	for _, zone := range []string{ifs[0].Name, strconv.Itoa(ifs[0].Index)} {
		a, e := bindAddr("fe80::1%" + zone)
		if e != nil || a.Addr().Zone() != zone || net.TCPAddrFromAddrPort(a).Zone != zone || net.UDPAddrFromAddrPort(a).Zone != zone {
			t.Fatal(zone, a, e)
		}
	}
	for _, s := range []string{"fe80::1%topo-missing0", "fe80::1%0", "not-an-ip"} {
		if _, e := bindAddr(s); e == nil {
			t.Fatal("bound", s)
		}
	}
}

type packetFixture struct {
	packet     []byte
	read       int
	closed     atomic.Bool
	neverValid bool
}

func (p *packetFixture) ReadFrom(b []byte) (int, net.Addr, error) {
	p.read++
	data := append([]byte(nil), p.packet...)
	data[0] = 0
	data[2] = 0
	data[3] = 0
	source := net.IPv4(127, 0, 0, 1)
	if p.read == 1 {
		source = net.IPv4(127, 0, 0, 2)
	}
	if p.read == 2 || p.neverValid {
		data[10] ^= 1
	}
	binary.BigEndian.PutUint16(data[2:], checksum(data))
	copy(b, data)
	return len(data), &net.IPAddr{IP: source}, nil
}
func (p *packetFixture) WriteTo(b []byte, a net.Addr) (int, error) {
	p.packet = append([]byte(nil), b...)
	return len(b), nil
}
func (p *packetFixture) Close() error                     { p.closed.Store(true); return nil }
func (p *packetFixture) LocalAddr() net.Addr              { return &net.IPAddr{IP: net.IPv4zero} }
func (p *packetFixture) SetDeadline(time.Time) error      { return nil }
func (p *packetFixture) SetReadDeadline(time.Time) error  { return nil }
func (p *packetFixture) SetWriteDeadline(time.Time) error { return nil }
func TestICMPCorrelationPermissionAndBudget(t *testing.T) {
	x := httpCheck("")
	x.Kind = "icmp"
	x.Target = "127.0.0.1"
	r := Runner{ListenPacket: func(string, string) (net.PacketConn, error) { return nil, os.ErrPermission }}
	o := r.Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" || o.Code != "raw_socket_permission_required" {
		t.Fatal(o)
	}
	fixture := &packetFixture{}
	r.ListenPacket = func(string, string) (net.PacketConn, error) { return fixture, nil }
	o = r.Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || fixture.read != 3 || !fixture.closed.Load() {
		t.Fatal("wrong source/nonce accepted or socket leaked", o, fixture.read)
	}
	fixture = &packetFixture{neverValid: true}
	o = r.Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "fail" || o.Code != "reply_budget_exceeded" || fixture.read != 128 {
		t.Fatal("unbounded unrelated packet loop", o, fixture.read)
	}
}
func TestICMPRealLoopback(t *testing.T) {
	x := httpCheck("")
	x.Kind = "icmp"
	x.Target = "127.0.0.1"
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome == "error" && (o.Code == "raw_socket_permission_required" || o.Code == "raw_socket_unavailable") {
		t.Skip("raw socket unavailable; permission and correlation covered by deterministic tests")
	}
	if o.Outcome != "ok" || o.Facts["response_proof"] != "icmp_echo" {
		t.Fatal(o)
	}
}
func FuzzDNSPacket(f *testing.F) {
	q, _ := dnsQuery(42, "test.example", 1)
	f.Add(dnsAnswer(q, "203.0.113.8"))
	f.Add([]byte{0xc0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 65535 {
			return
		}
		_, _, _ = dnsName(b, 0)
		ips, e := parseDNS(b, 42, "test.example", 1)
		if e == nil {
			for _, ip := range ips {
				if !ip.IsValid() {
					t.Fatal("invalid IP returned")
				}
			}
		}
	})
}
func FuzzICMPChecksum(f *testing.F) {
	f.Add([]byte("checksum test payload"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) < 4 || len(b) > 65535 {
			return
		}
		packet := bytes.Clone(b)
		packet[2] = 0
		packet[3] = 0
		binary.BigEndian.PutUint16(packet[2:], checksum(packet))
		if checksum(packet) != 0 {
			t.Fatal("checksum does not validate")
		}
	})
}

// blockingPacket stays inside ReadFrom until Close. A deadline alone does
// not unblock it, so this fails if cancellation no longer closes the socket.
type blockingPacket struct {
	closed    chan struct{}
	started   chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func (p *blockingPacket) ReadFrom([]byte) (int, net.Addr, error) {
	p.startOnce.Do(func() { close(p.started) })
	<-p.closed
	return 0, nil, net.ErrClosed
}
func (p *blockingPacket) WriteTo(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (p *blockingPacket) Close() error {
	p.closeOnce.Do(func() { close(p.closed) })
	return nil
}
func (p *blockingPacket) LocalAddr() net.Addr              { return &net.IPAddr{IP: net.IPv4zero} }
func (p *blockingPacket) SetDeadline(time.Time) error      { return nil }
func (p *blockingPacket) SetReadDeadline(time.Time) error  { return nil }
func (p *blockingPacket) SetWriteDeadline(time.Time) error { return nil }

func TestICMPCancelUnblocksBlockedRead(t *testing.T) {
	x := httpCheck("")
	x.Kind = "icmp"
	x.Target = "127.0.0.1"
	x.TimeoutSeconds = 30
	conn := &blockingPacket{closed: make(chan struct{}), started: make(chan struct{})}
	r := Runner{ListenPacket: func(string, string) (net.PacketConn, error) { return conn, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan model.Observation, 1)
	go func() { done <- r.Run(ctx, testkit.Config(), x) }()
	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("read never blocked")
	}
	cancel()
	select {
	case o := <-done:
		if o.Outcome == "" {
			t.Fatal(o)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel left ICMP blocked in ReadFrom")
	}
}
