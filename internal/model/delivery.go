package model

import (
	"slices"
	"strings"
	"time"
)

const MaxHeartbeatBytes = 6 << 10
const HeartbeatReservation = 8 << 10

// DeliveryStatus is worker-reported transport telemetry, not proof of remote health.
// Collector identities are URL digests, never credential URLs or arbitrary errors.
type DeliveryStatus struct {
	Collector          string    `json:"collector"`
	ReportedAt         time.Time `json:"reported_at"`
	PendingRecords     int       `json:"pending_records"`
	PendingBytes       int64     `json:"pending_bytes"`
	QuarantineBytes    int64     `json:"quarantine_bytes"` // entire worker outbox, shared by destinations
	QuarantinedSamples int64     `json:"quarantined_samples_since_boot"`
	LastAck            time.Time `json:"last_sample_ack_at,omitzero"`
	LastContact        time.Time `json:"last_contact_at,omitzero"`
	RetryAt            time.Time `json:"retry_at,omitzero"`
	Stage              string    `json:"stage,omitempty"`
	HTTPStatus         int       `json:"http_status,omitempty"`
	ServerCode         string    `json:"server_code,omitempty"`
	RetryAfterSeconds  int       `json:"retry_after_seconds,omitempty"`
	Code               string    `json:"code,omitempty"`
}

func CloneHeartbeat(h Heartbeat) Heartbeat { h.Delivery = slices.Clone(h.Delivery); return h }
func ValidDelivery(reports []DeliveryStatus, now time.Time) bool {
	if len(reports) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, r := range reports {
		if len(r.Collector) != 16 || strings.Trim(r.Collector, "0123456789abcdef") != "" || seen[r.Collector] || r.PendingRecords < 0 || r.PendingBytes < 0 || r.QuarantineBytes < 0 || r.QuarantinedSamples < 0 || r.ReportedAt.IsZero() || r.ReportedAt.After(now.Add(30*time.Second)) {
			return false
		}
		seen[r.Collector] = true
		for _, stamp := range []time.Time{r.LastAck, r.LastContact} {
			if stamp.After(now.Add(30 * time.Second)) {
				return false
			}
		}
		if r.RetryAt.After(now.Add(time.Minute)) {
			return false
		}

		if !ValidDeliveryCode(r.Code) || r.HTTPStatus < 0 || r.HTTPStatus > 599 || r.RetryAfterSeconds < 0 || r.RetryAfterSeconds > 60 || (r.ServerCode != "" && SafeDeliveryServerCode(r.ServerCode) == "") {
			return false
		}
		switch r.Stage {
		case "", "dns", "tcp", "tls", "request", "response", "response_read", "http", "ack", "identity":
		default:
			return false
		}

	}
	return true
}
