package cloudflare

import (
	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// The dedicated endpoint returns connectors containing sessions, not a flat
// list of sessions. No platform health status is inferred from this inventory.
func collectTunnelConnections(body []byte, options model.CloudflareOptions, o *model.Observation) error {
	var doc struct {
		Success *bool `json:"success"`
		Result  *[]struct {
			Conns *[]tunnelConnection `json:"conns"`
		} `json:"result"`
		Info *struct {
			Total *int `json:"total_count"`
		} `json:"result_info"`
	}
	if err := decodeJSON(body, &doc); err != nil {
		return err
	}
	if err := checkEnvelope(doc.Success); err != nil {
		return err
	}
	if doc.Result == nil || doc.Info != nil && doc.Info.Total != nil && *doc.Info.Total != len(*doc.Result) {
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	var sessions []tunnelConnection
	for _, connector := range *doc.Result {
		if connector.Conns == nil {
			return adapters.Local("adapter_api", "unsupported_response_shape")
		}
		sessions = append(sessions, (*connector.Conns)...)
	}
	o.Outcome = "ok"
	return collectConnections(sessions, options, o)
}
