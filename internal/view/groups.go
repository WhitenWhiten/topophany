package view

import (
	"fmt"
	"sort"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Scope is organizational metadata, never a claim about measurement or routing.
type Scope struct{ Application, Environment, Service, Node, Observer, Path string }
type GroupOptions struct {
	Collapsed                                     bool
	Application, Environment, Service, By, Search string
	Attention                                     bool
	Expanded                                      map[string]bool
}

// ScopeLabel is an application with its environment, "Shop / production",
// or the application alone where no environment is given.
func ScopeLabel(app, env string) string {
	if env == "" {
		return app
	}
	return app + " / " + env
}

// DeclaresEnvironments reports whether any service or check names an
// environment. Where none does, a group is named by its application alone:
// "Unspecified" on every group would say nothing.
func DeclaresEnvironments(c model.Config) bool {
	for _, s := range c.Services {
		if s.Environment != "" {
			return true
		}
	}
	for _, x := range c.Checks {
		if x.Environment != "" {
			return true
		}
	}
	return false
}

func ApplicationName(c model.Config, id string) string {
	if id == "" {
		return "Ungrouped"
	}
	for _, a := range c.Applications {
		if a.ID == id && a.Name != "" {
			return a.Name
		}
	}
	return id
}
func EntityScope(c model.Config, kind, id string) Scope {
	var scope Scope
	if kind == "check" {
		for _, x := range c.Checks {
			if x.ID == id {
				scope = Scope{Application: x.Application, Environment: x.Environment, Service: x.Service, Observer: x.Observer, Path: x.Path}
				break
			}
		}
		if scope.Service == "" {
			return scope
		}
		id = scope.Service
	}
	for _, s := range c.Services {
		if s.ID == id {
			scope.Application = s.Application
			scope.Environment = s.Environment
			scope.Service = s.ID
			scope.Node = s.Node
			break
		}
	}
	return scope
}

// Bulk list derivation shares these definitions across all rows. EntityScope
// remains useful for a single selection; calling it per row rescans the config.
type scopeIndex struct {
	checks, services                     map[string]Scope
	apps, nodes, observers, serviceNames map[string]string
}

func indexScopes(c model.Config) *scopeIndex {
	x := &scopeIndex{checks: map[string]Scope{}, services: map[string]Scope{}, apps: map[string]string{}, nodes: map[string]string{}, observers: map[string]string{}, serviceNames: map[string]string{}}
	for _, d := range c.Checks {
		x.checks[d.ID] = Scope{Application: d.Application, Environment: d.Environment, Service: d.Service, Observer: d.Observer, Path: d.Path}
	}
	for _, d := range c.Services {
		x.services[d.ID] = Scope{Application: d.Application, Environment: d.Environment, Service: d.ID, Node: d.Node}
		x.serviceNames[d.ID] = d.Name
	}
	for _, d := range c.Applications {
		x.apps[d.ID] = d.Name
	}
	for _, d := range c.Nodes {
		x.nodes[d.ID] = d.Name
	}
	for _, d := range c.Observers {
		x.observers[d.ID] = d.Name
	}
	return x
}

func (x *scopeIndex) scope(kind, id string) Scope {
	var out Scope
	if kind == "check" {
		out = x.checks[id]
		if out.Service == "" {
			return out
		}
		id = out.Service
	}
	if s, ok := x.services[id]; ok {
		out.Application, out.Environment, out.Service, out.Node = s.Application, s.Environment, s.Service, s.Node
	}
	return out
}

func indexedName(names map[string]string, id string) string {
	if name := names[id]; name != "" {
		return name
	}
	return id
}

func (x *scopeIndex) application(id string) string {
	if id == "" {
		return "Ungrouped"
	}
	return indexedName(x.apps, id)
}

// GroupedList uses the same evidence rows as CLI output. Group summaries describe
// members, not business availability. Environments never share a summary.
func (v View) GroupedList(tab int, o GroupOptions) []Row {
	c := v.Snapshot.Topology
	defs := indexScopes(c)
	rows := v.list(tab, "", defs)
	staleChecks := map[string]bool{}
	pausedChecks := map[string]bool{}
	staleServices, pausedServices := map[string]bool{}, map[string]bool{}
	checkServices := map[string]string{}
	for _, x := range c.Checks {
		checkServices[x.ID] = x.Service
	}
	for _, x := range v.Snapshot.Checks {
		staleChecks[x.ID] = x.Freshness == "stale"
		pausedChecks[x.ID] = x.Freshness == "paused" || x.Freshness == "maintenance"
		if staleChecks[x.ID] {
			staleServices[checkServices[x.ID]] = true
		}
		if pausedChecks[x.ID] {
			pausedServices[checkServices[x.ID]] = true
		}
	}
	type bucket struct {
		id, title string
		rows      []Row
	}
	buckets := map[string]*bucket{}
	order := []string{}
	flat := []Row{}
	envs := DeclaresEnvironments(c)
	for _, r := range rows {
		s := defs.scope(r.Kind, r.ID)
		if !scopeMatches(o.Application, s.Application) || !scopeMatches(o.Environment, s.Environment) || o.Service != "" && s.Service != o.Service {
			continue
		}
		app := defs.application(s.Application)
		env := s.Environment
		if env == "" {
			env = "Unspecified"
		}
		key, label := s.Application, app
		switch o.By {
		case "environment":
			key, label = s.Environment, env
		case "service":
			key = s.Service
			label = indexedName(defs.serviceNames, key)
			if key == "" {
				label = "Infrastructure checks"
			}
		case "host":
			key = s.Node
			label = indexedName(defs.nodes, key)
			if key == "" {
				label = "Unassigned"
			}
		case "observer":
			key = s.Observer
			label = indexedName(defs.observers, key)
			if key == "" {
				label = "Unassigned"
			}
		case "path":
			key = s.Path
			label = s.Path
			if key == "" {
				label = "No declared path"
			}
		}
		if o.Attention && !NeedsAttention(r) && r.Health != "unknown" && r.Note != "outdated" {
			continue
		}
		hay := strings.ToLower(r.ID + " " + r.Title + " " + r.Meta + " " + strings.Join(r.Cols, " ") + " " + app + " " + s.Application + " " + env + " " + label)
		if !strings.Contains(hay, strings.ToLower(o.Search)) {
			continue
		}
		if o.By == "flat" {
			flat = append(flat, r)
			continue
		}
		id := fmt.Sprintf("%d/%s/%s/%s", tab, o.By, key, s.Environment)
		if buckets[id] == nil {
			title := label
			if envs && o.By != "environment" {
				title = label + " / " + env
			}
			buckets[id] = &bucket{id: id, title: title}
			order = append(order, id)
		}
		buckets[id].rows = append(buckets[id].rows, r)
	}
	if o.By == "flat" {
		return flat
	}
	sort.SliceStable(order, func(i, j int) bool { return buckets[order[i]].title < buckets[order[j]].title })
	out := []Row{}
	for _, id := range order {
		b := buckets[id]
		problems, unknown, stale, paused := 0, 0, 0, 0
		details := []string{"Member evidence only; not business availability.", "Observer and path failures do not prove an application outage.", ""}
		for _, r := range b.rows {
			if r.Health == "down" || r.Health == "degraded" || r.Note == "affected" {
				problems++
			}
			if r.Health == "unknown" {
				unknown++
			}
			if r.Note == "outdated" || r.Note == "cached" || r.Kind == "check" && staleChecks[r.ID] || r.Kind == "service" && staleServices[r.ID] {
				stale++
			}
			if r.Note == "paused" || r.Note == "maintenance" || r.Kind == "service" && pausedServices[r.ID] {
				paused++
			}
			details = append(details, r.Title+": "+HealthWord(r.Health))
		}
		if evidence := v.groupEvidence(b.rows); len(evidence) > 0 {
			details = append(append(evidence, ""), details...)
		}
		summary := fmt.Sprintf("%s · %d attention · %d unknown · %d stale · %d paused", Plural(len(b.rows), "member"), problems, unknown, stale, paused)
		health := "up"
		if unknown > 0 || stale > 0 || paused > 0 {
			health = "unknown"
		}
		if problems > 0 {
			health = "degraded"
		}
		tree := "▶ "
		expanded, known := o.Expanded[id]
		if !known {
			expanded = !o.Collapsed
		}
		expanded = expanded || o.Search != "" || o.Attention
		if expanded {
			tree = "▼ "
		}
		compact := Plural(len(b.rows), "member")
		if problems > 0 {
			compact += fmt.Sprintf(" · %d attention", problems)
		}
		if unknown > 0 {
			compact += fmt.Sprintf(" · %d unknown", unknown)
		}
		if stale > 0 {
			compact += fmt.Sprintf(" · %d stale", stale)
		}
		if paused > 0 {
			compact += fmt.Sprintf(" · %d paused", paused)
		}
		group := Row{Kind: "group", ID: id, Title: b.title, Tree: tree, Health: health, Cols: []string{compact}, Details: append([]string{summary, ""}, details...)}
		if v.Cached {
			group.Health = "unknown"
			group.Note = "cached"
		}
		out = append(out, group)
		if expanded {
			for _, r := range b.rows {
				r.Tree = "  "
				out = append(out, r)
			}
		}
	}
	return out
}

// "-" selects the missing value; it cannot be a configured identifier.
func scopeMatches(filter, value string) bool {
	return filter == "" || filter == value || filter == "-" && value == ""
}
