//go:build webui

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
	"github.com/WhitenWhiten/topophany/internal/webmap"
)

// webOptions is empty in ordinary builds. The app wires a renderer adapter
// before serving, keeping the collector independent of the TUI package.
type webOptions struct {
	WebEnabled bool
	WebMap     func(model.Snapshot, string, view.Row, bool) webmap.Map
}

func (s *Server) registerWebUI(mux *http.ServeMux) {
	if !s.WebEnabled {
		return
	}
	mux.HandleFunc("GET /v1/web/view", s.authorize("viewer", s.webView))
	mux.HandleFunc("GET /v1/web/events", s.authorize("viewer", s.webEvents))
	mux.HandleFunc("GET /v1/web/history", s.authorize("viewer", s.webHistory))
	mux.HandleFunc("POST /v1/web/validate", s.authorize("viewer", s.webValidate))
}

type webProjection struct {
	Schema           int                         `json:"schema"`
	Project          string                      `json:"project"`
	Instance         string                      `json:"instance"`
	Version          string                      `json:"version"`
	Revision         string                      `json:"revision"`
	GeneratedAt      time.Time                   `json:"generated_at"`
	Observer         string                      `json:"observer"`
	Observers        []model.Observer            `json:"observers"`
	Tab              int                         `json:"tab"`
	Columns          []string                    `json:"columns"`
	Rows             []view.Row                  `json:"rows"`
	Total            int                         `json:"total"`
	Offset           int                         `json:"offset"`
	Verdict          view.Verdict                `json:"verdict"`
	Counts           view.Counts                 `json:"counts"`
	Live             int                         `json:"live"`
	ObserverCount    int                         `json:"observer_count"`
	ResourceValues   map[string][][2]string      `json:"resource_values,omitempty"`
	Applications     []view.Ref                  `json:"applications"`
	Environments     []string                    `json:"environments"`
	ServiceOptions   []view.Ref                  `json:"service_options"`
	Map              *webmap.Map                 `json:"map,omitempty"`
	Cards            map[string]view.Card        `json:"cards,omitempty"`
	Services         map[string]view.ServiceCard `json:"services,omitempty"`
	SummaryServices  []view.ApplicationGroup     `json:"summary_services,omitempty"`
	SummaryObservers []view.Row                  `json:"summary_observers,omitempty"`
	SummaryServers   []view.Row                  `json:"summary_servers,omitempty"`
	SummaryPaths     []view.Row                  `json:"summary_paths,omitempty"`
	TopologyCounts   map[string]int              `json:"topology_counts"`
	Detail           *view.Row                   `json:"detail,omitempty"`
	Checks           []view.Row                  `json:"related_checks,omitempty"`
	Routes           []model.Path                `json:"routes,omitempty"`
	Storage          *model.StorageUsage         `json:"storage,omitempty"`
	Ingress          *model.Ingress              `json:"ingress,omitempty"`
}

func webInt(r *http.Request, key string, def, lo, hi int) (int, bool) {
	q := r.URL.Query().Get(key)
	if q == "" {
		return def, true
	}
	n, e := strconv.Atoi(q)
	return n, e == nil && n >= lo && n <= hi
}
func (s *Server) projectWeb(r *http.Request, a authority) ([]byte, int, string) {
	q := r.URL.Query()
	if len(r.URL.RawQuery) > 4096 {
		return nil, 400, "query_too_large"
	}
	tab, ok := webInt(r, "tab", 0, 0, 8)
	if !ok {
		return nil, 400, "invalid_tab"
	}
	offset, ok := webInt(r, "offset", 0, 0, 1000000)
	if !ok {
		return nil, 400, "invalid_offset"
	}
	limit, ok := webInt(r, "limit", 100, 1, 200)
	if !ok {
		return nil, 400, "invalid_limit"
	}
	observer := q.Get("observer")
	if observer != "" {
		found := false
		for _, o := range a.gen.config.Observers {
			found = found || o.ID == observer
		}
		if !found {
			return nil, 400, "unknown_observer"
		}
	}
	by := q.Get("by")
	if by != "" && by != "application" && by != "environment" && by != "host" && by != "service" && by != "observer" && by != "path" && by != "flat" {
		return nil, 400, "invalid_grouping"
	}
	mode := q.Get("map")
	if mode == "" {
		mode = "virtual"
	}
	if mode != "virtual" && mode != "geo" && mode != "network" {
		return nil, 400, "invalid_map"
	}
	raw, status, code := s.cachedSnapshot(a.gen, observer, a.TokenSHA256)
	if status != 200 {
		return nil, status, code
	}
	var snap model.Snapshot
	if json.Unmarshal(raw, &snap) != nil {
		return nil, 503, "snapshot_unavailable"
	}
	v := view.View{Snapshot: snap, Now: s.Now(), Cached: view.Expired(snap, s.Now())}
	out := webProjection{Schema: 1, Project: snap.Project, Instance: snap.Instance, Version: snap.Version, Revision: snap.Revision, GeneratedAt: snap.GeneratedAt, Observer: observer, Observers: snap.Topology.Observers, Tab: tab, Columns: view.Columns(tab), Verdict: v.Verdict(), Counts: v.Counts(), Storage: snap.Storage, Ingress: snap.Ingress}
	out.Live, out.ObserverCount = v.Live()
	out.TopologyCounts = map[string]int{"nodes": len(snap.Topology.Nodes), "links": len(snap.Topology.Links), "checks": len(snap.Topology.Checks)}
	envs := map[string]bool{}
	for _, a := range snap.Topology.Applications {
		out.Applications = append(out.Applications, view.Ref{ID: a.ID, Name: a.Name})
	}
	for _, svc := range snap.Topology.Services {
		out.ServiceOptions = append(out.ServiceOptions, view.Ref{ID: svc.ID, Name: svc.Name})
		if !envs[svc.Environment] {
			out.Environments = append(out.Environments, svc.Environment)
			envs[svc.Environment] = true
		}
	}
	for _, x := range snap.Topology.Checks {
		if !envs[x.Environment] {
			out.Environments = append(out.Environments, x.Environment)
			envs[x.Environment] = true
		}
	}
	rows := []view.Row{}
	if tab < 8 {
		if tab == view.Services || tab == view.Checks {
			expanded := map[string]bool{}
			for _, id := range q["expanded"] {
				expanded[id] = true
			}
			for _, id := range q["folded"] {
				expanded[id] = false
			}
			rows = v.GroupedList(tab, view.GroupOptions{Collapsed: q.Get("collapsed") == "true", Expanded: expanded, By: by, Search: q.Get("search"), Application: q.Get("application"), Environment: q.Get("environment"), Service: q.Get("service"), Attention: q.Get("attention") == "true"})
		} else {
			for _, row := range v.List(tab, q.Get("search")) {
				if q.Get("attention") != "true" || view.NeedsAttention(row) {
					rows = append(rows, row)
				}
			}
		}
	}
	out.Total = len(rows)
	offset = min(offset, len(rows))
	out.Offset = offset
	out.Rows = rows[offset:min(len(rows), offset+limit)]
	selected := view.Row{Kind: q.Get("kind"), ID: q.Get("id")}
	// Detail selection can originate on a map, a summary or a dependency link.
	if selected.ID != "" {
		for t := 0; t < 8; t++ {
			for _, row := range v.List(t, "") {
				if row.ID == selected.ID && row.Kind == selected.Kind {
					copy := row
					out.Detail = &copy
					break
				}
			}
			if out.Detail != nil {
				break
			}
		}
		if out.Detail == nil {
			return nil, 404, "unknown_entity"
		}
	}
	if tab == view.Overview {
		out.SummaryServices = v.ApplicationGroups()
		out.SummaryObservers = v.List(view.Observers, "")
		out.SummaryServers = v.List(view.Servers, "")
		out.SummaryPaths = v.List(view.Paths, "")
	}
	if s.WebMap != nil && (tab == view.Overview || tab == view.Topology || tab == view.Paths || tab == 8) {
		if tab == 8 {
			mode = "globe"
		}
		graph := s.WebMap(snap, mode, selected, v.Cached)
		out.Map = &graph
	}
	if tab == view.Servers || selected.Kind == "node" {
		out.Cards = map[string]view.Card{}
		fleet := v.Fleet()
		for _, row := range out.Rows {
			if row.Kind == "node" {
				out.Cards[row.ID] = fleet.Card(row.ID)
			}
		}
		if selected.Kind == "node" {
			out.Cards[selected.ID] = fleet.Card(selected.ID)
		}
	}
	if tab == view.Services || selected.Kind == "service" {
		out.Services = map[string]view.ServiceCard{}
		fleet := v.Fleet()
		for _, row := range out.Rows {
			if row.Kind == "service" {
				c, ok := fleet.ServiceCard(row.ID)
				if ok {
					out.Services[row.ID] = c
				}
			}
		}
		if selected.Kind == "service" {
			c, ok := fleet.ServiceCard(selected.ID)
			if ok {
				out.Services[selected.ID] = c
			}
		}
	}
	if tab == view.Paths || selected.Kind == "path" {
		out.Routes = snap.Topology.Paths
	}
	if selected.ID != "" {
		ids := map[string]bool{}
		if selected.Kind == "incident" {
			for _, i := range snap.Incidents {
				if i.ID == selected.ID {
					for _, c := range i.Checks {
						ids[c] = true
					}
				}
			}
			for _, i := range snap.Ledger {
				if i.ID == selected.ID || strings.HasPrefix(selected.ID, i.ID+"#") {
					for _, c := range i.Checks {
						ids[c] = true
					}
				}
			}
		}
		services := map[string]string{}
		endpoints := map[string]string{}
		observers := map[string]string{}
		for _, d := range snap.Topology.Services {
			services[d.ID] = d.Node
		}
		for _, d := range snap.Topology.Endpoints {
			endpoints[d.ID] = d.Node
		}
		for _, d := range snap.Topology.Observers {
			observers[d.ID] = d.Node
		}
		for _, d := range snap.Topology.Checks {
			match := selected.Kind == "check" && d.ID == selected.ID || selected.Kind == "service" && d.Service == selected.ID || selected.Kind == "path" && d.Path == selected.ID || selected.Kind == "link" && d.Link == selected.ID || selected.Kind == "endpoint" && d.Endpoint == selected.ID || selected.Kind == "observer" && d.Observer == selected.ID || selected.Kind == "node" && (services[d.Service] == selected.ID || endpoints[d.Endpoint] == selected.ID || d.Kind == "host" && observers[d.Observer] == selected.ID)
			if match {
				ids[d.ID] = true
			}
		}
		for _, row := range v.List(view.Checks, "") {
			if ids[row.ID] {
				out.Checks = append(out.Checks, row)
			}
		}
	}
	out.ResourceValues = map[string][][2]string{}
	for _, card := range out.Cards {
		for _, r := range card.Resources {
			out.ResourceValues[r.Check] = view.ResourceValues(r)
		}
	}
	encoded, e := secure.MarshalJSON(out, "")
	if e != nil || len(encoded) > 16<<20 {
		return nil, 503, "web_projection_budget_exceeded"
	}
	return encoded, 200, ""
}
func (s *Server) webView(w http.ResponseWriter, r *http.Request, a authority) {
	raw, status, code := s.projectWeb(r, a)
	if status != 200 {
		writeError(w, status, code)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(raw)
}
func (s *Server) webEvents(w http.ResponseWriter, r *http.Request, a authority) {
	// One viewer admission is held per stream. Revalidate its token/generation
	// before every frame; revocation and reload cannot leave a live old session.
	raw, status, code := s.projectWeb(r, a)
	if status != 200 {
		writeError(w, status, code)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ctl := http.NewResponseController(w)
	send := func(b []byte) bool {
		_ = ctl.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, e := fmt.Fprintf(w, "event: view\ndata: %s\n\n", b); e != nil {
			return false
		}
		return ctl.Flush() == nil
	}
	if !send(raw) {
		return
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	end := time.NewTimer(45 * time.Second)
	defer end.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-end.C:
			return
		case <-tick.C:
			current, ok := s.admit(r)
			if !ok || current.Role != "viewer" {
				return
			}
			a = current
			raw, status, _ = s.projectWeb(r, a)
			if status != 200 {
				return
			}
			if !send(raw) {
				return
			}
		}
	}
}
func (s *Server) webHistory(w http.ResponseWriter, r *http.Request, a authority) {
	id := r.URL.Query().Get("check")
	x, ok := a.gen.checks[id]
	if !ok {
		writeError(w, 404, "unknown_check")
		return
	}
	entries := s.Store.History(id, map[string]string{id: a.gen.contracts[id]}, a.gen.revision, view.StripSamples)
	for i := range entries {
		entries[i].Contract = a.gen.publicContracts[id]
		entries[i].Revision = ""
	}
	strip := view.NewStrip(x, entries, s.Now(), view.StripSamples)
	writeJSON(w, 200, struct {
		Strip   view.Strip          `json:"strip"`
		Entries []model.Observation `json:"entries"`
		Latency string              `json:"latency"`
	}{strip, entries, strip.Latency.String()})
}
func (s *Server) webValidate(w http.ResponseWriter, r *http.Request, a authority) {
	// Validation is pure: no network calls, file access or config mutation.
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, 415, "json_required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	c, e := config.Decode(r.Body)
	if e != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_topology", "detail": secure.Text(e.Error())})
		return
	}
	writeJSON(w, 200, struct {
		Valid    bool         `json:"valid"`
		Revision string       `json:"revision"`
		Topology model.Config `json:"topology"`
	}{true, config.Revision(config.Public(c)), config.Public(c)})
}
