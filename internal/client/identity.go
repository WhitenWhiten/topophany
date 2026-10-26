package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/config"
)

type WorkerIdentity struct {
	Project, Observer, Instance string
	CollectorID                 string `json:"collector_id"`
}

// IdentifyWorker reads only authenticated enrollment metadata, never topology
// or samples. HTTPS authenticates the peer; plaintext needs explicit consent.
func IdentifyWorker(ctx context.Context, h *http.Client, ep Endpoint, project string) (WorkerIdentity, error) {
	var info WorkerIdentity
	req, err := http.NewRequestWithContext(ctx, "GET", endpointURL(ep, "/v1/worker", nil), nil)
	if err != nil {
		return info, err
	}
	if err = authorize(req, ep); err != nil {
		return info, err
	}
	trace := &requestTrace{}
	resp, err := h.Do(trace.attach(req))
	if err != nil {
		return info, transportFailure(err, trace.current())
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return info, transportFailure(err, "response_read")
	}
	if resp.StatusCode != 200 {
		return info, &HTTPError{Status: resp.StatusCode, Code: "identity_unavailable"}
	}
	if len(b) > 4096 || json.Unmarshal(b, &info) != nil || info.Project != project || !config.ID(info.Observer) || len(info.CollectorID) != 64 || strings.Trim(info.CollectorID, "0123456789abcdef") != "" {
		return info, errors.New("authenticated collector identity missing or mismatched; upgrade collector and verify project")
	}
	if ep.CollectorID != "" && ep.CollectorID != info.CollectorID {
		return info, &Failure{Stage: "identity", Code: "collector_identity_mismatch"}
	}
	return info, nil
}

// ResolveDeliveryTargets groups authenticated identities before the worker
// creates queues. Servers are independent replicas; alternate_urls are ordered
// addresses of one instance and inherit the endpoint's credentials and CA.
func ResolveDeliveryTargets(ctx context.Context, p Profile) (Profile, []string, error) {
	if err := p.Validate(); err != nil {
		return p, nil, err
	}
	out := Profile{Project: p.Project}
	var notes []string
	groups := map[string]int{}
	for _, ep := range p.Servers {
		var identity WorkerIdentity
		addresses := append([]string{ep.URL}, ep.AlternateURLs...)
		var last error
		for _, address := range addresses {
			candidate := ep
			candidate.URL = address
			h, err := HTTP(candidate)
			if err != nil {
				return out, notes, err
			}
			info, err := IdentifyWorker(ctx, h, candidate, p.Project)
			h.CloseIdleConnections()
			if err != nil {
				last = err
				continue
			}
			if identity.CollectorID != "" && identity.CollectorID != info.CollectorID {
				return out, notes, errors.New("alternate_urls identify independent collectors; move them to separate servers entries")
			}
			if identity.Observer != "" && identity.Observer != info.Observer {
				return out, notes, errors.New("collector aliases use different worker scopes; align their credentials")
			}
			identity = info
		}
		if identity.CollectorID == "" && ep.CollectorID != "" {
			identity.CollectorID = ep.CollectorID
		}
		if identity.CollectorID == "" {
			return out, notes, errors.New("cannot authenticate delivery target identity; check connectivity/credentials or upgrade collector: " + safeIdentityError(last))
		}
		ep.CollectorID = identity.CollectorID
		if at, ok := groups[identity.CollectorID]; ok {
			root := &out.Servers[at]
			// Fail closed when automatic aliases would change authorization/CA policy.
			if root.Token != ep.Token || root.TokenEnv != ep.TokenEnv || root.CAFile != ep.CAFile || root.AllowInsecure != ep.AllowInsecure {
				return out, notes, errors.New("duplicate authenticated collector has different credentials or CA policy; use one servers entry with alternate_urls")
			}
			root.AlternateURLs = append(root.AlternateURLs, addresses...)
			notes = append(notes, "duplicate authenticated collector addresses grouped as failover; use alternate_urls on one servers entry")
		} else {
			groups[identity.CollectorID] = len(out.Servers)
			out.Servers = append(out.Servers, ep)
		}
	}
	return out, notes, out.Validate()
}
func safeIdentityError(err error) string {
	if err == nil {
		return "no authenticated identity"
	}
	return FailureCode(err)
}
