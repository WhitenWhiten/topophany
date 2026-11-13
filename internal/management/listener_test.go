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
