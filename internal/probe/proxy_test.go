package probe

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func connectFixture(t *testing.T, user, pass string, visits chan string) string {
	return testkit.Stream(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		req, e := http.ReadRequest(br)
		if e != nil {
			return
		}
		if req.Method != "CONNECT" {
			t.Error("not CONNECT")
			return
		}
		if user != "" || pass != "" {
			want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
			if req.Header.Get("Proxy-Authorization") != want {
				_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
				return
			}
		}
		if visits != nil {
			visits <- req.Host
		}
		up, e := net.DialTimeout("tcp", req.Host, time.Second)
		if e != nil {
			_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return
		}
		defer up.Close()
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		testkit.Relay(&bufferedConn{Conn: c, r: br}, up)
	})
}
func socksFixture(t *testing.T, user, pass string, visits chan string) string {
	return testkit.Stream(t, func(c net.Conn) {
		var g [3]byte
		if _, e := io.ReadFull(c, g[:]); e != nil {
			return
		}
		method := byte(0)
		if user != "" || pass != "" {
			method = 2
		}
		_, _ = c.Write([]byte{5, method})
		if g[0] != 5 || g[1] != 1 || g[2] != method {
			return
		}
		if method == 2 {
			var head [2]byte
			if _, e := io.ReadFull(c, head[:]); e != nil {
				return
			}
			u := make([]byte, int(head[1]))
			if _, e := io.ReadFull(c, u); e != nil {
				return
			}
			var n [1]byte
			if _, e := io.ReadFull(c, n[:]); e != nil {
				return
			}
			p := make([]byte, int(n[0]))
			if _, e := io.ReadFull(c, p); e != nil {
				return
			}
			if string(u) != user || string(p) != pass {
				_, _ = c.Write([]byte{1, 1})
				return
			}
			_, _ = c.Write([]byte{1, 0})
		}
		var h [4]byte
		if _, e := io.ReadFull(c, h[:]); e != nil {
			return
		}
		if h[0] != 5 || h[1] != 1 || h[2] != 0 {
			t.Error("invalid SOCKS request")
			return
		}
		host := ""
		switch h[3] {
		case 1:
			b := make([]byte, 4)
			if _, e := io.ReadFull(c, b); e != nil {
				return
			}
			host = net.IP(b).String()
		case 4:
			b := make([]byte, 16)
			if _, e := io.ReadFull(c, b); e != nil {
				return
			}
			host = net.IP(b).String()
		case 3:
			var n [1]byte
			if _, e := io.ReadFull(c, n[:]); e != nil {
				return
			}
			b := make([]byte, n[0])
			if _, e := io.ReadFull(c, b); e != nil {
				return
			}
			host = string(b)
		default:
			return
		}
		var port [2]byte
		if _, e := io.ReadFull(c, port[:]); e != nil {
			return
		}
		address := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
		if visits != nil {
			visits <- address
		}
		if host == "remote-dns.example.invalid" {
			address = net.JoinHostPort("127.0.0.1", strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
		}
		up, e := net.DialTimeout("tcp", address, time.Second)
		if e != nil {
			_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		defer up.Close()
		_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		testkit.Relay(c, up)
	})
}
func TestRealThreeHopConnectSOCKSConnect(t *testing.T) {
	t.Setenv("TOPH_PROXY_PASSWORD", "chain-secret")
	visited := make(chan string, 8)
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials reached the origin")
		}
		_, _ = io.WriteString(w, "203.0.113.8")
	}))
	defer target.Close()
	last := connectFixture(t, "", "", visited)
	middle := socksFixture(t, "", "", visited)
	first := connectFixture(t, "connect-user", "chain-secret", visited)
	c := testkit.Config()
	c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: first, Username: "connect-user", PasswordEnv: "TOPH_PROXY_PASSWORD"}, {Kind: "socks5", Address: middle}, {Kind: "http-connect", Address: last}}
	x := httpCheck(target.URL)
	x.Path = "direct"
	x.HTTP.ExpectedExit = []string{"203.0.113.8"}
	o := (Runner{}).Run(context.Background(), c, x)
	// A hop sent a password can write a plain HTTP answer, so what the
	// answer says of the exit is checked but not kept.
	if o.Outcome != "ok" || reached.Load() != 1 || o.Facts["exit_ip"] != "" || o.Facts["peer_text"] != withheld {
		t.Fatal(o)
	}
	for _, want := range []string{middle, last, strings.TrimPrefix(target.URL, "http://")} {
		select {
		case got := <-visited:
			if got != want {
				t.Fatalf("wire order: want %s got %s", want, got)
			}
		default:
			t.Fatal("missing proxy hop")
		}
	}
	if o.PathEvidence != "explicit_proxy_transport" || o.Facts["transport_hops"] != "3" {
		t.Fatal("missing measured transport description", o)
	}
	b, _ := json.Marshal(o)
	if strings.Contains(string(b), "chain-secret") {
		t.Fatal("secret leaked in observation")
	}
}
func TestProxyFailureDoesNotFallbackDirect(t *testing.T) {
	var reached atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); w.WriteHeader(200) }))
	defer s.Close()
	proxy := connectFixture(t, "required", "correct", nil)
	c := testkit.Config()
	c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: proxy, Username: "wrong"}}
	x := httpCheck(s.URL)
	x.Path = "direct"
	o := (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "fail" || o.Stage != "proxy_connect" || o.Code != "http_407" || reached.Load() != 0 {
		t.Fatal("proxy failure fell through to direct", o)
	}
	c.Paths[0].Proxies[0].PasswordEnv = "TOPH_EMPTY_PROXY_SECRET"
	t.Setenv("TOPH_EMPTY_PROXY_SECRET", "")
	o = (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "error" || o.Code != "proxy_secret_missing" {
		t.Fatal(o)
	}
}
func TestSOCKSRemoteDNSAndAuthenticationRejection(t *testing.T) {
	visits := make(chan string, 2)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer s.Close()
	proxy := socksFixture(t, "", "", visits)
	c := testkit.Config()
	c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: proxy}}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	x := httpCheck("http://remote-dns.example.invalid:" + port + "/")
	x.Path = "direct"
	o := (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "ok" {
		t.Fatal("SOCKS target was resolved locally", o)
	}
	if got := <-visits; !strings.HasPrefix(got, "remote-dns.example.invalid:") {
		t.Fatal("hostname not delegated to SOCKS resolver", got)
	}
	t.Setenv("TOPH_SOCKS_PASSWORD", "wrong")
	proxy = socksFixture(t, "user", "correct", nil)
	c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: proxy, Username: "user", PasswordEnv: "TOPH_SOCKS_PASSWORD"}}
	o = (Runner{}).Run(context.Background(), c, x)
	if o.Code != "authentication_rejected" {
		t.Fatal(o)
	}
}

// connectHandler answers CONNECT by relaying to the host it names.
func connectHandler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT required", 405)
			return
		}
		up, e := net.DialTimeout("tcp", r.Host, time.Second)
		if e != nil {
			http.Error(w, "dial failed", 502)
			return
		}
		defer up.Close()
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijacker")
			return
		}
		c, rw, e := hj.Hijack()
		if e != nil {
			return
		}
		defer c.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
		testkit.Relay(&bufferedConn{Conn: c, r: rw.Reader}, up)
	})
}
func TestHTTPSConnectVerifiesOuterCertificate(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ready") }))
	defer target.Close()
	proxy, ca := tlsFixture(t, connectHandler(t))
	c := testkit.Config()
	c.Paths[0].Proxies = []model.Proxy{{Kind: "https-connect", Address: strings.TrimPrefix(proxy.URL, "https://")}}
	x := httpCheck(target.URL)
	x.Path = "direct"
	o := (Runner{}).Run(context.Background(), c, x)
	if o.Stage != "proxy_tls" || o.Outcome != "fail" {
		t.Fatal("untrusted outer proxy accepted", o)
	}
	c.Paths[0].Proxies[0].CAFile = ca
	o = (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "ok" {
		t.Fatal(o)
	}
}
func TestMalformedProxyResponsesAndBudgets(t *testing.T) {
	for _, tt := range []struct{ name, response, code string }{{"oversized-header", "HTTP/1.1 200 OK\r\nX-Large: " + strings.Repeat("x", 40000) + "\r\n\r\n", "header_too_large"}, {"invalid-response", "not-http\r\n\r\n", "invalid_response"}, {"rejected", "HTTP/1.1 502 Bad Gateway\r\n\r\n", "http_502"}} {
		t.Run(tt.name, func(t *testing.T) {
			p := testkit.Stream(t, func(c net.Conn) { _, _ = http.ReadRequest(bufio.NewReader(c)); _, _ = io.WriteString(c, tt.response) })
			c := testkit.Config()
			c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: p}}
			x := httpCheck("http://127.0.0.1:1/")
			x.Path = "direct"
			o := (Runner{}).Run(context.Background(), c, x)
			if o.Code != tt.code {
				t.Fatal(o)
			}
		})
	}
	p := testkit.Stream(t, func(c net.Conn) { var q [3]byte; _, _ = io.ReadFull(c, q[:]); _, _ = c.Write([]byte{5, 0}) })
	a, b := net.Pipe()
	b.Close()
	if e := socks(a, "127.0.0.1:1", "", ""); e == nil {
		t.Fatal("closed proxy accepted")
	}
	a.Close()
	t.Setenv("TOPH_PASSWORD", "secret")
	c := testkit.Config()
	c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: p, Username: "user", PasswordEnv: "TOPH_PASSWORD"}}
	x := httpCheck("http://127.0.0.1:1/")
	x.Path = "direct"
	if o := (Runner{}).Run(context.Background(), c, x); o.Code != "authentication_method_rejected" {
		t.Fatal("SOCKS authentication silently downgraded", o)
	}
}
func TestProxyCancellationIsBounded(t *testing.T) {
	p := testkit.Stream(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	c := testkit.Config()
	c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: p}}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	x := httpCheck("http://127.0.0.1:1/")
	x.Path = "direct"
	start := time.Now()
	o := (Runner{}).Run(ctx, c, x)
	if time.Since(start) > 500*time.Millisecond || o.Outcome != "fail" {
		t.Fatal("proxy handshake ignored cancellation", o)
	}
}
func TestSOCKSAddressEncoding(t *testing.T) {
	for _, s := range []string{"127.0.0.1:443", "[::1]:443", "example.invalid:443"} {
		b, e := socksAddress(s)
		if e != nil || binary.BigEndian.Uint16(b[len(b)-2:]) != 443 {
			t.Fatal(s, e)
		}
	}
	for _, s := range []string{"bad", "a:0", "a:70000", strings.Repeat("a", 256) + ":1"} {
		if _, e := socksAddress(s); e == nil {
			t.Fatal("invalid SOCKS target accepted", s)
		}
	}
}
func TestMain(m *testing.M) {
	if os.Getenv("TOPH_SSH_HELPER") == "1" {
		sshProcessFixture()
		return
	}
	os.Exit(m.Run())
}
func sshProcessFixture() {
	args := os.Args[1:]
	_ = os.WriteFile(os.Getenv("TOPH_SSH_ARGS"), []byte(strings.Join(args, "\n")), 0600)
	target := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-W" {
			target = args[i+1]
		}
	}
	c, e := net.DialTimeout("tcp", target, time.Second)
	if e != nil {
		os.Exit(2)
	}
	defer c.Close()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(c, os.Stdin); c.Close(); close(done) }()
	_, _ = io.Copy(os.Stdout, c)
	c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	os.Exit(0)
}
func sshFixture(t *testing.T, user string) (model.Proxy, string) {
	t.Helper()
	dir := t.TempDir()
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	helper := filepath.Join(dir, "ssh")
	if strings.HasSuffix(strings.ToLower(self), ".exe") {
		helper += ".exe"
	}
	b, e := os.ReadFile(self)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(helper, b, 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TOPH_SSH_HELPER", "1")
	argsFile := filepath.Join(dir, "args")
	t.Setenv("TOPH_SSH_ARGS", argsFile)
	identity := filepath.Join(dir, "identity")
	known := filepath.Join(dir, "known hosts")
	_ = os.WriteFile(identity, []byte("fixture only; not a real key"), 0600)
	_ = os.WriteFile(known, []byte("fixture only"), 0600)
	return model.Proxy{Kind: "ssh", Address: "127.0.0.1:22", SSHUser: user, SSHIdentityFile: identity, SSHKnownHosts: known}, argsFile
}

func TestSSHJumpProcessBridgeAndStrictArguments(t *testing.T) {
	p, argsFile := sshFixture(t, "fixture")
	known := p.SSHKnownHosts
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "jump-success") }))
	defer target.Close()
	c := testkit.Config()
	c.Paths[0].Proxies = []model.Proxy{p}
	x := httpCheck(target.URL)
	x.Path = "direct"
	x.HTTP.Contains = "jump-success"
	x.BindIP = "127.0.0.1"
	// This checks the bridge and argv, not process startup latency. A fresh
	// helper executable can start slowly while other packages run in parallel.
	x.TimeoutSeconds = 5
	o := (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "ok" {
		t.Fatal("SSH stdio bridge failed", o)
	}
	args, e := os.ReadFile(argsFile)
	if e != nil {
		t.Fatal(e)
	}
	for _, required := range []string{"StrictHostKeyChecking=yes", "BatchMode=yes", "IdentitiesOnly=yes", "IdentityAgent=none", "ForwardAgent=no", "ControlPath=none", "-b\n127.0.0.1", "UserKnownHostsFile=\"" + filepath.ToSlash(known) + "\"", "-W\n" + strings.TrimPrefix(target.URL, "http://")} {
		if !strings.Contains(string(args), required) {
			t.Fatal("missing SSH safety option", required)
		}
	}
	x.Kind = "tcp"
	x.Target = strings.TrimPrefix(target.URL, "http://")
	o = (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "unknown" || o.Connectivity || o.Code != "ssh_forward_unverified" {
		t.Fatal("process startup was treated as verified TCP connection", o)
	}
	x.Kind = "http"
	x.Target = target.URL
	c.Paths[0].Proxies[0].SSHKnownHosts = filepath.Join(t.TempDir(), "missing")
	o = (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "error" || o.Code != "ssh_file_unreadable" {
		t.Fatal("missing known_hosts bypassed", o)
	}
	// This fixture validates argv/stream/process semantics, NOT SSH cryptography.
}
func TestSSHBinaryLookupFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	identity := filepath.Join(t.TempDir(), "key")
	known := filepath.Join(t.TempDir(), "known")
	_ = os.WriteFile(identity, []byte("fixture"), 0600)
	_ = os.WriteFile(known, []byte("fixture"), 0600)
	_, e := sshDial(context.Background(), model.Proxy{Address: "127.0.0.1:22", SSHIdentityFile: identity, SSHKnownHosts: known}, "127.0.0.1:1", "")
	if o, _, code := classify(e); o != "error" || code != "ssh_not_installed" {
		t.Fatal(e)
	}
	if _, e := exec.LookPath("ssh"); e == nil {
		t.Fatal("PATH fixture failed")
	}
}
func FuzzSOCKSAddress(f *testing.F) {
	f.Add("example.invalid:443")
	f.Add("a:1")
	f.Add("[::1]:80")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 2048 {
			return
		}
		b, e := socksAddress(s)
		if e == nil && (len(b) < 5 || len(b) > 259) {
			t.Fatal(fmt.Sprint("invalid encoded length ", len(b)))
		}
	})
}
