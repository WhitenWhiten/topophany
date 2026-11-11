// Package monitor projects evidence into bounded, versioned display snapshots.
// It never schedules probes or changes evidence persistence.
package monitor

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/config"
)

type Template struct {
	Schema int    `json:"schema"`
	Title  string `json:"title"`
	Icon   string `json:"icon,omitempty"`
	Bar    struct {
		Row string `json:"row"`
	} `json:"bar"`
	Rows []Row `json:"rows"`
}
type Row struct {
	History *bool    `json:"history,omitempty"`
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Metric  string   `json:"metric"`
	Select  Selector `json:"select,omitempty"`
	Format  string   `json:"format,omitempty"`
	Visual  string   `json:"visual"`
	Limit   *int     `json:"limit,omitempty"`
}
type Selector struct {
	Link      string `json:"link,omitempty"`
	Node      string `json:"node,omitempty"`
	Service   string `json:"service,omitempty"`
	Check     string `json:"check,omitempty"`
	Collector string `json:"collector,omitempty"`
}
type Metric struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Unit     string   `json:"unit,omitempty"`
	Sources  []string `json:"sources"`
	Selector string   `json:"selector,omitempty"`
	Visual   string   `json:"visual"`
	Fact     string   `json:"-"`
}

func Catalog() []Metric {
	all := []string{"client", "collector", "worker"}
	catalog := []Metric{
		{Name: "sources.status", Type: "object", Sources: []string{"direct"}, Visual: "status"},
		{Name: "summary.verdict", Type: "object", Sources: []string{"client", "collector"}, Visual: "status"},
		{Name: "observers.verdicts", Type: "array", Sources: []string{"client", "collector"}, Visual: "list"},
		{Name: "attention.list", Type: "object", Sources: []string{"client", "collector"}, Visual: "list"},
		{Name: "applications.list", Type: "array", Sources: []string{"client", "collector"}, Visual: "list"},
		{Name: "server.card", Type: "object", Sources: []string{"client", "collector"}, Selector: "node", Visual: "card"},
		{Name: "servers.list", Type: "array", Sources: []string{"client", "collector"}, Visual: "list"},
		{Name: "service.card", Type: "object", Sources: []string{"client", "collector"}, Selector: "service", Visual: "card"},
		{Name: "check.card", Type: "object", Sources: []string{"client", "collector"}, Selector: "check", Visual: "card"},
		{Name: "checks.list", Type: "array", Sources: []string{"client", "collector"}, Visual: "list"},
		{Name: "link.card", Type: "object", Sources: []string{"client", "collector"}, Selector: "link", Visual: "card"},
		{Name: "links.list", Type: "array", Sources: []string{"client", "collector"}, Visual: "list"},
		{Name: "services.list", Type: "array", Sources: []string{"client", "collector"}, Visual: "list"},
		{Name: "summary.status", Type: "text", Sources: []string{"client", "collector"}},
		{Name: "local.status", Type: "text", Sources: []string{"worker"}},
		{Name: "check.status", Type: "text", Sources: all, Selector: "check"},
		{Name: "host.cpu.used_percent", Type: "number", Unit: "percent", Sources: all, Selector: "check", Fact: "cpu_used_percent"},
		{Name: "host.memory.used_percent", Type: "number", Unit: "percent", Sources: all, Selector: "check", Fact: "memory_used_percent"},
		{Name: "host.disk.used_percent", Type: "number", Unit: "percent", Sources: all, Selector: "check", Fact: "disk_used_percent"},
		{Name: "resource.cpu.used_percent", Type: "number", Unit: "percent", Sources: all, Selector: "node", Fact: "cpu_used_percent"},
		{Name: "resource.memory.used_percent", Type: "number", Unit: "percent", Sources: all, Selector: "node", Fact: "memory_used_percent"},
		{Name: "resource.disk.used_percent", Type: "number", Unit: "percent", Sources: all, Selector: "node", Fact: "disk_used_percent"},
		{Name: "delivery.status", Type: "text", Sources: []string{"worker"}, Selector: "collector"},
		{Name: "delivery.pending_records", Type: "number", Unit: "count", Sources: []string{"worker"}, Selector: "collector"},
		{Name: "delivery.pending_bytes", Type: "number", Unit: "bytes", Sources: []string{"worker"}, Selector: "collector"},
		{Name: "storage.journal.write_bytes", Type: "number", Unit: "bytes", Sources: []string{"collector"}},
		{Name: "storage.checkpoint.write_bytes", Type: "number", Unit: "bytes", Sources: []string{"collector"}},
		{Name: "storage.queue.operations", Type: "number", Unit: "count", Sources: []string{"worker"}},
		{Name: "storage.queue.write_bytes", Type: "number", Unit: "bytes", Sources: []string{"worker"}},
		{Name: "storage.sync.count", Type: "number", Unit: "count", Sources: []string{"collector", "worker"}},
		{Name: "storage.sync.duration_ms", Type: "number", Unit: "milliseconds", Sources: []string{"collector", "worker"}},
	}
	for _, m := range []struct{ name, unit, fact string }{
		{"network.rx_bytes_per_second", "bytes_per_second", "network_rx_bytes_per_second"},
		{"network.tx_bytes_per_second", "bytes_per_second", "network_tx_bytes_per_second"},
		{"swap.total_bytes", "bytes", "swap_total_bytes"},
		{"swap.used_bytes", "bytes", "swap_used_bytes"},
		{"load.avg_1m", "", "load_avg_1m"},
		{"load.avg_5m", "", "load_avg_5m"},
		{"load.avg_15m", "", "load_avg_15m"},
	} {
		catalog = append(catalog,
			Metric{Name: "host." + m.name, Type: "number", Unit: m.unit, Sources: all, Selector: "check", Fact: m.fact},
			Metric{Name: "resource." + m.name, Type: "number", Unit: m.unit, Sources: all, Selector: "node", Fact: m.fact},
		)
	}
	for i := range catalog {
		if slices.Contains(catalog[i].Sources, "client") {
			catalog[i].Sources = append(catalog[i].Sources, "preview")
		}
		if catalog[i].Name == "server.card" || catalog[i].Name == "servers.list" || strings.HasPrefix(catalog[i].Name, "resource.") {
			catalog[i].Sources = append(catalog[i].Sources, "direct")
		}
		// This machine's own checks have no topology beyond the checks.
		if catalog[i].Name == "check.card" || catalog[i].Name == "checks.list" {
			catalog[i].Sources = append(catalog[i].Sources, "local")
		}
		if catalog[i].Visual == "" {
			catalog[i].Visual = "text"
			if catalog[i].Unit == "percent" {
				catalog[i].Visual = "bar"
			}
		}
	}
	return catalog
}
func metric(name string) (Metric, bool) {
	for _, m := range Catalog() {
		if m.Name == name {
			return m, true
		}
	}
	return Metric{}, false
}
func (t Template) Validate(source string) error {
	if source == "preview" {
		source = "client"
	}
	if !slices.Contains([]string{"client", "collector", "worker", "direct", "local"}, source) {
		return errors.New("unknown monitor source")
	}
	if t.Schema != 1 || len(t.Title) == 0 || len(t.Title) > 256 || len(t.Icon) > 64 || len(t.Rows) == 0 || len(t.Rows) > 128 {
		return errors.New("invalid template schema, title, icon or row count (1..128)")
	}
	ids := map[string]bool{}
	for _, r := range t.Rows {
		if !config.ID(r.ID) || ids[r.ID] || len(r.Label) > 256 {
			return fmt.Errorf("row %s: invalid or duplicate row ID/label", r.ID)
		}
		ids[r.ID] = true
		m, ok := metric(r.Metric)
		if !ok || !slices.Contains(m.Sources, source) {
			return fmt.Errorf("row %s: metric %s unsupported by %s", r.ID, r.Metric, source)
		}
		if m.Visual == "status" || m.Visual == "list" || m.Visual == "card" {
			if r.Visual != m.Visual {
				return fmt.Errorf("row %s: visual must be %s", r.ID, m.Visual)
			}
		} else if r.Visual != "text" && !(r.Visual == "bar" && m.Unit == "percent") {
			return fmt.Errorf("row %s: incompatible visual", r.ID)
		}
		if r.Limit != nil && (r.Metric != "attention.list" || *r.Limit < 1 || *r.Limit > 20) {
			return fmt.Errorf("row %s: limit must be 1..20 and is only for attention.list", r.ID)
		}
		if r.History != nil && r.Metric != "service.card" && r.Metric != "check.card" && r.Metric != "link.card" {
			return fmt.Errorf("row %s: history is only for service, check or link cards", r.ID)
		}
		if m.Selector == "node" && !config.ID(r.Select.Node) || r.Select.Node != "" && m.Selector != "node" {
			return fmt.Errorf("row %s: invalid or missing node selector", r.ID)
		}
		if m.Selector == "service" && !config.ID(r.Select.Service) || r.Select.Service != "" && m.Selector != "service" {
			return fmt.Errorf("row %s: invalid or missing service selector", r.ID)
		}
		if m.Selector == "link" && !config.ID(r.Select.Link) || r.Select.Link != "" && m.Selector != "link" {
			return fmt.Errorf("row %s: invalid or missing link selector", r.ID)
		}
		if r.Format != "" && r.Format != m.Unit {
			return fmt.Errorf("row %s: incompatible format", r.ID)
		}
		if r.Metric == "check.card" && !config.ID(r.Select.Check) {
			return fmt.Errorf("row %s: check.card requires a check selector", r.ID)
		}
		if r.Select.Check != "" && (!config.ID(r.Select.Check) || m.Selector != "check") {
			return fmt.Errorf("row %s: invalid check selector", r.ID)
		}
		if r.Select.Collector != "" && (len(r.Select.Collector) != 16 || strings.Trim(r.Select.Collector, "0123456789abcdef") != "" || m.Selector != "collector") {
			return fmt.Errorf("row %s: invalid collector selector", r.ID)
		}
	}
	if !ids[t.Bar.Row] {
		return errors.New("bar.row must reference a template row")
	}
	return nil
}

// DefaultDirectTemplate names acquisition state and resource-bearing servers.
func DefaultDirectTemplate() Template {
	t := Template{Schema: 1, Title: "Topophany", Rows: []Row{{ID: "status", Metric: "sources.status", Visual: "status"}, {ID: "servers", Metric: "servers.list", Visual: "list"}}}
	t.Bar.Row = "status"
	return t
}
