package model

import (
	"encoding/json"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// DirectInfo is public process-local acquisition metadata. It never contains
// endpoints, credential references, file paths or bearer values.
type DirectInfo struct {
	Profile         string         `json:"profile"`
	ProfileRevision string         `json:"profile_revision"`
	DatasetID       string         `json:"dataset_id"`
	Sources         []DirectSource `json:"sources"`
}
type DirectSource struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Adapter           string     `json:"adapter"`
	State             string     `json:"state"`
	Code              *string    `json:"code"`
	LastAttemptAt     *time.Time `json:"last_attempt_at"`
	LastContactAt     *time.Time `json:"last_contact_at"`
	ContactValidUntil *time.Time `json:"contact_valid_until"`
	NextRetryAt       *time.Time `json:"next_retry_at"`
}
type Origin struct {
	Adapter  string `json:"adapter"`
	SourceID string `json:"source_id"`
	Name     string `json:"name"`
	ObjectID string `json:"object_id"`
}

// UnmarshalJSON treats malformed additive origin metadata locally. It cannot
// make an otherwise usable historical card fail decoding.
func (o *Origin) UnmarshalJSON(b []byte) error {
	var fields map[string]json.RawMessage
	*o = Origin{}
	if json.Unmarshal(b, &fields) != nil {
		return nil
	}
	for key, target := range map[string]*string{"adapter": &o.Adapter, "source_id": &o.SourceID, "name": &o.Name, "object_id": &o.ObjectID} {
		if raw, ok := fields[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && len(s) <= 256 && secure.Text(s) == s {
				*target = s
			}
		}
	}
	return nil
}

type SourceCounts struct {
	Selected int `json:"selected"`
	Current  int `json:"current"`
	Stale    int `json:"stale"`
	Missing  int `json:"missing"`
}
type SourcesCounts struct {
	Sources   int `json:"sources"`
	Selected  int `json:"selected"`
	Current   int `json:"current"`
	Stale     int `json:"stale"`
	Missing   int `json:"missing"`
	Attention int `json:"attention"`
}
type SourceStatus struct {
	DirectSource
	Counts SourceCounts `json:"counts"`
}
type SourcesStatus struct {
	// Reason refines acquisition state without changing the schema-1 state vocabulary.
	Reason  string         `json:"reason,omitempty"`
	State   string         `json:"state"`
	Counts  SourcesCounts  `json:"counts"`
	Sources []SourceStatus `json:"sources"`
}
