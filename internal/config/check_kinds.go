package config

import (
	"fmt"

	"github.com/WhitenWhiten/topophany/internal/model"
)

type optionBlocks uint8

const (
	optionHTTP optionBlocks = 1 << iota
	optionDNS
	optionAdapter
	optionHost
)

type checkKind struct {
	name        string
	options     optionBlocks
	streamProxy bool
	validate    func(model.Config, model.Check) error
}

// This catalog owns kind validation, executed option blocks and editor choices.
// External bindings are configured separately and are not native editor choices.
var nativeKinds = []checkKind{
	{"http", optionHTTP, true, validateHTTPCheck},
	{"tcp", 0, true, validateStreamCheck},
	{"tls", 0, true, validateStreamCheck},
	{"ssh-banner", 0, true, validateStreamCheck},
	{"dns", optionDNS, false, validateDNSCheck},
	{"udp-echo", 0, false, validateStreamCheck},
	{"icmp", 0, false, validateICMPCheck},
	{"mihomo", optionHTTP | optionAdapter, true, validateMihomoCheck},
	{"tailscale", optionAdapter, false, validateTailscaleCheck},
	{"wireguard", optionAdapter, false, validateWireGuardCheck},
	{"host", optionHost, false, nil},
	{"cloudflared", optionAdapter, false, validateCloudflaredCheck},
	{"cloudflare", optionAdapter, true, validateCloudflareCheck},
}

func checkKindOf(name string) (checkKind, bool) {
	for _, kind := range nativeKinds {
		if kind.name == name {
			return kind, true
		}
	}
	if name == "external" {
		return checkKind{name: "external"}, true
	}
	return checkKind{}, false
}

// EditableCheckKinds returns detached native choices in the stable UI order.
func EditableCheckKinds() []string {
	names := make([]string, 0, len(nativeKinds))
	for _, kind := range nativeKinds {
		names = append(names, kind.name)
	}
	return names
}

func validateCheckKind(c model.Config, x model.Check) error {
	if err := ValidateProbePolicies(c, x); err != nil {
		return err
	}
	kind, ok := checkKindOf(x.Kind)
	if !ok {
		return fmt.Errorf("check %s: unsupported kind %q", x.ID, x.Kind)
	}
	if (x.Kind == "tailscale" || x.Kind == "wireguard") && (x.BindIP != "" || x.ResolveIP != "" || x.Target != "") {
		return fmt.Errorf("check %s: local state adapters do not execute dial/bind/target overrides", x.ID)
	}
	if len(x.HTTP.RequestHeaderEnvs) > 0 && x.Kind != "http" {
		return fmt.Errorf("check %s: request_header_envs is supported only by http checks", x.ID)
	}
	if kind.validate != nil {
		if err := kind.validate(c, x); err != nil {
			return err
		}
	}
	if x.Path != "" && len(Path(c, x.Path).Proxies) > 0 && !kind.streamProxy {
		return fmt.Errorf("check %s: this probe cannot traverse stream proxy chains", x.ID)
	}
	return nil
}
