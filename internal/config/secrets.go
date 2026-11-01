package config

import (
	"net/url"
	"os"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// HasLiteralSecret reports whether c stores a secret value, not merely the
// name of an environment variable that holds one. A topology without one may
// stay group-readable; a topology with one may not.
func HasLiteralSecret(c model.Config) bool {
	for _, e := range c.Endpoints {
		if urlCarriesSecret(e.Address) {
			return true
		}
	}
	for _, p := range c.Paths {
		for _, proxy := range p.Proxies {
			if urlCarriesSecret(proxy.Address) {
				return true
			}
		}
	}
	for _, x := range c.Checks {
		if len(x.HTTP.RequestHeaders) > 0 || len(x.HTTP.JSONEquals) > 0 || urlCarriesSecret(x.Target) {
			return true
		}
		if ref := x.Adapter.ProviderReference; ref != nil && urlCarriesSecret(ref.URL) {
			return true
		}
	}
	return false
}

// WriteMode is the mode to use when replacing a topology file. A file that
// holds a literal secret is always 0600; a secret-free file keeps the mode
// it already had.
func WriteMode(c model.Config, existing os.FileMode) os.FileMode {
	if HasLiteralSecret(c) || existing == 0 {
		return 0600
	}
	return existing.Perm()
}

func urlCarriesSecret(s string) bool {
	if s == "" {
		return false
	}
	if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Opaque == "" {
		return u.User != nil || u.RawQuery != ""
	}
	return strings.Contains(s, "@") || strings.Contains(s, "?")
}
