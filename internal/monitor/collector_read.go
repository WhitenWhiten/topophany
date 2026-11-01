package monitor

import "time"

// CollectorRead describes the last snapshot read, not the health or freshness
// of the services it contains. Publication never advances these timestamps.
// Values are historical diagnostics after the publisher lease expires.
type CollectorRead struct {
	State               string    `json:"state"`
	AttemptedAt         time.Time `json:"attempted_at,omitzero"`
	CompletedAt         time.Time `json:"completed_at,omitzero"`
	LastSuccessAt       time.Time `json:"last_success_at,omitzero"`
	DurationMS          int64     `json:"duration_ms"`
	ConsecutiveFailures uint64    `json:"consecutive_failures"`
	Stage               string    `json:"stage,omitempty"`
	Code                string    `json:"code,omitempty"`
}
