package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Advance the accept clock to exercise the production two-minute policy without
// sleeping two minutes. A body is held across expiration after authentication.
func TestTwoMinuteRotationDrainsPOST(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint(h2), func(t *testing.T) {
			_, handler, batch := fixture(t)
			sockets := make(chan *limitedConn, 4)
			ts := httptest.NewUnstartedServer(handler)
			ts.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
				ctx = ConnectionContext(ctx, c)
				lc := ctx.Value(connectionKey{}).(*limitedConn)
				sockets <- lc
				return ctx
			}
			ts.Listener = LimitListener(ts.Listener, 128)
			ts.EnableHTTP2 = h2
			ts.StartTLS()
			defer ts.Close()
			h := ts.Client()
			h.Timeout = 4 * time.Second
			rd, wr := io.Pipe()
			defer rd.Close()
			defer wr.Close()
			data := payload(t, batch)
			req, _ := http.NewRequest("POST", ts.URL+"/v1/observations", rd)
			req.Header.Set("Authorization", "Bearer "+workerToken)
			req.Header.Set("Content-Type", "application/json")
			result := make(chan error, 1)
			go func() {
				resp, err := h.Do(req)
				if err != nil {
					result <- err
					return
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err == nil && (resp.StatusCode != 202 || !strings.Contains(string(body), `"accepted":1`)) {
					err = fmt.Errorf("POST: %d %s", resp.StatusCode, body)
				}
				if err == nil && (resp.ProtoMajor == 2) != h2 {
					err = fmt.Errorf("protocol: %s", resp.Proto)
				}
				if err == nil && !h2 && !resp.Close {
					err = fmt.Errorf("missing HTTP/1 close notice")
				}
				result <- err
			}()
			if _, err := wr.Write(data[:len(data)/2]); err != nil {
				t.Fatal(err)
			}
			lc := <-sockets
			deadline := time.Now().Add(time.Second)
			for {
				lc.listener.mu.Lock()
				ready := lc.principal != ""
				lc.listener.mu.Unlock()
				if ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("POST did not authenticate")
				}
				time.Sleep(time.Millisecond)
			}
			lc.mu.Lock()
			lc.accepted = time.Now().Add(-maxConnectionLifetime)
			lc.timer.Reset(time.Millisecond)
			lc.mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			if !rotatingConnection(context.WithValue(context.Background(), connectionKey{}, lc), 0) {
				t.Fatal("not draining")
			}
			if _, err := wr.Write(data[len(data)/2:]); err != nil {
				t.Fatal("rotation cut body:", err)
			}
			wr.Close()
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			// GOAWAY/Connection: close makes the next request use a new socket.
			req, _ = http.NewRequest("GET", ts.URL+"/v1/config", nil)
			req.Header.Set("Authorization", "Bearer "+viewerToken)
			resp, err := h.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			select {
			case <-sockets:
			case <-time.After(time.Second):
				t.Fatal("expired socket reused")
			}
		})
	}
}

func TestAuthenticatedRotationHardBoundsDrain(t *testing.T) {
	socket, peer := sourcePipe(t, "192.0.2.1:1")
	listener := newLimitedListener(&scriptedListener{conns: []net.Conn{socket}}, 4, 1, maxConnectionLifetime)
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	limited := conn.(*limitedConn)
	if !limited.authenticate("worker") {
		t.Fatal("authentication refused")
	}
	limited.expire() // admission closes; existing work retains its slot
	if len(listener.slots) != 1 {
		t.Fatal("draining request lost its charged slot")
	}
	limited.expire() // end of the fixed grace, even if work is stuck
	if len(listener.slots) != 0 {
		t.Fatal("drain outlived hard quota bound")
	}
	peer.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err = peer.Read(b[:]); err == nil {
		t.Fatal("stalled connection survived drain")
	}
}
