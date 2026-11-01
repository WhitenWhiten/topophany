package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type coreProviders struct {
	Providers map[string]struct {
		Proxies   []coreProxy     `json:"proxies"`
		UpdatedAt json.RawMessage `json:"updatedAt"`
	} `json:"providers"`
}

func collectProviders(ctx context.Context, deps Dependencies, client *http.Client, x model.Check, target, token string, name func(string) string, o *model.Observation, out *coreProviders, needChain bool) error {
	options := x.Adapter.Mihomo()
	// Comparison counts stay withheld for every secret-bearing read,
	// including one whose names a local viewer keeps.
	private := o.Facts["peer_text"] == adapters.Withheld || o.Facts["peer_text"] == adapters.NamesKeptLocal
	if len(options.ProviderMatchModes) > 0 && Validate(options) != nil {
		return adapters.Local("setup", "invalid_provider_options")
	}
	if len(options.ExpectedProviders) == 0 && options.ProviderReference == nil && !needChain {
		return nil
	}
	// Preserve the existing direct-run checks and diagnostic codes for static lists.
	if len(options.ExpectedProviders) > 16 {
		return adapters.Local("setup", "provider_evidence_budget_exceeded")
	}
	for k := range options.ExpectedProviders {
		if len(k) > 119 || secure.Text(k) != k {
			return adapters.Local("setup", "invalid_provider_name")
		}
	}
	expected := options.ExpectedProviders
	var before referenceSnapshot
	if options.ProviderReference != nil {
		if Validate(options) != nil || ValidateReferenceTransport(options.ProviderReference, deps.Proxied) != nil {
			return adapters.Local("setup", "invalid_provider_reference")
		}
		var err error
		before, err = readReference(ctx, deps, x)
		if err != nil {
			return err
		}
		expected = make(map[string][]string, len(options.ExpectedProviders)+len(before.Providers))
		for k, v := range options.ExpectedProviders {
			expected[k] = v
		}
		for k, v := range before.Providers {
			expected[k] = v
		}
	}
	if err := readCore(ctx, client, target, "/providers/proxies", token, out); err != nil {
		return err
	}
	if len(expected) == 0 && options.ProviderReference == nil {
		return nil
	}
	stable := true
	settlingWindow := false
	if options.ProviderReference != nil {
		after, err := readReference(ctx, deps, x)
		if err != nil {
			return err
		}
		stable = before.Revision == after.Revision
		o.Facts["provider_reference_published_at"] = before.PublishedAt.UTC().Format(time.RFC3339Nano)
		o.Facts["provider_reference_consistency"] = "stable"
		if !stable {
			o.Facts["provider_reference_consistency"] = "changed"
		}
		// A controller loads a new provider over a minute or two. Inside
		// that window a difference is not yet confirmed; the sample still
		// says policy, and the engine needs consecutive policy samples
		// (including this window) before it opens an incident. The window
		// is measured from the sample's own time, not the wall clock at
		// comparison time, so a replay keeps its original decision.
		at := o.SampledAt
		if at.IsZero() {
			at = time.Now()
		}
		if wait := settle(options.ProviderReference); wait > 0 && !before.PublishedAt.IsZero() && at.Sub(before.PublishedAt) < wait {
			settlingWindow = true
		}
	}
	// A policy that was already known before the provider comparison (mode
	// or TUN) is not a settling publication; do not let a provider's settle
	// window delay its confirmation.
	policyBefore := o.Outcome == "policy"
	keys := make([]string, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	summaries := []string{}
	mismatched, pending := 0, 0
	// The controller's own account of when it last loaded each provider,
	// beside the publisher's time and the sample's time. Reference
	// providers come first: settling turns on their load times.
	var loads []providerLoad
	for _, k := range keys {
		p, ok := out.Providers[k]
		_, dynamic := before.Providers[k]
		updatedAt := providerUpdatedAt(p.UpdatedAt)
		if !updatedAt.IsZero() {
			loads = append(loads, providerLoad{name: k, at: updatedAt, dynamic: dynamic})
		}
		actual, shown := []string{}, []string{}
		for _, v := range p.Proxies {
			actual = append(actual, v.Name)
			shown = append(shown, name(v.Name))
		}
		comparison := compareMembers(actual, expected[k], options.ProviderMatchModes[k])
		same := ok && comparison.matches

		o.Facts["provider."+adapters.SafeFact(k)] = adapters.SafeFact(strings.Join(shown, ","))
		status := "match"
		if !same {
			status = "mismatch"
		}
		if dynamic && !stable {
			status = "unverified"
		}
		// The controller may still be loading a fresh publication. If it
		// says it loaded this provider after that publication, it is not
		// still loading: the difference is real.
		loadPending := dynamic && stable && !same &&
			(updatedAt.IsZero() || !updatedAt.After(before.PublishedAt))
		providerSettling := loadPending && settlingWindow
		if providerSettling {
			status = "settling"
		}
		if private {
			summaries = append(summaries, fmt.Sprintf("%s=%s", k, status))
		} else {
			summaries = append(summaries, fmt.Sprintf("%s=%s(expected:%d loaded:%d missing:%d extra:%d order_changed:%t present:%t)", k, status, len(expected[k]), len(actual), comparison.missing, comparison.extra, comparison.orderChanged, ok))
		}
		if !same && (!dynamic || stable) {
			mismatched++
			if loadPending {
				pending++
			}
			o.Outcome, o.Code = "policy", "provider_membership_mismatch"
			o.Detail = "desired provider membership/order differs from the loaded runtime"
		}
	}
	allPending := mismatched > 0 && pending == mismatched && !policyBefore
	if allPending && settlingWindow {
		// Every difference is inside a window the controller may still be
		// loading. It is a policy sample for confirmation purposes, not an
		// unknown: a publisher that keeps republishing cannot reset it.
		o.Facts["provider_reference_settling"] = "true"
		o.Detail = "provider membership differs while the controller may still be loading the publication; not yet confirmed"
	}
	o.Facts["provider_comparison"] = adapters.SafeFact(strings.Join(summaries, "; "))
	if options.ProviderReference != nil && len(o.Facts) < model.MaxObservationFacts {
		// This evidence does not depend on the worker's settle_seconds. The
		// collector can apply a new window before the worker reloads. At the
		// full budget, a true settling marker already establishes a pending
		// load; without that marker there is always room for this fact.
		o.Facts["provider_reference_load_pending"] = strconv.FormatBool(allPending)
	}
	if !stable && o.Outcome != "policy" {
		o.Outcome, o.Code = "unknown", "provider_reference_changed"
		o.Detail = "provider publication changed during sampling; loaded membership cannot be compared consistently"
	}
	addLoadTimes(o, loads)
	return nil
}

type providerLoad struct {
	name    string
	at      time.Time
	dynamic bool
}

// addLoadTimes records each provider's load time while the observation has
// room for it. A load time is optional evidence, one fact per provider:
// sixteen providers with their members and the fixed facts already fill
// the collector's budget, and one fact more would refuse the observation
// with its whole batch. The facts it needs are all set before this runs.
func addLoadTimes(o *model.Observation, loads []providerLoad) {
	slices.SortStableFunc(loads, func(a, b providerLoad) int {
		switch {
		case a.dynamic == b.dynamic:
			return 0
		case a.dynamic:
			return -1
		}
		return 1
	})
	for _, l := range loads {
		// Keep metadata outside the provider.<name> membership namespace.
		// A valid provider name can still make this optional key too long.
		key := "provider_updated_at." + l.name
		if len(key) > 128 {
			continue
		}
		if len(o.Facts) >= model.MaxObservationFacts {
			return
		}
		o.Facts[key] = l.at.UTC().Format(time.RFC3339Nano)
	}
}

// providerUpdatedAt parses the controller's optional loaded-at metadata.
// It is display and settlement evidence, not a measurement: a controller
// that sends an empty, numeric or malformed value must not fail every
// Mihomo check just because this field was added.
func providerUpdatedAt(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}
	}
	return at
}
