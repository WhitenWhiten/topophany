package local

import (
	"context"
	"errors"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/adapters/mihomo"
	"github.com/WhitenWhiten/topophany/internal/probe"
)

type Engine struct {
	Version string `json:"version"`
	Mode    string `json:"mode"`
	TUN     *bool  `json:"tun"`
}

// Inspection is one read of an engine for configuring a check of it. Groups
// are those usable as root_proxy, never their members or selections.
type Inspection struct {
	Check     Check          `json:"check"`
	Engine    Engine         `json:"engine"`
	Groups    []mihomo.Group `json:"groups"`
	Truncated bool           `json:"truncated"`
	Warnings  []string       `json:"warnings"`
}

// draftID stands in for a check ID not chosen yet; a save assigns one.
const draftID = "lc-draft"

// ValidateDraft is Validate for a check whose ID may still be empty.
func (c Check) ValidateDraft() error {
	if c.ID == "" {
		c.ID = draftID
	}
	return c.Validate()
}

// Inspect reads the engine c names once with token ("" for none), through
// the transport its local check uses. It writes nothing.
func Inspect(ctx context.Context, c Check, token string) (Inspection, error) {
	if err := c.ValidateDraft(); err != nil {
		return Inspection{}, err
	}
	probeCheck := c
	if probeCheck.ID == "" {
		probeCheck.ID = draftID
	}
	cfg, err := Config([]Check{probeCheck}, func(string) bool { return token != "" })
	if err != nil {
		return Inspection{}, err
	}
	runner := probe.Runner{Secret: secretResolver(map[string]string{probeCheck.ID: token})}
	read, err := runner.InspectMihomo(ctx, cfg, cfg.Checks[0])
	if err != nil {
		return Inspection{}, EngineFailure(err)
	}
	out := Inspection{Check: c.Normalized(), Engine: Engine{Version: read.Version, Mode: read.Mode, TUN: read.TUN}, Groups: []mihomo.Group{}, Warnings: []string{}}
	found := c.RootProxy == ""
	for _, g := range read.Groups {
		found = found || g.Name == c.RootProxy
		if len(out.Groups) == MaxGroups {
			out.Truncated = true
			continue
		}
		out.Groups = append(out.Groups, g)
	}
	if read.Omitted > 0 {
		out.Warnings = append(out.Warnings, "group_names_omitted")
	}
	if !found {
		out.Warnings = append(out.Warnings, "root_proxy_not_found")
	}
	return out, nil
}

// EngineFailure maps the adapter's closed diagnostic codes to the local
// configuration API's codes.
func EngineFailure(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var f *adapters.Failure
	if !errors.As(err, &f) {
		return fail("connection_failed")
	}
	switch code := f.Code; {
	case code == "authentication_failed" || code == "adapter_secret_missing":
		return fail("authentication_failed")
	case strings.HasPrefix(code, "tls_"):
		return fail("tls_error")
	case strings.HasPrefix(code, "http_") || code == "unexpected_status":
		return fail("unexpected_status")
	case code == "missing_mode" || code == "missing_proxies" || code == "unsupported_response_shape":
		return fail("unsupported_response_shape")
	case code == "response_limit_exceeded" || code == "response_complexity_exceeded" || code == "proxy_inventory_capacity_exceeded" || code == "proxy_members_capacity_exceeded":
		return fail("response_too_large")
	case code == "insecure_http_credentials" || code == "invalid_adapter_url" || code == "invalid_address_reference":
		return fail("not_local_address")
	}
	return fail("connection_failed")
}

// secretResolver resolves only this store's secrets by their token_env
// names. It never falls back to the process environment.
func secretResolver(tokens map[string]string) func(string) (string, error) {
	byName := map[string]string{}
	for id, token := range tokens {
		if token != "" {
			byName[tokenEnv(id)] = token
		}
	}
	return func(name string) (string, error) {
		if name == "" {
			return "", nil
		}
		if token, ok := byName[name]; ok {
			return token, nil
		}
		return "", errors.New("local secret unavailable")
	}
}
