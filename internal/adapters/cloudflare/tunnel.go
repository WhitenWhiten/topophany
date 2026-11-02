package cloudflare

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// tunnelResponse reads only the documented fields this adapter reports.
// origin_ip, connection and client identifiers, the tunnel name and the
// account tag are deliberately absent: they never enter a fact even in
// memory beyond the JSON decoder's ignored fields.
type tunnelResponse struct {
	Success *bool `json:"success"`
	Result  *struct {
		Status      string             `json:"status"`
		Connections []tunnelConnection `json:"connections"`
	} `json:"result"`
}

type tunnelConnection struct {
	ColoName           string `json:"colo_name"`
	IsPendingReconnect bool   `json:"is_pending_reconnect"`
	OpenedAt           string `json:"opened_at"`
}

// Closed tunnel statuses, per Cloudflare's Get a Cloudflare Tunnel reference.
const (
	tunnelHealthy  = "healthy"
	tunnelDegraded = "degraded"
	tunnelDown     = "down"
	tunnelInactive = "inactive"
)

// connectionsNotReported is the detail of a read whose connection list could
// not be taken at its word; Collect keeps it instead of the generic one.
const connectionsNotReported = "Cloudflare API state reported; the connections list was not reported"

// collectTunnel turns the tunnel read into facts and declared-state policies.
// Cloudflare documents the connections array as deprecated and says it will
// start returning empty in favour of the dedicated connections endpoint. The
// statuses healthy and degraded mean at least one live connection by
// definition, so an empty list beside either is the field having moved, not a
// tunnel without a connector: the list is then treated as not reported, with
// no connection fact and neither connection policy. If a connection policy
// is configured, its result is unknown even though the API read succeeded.
// An empty list beside down or inactive is a count of zero, and the policies
// apply. tunnel_status carries the API's own word either way.
func collectTunnel(body []byte, options model.CloudflareOptions, o *model.Observation) (err error) {
	defer func() {
		if err == nil && options.ExpectedStatus != "" && o.Facts["tunnel_status"] != options.ExpectedStatus {
			o.Outcome, o.Code, o.Detail = "policy", "tunnel_status_mismatch", "Cloudflare tunnel status differs from the declared policy"
		}
	}()

	var doc tunnelResponse
	if err := decodeJSON(body, &doc); err != nil {
		return err
	}
	if err := checkEnvelope(doc.Success); err != nil {
		return err
	}
	if doc.Result == nil {
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	switch doc.Result.Status {
	case tunnelHealthy, tunnelDegraded, tunnelDown, tunnelInactive:
		o.Facts["tunnel_status"] = doc.Result.Status
	default:
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	o.Outcome = "ok"

	listed := len(doc.Result.Connections) > 0 || doc.Result.Status == tunnelDown || doc.Result.Status == tunnelInactive
	if !listed {
		o.Detail = connectionsNotReported
		if options.ExpectedConnections != nil || len(options.ExpectedColos) > 0 {
			o.Outcome, o.Code = "unknown", "connections_unreported"
		}
		return nil
	}
	return collectConnections(doc.Result.Connections, options, o)
}

func collectConnections(list []tunnelConnection, options model.CloudflareOptions, o *model.Observation) error {
	connections := len(list)
	pending := 0
	colos := map[string]bool{}
	missingColo := false
	var oldest time.Time
	for _, c := range list {
		if c.IsPendingReconnect {
			pending++
		}
		if model.IsColo(c.ColoName) {
			colos[c.ColoName] = true
		} else {
			missingColo = true
		}
		if opened, err := time.Parse(time.RFC3339Nano, c.OpenedAt); err == nil && (oldest.IsZero() || opened.Before(oldest)) {
			oldest = opened
		}
	}
	o.Facts["tunnel_connections"] = strconv.Itoa(connections)
	o.Facts["tunnel_pending_reconnect"] = strconv.Itoa(pending)
	allColos := sortedTokens(colos)
	if len(allColos) > 0 {
		fact := allColos
		if len(fact) > MaxTunnelColos {
			fact = fact[:MaxTunnelColos]
		}
		o.Facts["tunnel_colos"] = strings.Join(fact, ",")
	}
	if !oldest.IsZero() {
		o.Facts["oldest_connection_at"] = oldest.UTC().Format(time.RFC3339)
	}

	if want := options.ExpectedConnections; want != nil && connections < *want {
		o.Outcome, o.Code = "policy", "connections_below_expected"
		o.Detail = "the tunnel has fewer live connections than the deployment declares"
		return nil
	}
	// Compare every distinct reported colo, not only the ones the capped fact
	// can show: the ninth colo is still a connection outside a declared list.
	if outsideColos(allColos, options.ExpectedColos) {
		o.Outcome, o.Code = "policy", "colo_outside_expected"
		o.Detail = "a tunnel connection is established at a colo outside the declared list"
	} else if len(options.ExpectedColos) > 0 && (missingColo || connections == 0) {
		o.Outcome, o.Code = "unknown", "connection_colo_unreported"
		o.Detail = "Cloudflare API did not provide complete tunnel connection colo evidence"
	}
	return nil
}

// sortedTokens returns the distinct tokens in sorted order. The fact caps the
// list it prints; the colo policy compares this full set.
func sortedTokens(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for token := range set {
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}

// outsideColos reports whether a reported connection sits outside the
// declared list. An empty report or an empty declaration asserts nothing.
func outsideColos(reported, expected []string) bool {
	if len(reported) == 0 || len(expected) == 0 {
		return false
	}
	allowed := make(map[string]bool, len(expected))
	for _, colo := range expected {
		allowed[colo] = true
	}
	for _, colo := range reported {
		if !allowed[colo] {
			return true
		}
	}
	return false
}
