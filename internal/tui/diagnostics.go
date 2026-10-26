package tui

import (
	"slices"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/session"
)

func (m *Model) diagnosticEvent(kind, code, message string) {
	now := m.Now
	if now.IsZero() {
		now = time.Now()
	}
	m.diagnosticEvents = slices.DeleteFunc(m.diagnosticEvents, func(e session.Event) bool { return now.Sub(e.At) > session.Retention })
	m.diagnosticEvents = append(m.diagnosticEvents, session.Event{At: now, Kind: kind, Code: code, Message: secure.Text(message[:min(len(message), 1024)])})
	if len(m.diagnosticEvents) > session.MaxEvents {
		m.diagnosticEvents = slices.Clone(m.diagnosticEvents[len(m.diagnosticEvents)-session.MaxEvents:])
	}
}

// Diagnostics is derived from the same Model that draws the screen. A later
// successful read clears the current error, while bounded events retain it.
func (m *Model) Diagnostics() session.State {
	collectors := make([]string, 0, len(m.Collectors))
	for _, url := range m.Collectors {
		collectors = append(collectors, secure.PublicURL(url))
	}
	tab := ""
	if m.Tab >= 0 && m.Tab < len(tabs) {
		tab = tabs[m.Tab]
	}
	selection := ""
	rows := m.Rows()
	if m.Selected >= 0 && m.Selected < len(rows) {
		selection = rows[m.Selected].ID
	}
	events := []session.Event{}
	for _, e := range m.diagnosticEvents {
		if m.Now.Sub(e.At) <= session.Retention {
			events = append(events, e)
		}
	}
	var requests []model.TransportEvent
	if m.requestDiagnostics != nil {
		requests = m.requestDiagnostics()
	}
	return session.State{Requests: requests, Project: m.Project, Server: secure.PublicURL(m.Server), Collectors: collectors, Observer: m.Observer,
		Filters: session.Filters{Search: m.Search, Application: m.Application, Environment: m.Environment, Service: m.ServiceFilter, AttentionOnly: m.AttentionOnly}, Tab: tab, Selection: selection,
		SnapshotTime: m.Snapshot.GeneratedAt, Instance: m.Snapshot.Instance, Revision: m.Snapshot.Revision, Cached: m.Cached(), Fetching: m.Fetching, Loading: m.Loading, Error: m.Error,
		Notice: session.Notice{Visible: m.notice != "" && m.Now.Sub(m.noticeAt) < noticeFor, Text: m.notice, Error: m.noticeErr, At: m.noticeAt}, History: session.History{Check: m.samples.check, Error: m.samples.err, Samples: len(m.samples.entries)}, Events: events}
}
func (m *Model) keepFetchError(err error) {
	if err != nil {
		m.Error = err.Error()
		m.diagnosticEvent("connection_error", "snapshot_read_failed", m.Error)
	} else {
		if m.Error != "" {
			m.diagnosticEvent("recovered", "snapshot_read_recovered", "snapshot refresh recovered")
		}
		m.Error = ""
	}
}
