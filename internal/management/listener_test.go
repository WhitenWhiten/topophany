package management

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestControlListenerBoundsUnauthenticatedConnections(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &connectionBudget{Listener: base, slots: make(chan struct{}, 1)}
	defer listener.Close()
	accepted := make(chan net.Conn, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	defer func() { listener.Close(); <-done }()
	first, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("first peer not accepted")
	}
	second, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	second.SetReadDeadline(time.Now().Add(time.Second))
	_, err = second.Read(make([]byte, 1))
	second.Close()
	if err != io.EOF {
		t.Fatal("anonymous idle peer exceeded connection budget", err)
	}
	server.Close()
	server.Close() // a repeated close must not release another slot
	third, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("closed connection did not release capacity")
	}
}

func TestPeerAdmissionReleasesQuotaOnAuthentication(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &connectionBudget{Listener: base, slots: make(chan struct{}, 3), perPeer: 1}
	defer l.Close()
	accepted := make(chan net.Conn, 3)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			accepted <- c
		}
	}()
	defer func() { l.Close(); <-done }()
	dial := func() net.Conn {
		c, e := net.DialTimeout("tcp", base.Addr().String(), time.Second)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	receive := func() net.Conn {
		select {
		case c := <-accepted:
			return c
		case <-time.After(time.Second):
			t.Fatal("peer was not admitted")
			return nil
		}
	}
	dial()
	first := receive()
	defer first.Close()
	rejected := dial()
	rejected.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := rejected.Read(make([]byte, 1)); err != io.EOF {
		t.Fatal("anonymous per-peer limit ineffective", err)
	}
	first.(*budgetConnection).authenticate()
	dial()
	second := receive()
	defer second.Close()
	first.Close() // Must not release the second connection's anonymous quota.
	rejected = dial()
	rejected.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := rejected.Read(make([]byte, 1)); err != io.EOF {
		t.Fatal("authentication/close double-released peer quota", err)
	}
}
