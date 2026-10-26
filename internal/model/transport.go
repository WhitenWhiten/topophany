package model

import "time"

// TransportEvent contains protocol metadata only: never URLs with credentials,
// request/response bodies or an arbitrary underlying error's string.
type TransportEvent struct {
	At                 time.Time `json:"at"`
	Stage              string    `json:"stage,omitempty"`
	Code               string    `json:"code,omitempty"`
	HTTPStatus         int       `json:"http_status,omitempty"`
	ServerCode         string    `json:"server_code,omitempty"`
	RetryAfterSeconds  int       `json:"retry_after_seconds,omitempty"`
	DurationMS         int64     `json:"duration_ms"`
	ConnectionKnown    bool      `json:"connection_known,omitempty"`
	ConnectionReused   bool      `json:"connection_reused,omitempty"`
	Attempts           int       `json:"attempts,omitempty"`
	AddressAttempts    int       `json:"address_attempts,omitempty"`
	RetryOutcome       string    `json:"retry_outcome,omitempty"`
	PreviousStage      string    `json:"previous_stage,omitempty"`
	PreviousHTTPStatus int       `json:"previous_http_status,omitempty"`
	PreviousCode       string    `json:"previous_code,omitempty"`
}

func ValidTransportEvent(e TransportEvent, now time.Time) bool {
	if e.AddressAttempts < 0 || e.AddressAttempts > 8 || e.AddressAttempts > e.Attempts {
		return false
	}
	if e.PreviousHTTPStatus < 0 || e.PreviousHTTPStatus > 599 {
		return false
	}
	switch e.PreviousStage {
	case "", "dns", "tcp", "tls", "request", "response", "response_read", "http", "ack", "identity", "complete":
	default:
		return false
	}
	if e.At.IsZero() || e.At.After(now.Add(30*time.Second)) || e.DurationMS < 0 || e.DurationMS > 120000 || e.HTTPStatus < 0 || e.HTTPStatus > 599 || e.RetryAfterSeconds < 0 || e.RetryAfterSeconds > 60 || e.Attempts < 0 || e.Attempts > 16 || !ValidDeliveryCode(e.Code) || !ValidDeliveryCode(e.PreviousCode) || (e.ServerCode != "" && SafeDeliveryServerCode(e.ServerCode) == "") {
		return false
	}
	switch e.Stage {
	case "", "dns", "tcp", "tls", "request", "response", "response_read", "http", "ack", "identity", "complete":
	default:
		return false
	}
	switch e.RetryOutcome {
	case "", "recovered", "failed":
	default:
		return false
	}
	return !e.ConnectionReused || e.ConnectionKnown
}
