package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/local"
	"github.com/WhitenWhiten/topophany/internal/monitor/stream"
)

func TestLocalHTTPSKeepsNameForInspectSaveAndStream(t *testing.T) {
	if os.Getenv("TOPO_TEST_LOCAL_TLS_CHILD") == "1" {
		testLocalHTTPSChild(t)
		return
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pair, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.TLS.ServerName != "localhost" {
			t.Errorf("unexpected TLS name: %+v", r.TLS)
		}
		if r.Header.Get("Authorization") != "Bearer "+localSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		reads.Add(1)
		switch r.URL.Path {
		case "/configs":
			_, _ = io.WriteString(w, `{"mode":"rule","tun":{"enable":false}}`)
		case "/version":
			_, _ = io.WriteString(w, `{"version":"v1.19.99"}`)
		case "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{"Proxy":{"type":"Selector","now":"DIRECT","all":["DIRECT"]},"DIRECT":{"type":"Direct"}}}`)
		case "/providers/proxies":
			_, _ = io.WriteString(w, `{"providers":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = server.Listener.Close()
	server.Listener = listener
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	server.StartTLS()
	defer server.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	// Keep the system root override inside a child process so it cannot affect
	// other tests. This also works with the native macOS and Windows verifiers.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLocalHTTPSKeepsNameForInspectSaveAndStream$")
	cmd.Env = append(os.Environ(),
		"TOPO_TEST_LOCAL_TLS_CHILD=1",
		"TOPO_TEST_LOCAL_TLS_CA="+caFile,
		"TOPO_TEST_LOCAL_TLS_PORT="+port,
		"TOPO_TEST_LOCAL_TLS_ROOT="+filepath.Join(t.TempDir(), "local"),
		"GODEBUG="+os.Getenv("GODEBUG")+",x509usefallbackroots=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("local TLS child: %v\n%s", err, out)
	}
	if reads.Load() == 0 {
		t.Fatal("the TLS engine received no authenticated requests")
	}
}

func testLocalHTTPSChild(t *testing.T) {
	t.Helper()
	ca, err := os.ReadFile(os.Getenv("TOPO_TEST_LOCAL_TLS_CA"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	x509.SetFallbackRoots(roots)

	port := os.Getenv("TOPO_TEST_LOCAL_TLS_PORT")
	root := os.Getenv("TOPO_TEST_LOCAL_TLS_ROOT")
	request := local.Request{
		Schema: 1, RequestID: "tls-check",
		Check: local.Check{ID: "lc-engine", Name: "Engine", Adapter: "mihomo", URL: "https://LOCALHOST:" + port, Interval: 30, Timeout: 5, Stale: 90, RootProxy: "Proxy", ExpectedMode: "rule"},
		Auth:  local.Auth{Mode: "token", Token: localSecret},
	}
	run := func(cmd string, want int) localEnvelope {
		t.Helper()
		b, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var out, diagnostic bytes.Buffer
		err = (&App{In: bytes.NewReader(b), Out: &out, Err: &diagnostic}).Run(context.Background(), []string{"local", cmd, "--json-stdin", "--local-root", root})
		status := 0
		var exit *ExitError
		if errors.As(err, &exit) {
			status = exit.Code
		} else if err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Fatalf("%s exit %d, want %d: %s %s", cmd, status, want, out.String(), diagnostic.String())
		}
		var result localEnvelope
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	// The trusted certificate names localhost only; the IP must still fail.
	request.Check.URL = "https://127.0.0.1:" + port
	if result := run("inspect", 1); result.Error == nil || result.Error.Code != "tls_error" {
		t.Fatalf("literal IP unexpectedly accepted: %+v", result)
	}
	request.Check.URL = "https://LOCALHOST:" + port
	if result := run("inspect", 0); !result.OK {
		t.Fatal(result)
	}
	if result := run("save", 0); !result.OK {
		t.Fatal(result)
	}
	request.Auth = local.Auth{Mode: "saved"}
	if result := run("inspect", 0); !result.OK {
		t.Fatal(result)
	}

	manager := stream.NewLocalManager(root, time.Now)
	defer manager.Close()
	source, failure := manager.Open(context.Background(), stream.Request{Source: "local"})
	if failure != nil {
		t.Fatal(failure)
	}
	defer source.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input, err := source.Fetch(ctx)
	if err != nil || len(input.Snapshot.Checks) != 1 || input.Snapshot.Checks[0].Last == nil || input.Snapshot.Checks[0].Last.Outcome != "ok" {
		t.Fatalf("stream result: %+v, %v", input.Snapshot.Checks, err)
	}
}
