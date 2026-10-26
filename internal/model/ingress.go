package model

import "time"

// Ingress sums TLS handshake failures the collector saw before a request
// carried any observer identity. It is evidence about delivery into the
// collector, not about the targets observers watch: a failed handshake
// means the evidence it carried never arrived, so the checks behind it read
// stale rather than failed. A source stays an address: the observer names
// itself only after the handshake succeeds.
//
// Failures is the total inside the window. Sources holds the busiest of
// SourceCount distinct addresses; when more addresses were seen than the
// counter can track, Truncated says SourceCount is a lower bound.
type Ingress struct {
	FailureTypes  map[string]int  `json:"failure_types,omitempty"`
	WindowSeconds int             `json:"window_seconds"`
	Failures      int             `json:"failures"`
	SourceCount   int             `json:"source_count"`
	Truncated     bool            `json:"truncated,omitempty"`
	Last          time.Time       `json:"last,omitzero"`
	Sources       []IngressSource `json:"sources,omitempty"`
}

// IngressSource is one remote address's share of those failures inside the
// window.
type IngressSource struct {
	Address string    `json:"address"`
	Count   int       `json:"count"`
	Last    time.Time `json:"last"`
}
