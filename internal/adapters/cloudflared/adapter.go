// Package cloudflared reads a Cloudflare Tunnel connector's own report from
// its local metrics listener. The listener is loopback-only and read-only;
// the connector's statement is reported evidence about the tunnel session,
// never proof that the service behind it answers.
package cloudflared

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// PathEvidence says the sample is the connector's own runtime statement; the
// constant lives in model because the engine reads the same marker.
const PathEvidence = model.LocalConnectorPathEvidence

// The two reads, in order. Each has its own byte budget and both happen under
// the check's one timeout.
const (
	readyPath   = "/ready"
	metricsPath = "/metrics"
)

// Dependencies are supplied by the probe, preserving its TLS policy and its
// refusal to reach anything but the literal loopback listener.
type Dependencies struct {
	Client func(model.Check, string) (*http.Client, func(), error)
}

// Collect reads /ready and then /metrics. Readiness is the connector's own
// word: a 503, a body status other than 200, or zero ready connections is
// tunnel_not_ready, a failure the connector itself states. /ready decides
// before /metrics is required; metrics is best-effort facts when the connector
// says it holds no session. Anything that keeps the listener from answering in
// the documented shapes is a local monitor error, never a target outage.
func Collect(ctx context.Context, deps Dependencies, x model.Check, target string, o *model.Observation) error {
	options := x.Adapter.CloudflaredOptions()
	if err := Validate(options); err != nil {
		return adapters.Local("setup", "invalid_cloudflared_options")
	}
	if err := ValidateTarget(target, x.CAFile); err != nil {
		return adapters.Local("setup", "invalid_adapter_url")
	}
	if o.Facts == nil {
		o.Facts = map[string]string{}
	}
	o.PathEvidence = PathEvidence
	client, closeClient, err := deps.Client(x, target)
	if err != nil {
		return err
	}
	defer closeClient()

	readyStatus, body, err := read(ctx, client, target, readyPath, ReadyBudget)
	if err != nil || readyStatus != http.StatusOK && readyStatus != http.StatusServiceUnavailable {
		return adapters.Local("adapter", "metrics_unavailable")
	}
	ready, err := ParseReady(body)
	if err != nil {
		return adapters.Local("adapter", "metrics_unavailable")
	}
	// /ready decides the outcome. /metrics is a fact source: required once
	// the connector says it holds a session, best-effort when the connector
	// itself says it does not. A closed listener fails /ready first and is a
	// monitor error, which is how a stopped connector differs from a live one
	// with no session.
	established := readyStatus == http.StatusOK && ready.Status == http.StatusOK && ready.Connections > 0
	metricsStatus, body, metricsErr := read(ctx, client, target, metricsPath, MetricsBudget)
	var metrics Metrics
	if metricsErr == nil && metricsStatus == http.StatusOK {
		metrics = ParseMetrics(body)
	}
	o.Facts["ready_connections"] = strconv.Itoa(ready.Connections)
	if ready.ConnectorID != "" {
		o.Facts["connector_id"] = ready.ConnectorID
	}
	if established && (metricsErr != nil || metricsStatus != http.StatusOK || !metrics.Parsed()) {
		o.Facts["connector_ready"] = "true"
		o.Facts["metrics_status"] = "unavailable"
		return adapters.Local("adapter", "metrics_unavailable")
	}

	for fact, value := range map[string]*int64{
		"ha_connections": metrics.HAConnections,
		"requests_total": metrics.TotalRequests,
		"request_errors": metrics.RequestErrors,
		"active_streams": metrics.ActiveStreams,
	} {
		if text, ok := metrics.Count(value); ok {
			o.Facts[fact] = text
		}
	}
	if metrics.ConnectorVersion != "" {
		o.Facts["connector_version"] = metrics.ConnectorVersion
	}
	if len(metrics.ServerLocations) > 0 {
		o.Facts["connection_colos"] = strings.Join(metrics.ServerLocations, ",")
	}

	o.Stage = "adapter"
	if !established {
		o.Outcome, o.Code = "fail", "tunnel_not_ready"
		o.Detail = "the connector reports no ready edge connection; the service behind it is unverified"
		return nil
	}
	o.Outcome, o.Detail = "ok", "tunnel established; service behind it unproven"
	if options.ExpectedConnections != nil && ready.Connections < *options.ExpectedConnections {
		o.Outcome, o.Code = "policy", "connections_below_expected"
		o.Detail = "the connector holds fewer edge connections than the deployment declares"
		return nil
	}
	// A colo the report did not name asserts nothing, as a missing trace line
	// does not violate a trace policy. A reported colo is peer-reported
	// metadata from the connector, not proof of which edge carried a service
	// request. Every distinct colo is compared, not only the eight the fact
	// can show: the ninth is still a connection outside a declared list.
	if outsideColos(metrics.allLocations, options.ExpectedColos) {
		o.Outcome, o.Code = "policy", "colo_outside_expected"
		o.Detail = "the connector holds an edge connection outside the declared colos"
	}
	return nil
}

// read performs one bounded GET against the listener. The target's host and
// port are already known literal loopback; the path is one of the two fixed
// reads, never user text.
func read(ctx context.Context, client *http.Client, target, path string, limit int64) (int, []byte, error) {
	address, err := readAddress(target, path)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "Topophany/"+model.Version)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if int64(len(body)) > limit {
		return resp.StatusCode, nil, errors.New("response exceeds its budget")
	}
	return resp.StatusCode, body, nil
}

func readAddress(target, path string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// outsideColos reports whether a reported connection sits outside the
// declared list. Both sides compare without case, because Cloudflare writes a
// colo uppercase and the listener labels it lowercase. An empty report, an
// empty declaration, or an unreported colo is no violation.
func outsideColos(reported, expected []string) bool {
	if len(reported) == 0 || len(expected) == 0 {
		return false
	}
	allowed := make(map[string]bool, len(expected))
	for _, colo := range expected {
		allowed[strings.ToLower(colo)] = true
	}
	for _, colo := range reported {
		if !allowed[strings.ToLower(colo)] {
			return true
		}
	}
	return false
}
