package view

import (
	"fmt"
	"sort"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// A rowBuilder shares one set of indexes across the tabs contributing to a list.
// It belongs to one derivation; changing evidence or presentation never reuses it.
type rowBuilder struct {
	assessments       map[string]ServiceAssessment
	view              View
	nodeNames         map[string]string
	endpointsByNode   map[string][]model.Endpoint
	frontsByNode      map[string][]string
	checkDefs         map[string]model.Check
	serviceDefs       map[string]model.Service
	endpointDefs      map[string]model.Endpoint
	endpointOrder     map[string]int
	pathDefs          map[string]model.Path
	observerDefs      map[string]model.Observer
	storageByObserver map[string][]int
	observerNames     map[string]string
	nodeStates        map[string]model.EntityState
	endpointStates    map[string]model.EntityState
	linkStates        map[string]model.EntityState
	resources         map[string][]CardResource
	opened            map[string]*model.IncidentRecord
	policy            *policyIndex // built when an incident first needs it
}

func newRowBuilder(v View, tab int) *rowBuilder {
	s := v.Snapshot
	nodeNames := map[string]string{}
	for _, n := range s.Topology.Nodes {
		nodeNames[n.ID] = n.Name
		if n.Name == "" {
			nodeNames[n.ID] = n.ID
		}
	}
	// Traverse ownership and Via references once. Each node then visits only
	// its own endpoints and the endpoints that actually name it as a front.
	endpointsByNode := map[string][]model.Endpoint{}
	frontsByNode := map[string][]string{}
	if tab == Topology || tab == Servers || tab == Overview {
		for _, ep := range s.Topology.Endpoints {
			endpointsByNode[ep.Node] = append(endpointsByNode[ep.Node], ep)
			seen := map[string]bool{}
			for _, id := range ep.Via {
				if !seen[id] {
					frontsByNode[id] = append(frontsByNode[id], ep.ID+" → "+nodeNames[ep.Node])
					seen[id] = true
				}
			}
		}
	}
	checkDefs := map[string]model.Check{}
	if tab == Checks || tab == Overview || tab == Observers {
		for _, c := range s.Topology.Checks {
			checkDefs[c.ID] = c
		}
	}
	serviceDefs := map[string]model.Service{}
	if tab == Services || tab == Overview {
		for _, d := range s.Topology.Services {
			serviceDefs[d.ID] = d
		}
	}
	endpointDefs := map[string]model.Endpoint{}
	endpointOrder := map[string]int{}
	if tab == Services || tab == Overview {
		for i, ep := range s.Topology.Endpoints {
			endpointDefs[ep.ID], endpointOrder[ep.ID] = ep, i
		}
	}
	pathDefs := map[string]model.Path{}
	if tab == Paths || tab == Overview {
		for _, d := range s.Topology.Paths {
			pathDefs[d.ID] = d
		}
	}
	observerDefs := map[string]model.Observer{}
	storageByObserver := map[string][]int{}
	if (tab == Observers || tab == Overview) && s.Storage != nil {
		for i, u := range s.Storage.Observers {
			storageByObserver[u.Observer] = append(storageByObserver[u.Observer], i)
		}
	}
	observerNames := map[string]string{}
	for _, o := range s.Topology.Observers {
		observerDefs[o.ID] = o
		observerNames[o.ID] = o.Name
		if o.Name == "" {
			observerNames[o.ID] = o.ID
		}
	}
	nodeStates, endpointStates, linkStates := indexEntityStates(s.Nodes), indexEntityStates(s.Endpoints), indexEntityStates(s.Links)
	resources := map[string][]CardResource{}
	if tab == Servers || tab == Topology || tab == Overview {
		resources = v.ResourceCards()
	}
	opened := map[string]*model.IncidentRecord{}
	for k, r := range s.Ledger {
		if r.State == "open" {
			opened[r.ID] = &s.Ledger[k]
		}
	}
	var assessments map[string]ServiceAssessment
	if tab == Services || tab == Overview {
		assessments = serviceAssessments(s, v.Cached)
	}
	return &rowBuilder{assessments: assessments,
		view:              v,
		nodeNames:         nodeNames,
		endpointsByNode:   endpointsByNode,
		frontsByNode:      frontsByNode,
		checkDefs:         checkDefs,
		serviceDefs:       serviceDefs,
		endpointDefs:      endpointDefs,
		endpointOrder:     endpointOrder,
		pathDefs:          pathDefs,
		observerDefs:      observerDefs,
		storageByObserver: storageByObserver,
		observerNames:     observerNames,
		nodeStates:        nodeStates,
		endpointStates:    endpointStates,
		linkStates:        linkStates,
		resources:         resources,
		opened:            opened,
	}
}

func (rb *rowBuilder) serviceFronts(ids []string) []string {
	// Preserve topology order without rescanning all endpoints per service.
	ordered := append([]string{}, ids...)
	sort.Slice(ordered, func(i, j int) bool { return rb.endpointOrder[ordered[i]] < rb.endpointOrder[ordered[j]] })
	out, seen := []string{}, map[string]bool{}
	for _, id := range ordered {
		for _, via := range rb.endpointDefs[id].Via {
			if !seen[via] {
				out = append(out, rb.nodeNames[via])
				seen[via] = true
			}
		}
	}
	return out
}

func entityNeedsAttention(kind string, e model.EntityState) bool {
	health, note := entityPresentation(e)
	return NeedsAttention(Row{Kind: kind, Health: health, Note: note})
}

func entityRow(kind string, e model.EntityState, title, meta string, details []string) Row {
	r := Row{Kind: kind, ID: e.ID, Health: e.Health, Title: title, Meta: meta, Details: append([]string{"ID: " + e.ID, "State: " + e.Health, "Evidence: " + e.Evidence, "Affected dependency: " + fmt.Sprint(e.Affected), "", e.Reason, "Checks: " + strings.Join(e.Checks, ", ")}, details...)}
	r.Health, r.Note = entityPresentation(e)
	return r
}

func indexEntityStates(list []model.EntityState) map[string]model.EntityState {
	out := make(map[string]model.EntityState, len(list))
	for _, e := range list {
		out[e.ID] = e
	}
	return out
}

func findEntityState(list map[string]model.EntityState, id string) model.EntityState {
	if e, ok := list[id]; ok {
		return e
	}
	return model.EntityState{ID: id, Health: "unknown", Evidence: "unknown", Reason: "no state in this view"}
}

func (rb *rowBuilder) nodeRow(n model.Node) Row {
	v := rb.view
	place := "Location: not declared"
	switch l := n.Location; {
	case l != nil:
		place = fmt.Sprintf("Location: %s (%.2f, %.2f) — declared, not inferred from IP", l.Label, l.Lat, l.Lon)
	case n.Kind == "edge":
		place = "Location: anycast edge — answers from many places"
	}
	details := []string{"", "Node identity: " + n.ID, "Group: " + n.Group, "Kind: " + n.Kind, "Provider: " + ProviderName(n.Provider), "Parent: " + n.Parent, place, "", n.Notes}
	for _, r := range rb.resources[n.ID] {
		heading := "Local resources: "
		if r.External {
			heading = "Reported resources: "
		}
		details = append(details, "", heading+r.Observer.Name+" · "+r.Health+" · "+r.Freshness, "Resource check: "+r.Check, "Source: "+r.Source, "Sampled: "+Stamp(v.Now, r.SampledAt))
		if !r.ValidUntil.IsZero() {
			details = append(details, "Valid until: "+Stamp(v.Now, r.ValidUntil))
		}
		if r.Reason != "" {
			details = append(details, "Selection: "+r.Reason)
		}
		for _, pair := range ResourceValues(r) {
			details = append(details, pair[0]+": "+pair[1])
		}
	}
	details = append(details, "", "Access endpoints:")
	for _, ep := range rb.endpointsByNode[n.ID] {
		details = append(details, ep.ID+" ["+ep.Network+"] "+ep.Address+via(ep))
	}
	fronts := rb.frontsByNode[n.ID]
	if len(fronts) > 0 {
		details = append(details, "", "Answers first for:")
		details = append(details, fronts...)
	}
	return entityRow("node", findEntityState(rb.nodeStates, n.ID), rb.nodeNames[n.ID], n.Group, details)
}

func (rb *rowBuilder) incidentRow(i model.Incident) Row {
	v := rb.view
	if r := rb.opened[i.ID]; r != nil && r.Since.Before(i.Since) {
		i.Since = r.Since
	}
	i.Hypothesis = rb.incidentHypothesis(i)
	return Row{Kind: "incident", ID: i.ID, Health: "down", Title: v.IncidentTitle(i), Meta: i.Summary, Cols: []string{Age(v.Now, i.Since), Plural(len(i.Checks), "check")}, Details: IncidentDetails(v.Now, i, rb.opened[i.ID])}
}

func (rb *rowBuilder) rememberedRows(recovering bool) []Row {
	v := rb.view
	s := rb.view.Snapshot
	out := []Row{}
	for _, r := range s.Ledger {
		row := Row{Kind: "incident", ID: r.ID, Title: v.IncidentTitle(model.Incident{Scope: r.Scope}), Meta: r.Summary, Cols: []string{Age(v.Now, r.Since), Plural(len(r.Checks), "check")}, Details: RecordDetails(v.Now, r)}
		switch {
		case r.State == "recovering":
			row.Health, row.Note = "degraded", "recovering"
		case r.State == "closed" && !recovering:
			row.ID += "#" + itoa(r.Previous+1)
			row.Health, row.Note = "unknown", "closed"
			if r.Ended == "recovered" {
				row.Health = "up"
			}
			row.Meta = "closed · " + r.Ended
			row.Cols[0] = "ended " + Age(v.Now, r.Closed)
		default:
			continue
		}
		out = append(out, row)
	}
	return out
}
