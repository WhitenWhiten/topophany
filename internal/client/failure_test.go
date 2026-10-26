package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestDeliveryFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err         error
		stage, code string
	}{
		{&net.DNSError{Err: "private", Name: "secret"}, "response", "dns_failure"},
		{&net.DNSError{IsTimeout: true}, "dns", "dns_timeout"},
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "tcp", "tcp_failure"},
		{context.DeadlineExceeded, "tcp", "tcp_timeout"},
		{context.DeadlineExceeded, "tls", "tls_timeout"},
		{context.DeadlineExceeded, "response", "response_timeout"},
		{context.DeadlineExceeded, "response_read", "response_read_timeout"},
		{io.EOF, "response_read", "connection_closed"},
	} {
		err := transportFailure(tc.err, tc.stage)
		if FailureCode(err) != tc.code || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
		if !errors.Is(err, tc.err) {
			t.Fatal("lost cause")
		}
	}
}
func TestDeliveryHTTPAndAcknowledgementFailures(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
	}{
		{429, `{"error":"observer_heartbeat_rate"}`, "rate_limited"},
		{401, `{"error":"invalid_token"}`, "authorization_rejected"},
		{409, `{"error":"contract_mismatch"}`, "configuration_mismatch"},
		{503, `{"error":"storage_receipt_capacity"}`, "storage_backpressure"},
		{202, `{"invalid`, "ack_format_invalid"},
		{202, `{"project":"lab","revision":"r"}`, "ack_format_invalid"},
		{202, `{"project":"lab","revision":"r","accepted":null}`, "ack_format_invalid"},
		{202, `{"project":"other","revision":"r","accepted":0}`, "ack_identity_mismatch"},
		{503, `{"error":"super-secret-token"}`, "server_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			err := PostBatch(context.Background(), srv.Client(), Endpoint{URL: srv.URL, Token: token}, model.Batch{Project: "lab", Revision: "r"})
			if FailureCode(err) != tc.code || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
			if tc.status == 429 {
				_, server, status, retry := FailureDetail(err)
				if server != "observer_heartbeat_rate" || status != 429 || retry != 7*time.Second {
					t.Fatal(server, status, retry)
				}
			}
		})
	}
}
func TestTLSPacketLossClassifiedAsHandshakeTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			defer conn.Close()
			<-done
		}
	}()
	ep := Endpoint{URL: "https://" + ln.Addr().String(), Token: token}
	h, err := HTTP(ep)
	if err != nil {
		t.Fatal(err)
	}
	defer h.CloseIdleConnections()
	h.Timeout = 80 * time.Millisecond
	err = PostBatch(context.Background(), h, ep, model.Batch{Project: "lab"})
	if FailureCode(err) != "tls_timeout" {
		t.Fatal(err)
	}
}
