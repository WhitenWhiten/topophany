package direct

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestKomariDiscoveryReleasesCanceledTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := ln.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	h := &bearerConnection{source: SourceConfig{ID: "km", Name: "test", Adapter: "komari", URL: "https://" + ln.Addr().String(), Timeout: 30}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, e := h.Discover(ctx, Credential{Mode: "anonymous", Subject: "komari:anonymous"})
		done <- e
	}()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-ctx.Done():
		t.Fatal("no connection")
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, e := conn.Read(make([]byte, 8192)); e != nil {
		t.Fatal(e)
	}
	cancel()
	if e := <-done; e == nil {
		t.Fatal("canceled discovery succeeded")
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, e := io.Copy(io.Discard, conn); e != nil {
		t.Fatalf("discovery retained TLS socket: %v", e)
	}
}
