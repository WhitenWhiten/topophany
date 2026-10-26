package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// header says where a view comes from, as the TUI's header bar does.
type header struct {
	Source          string              `json:"source,omitempty"`
	Profile         string              `json:"profile,omitempty"`
	ProfileRevision string              `json:"profile_revision,omitempty"`
	DatasetID       string              `json:"dataset_id,omitempty"`
	Sources         *view.SourcesStatus `json:"sources_status,omitempty"`
	Project         string              `json:"project"`
	Instance        string              `json:"instance"`
	Server          string              `json:"server"`
	Scenario        string              `json:"scenario,omitempty"` // the synthetic scenario in effect, under topo preview
	Observer        string              `json:"observer"`
	GeneratedAt     time.Time           `json:"generated_at"`
	Cached          bool                `json:"cached"`
}

func (h header) print(t *text, now time.Time) {
	vantage := h.Observer
	if vantage == "" {
		vantage = "all observers"
	}
	t.kv("Project", h.Project)
	if h.Source == "direct" {
		status := view.SourcesStatus{}
		if h.Sources != nil {
			status = *h.Sources
		}
		t.kv(view.DirectText("source_label"), view.DirectConnection(status)+" / "+h.Profile)
	} else {
		t.kv("Collector", h.Instance+" @ "+h.Server)
	}
	if h.Scenario != "" {
		t.kv("Scenario", h.Scenario)
	}
	t.kv("View", vantage)
	updated := view.Stamp(now, h.GeneratedAt)
	if h.Cached {
		updated += " (cached: nothing current is known)"
	}
	t.kv("Updated", updated)
}

// standing is what topo status concludes, as a word and an exit status:
// clear when the verdict is all clear and nothing needs attention, no
// evidence when nothing current is known, otherwise attention.
func standing(v view.View, attention []view.Row) (string, int) {
	if v.Snapshot.Direct != nil {
		status, _ := v.SourcesStatus()
		if status.State == "ready" {
			return "clear", 0
		}
		if status.Counts.Current > 0 {
			return "attention", ExitAttention
		}
		allFailed := status.Counts.Selected > 0
		for _, src := range status.Sources {
			if src.Counts.Selected > 0 && (src.State == "connected" || src.State == "loading") {
				allFailed = false
			}
		}
		if allFailed {
			return "no-evidence", ExitUnreachable
		}
		return "no-evidence", ExitNoEvidence
	}
	switch v.Verdict().State {
	case "incidents", "down", "degraded":
		return "attention", ExitAttention
	case "clear":
		if len(attention) > 0 {
			return "attention", ExitAttention
		}
		return "clear", 0
	}
	return "no-evidence", ExitNoEvidence
}

// tabBadge is what a tab counts beside its name.
type tabBadge struct {
	Tab   string `json:"tab"`
	Count int    `json:"count"`
	Down  bool   `json:"down"`
}

// statusJSON is the Overview as data. Status says what the exit status
// does; Quiet is what the Overview says when nothing needs attention.
type statusJSON struct {
	header
	Verdict   view.Verdict `json:"verdict"`
	Status    string       `json:"status"`
	Checks    view.Counts  `json:"checks"`
	Incidents int          `json:"incidents"`
	Observers struct {
		Live  int        `json:"live"`
		Total int        `json:"total"`
		Rows  []view.Row `json:"rows"`
	} `json:"observers"`
	Ingress   *model.Ingress `json:"ingress,omitempty"`
	Tabs      []tabBadge     `json:"tabs"`
	Attention []view.Row     `json:"attention"`
	Quiet     *quietJSON     `json:"quiet,omitempty"`
	Services  []view.Row     `json:"services"`
}

type quietJSON struct {
	State    string `json:"state"`
	Headline string `json:"headline"`
	More     string `json:"more"`
}

func (a *App) status(v view.View, h header, asJSON bool) error {
	attention := v.List(view.Overview, "")
	services := v.List(view.Services, "")
	sort.SliceStable(services, func(i, j int) bool { return view.ServiceRank(services[i]) < view.ServiceRank(services[j]) })
	word, code := standing(v, attention)
	s := statusJSON{header: h, Verdict: v.Verdict(), Status: word, Checks: v.Counts(), Incidents: len(v.Snapshot.Incidents), Attention: attention, Services: services, Ingress: v.Snapshot.Ingress}
	s.Observers.Live, s.Observers.Total = v.Live()
	s.Observers.Rows = v.List(view.Observers, "")
	for i := range view.Tabs {
		n, down := v.Badge(i)
		s.Tabs = append(s.Tabs, tabBadge{tabNames[i], n, down})
	}
	calm, headline, more := v.Calm()
	if len(attention) == 0 {
		s.Quiet = &quietJSON{calm, headline, more}
	}
	done := func(e error) error {
		if e == nil && code != 0 {
			return &ExitError{Code: code}
		}
		return e
	}
	if asJSON {
		return done(printJSON(a.Out, s))
	}
	t := &text{w: a.Out}
	t.line(s.Verdict.Glyph + " " + s.Verdict.Text)
	h.print(t, v.Now)
	t.kv("Checks", fmt.Sprintf("%d up, %d warning, %d down, %d unknown", s.Checks.Up, s.Checks.Warn, s.Checks.Down, s.Checks.Unknown))
	t.kv("Incidents", strconv.Itoa(s.Incidents))
	t.kv("Observers", fmt.Sprintf("%d/%d live", s.Observers.Live, s.Observers.Total))
	if in := s.Ingress; in != nil {
		// A snapshot from before source_count existed still has its listed
		// sources; do not print "from 0 sources" for it.
		sourceCount := in.SourceCount
		if sourceCount == 0 && len(in.Sources) > 0 {
			sourceCount = len(in.Sources)
		}
		failures, sources := "failures", "sources"
		if in.Failures == 1 {
			failures = "failure"
		}
		if sourceCount == 1 {
			sources = "source"
		}
		count := strconv.Itoa(sourceCount)
		if in.Truncated {
			count = "at least " + count
		}
		t.kv("Collector ingress", fmt.Sprintf("%d TLS handshake %s in %s from %s %s, last %s", in.Failures, failures, view.Duration(time.Duration(in.WindowSeconds)*time.Second), count, sources, view.Age(v.Now, in.Last)))
		types := []string{}
		for kind, count := range in.FailureTypes {
			types = append(types, fmt.Sprintf("%s=%d", kind, count))
		}
		sort.Strings(types)
		if len(types) > 0 {
			t.kv("Ingress failure types", strings.Join(types, ", "))
		}
	}
	look := []string{}
	for i, b := range s.Tabs {
		if b.Count > 0 {
			look = append(look, view.Tabs[i]+" "+strconv.Itoa(b.Count))
		}
	}
	if len(look) == 0 {
		look = []string{"nothing"}
	}
	t.kv("Needs a look", strings.Join(look, ", "))
	t.line()
	if s.Quiet != nil {
		t.line("NEEDS ATTENTION · 0")
		t.line(headline)
		t.line(more)
	} else {
		t.rows("NEEDS ATTENTION", view.Overview, attention)
	}
	t.line()
	t.rows("SERVICES", view.Services, services)
	t.line()
	t.rows("OBSERVERS", view.Observers, s.Observers.Rows)
	return done(t.err)
}

// shown is a row as show prints it: a server's carries its card, and a
// check shown on its own its recent samples, how it fared from the
// collector's rollups, as its evidence panel has them, or why the samples
// could not be read.
type shown struct {
	view.Row
	Card         *view.Card           `json:"card,omitempty"`
	Samples      *view.Strip          `json:"samples,omitempty"`
	SamplesError string               `json:"samples_error,omitempty"`
	Availability []model.Availability `json:"availability,omitempty"`
}

func withCards(v view.View, tab int, rows []view.Row) []shown {
	out := []shown{}
	var f *view.Fleet
	if tab == view.Servers {
		f = v.Fleet()
	}
	for _, r := range rows {
		s := shown{Row: r}
		if f != nil {
			c := f.Card(r.ID)
			s.Card = &c
		}
		out = append(out, s)
	}
	return out
}

type tabJSON struct {
	header
	Tab     string   `json:"tab"`
	Columns []string `json:"columns"`
	Rows    []shown  `json:"rows"`
}

func (a *App) showTab(v view.View, h header, tab int, asJSON bool) error {
	rows := v.List(tab, "")
	if asJSON {
		return printJSON(a.Out, tabJSON{h, tabNames[tab], view.Columns(tab), withCards(v, tab, rows)})
	}
	t := &text{w: a.Out}
	h.print(t, v.Now)
	t.line()
	t.rows(strings.ToUpper(view.Tabs[tab]), tab, rows)
	return t.err
}

// showEntry prints the evidence behind the entries of a tab with an ID,
// given bare or as KIND:ID. IDs are unique only within a kind, so on the
// Overview and Topology tabs a bare ID can name several.
func (a *App) showEntry(ctx context.Context, r reader, v view.View, h header, tab int, id string, asJSON bool) error {
	kind := ""
	if k, rest, ok := strings.Cut(id, ":"); ok {
		kind, id = k, rest
	}
	rows := []view.Row{}
	for _, r := range v.List(tab, "") {
		if r.ID == id && (kind == "" || r.Kind == kind) {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return fmt.Errorf("%s has no entry %q; topo show %s lists them", tabNames[tab], secure.Text(id), tabNames[tab])
	}
	entries := withCards(v, tab, rows)
	for i := range entries {
		e := &entries[i]
		if e.Kind != "check" {
			continue
		}
		x, _ := config.Check(v.Snapshot.Topology, e.ID)
		// The check's rollup windows carry the first-byte, colo and cache
		// fields; the row's details already print them, and --json hands
		// them over in the same shape the API uses.
		for _, cs := range v.Snapshot.Checks {
			if cs.ID == e.ID {
				e.Availability = cs.Availability
			}
		}
		binding := client.SnapshotHistory(v.Snapshot, h.Server, e.ID)
		samples, server, err := r.snapshotHistory(ctx, binding, view.StripSamples)
		if err == nil {
			err = binding.Match(samples, server)
		}
		if err != nil {
			e.SamplesError = err.Error()
			continue
		}
		strip := view.NewStrip(x, samples.Entries, v.Now, textSlots)
		e.Samples = &strip
	}
	if asJSON {
		return printJSON(a.Out, tabJSON{h, tabNames[tab], view.Columns(tab), entries})
	}
	t := &text{w: a.Out}
	h.print(t, v.Now)
	for _, e := range entries {
		t.line()
		t.entry(e.Row, func() {
			if e.Samples != nil || e.SamplesError != "" {
				t.samples(e.Samples, e.SamplesError, v.Now)
			}
		})
		if e.Card != nil {
			t.card(*e.Card, v.Now)
		}
	}
	return t.err
}

func (r reader) snapshotHistory(ctx context.Context, v client.HistoryView, limit int) (client.History, string, error) {
	if r.historyAt != nil {
		return r.historyAt(ctx, v, limit)
	}
	return r.history(ctx, v.Check, limit)
}

func (a *App) showMap(v view.View, h header, asJSON bool) error {
	nodes, edges := v.Graph()
	type node struct {
		view.MapNode
		Signal string `json:"signal"`
	}
	type edge struct {
		view.MapEdge
		Kind string `json:"kind"`
	}
	ns, es := []node{}, []edge{}
	names := map[string]string{}
	for _, n := range nodes {
		ns = append(ns, node{n, view.NodeSignal(n)})
		names[n.ID] = n.Name
	}
	for _, e := range edges {
		es = append(es, edge{e, view.EdgeKinds[e.Kind]})
	}
	if asJSON {
		return printJSON(a.Out, struct {
			header
			Nodes []node `json:"nodes"`
			Edges []edge `json:"edges"`
		}{h, ns, es})
	}
	t := &text{w: a.Out}
	h.print(t, v.Now)
	t.line()
	t.line(fmt.Sprintf("MAP · %s · %s", view.Plural(len(ns), "node"), view.Plural(len(es), "edge")))
	t.line()
	cells := [][]string{{"SIGNAL", "ID", "NAME", "KIND", "GROUP", "PLACE"}}
	for _, n := range ns {
		place := "no location"
		switch {
		case n.Located && n.Place != "":
			place = n.Place
		case n.Located:
			place = view.LatLon(model.Location{Lat: n.Lat, Lon: n.Lon})
		}
		if n.Inherited {
			place += " (its parent's)"
		}
		cells = append(cells, []string{n.Signal, n.ID, n.Name, view.KindLabel(model.Node{Kind: n.Kind, Provider: n.Provider}), n.Group, place})
	}
	t.table(cells)
	t.line()
	cells = [][]string{{"SIGNAL", "KIND", "FROM", "TO", "ID", "LABEL"}}
	for _, e := range es {
		cells = append(cells, []string{e.Signal, e.Kind, names[e.From], names[e.To], e.ID, e.Label})
	}
	t.table(cells)
	return t.err
}
