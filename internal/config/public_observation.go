package config

import "github.com/WhitenWhiten/topophany/internal/model"

// RedactObservation removes fields from an owned observation before it is
// exposed to viewers. Older Cloudflare workers could have stored arbitrary
// client-version strings containing fragments of their bearer token.
func RedactObservation(check model.Check, observation *model.Observation) {
	if check.Kind == "cloudflare" {
		delete(observation.Facts, "connector_versions")
	}
}
