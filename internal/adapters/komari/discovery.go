package komari

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters/sourceutil"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// Discover uses Komari 1.6.0's read-only node listings. The admin listing is
// protected by RequireRole(RoleAdmin), so even an empty list verifies an API
// key. Public /api/nodes and /api/me cannot prove API-key authentication.
// Neither endpoint creates tasks, contacts agents or changes records.
func Discover(ctx context.Context, io source.IO, s model.Source) ([]source.DiscoveredObject, error) {
	if (Adapter{}).Validate(s) != nil {
		return nil, failure("komari_config")
	}
	if code := sourceutil.ReadBudget(s, io.Pages(), 1); code != "" {
		return nil, failure(code)
	}
	path := "/api/nodes"
	headers := map[string]string{"Accept": "application/json"}
	var token string
	if s.CredentialEnv != "" {
		var err error
		token, err = io.Credential(ctx, s)
		if err != nil {
			return nil, failure(sourceutil.IOFailureCode(ctx, err))
		}
		if !sourceutil.ValidCredential(token, 12, 1024-len("Bearer ")) {
			return nil, failure("komari_credential")
		}
		headers["Authorization"] = "Bearer " + token
		path = "/api/admin/client/list"
	}
	body, err := get(ctx, io, s, strings.TrimRight(s.Target, "/")+path, headers)
	if err != nil {
		return nil, err
	}
	if err := jsonBudget(body); err != nil {
		return nil, err
	}
	if token == "" {
		envelope, err := decodeObject(body)
		if err != nil {
			return nil, err
		}
		var status string
		if json.Unmarshal(envelope["status"], &status) != nil || status != "success" {
			return nil, failure("komari_response")
		}
		body = envelope["data"]
	}
	var records []json.RawMessage
	if json.Unmarshal(body, &records) != nil || records == nil {
		return nil, failure("komari_json")
	}
	if len(records) > 1000 {
		return nil, failure("komari_discovery_budget")
	}
	nodes := make([]source.DiscoveredObject, 0, len(records))
	seen := map[string]bool{}
	for _, raw := range records {
		record, err := decodeObject(raw)
		if err != nil {
			return nil, err
		}
		var id, name string
		if json.Unmarshal(record["uuid"], &id) != nil || !ValidObjectID(id) || seen[id] || token != "" && strings.Contains(id, token) {
			return nil, failure("komari_record_identity")
		}
		seen[id] = true
		if rawName, ok := record["name"]; ok && json.Unmarshal(rawName, &name) != nil {
			return nil, failure("komari_json")
		}
		name = strings.TrimSpace(secure.Text(name))
		if name == "" || len(name) > 128 || token != "" && strings.Contains(name, token) {
			name = "Node " + id
		}
		nodes = append(nodes, source.DiscoveredObject{ID: id, Name: name})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes, nil
}
