package cfservermonitor

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters/sourceutil"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// Discover lists accessible servers using read-only Worker endpoints.
func Discover(ctx context.Context, io source.IO, s model.Source) ([]source.DiscoveredObject, error) {
	if (Adapter{}).Validate(s) != nil {
		return nil, failure("cfsm_config")
	}
	pages := 1
	if s.CredentialEnv != "" {
		pages++
	}
	if code := sourceutil.ReadBudget(s, io.Pages(), pages); code != "" {
		return nil, failure(code)
	}
	headers, token, err := requestHeaders(ctx, io, s)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(s.Target, "/")
	if token != "" {
		// Public Workers can serve a guest list when a supplied JWT is invalid.
		// Only /api/config explicitly reports whether the Worker accepted it.
		body, err := get(ctx, io, s, base+"/api/config", headers)
		if err != nil {
			return nil, err
		}
		config, err := decodeObject(body)
		if err != nil {
			return nil, err
		}
		var authorized *bool
		if json.Unmarshal(config["authorization"], &authorized) != nil || authorized == nil {
			return nil, failure("cfsm_json")
		}
		if !*authorized {
			return nil, failure("cfsm_auth")
		}
	}
	body, err := get(ctx, io, s, base+"/api/servers", headers)
	if err != nil {
		return nil, err
	}
	return decodeServers(body, token)
}

func decodeServers(body []byte, token string) ([]source.DiscoveredObject, error) {
	envelope, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	var records []map[string]json.RawMessage
	if json.Unmarshal(envelope["servers"], &records) != nil || records == nil {
		return nil, failure("cfsm_json")
	}
	if len(records) > 1000 {
		return nil, failure("cfsm_discovery_budget")
	}
	servers := make([]source.DiscoveredObject, 0, len(records))
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		var id, name string
		if json.Unmarshal(record["id"], &id) != nil || !ValidObjectID(id) || seen[id] || token != "" && strings.Contains(token, id) {
			return nil, failure("cfsm_record_identity")
		}
		seen[id] = true
		if raw := record["name"]; len(raw) != 0 && json.Unmarshal(raw, &name) != nil {
			return nil, failure("cfsm_json")
		}
		label := strings.TrimSpace(secure.Text(name))
		if label == "" || len(label) > 128 || token != "" && (strings.Contains(name, token) || strings.Contains(label, token)) {
			label = "Server " + id
		}
		servers = append(servers, source.DiscoveredObject{ID: id, Name: label})
	}
	return servers, nil
}
