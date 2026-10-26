package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

type HTTPError struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("collector HTTP %d: %s", e.Status, safeHTTPCode(e.Code))
}
func safeHTTPCode(code string) string {
	if safe := model.SafeDeliveryServerCode(code); safe != "" {
		return safe
	}
	return "invalid_error"
}

func PostBatch(ctx context.Context, h *http.Client, ep Endpoint, b model.Batch) error {
	_, err := PostBatchDetailed(ctx, h, ep, b)
	return err
}

// PostBatchDetailed retries exactly once after an interrupted deduplicated
// sample POST. A recovered rotation is successful delivery with a recovery
// diagnostic, and never a collector-health failure.
func PostBatchDetailed(ctx context.Context, h *http.Client, ep Endpoint, b model.Batch) (model.TransportEvent, error) {
	safe := len(b.Observations) > 0
	for _, v := range b.Observations {
		safe = safe && v.ID != ""
	}
	first, err := postBatchAttempt(ctx, h, ep, b)
	if !safe || ctx.Err() != nil || !connectionClosed(err) {
		return first, err
	}
	h.CloseIdleConnections()
	second, err := postBatchAttempt(ctx, h, ep, b)
	second.Attempts = 2
	second.DurationMS += first.DurationMS
	second.PreviousCode = first.Code
	second.PreviousStage = first.Stage
	second.PreviousHTTPStatus = first.HTTPStatus
	second.RetryOutcome = "recovered"
	if err != nil {
		second.RetryOutcome = "failed"
	}
	return second, err
}
func connectionClosed(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}

func postBatchAttempt(ctx context.Context, h *http.Client, ep Endpoint, b model.Batch) (event model.TransportEvent, err error) {
	start := time.Now()
	trace := &requestTrace{}
	event.At = start.UTC()
	event.Attempts = 1
	defer func() {
		event.DurationMS = time.Since(start).Milliseconds()
		event.ConnectionKnown, event.ConnectionReused = trace.connection()
		event.Code = FailureCode(err)
		stage, server, status, _ := FailureDetail(err)
		event.Stage, event.ServerCode = stage, server
		if status != 0 {
			event.HTTPStatus = status
		}
		if err == nil {
			event.Stage = "complete"
			event.HTTPStatus = 202
		}
		var he *HTTPError
		if errors.As(err, &he) {
			event.RetryAfterSeconds = int(he.RetryAfter / time.Second)
		}
	}()
	body, err := json.Marshal(b)
	if err != nil {
		return event, &Failure{Code: "sample_rejected", Stage: "request", Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL(ep, "/v1/observations", nil), bytes.NewReader(body))
	if err != nil {
		return event, &Failure{Code: "transport_failure", Stage: "request", Cause: err}
	}
	if err = authorize(req, ep); err != nil {
		return event, &Failure{Code: "authorization_rejected", Stage: "request", Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Do(trace.attach(req))
	if err != nil {
		return event, transportFailure(err, trace.current())
	}
	event.HTTPStatus = resp.StatusCode
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return event, transportFailure(err, "response_read")
	}
	if resp.StatusCode != 202 {
		var v struct {
			Error string `json:"error"`
		}
		if len(payload) <= 4096 {
			_ = json.Unmarshal(payload, &v)
		}
		return event, &HTTPError{Status: resp.StatusCode, Code: safeHTTPCode(v.Error), RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	if len(payload) > 4096 {
		return event, &Failure{Code: "ack_format_invalid", Stage: "ack"}
	}
	var ack struct {
		Project  *string `json:"project"`
		Revision *string `json:"revision"`
		Accepted *int    `json:"accepted"`
	}
	if err = json.Unmarshal(payload, &ack); err != nil {
		return event, &Failure{Code: "ack_format_invalid", Stage: "ack", Cause: err}
	}
	if ack.Project == nil || ack.Revision == nil || ack.Accepted == nil {
		return event, &Failure{Code: "ack_format_invalid", Stage: "ack"}
	}
	if *ack.Project != b.Project || *ack.Revision != b.Revision || *ack.Accepted != len(b.Observations) {
		return event, &Failure{Code: "ack_identity_mismatch", Stage: "ack"}
	}
	return event, nil
}
func retryAfter(value string, now time.Time) time.Duration {
	if n, err := strconv.ParseInt(value, 10, 32); err == nil {
		return time.Duration(max(0, min(n, 60))) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(0, min(at.Sub(now), time.Minute))
	}
	return 0
}
