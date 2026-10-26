package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Failure retains its cause for classification and errors.Is, but never renders
// a URL, credential, socket error string or response body to logs or a session.
type Failure struct {
	Code, Stage string
	Cause       error
}

func (e *Failure) Error() string { return fmt.Sprintf("collector %s (%s)", e.Code, e.Stage) }
func (e *Failure) Unwrap() error { return e.Cause }

type requestTrace struct {
	mu            sync.Mutex
	stage         string
	known, reused bool
}

func (t *requestTrace) set(stage string) { t.mu.Lock(); t.stage = stage; t.mu.Unlock() }
func (t *requestTrace) current() string  { t.mu.Lock(); defer t.mu.Unlock(); return t.stage }
func (t *requestTrace) attach(req *http.Request) *http.Request {
	t.set("tcp")
	return req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { t.set("dns") },
		ConnectStart:      func(string, string) { t.set("tcp") },
		TLSHandshakeStart: func() { t.set("tls") },
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			t.stage = "request"
			t.known = true
			t.reused = info.Reused
			t.mu.Unlock()
		},
		WroteRequest: func(httptrace.WroteRequestInfo) { t.set("response") },
	}))
}
func transportFailure(err error, stage string) error {
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var record tls.RecordHeaderError
	var op *net.OpError
	var timeout net.Error
	switch {
	case errors.As(err, &dns):
		stage = "dns"
	case errors.As(err, &cert), errors.As(err, &unknown), errors.As(err, &record):
		stage = "tls"
	case errors.As(err, &op) && op.Op == "dial":
		stage = "tcp"
	}
	code := "transport_failure"
	if connectionClosed(err) {
		code = "connection_closed"
	} else {
		switch stage {
		case "dns", "tcp", "tls":
			code = stage + "_failure"
		}
		if errors.As(err, &timeout) && timeout.Timeout() {
			switch stage {
			case "dns", "tcp", "tls":
				code = stage + "_timeout"
			case "response_read":
				code = "response_read_timeout"
			default:
				code = "response_timeout"
			}
		}
	}
	return &Failure{Code: code, Stage: stage, Cause: err}
}

// FailureCode is shared by delivery reports, terminal views and log records.
func FailureCode(err error) string {
	if err == nil {
		return ""
	}
	var f *Failure
	if errors.As(err, &f) {
		return f.Code
	}
	var h *HTTPError
	if errors.As(err, &h) {
		switch h.Code {
		case "contract_mismatch", "observation_contract_mismatch", "revision_mismatch", "project_mismatch", "topology_reloaded":
			return "configuration_mismatch"
		case "connection_rotating":
			return "connection_rotating"
		}
		switch h.DeliveryAction() {
		case WaitForAuthorization:
			return "authorization_rejected"
		case StorageBackpressure:
			return "storage_backpressure"
		case RejectSample:
			return "sample_rejected"
		}
		if h.Status == 429 {
			return "rate_limited"
		}
		return "server_error"
	}
	return "transport_failure"
}
func FailureDetail(err error) (stage, serverCode string, status int, retry time.Duration) {
	var f *Failure
	if errors.As(err, &f) {
		stage = f.Stage
	}
	var h *HTTPError
	if errors.As(err, &h) {
		stage = "http"
		serverCode = model.SafeDeliveryServerCode(h.Code)
		status = h.Status
		retry = h.RetryAfter
	}
	return
}

func (t *requestTrace) connection() (bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.known, t.reused
}
