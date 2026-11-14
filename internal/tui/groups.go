package tui

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
	"sort"
	"strings"
)

func (m *Model) businessTab() bool { return m.Tab == tabServices || m.Tab == tabChecks }
func (m *Model) groupMode() string {
	if mode := m.GroupBy[m.Tab]; mode != "" {
		return mode
	}
	// Existing unclassified topologies retain their compact list.
	if len(m.Snapshot.Topology.Applications) == 0 {
		return "flat"
	}
	if m.Tab == tabChecks {
		return "service"
	}
	return "application"
}
func (m *Model) resetBusinessList() { m.Selected = 0; m.Offset = 0; m.Detail = false }
func (m *Model) toggleGroup() bool {
	r, ok := m.selectedRow()
	if !ok || r.Kind != "group" {
		return false
	}
	if m.Search != "" || m.AttentionOnly {
		m.say("Matching groups stay expanded; clear search and attention-only to fold", false)
		return true
	}
	if m.Expanded == nil {
		m.Expanded = map[string]bool{}
	}
	expanded, known := m.Expanded[r.ID]
	if !known {
		expanded = !m.GroupsCollapsed
	}
	m.Expanded[r.ID] = !expanded
	return true
}
func (m *Model) businessStatus() string {
	text := ""
	for i, p := range m.browseParts() {
		if i > 0 {
			text += p.sep
		}
		text += p.text
	}
	return text
}

// browsePart is one thing the business list is browsed by, and whether it
// is chosen rather than left at everything.
type browsePart struct {
	text, sep string
	chosen    bool
}

// browseParts are what the business list is browsed by: the application
// and environment, how it is grouped, the service and attention filters.
func (m *Model) browseParts() []browsePart {
	app := browsePart{text: "All applications"}
	if m.Application == "-" {
		app = browsePart{text: "Ungrouped", chosen: true}
	} else if m.Application != "" {
		app = browsePart{text: view.ApplicationName(m.Snapshot.Topology, m.Application), chosen: true}
	}
	parts := []browsePart{app}
	// Without environments declared there is none to choose between.
	if m.Environment != "" || view.DeclaresEnvironments(m.Snapshot.Topology) {
		env := browsePart{text: m.Environment, sep: " / ", chosen: m.Environment != ""}
		switch m.Environment {
		case "":
			env.text = "All environments"
		case "-":
			env.text = "Unspecified"
		}
		parts = append(parts, env)
	}
	by := m.groupMode()
	if by != "flat" {
		by = "by " + by
	}
	parts = append(parts, browsePart{text: by, sep: " · ", chosen: m.GroupBy[m.Tab] != ""})
	if m.ServiceFilter != "" {
		parts = append(parts, browsePart{text: m.ServiceFilter, sep: " · ", chosen: true})
	}
	if m.AttentionOnly {
		parts = append(parts, browsePart{text: "attention only", sep: " · ", chosen: true})
	}
	return parts
}

func (m *Model) choiceMenu(title string, labels []string, pick func(int)) {
	items := make([]menuItem, len(labels))
	for i, label := range labels {
		items[i] = menuItem{label: label}
	}
	m.modal = &modal{kind: "menu", title: title, items: items, pick: func(i int) string { m.resetBusinessList(); pick(i); return "" }}
}
func (m *Model) businessMenu() {
	labels := []string{"Application…", "Environment…", "Group by…", "Toggle attention only", "Clear all filters", "Expand all groups", "Collapse all groups", "Shared dependencies…"}
	if m.Edit != nil {
		labels = append(labels, "Edit application…", "Delete application…")
	}
	m.choiceMenu("Browse · "+m.businessStatus(), labels, func(i int) {
		switch i {
		case 0:
			ids := []string{"", "-"}
			labels := []string{"All applications", "Ungrouped"}
			for _, a := range m.Snapshot.Topology.Applications {
				ids = append(ids, a.ID)
				labels = append(labels, view.ApplicationName(m.Snapshot.Topology, a.ID))
			}
			m.choiceMenu("Application", labels, func(i int) { m.Application = ids[i]; m.ServiceFilter = "" })
		case 1:
			seen := map[string]bool{}
			for _, s := range m.Snapshot.Topology.Services {
				if s.Environment != "" {
					seen[s.Environment] = true
				}
			}
			for _, x := range m.Snapshot.Topology.Checks {
				if x.Environment != "" {
					seen[x.Environment] = true
				}
			}
			envs := []string{}
			for env := range seen {
				envs = append(envs, env)
			}
			sort.Strings(envs)
			envs = append([]string{"", "-"}, envs...)
			labels := append([]string{"All environments", "Unspecified"}, envs[2:]...)
			m.choiceMenu("Environment", labels, func(i int) { m.Environment = envs[i]; m.ServiceFilter = "" })
		case 2:
			modes := []string{"application", "host", "flat"}
			if m.Tab == tabChecks {
				modes = []string{"service", "application", "observer", "path", "flat"}
			}
			m.choiceMenu("Group by", modes, func(i int) {
				if m.GroupBy == nil {
					m.GroupBy = map[int]string{}
				}
				m.GroupBy[m.Tab] = modes[i]
			})
		case 3:
			m.AttentionOnly = !m.AttentionOnly
		case 4:
			m.Application = ""
			m.Environment = ""
			m.ServiceFilter = ""
			m.Search = ""
			m.AttentionOnly = false
		case 8, 9:
			deleting := i == 9
			ids, labels := []string{}, []string{}
			for _, a := range m.Edit.Config.Applications {
				ids = append(ids, a.ID)
				labels = append(labels, view.ApplicationName(m.Edit.Config, a.ID))
			}
			if len(ids) == 0 {
				m.info("Applications", "Press a to add an application.")
				return
			}
			m.choiceMenu("Choose application", labels, func(i int) {
				if !deleting {
					m.openForm("application", ids[i])
					return
				}
				id := ids[i]
				m.confirm("Delete application "+id+"?", []string{"Referenced applications cannot be deleted.", "y delete · Esc keep"}, func() string {
					err := m.Edit.Apply(func(c *model.Config) error { return remove(c, "application", id) })
					if err != nil {
						m.say(err.Error(), true)
					} else {
						m.rebuild()
						if m.Application == id {
							m.Application = ""
						}
						m.say("Application deleted · u undo · ^S save", false)
					}
					return ""
				})
			})
		case 7:
			m.dependencyMenu()
		case 5, 6:
			if m.Expanded == nil {
				m.Expanded = map[string]bool{}
			}
			for _, r := range m.Rows() {
				if r.Kind == "group" {
					m.Expanded[r.ID] = i == 5
				}
			}
		}
	})
}
func (m *Model) serviceChecks() {
	r, ok := m.selectedRow()
	if !ok {
		return
	}
	if r.Kind == "group" && m.Tab == tabServices && m.groupMode() == "application" {
		parts := strings.Split(r.ID, "/")
		if len(parts) == 4 {
			m.Application = parts[2]
			if m.Application == "" {
				m.Application = "-"
			}
			m.Environment = parts[3]
			if m.Environment == "" {
				m.Environment = "-"
			}
			m.ServiceFilter = ""
			m.Search = ""
			m.setTab(tabChecks)
		}
		return
	}
	if r.Kind != "service" {
		return
	}
	s := view.EntityScope(m.Snapshot.Topology, r.Kind, r.ID)
	m.Application = s.Application
	m.Environment = s.Environment
	m.ServiceFilter = s.Service
	m.Search = ""
	m.setTab(tabChecks)
	if m.GroupBy == nil {
		m.GroupBy = map[int]string{}
	}
	m.GroupBy[tabChecks] = "flat"
}

// A direct entity link must reveal its target even if its group was collapsed.
func (m *Model) revealBusinessEntity(kind, id string) {
	if kind != "service" && kind != "check" {
		return
	}
	s := view.EntityScope(m.Snapshot.Topology, kind, id)
	m.Application = s.Application
	m.Environment = s.Environment
	m.ServiceFilter = ""
	m.AttentionOnly = false
	if m.Expanded == nil {
		m.Expanded = map[string]bool{}
	}
	// Search temporarily expands every matching group without altering remembered folds.
	old := m.Search
	m.Search = id
	for _, r := range m.Rows() {
		if r.Kind == "group" {
			m.Expanded[r.ID] = true
		}
	}
	m.Search = old
}

func (m *Model) dependencyMenu() {
	c := m.Snapshot.Topology
	ids, labels := []string{}, []string{}
	seen := map[string]bool{}
	for _, s := range c.Services {
		if !businessScopeMatches(m.Application, s.Application) || !businessScopeMatches(m.Environment, s.Environment) {
			continue
		}
		for _, id := range s.DependsOn {
			for _, d := range c.Services {
				if d.ID != id || d.Application == s.Application || seen[id] {
					continue
				}
				seen[id] = true
				ids = append(ids, id)
				labels = append(labels, d.Name+" · "+view.ScopeLabel(view.ApplicationName(c, d.Application), d.Environment))
			}
		}
	}
	if len(ids) == 0 {
		m.info("Shared dependencies", "No shared dependencies in this application scope.")
		return
	}
	m.choiceMenu("Shared dependencies · jump to service", labels, func(i int) { m.selectEntity("service", ids[i]) })
}

func businessScopeMatches(filter, value string) bool {
	return filter == "" || filter == value || filter == "-" && value == ""
}
