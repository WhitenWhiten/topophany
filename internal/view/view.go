// Package view derives what a reader is shown from a snapshot: the verdict,
// the counts, what needs attention, each tab's rows with the evidence behind
// them, the server cards and the map's nodes and edges. The TUI draws it and
// the CLI prints it, so neither concludes anything the other cannot show.
package view

import (
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// View is a snapshot as read at a moment. Cached means nothing current is
// known: the snapshot expired, or the collector stopped answering. Draft
// means a local draft in the editor, which has no live evidence.
type View struct {
	Snapshot model.Snapshot
	Now      time.Time
	Cached   bool
	Draft    bool
}

// Expired reports whether a snapshot no longer says anything current: none
// was received, or it was generated more than SnapshotLifetime before now.
func Expired(s model.Snapshot, now time.Time) bool {
	return s.GeneratedAt.IsZero() || now.Sub(s.GeneratedAt) > model.SnapshotLifetime
}

// The tabs that show evidence, in the TUI's order.
const (
	Overview = iota
	Topology
	Servers
	Services
	Paths
	Checks
	Incidents
	Observers
)

// Tabs names them.
var Tabs = []string{"Overview", "Topology", "Servers", "Services", "Paths", "Checks", "Incidents", "Observers"}

// Row is one entry of a tab. Cols are its secondary columns, headed by
// Columns; Note overrides the state, e.g. "affected"; Tree is the guide
// drawn before the title in the Topology hierarchy; Details are the
// evidence behind it: "Key: value" lines, headings and caveats.
type Row struct {
	Assessment *ServiceAssessment `json:"assessment,omitempty"`
	Kind       string             `json:"kind"`
	ID         string             `json:"id"`
	Health     string             `json:"health"`
	Title      string             `json:"title"`
	Meta       string             `json:"meta,omitempty"`
	Cols       []string           `json:"cols"`
	Note       string             `json:"note,omitempty"`
	Tree       string             `json:"tree,omitempty"`
	Details    []string           `json:"details"`
}

// Columns heads a tab's rows: the title column first, then one heading per
// Row.Cols entry.
func Columns(tab int) []string {
	switch tab {
	case Overview:
		return []string{"ITEM", "KIND"}
	case Topology:
		return []string{"NODE · ENDPOINT · LINK", "GROUP · NETWORK", "KIND · ADDRESS"}
	case Servers:
		return []string{"SERVER", "GROUP", "KIND", "PLACE", "MEMO"}
	case Services:
		return []string{"SERVICE", "HOST", "ENDPOINTS"}
	case Paths:
		return []string{"PATH", "OBSERVER", "DECLARED ROUTE"}
	case Checks:
		return []string{"CHECK", "OBSERVER", "KIND", "TIME", "FRESHNESS", "UP 24H"}
	case Incidents:
		return []string{"SCOPE", "SINCE", "CHECKS"}
	case Observers:
		return []string{"OBSERVER", "HOST", "LAST SEEN"}
	}
	return nil
}

// List is a tab as shown: the rows that match search, case-insensitively
// over their ID, title, meta and columns, each marked when the view is
// cached or a local draft.
func (v View) List(tab int, search string) []Row {
	var defs *scopeIndex
	if tab == Services || tab == Checks {
		defs = indexScopes(v.Snapshot.Topology)
	}
	return v.list(tab, search, defs)
}

func (v View) list(tab int, search string, defs *scopeIndex) []Row {
	rows := v.Rows(tab)
	filtered := rows[:0]
	search = strings.ToLower(search)
	for _, r := range rows {
		if tab == Services || tab == Checks {
			scope := defs.scope(r.Kind, r.ID)
			if scope.Application != "" || scope.Environment != "" {
				app := defs.application(scope.Application)
				r.Meta += " · " + ScopeLabel(app, scope.Environment)
				r.Details = append(r.Details, "", "Application: "+app)
				if scope.Environment != "" {
					r.Details = append(r.Details, "Environment: "+scope.Environment)
				}
			}
		}
		if search == "" || strings.Contains(strings.ToLower(r.ID+" "+r.Title+" "+r.Meta+" "+strings.Join(r.Cols, " ")), search) {
			if v.Cached {
				r.Health = "unknown"
				r.Note = "cached"
				r.Meta = "CACHED · " + r.Meta
				r.Details = append([]string{"CACHED VIEW — current status not established.", ""}, r.Details...)
			}
			if v.Draft {
				r.Details = append([]string{"LOCAL DRAFT — no live evidence in the editor.", ""}, r.Details...)
			}
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// NeedsAttention is true for rows a person should look at: confirmed or
// partial failures, entities on a failing path, and silent observers.
func NeedsAttention(r Row) bool {
	switch {
	case r.Kind == "incident", r.Health == "down", r.Health == "degraded", r.Note == "affected":
		return true
	case r.Kind == "observer":
		return r.Health != "up" || r.Note == "outdated"
	case r.Kind == "check":
		return r.Health == "unknown" && r.Note != "paused" && r.Note != "maintenance"
	}
	return false
}

// AttentionRank orders what needs attention: incidents, failures, partial
// failures, silent observers, then whatever sits on a failing path.
func AttentionRank(r Row) int {
	switch {
	case r.Kind == "incident":
		return 0
	case r.Health == "down":
		return 1
	case r.Health == "degraded":
		return 2
	case r.Kind == "observer":
		return 3
	}
	return 4
}

// ServiceRank orders services problems first: down, degraded, affected, up,
// then unknown.
func ServiceRank(r Row) int {
	switch {
	case r.Health == "down":
		return 0
	case r.Health == "degraded":
		return 1
	case r.Note == "affected":
		return 2
	case r.Health == "unknown":
		return 4
	}
	return 3
}

// State is the word in a row's state column; "" for a row that is simply up.
func State(r Row) string {
	switch r.Note {
	case "cached", "affected", "paused", "maintenance", "outdated", "recovering", "closed":
		return r.Note
	}
	if r.Kind == "service" && r.Assessment != nil {
		return r.Assessment.Label()
	}
	switch r.Health {
	case "down":
		return "DOWN"
	case "degraded":
		return "WARN"
	case "up":
		return ""
	}
	return "unknown"
}

// HealthWord is the short label of a health.
func HealthWord(h string) string {
	switch h {
	case "up", "down":
		return h
	case "degraded":
		return "warn"
	}
	return "unknown"
}

// Pill is the state shown beside an entity's name in its evidence: CACHED
// in a cached view; OPEN, RECOVERING or CLOSED for an incident; AFFECTED
// or OUTDATED when the row says so, otherwise its health.
func Pill(r Row) string {
	switch {
	case r.Note == "cached":
		return "CACHED"
	case r.Kind == "incident" && (r.Note == "recovering" || r.Note == "closed"):
		return strings.ToUpper(r.Note)
	case r.Kind == "incident":
		return "OPEN"
	case r.Note == "affected":
		return "AFFECTED"
	case r.Note == "outdated":
		return "OUTDATED"
	}
	if r.Kind == "service" && r.Assessment != nil {
		return r.Assessment.Label()
	}
	return strings.ToUpper(HealthWord(r.Health))
}

// Counts are the checks in a view by health. A cached view knows none of
// them, so all count as unknown.
type Counts struct {
	Up      int `json:"up"`
	Warn    int `json:"warning"`
	Down    int `json:"down"`
	Unknown int `json:"unknown"`
}

func (c Counts) Total() int { return c.Up + c.Warn + c.Down + c.Unknown }

func (v View) Counts() Counts {
	c := Counts{}
	for _, s := range v.Snapshot.Checks {
		if v.Cached {
			c.Unknown++
			continue
		}
		switch s.Health {
		case "up":
			c.Up++
		case "down":
			c.Down++
		case "degraded":
			c.Warn++
		default:
			c.Unknown++
		}
	}
	return c
}

// Live counts the observers reporting, of all in the view; none are live
// in a cached view.
func (v View) Live() (live, all int) {
	for _, o := range v.Snapshot.Observers {
		if o.Health == "up" && !v.Cached {
			live++
		}
	}
	return live, len(v.Snapshot.Observers)
}

// Verdict is the one-line answer at the top of the Overview. State is one
// of draft, cached, no-checks, incidents, down, degraded, no-evidence and
// incomplete and clear.
type Verdict struct {
	State string `json:"state"`
	Glyph string `json:"glyph"`
	Text  string `json:"text"`
}

func (v View) Verdict() Verdict {
	if v.Snapshot.Direct != nil && !v.Draft && !v.Cached {
		return v.directVerdict()
	}
	c := v.Counts()
	inc := len(v.Snapshot.Incidents)
	switch {
	case v.Draft:
		return Verdict{"draft", "◇", "LOCAL DRAFT"}
	case v.Cached:
		return Verdict{"cached", "◌", "CACHED"}
	case c.Total() == 0:
		return Verdict{"no-checks", "○", "NO CHECKS"}
	case inc > 0:
		return Verdict{"incidents", "✖", itoa(inc) + map[bool]string{true: " INCIDENT", false: " INCIDENTS"}[inc == 1]}
	case c.Down > 0:
		return Verdict{"down", "✖", itoa(c.Down) + " DOWN"}
	case c.Warn > 0:
		return Verdict{"degraded", "▲", "DEGRADED"}
	case c.Up == 0:
		return Verdict{"no-evidence", "○", "NO EVIDENCE"}
	}
	for _, s := range v.Snapshot.Checks {
		if s.Health == "unknown" && s.Freshness != "paused" && s.Freshness != "maintenance" {
			return Verdict{"incomplete", "○", "INCOMPLETE EVIDENCE"}
		}
	}
	return Verdict{"clear", "●", "ALL CLEAR"}
}

// Calm is what the Overview says when nothing needs attention, and why:
// a state (draft, cached, no-checks or clear), a headline and a line more.
func (v View) Calm() (state, headline, more string) {
	if v.Snapshot.Direct != nil && !v.Draft && !v.Cached {
		verdict := v.directVerdict()
		return verdict.State, verdict.Glyph + " " + verdict.Text, DirectText("scope")
	}
	switch {
	case v.Draft:
		return "draft", "◇ Local draft", "The editor has no live evidence; preview it with topo preview --config."
	case v.Cached:
		return "cached", "◌ Status not established", "Showing the last snapshot; nothing current is known."
	case len(v.Snapshot.Checks) == 0:
		return "no-checks", "○ No checks in this view", "Nothing is measured from here yet."
	case v.Verdict().State == "incomplete" || v.Verdict().State == "no-evidence":
		return "incomplete", "○ Evidence incomplete", "Some active checks have no current conclusive evidence."
	}
	return "clear", "✓ Nothing needs attention", "Every active check in this view passes; no incidents."
}

// Badge is what a tab counts beside its name: the entries in it that need
// a look, and whether any of them is down. A cached view or a draft claims
// nothing.
func (v View) Badge(tab int) (n int, down bool) {
	if v.Cached || v.Draft {
		return 0, false
	}
	s := v.Snapshot
	look := func(health string, affected bool) {
		switch {
		case health == "down":
			n, down = n+1, true
		case health == "degraded", affected:
			n++
		}
	}
	switch tab {
	case Servers:
		known, servers := map[string]bool{}, map[string]bool{}
		for _, nd := range s.Topology.Nodes {
			known[nd.ID] = true
		}
		for _, nd := range s.Topology.Nodes {
			servers[nd.ID] = ServerNode(nd, known)
		}
		for _, e := range s.Nodes {
			if servers[e.ID] {
				look(e.Health, e.Affected)
			}
		}
	case Services:
		for _, e := range s.Services {
			look(e.Health, e.Affected)
		}
	case Paths:
		for _, e := range s.Paths {
			look(e.Health, e.Affected)
		}
	case Checks:
		for _, c := range s.Checks {
			look(c.Health, c.Health == "unknown" && c.Freshness != "paused" && c.Freshness != "maintenance")
		}
	case Incidents:
		n, down = len(s.Incidents), len(s.Incidents) > 0
		for _, r := range s.Ledger {
			if r.State == "recovering" {
				n++
			}
		}
	case Observers:
		for _, o := range s.Observers {
			look(o.Health, o.Health != "up" || o.Contracts == "outdated")
		}
	}
	return n, down
}
