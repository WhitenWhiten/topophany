package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// Event is something to tell: incident.opened, incident.closed,
// observer.silent or observer.back. Observer is whose evidence it is.
type Event struct {
	Kind     string                `json:"event"`
	At       time.Time             `json:"at"`
	Observer string                `json:"-"`
	Title    string                `json:"title"`
	Text     string                `json:"text"`
	Incident *model.IncidentRecord `json:"incident,omitempty"`
	State    *model.ObserverState  `json:"observer,omitempty"`
}

// group is the events a webhook names that e belongs to.
func (e Event) group() string {
	if strings.HasPrefix(e.Kind, "incident.") {
		return "incidents"
	}
	return "observers"
}

// stamp writes a moment as a collector does: in UTC, to the second.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") }

// incidentEvent tells of a record that opened or closed, named as the TUI
// names it from snapshot v.
func incidentEvent(v view.View, kind string, r model.IncidentRecord, at time.Time) Event {
	observer, _, _ := strings.Cut(r.Scope, "/")
	name := v.IncidentTitle(model.Incident{Scope: r.Scope})
	e := Event{Kind: kind, At: at, Observer: observer, Incident: &r}
	lines := []string{}
	switch kind {
	case "incident.opened":
		e.Title = "✖ Incident: " + name
		lines = append(lines, r.Summary, "Checks: "+strings.Join(r.Checks, ", "), "Since: "+stamp(r.Since))
		if r.Previous > 0 {
			lines = append(lines, "Seen before: "+view.Plural(r.Previous, "time"))
		}
	default:
		glyph := "○"
		if r.Ended == "recovered" {
			glyph = "●"
		}
		e.Title = glyph + " Incident " + r.Ended + ": " + name
		lines = append(lines, "Ended: "+r.Ended+" — "+view.Ending(r.Ended), "Lasted: "+view.Duration(r.Closed.Sub(r.Since)), "Checks: "+strings.Join(r.Checks, ", "))
	}
	e.Text = strings.Join(append(lines, "Scope: "+r.Scope), "\n")
	return e
}

// observerEvent tells of an observer that fell silent or reports again.
func observerEvent(v view.View, kind string, o model.ObserverState, at time.Time) Event {
	name := o.ID
	for _, d := range v.Snapshot.Topology.Observers {
		if d.ID == o.ID && d.Name != "" {
			name = d.Name
		}
	}
	e := Event{Kind: kind, At: at, Observer: o.ID, State: &o}
	if kind == "observer.silent" {
		e.Title = "○ Observer silent: " + name
		e.Text = "No recent executor evidence; last seen: " + lastSeen(o.LastSeen) + "\nIts checks are unknown, not down, until it reports again."
	} else {
		e.Title = "● Observer reporting: " + name
		e.Text = "Recent executor evidence again; last seen: " + lastSeen(o.LastSeen)
	}
	e.Text += "\nObserver: " + o.ID
	return e
}

func lastSeen(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return stamp(t)
}

// maxText bounds what a message says, under every service's own limit.
const maxText = 1500

// bounded is the representation both persisted and delivered. Leave room in
// each recipient's share for queue metadata, observer IDs and retry counters.
func (e Event) bounded() (Event, error) {
	e.Title, e.Text = clip(e.Title, 250), clip(e.Text, maxText)
	b, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	if len(b) > maxBytes/(maxWebhooks*queueSize)-1024 {
		return e, errors.New("encoded alert exceeds queue budget")
	}
	return e, secure.JSONBudget(b)
}

// clip makes s safe and short: no control characters but line breaks, at
// most n bytes.
func clip(s string, n int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = secure.Text(l)
	}
	s = strings.Join(lines, "\n")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// EventKey is stable for retries and equivalent evidence on two collectors.
// Receivers may deduplicate it; independent collector judgments can still differ.
func EventKey(e Event, project string) string {
	parts := []string{project, e.Kind}
	if e.Incident != nil {
		checks := slices.Clone(e.Incident.Checks)
		slices.Sort(checks)
		parts = append(parts, e.Incident.Scope, e.Incident.Since.UTC().Format(time.RFC3339Nano), e.Incident.Ended, strings.Join(checks, ","))
	} else if e.State != nil {
		parts = append(parts, e.State.ID, e.State.LastSeen.UTC().Format(time.RFC3339Nano))
	} else {
		parts = append(parts, e.At.UTC().Format(time.RFC3339Nano))
	}
	data, _ := json.Marshal(parts)
	return secure.Hash(string(data))
}

// request is e as w's format posts it.
func (w Webhook) request(ctx context.Context, e Event, project, instance string) (*http.Request, error) {
	title, text := clip(e.Title, 250), clip(e.Text, maxText)
	var body []byte
	content := "application/json"
	header := http.Header{}
	switch w.Format {
	case "json":
		e.Title, e.Text = title, text
		body, _ = json.Marshal(struct {
			Schema    int    `json:"schema"`
			Project   string `json:"project"`
			Collector string `json:"collector"`
			EventKey  string `json:"event_key"`
			Event
		}{1, project, instance, EventKey(e, project), e})
	case "ntfy":
		content = "text/plain; charset=utf-8"
		body = []byte(text + "\nCollector: " + instance + " · " + project)
		header.Set("Title", mime.BEncoding.Encode("utf-8", title))
		header.Set("Tags", map[string]string{"incident.opened": "rotating_light", "incident.closed": "white_check_mark", "observer.silent": "warning", "observer.back": "white_check_mark"}[e.Kind])
		if e.Kind == "incident.opened" || e.Kind == "observer.silent" {
			header.Set("Priority", "high")
		}
	case "slack":
		// Slack reads <, > and & as markup, such as <!channel>.
		escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
		body, _ = json.Marshal(map[string]string{"text": "*" + escape(title) + "*\n" + escape(text) + "\n_" + escape(instance+" · "+project) + "_"})
	case "discord":
		// Names must not mention anyone, such as @everyone.
		body, _ = json.Marshal(map[string]any{"content": "**" + title + "**\n" + text + "\n*" + instance + " · " + project + "*", "allowed_mentions": map[string]any{"parse": []string{}}})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = header
	req.Header.Set("Content-Type", content)
	req.Header.Set("Idempotency-Key", EventKey(e, project))
	req.Header.Set("User-Agent", "Topophany/"+model.Version)
	if w.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+w.Bearer)
	}
	return req, nil
}
