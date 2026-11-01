package mihomo

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Inspection is what configuring a check needs to know of a controller: its
// version, mode and TUN state, and the groups a check can resolve as its
// root_proxy. It never names a member proxy or a group's current selection.
type Inspection struct {
	Version string
	Mode    string
	TUN     *bool
	// Groups are in the controller's configuration order, as its GLOBAL
	// group lists them, then any others by name, then GLOBAL itself.
	Groups []Group
	// Omitted counts groups whose names could not be a root_proxy: empty,
	// over 128 bytes or containing control or bidi characters.
	Omitted int
}
type Group struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// rootGroups are the group types expandProxy resolves through a single
// selection. A load balancer is listed although its path stays unresolved;
// a relay is not a selection and is left out.
var rootGroups = map[string]string{"selector": "Selector", "select": "Selector", "urltest": "URLTest", "url-test": "URLTest", "fallback": "Fallback", "loadbalance": "LoadBalance", "load-balance": "LoadBalance"}

// Inspect reads /configs, /version and /proxies once, through the same
// transport, budgets and read allowlist as Collect. It writes nothing and
// triggers no delay test. /version is optional; a controller without it
// reports an empty version.
func Inspect(ctx context.Context, deps Dependencies, x model.Check, target string) (Inspection, error) {
	var out Inspection
	token, e := deps.secret(x.Adapter.Mihomo().TokenEnv)
	if e != nil {
		return out, adapters.Local("setup", "adapter_secret_missing")
	}
	client, closeClient, e := deps.Client(x, target)
	if e != nil {
		return out, e
	}
	defer closeClient()
	var cfg struct {
		Mode string `json:"mode"`
		TUN  struct {
			Enable *bool `json:"enable"`
		} `json:"tun"`
	}
	if e = readCore(ctx, client, target, "/configs", token, &cfg); e != nil {
		return out, e
	}
	if cfg.Mode == "" {
		return out, adapters.Local("adapter_api", "missing_mode")
	}
	out.Mode, out.TUN = closedMode(cfg.Mode), cfg.TUN.Enable
	var version struct {
		Version string `json:"version"`
	}
	if readCore(ctx, client, target, "/version", token, &version) == nil && len(version.Version) <= 64 && secure.Text(version.Version) == version.Version {
		out.Version = version.Version
	}
	var proxies struct {
		Proxies map[string]coreProxy `json:"proxies"`
	}
	if e = readCore(ctx, client, target, "/proxies", token, &proxies); e != nil {
		return out, e
	}
	if proxies.Proxies == nil {
		return out, adapters.Local("adapter_api", "missing_proxies")
	}
	order := map[string]int{}
	for i, name := range proxies.Proxies["GLOBAL"].All {
		if _, seen := order[name]; !seen {
			order[name] = i
		}
	}
	rank := func(name string) int {
		if name == "GLOBAL" {
			return len(order) + 1
		}
		if i, ok := order[name]; ok {
			return i
		}
		return len(order)
	}
	for name, p := range proxies.Proxies {
		kind, ok := rootGroups[strings.ToLower(p.Type)]
		if !ok {
			continue
		}
		if name == "" || len(name) > 128 || secure.Text(name) != name {
			out.Omitted++
			continue
		}
		out.Groups = append(out.Groups, Group{Name: name, Type: kind})
	}
	slices.SortFunc(out.Groups, func(a, b Group) int {
		return cmp.Or(cmp.Compare(rank(a.Name), rank(b.Name)), strings.Compare(a.Name, b.Name))
	})
	return out, nil
}
