// Package examples embeds reviewed starter topologies in the single topo binary.
package examples

import "embed"

// Topologies excludes credentials and monitor output configuration.
//
//go:embed minimal.json homelab.json host-resources.json cloudflare.json beszel.json komari.json cf-server-monitor.json applications.json products.json address-registry.json mihomo.json cloudflare-diagnostics.json mihomo-diagnostics.json
var Topologies embed.FS
