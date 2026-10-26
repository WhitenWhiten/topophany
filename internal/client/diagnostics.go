package client

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

const diagnosticRetention = 30 * time.Minute
const maxDiagnosticEvents = 64

type diagnosticLog struct {
	mu     sync.Mutex
	events []model.TransportEvent
}

func (l *diagnosticLog) add(e model.TransportEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = slices.DeleteFunc(l.events, func(old model.TransportEvent) bool { return e.At.Sub(old.At) > diagnosticRetention })
	l.events = append(l.events, e)
	if len(l.events) > maxDiagnosticEvents {
		l.events = slices.Clone(l.events[len(l.events)-maxDiagnosticEvents:])
	}
}
func (c *Client) Diagnostics() []model.TransportEvent {
	if c.diagnostics == nil {
		return nil
	}
	c.diagnostics.mu.Lock()
	defer c.diagnostics.mu.Unlock()
	out := []model.TransportEvent{}
	for _, e := range c.diagnostics.events {
		if c.Now().Sub(e.At) <= diagnosticRetention {
			out = append(out, e)
		}
	}
	return out
}
func (c *Client) recordProtocol(code string) {
	if c.diagnostics != nil {
		c.diagnostics.add(model.TransportEvent{At: c.Now().UTC(), Stage: "response_read", Code: code, HTTPStatus: 200, Attempts: 1})
	}
}

type diagnosticTransport struct {
	inner http.RoundTripper
	log   *diagnosticLog
}

func (t diagnosticTransport) CloseIdleConnections() {
	if closer, ok := t.inner.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
func (t diagnosticTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	trace := &requestTrace{}
	resp, err := t.inner.RoundTrip(trace.attach(req))
	event := model.TransportEvent{At: start.UTC(), Attempts: 1}
	finish := func(readErr error, payload []byte) {
		event.DurationMS = time.Since(start).Milliseconds()
		event.ConnectionKnown, event.ConnectionReused = trace.connection()
		if readErr != nil {
			event.Code = FailureCode(transportFailure(readErr, "response_read"))
			event.Stage = "response_read"
		} else if event.HTTPStatus >= 400 {
			var v struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(payload, &v)
			event.ServerCode = model.SafeDeliveryServerCode(v.Error)
			event.Code = FailureCode(&HTTPError{Status: event.HTTPStatus, Code: event.ServerCode})
			event.Stage = "http"
		} else {
			event.Stage = "complete"
		}
		t.log.add(event)
	}
	if err != nil {
		event.DurationMS = time.Since(start).Milliseconds()
		event.Code = FailureCode(transportFailure(err, trace.current()))
		event.Stage = trace.current()
		event.ConnectionKnown, event.ConnectionReused = trace.connection()
		t.log.add(event)
		return resp, err
	}
	event.HTTPStatus = resp.StatusCode
	event.RetryAfterSeconds = int(retryAfter(resp.Header.Get("Retry-After"), time.Now()) / time.Second)
	resp.Body = &diagnosticBody{ReadCloser: resp.Body, finish: finish, capture: resp.StatusCode >= 400}
	return resp, nil
}

type diagnosticBody struct {
	io.ReadCloser
	once    sync.Once
	finish  func(error, []byte)
	capture bool
	payload []byte
	err     error
}

func (b *diagnosticBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if b.capture && len(b.payload) < 1024 {
		b.payload = append(b.payload, p[:min(n, 1024-len(b.payload))]...)
	}
	if err != nil && err != io.EOF {
		b.err = err
	}
	if err != nil {
		b.once.Do(func() { b.finish(b.err, b.payload); b.payload = nil })
	}
	return n, err
}
func (b *diagnosticBody) Close() error {
	err := b.ReadCloser.Close()
	if err != nil && b.err == nil {
		b.err = err
	}
	b.once.Do(func() { b.finish(b.err, b.payload); b.payload = nil })
	return err
}
