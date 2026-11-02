package mihomo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// A reference is a names-only publication made independently of the controller
// being checked. Never derive desired membership from that same controller.
type providerReference struct {
	Schema      int                 `json:"schema"`
	PublishedAt time.Time           `json:"published_at"`
	Providers   map[string][]string `json:"providers"`
}
type referenceSnapshot struct {
	PublishedAt time.Time
	Providers   map[string][]string
	Revision    string
}

func readReference(ctx context.Context, deps Dependencies, x model.Check) (referenceSnapshot, error) {
	ref := x.Adapter.ProviderReference
	token, err := secure.SecretEnv(ref.TokenEnv)
	if err != nil {
		return referenceSnapshot{}, adapters.Local("provider_reference", "reference_secret_missing")
	}
	// A reference has its own hostname, CA and credential. A controller's SNI,
	// resolve override and token must never follow a request to the publisher.
	sourceCheck := x
	sourceCheck.ServerName, sourceCheck.ResolveIP, sourceCheck.CAFile = "", "", ref.CAFile
	client, closeClient, err := deps.Client(sourceCheck, ref.URL)
	if err != nil {
		return referenceSnapshot{}, adapters.Local("provider_reference", "reference_transport_unavailable")
	}
	defer closeClient()
	req, err := http.NewRequestWithContext(ctx, "GET", ref.URL, nil)
	if err != nil {
		return referenceSnapshot{}, adapters.Local("provider_reference", "invalid_provider_reference")
	}
	req.Header.Set("Cache-Control", "no-cache")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return referenceSnapshot{}, adapters.Local("provider_reference", "reference_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return referenceSnapshot{}, adapters.Local("provider_reference", fmt.Sprintf("reference_http_%d", resp.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return referenceSnapshot{}, adapters.Local("provider_reference", "reference_unavailable")
	}
	if len(raw) > 1<<20 {
		return referenceSnapshot{}, adapters.Local("provider_reference", "reference_response_limit_exceeded")
	}
	if secure.JSONResponseBudget(raw, 128, 4096, 4096) != nil {
		return referenceSnapshot{}, adapters.Local("provider_reference", "reference_complexity_exceeded")
	}
	var doc providerReference
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&doc) != nil || doc.Schema != 1 || doc.PublishedAt.IsZero() || len(doc.Providers) > 16 {
		return referenceSnapshot{}, adapters.Local("provider_reference", "invalid_reference_document")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return referenceSnapshot{}, adapters.Local("provider_reference", "invalid_reference_document")
	}
	now := time.Now()
	if doc.PublishedAt.After(now.Add(30*time.Second)) || now.Sub(doc.PublishedAt) > time.Duration(ref.MaxAgeSeconds)*time.Second {
		return referenceSnapshot{}, adapters.Local("provider_reference", "reference_not_fresh")
	}
	selected := map[string][]string{}
	for _, provider := range ref.Providers {
		members, ok := doc.Providers[provider]
		if !ok || len(members) == 0 {
			return referenceSnapshot{}, adapters.Local("provider_reference", "reference_provider_missing")
		}
		seen := map[string]bool{}
		for _, name := range members {
			if name == "" || len(name) > 2048 || secure.Text(name) != name || seen[name] {
				return referenceSnapshot{}, adapters.Local("provider_reference", "invalid_reference_members")
			}
			seen[name] = true
		}
		selected[provider] = members
	}
	// Include the publication timestamp, but ignore unrelated providers. The hash
	// is provenance, not proof of proxy parameters or end-to-end connectivity.
	canonical, _ := json.Marshal(providerReference{Schema: 1, PublishedAt: doc.PublishedAt.UTC(), Providers: selected})
	digest := sha256.Sum256(canonical)
	return referenceSnapshot{PublishedAt: doc.PublishedAt, Providers: selected, Revision: hex.EncodeToString(digest[:])}, nil
}
