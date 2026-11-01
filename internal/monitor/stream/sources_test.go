package stream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestClientSourceProjectGuardAndObserverScope(t *testing.T) {
	var foreign atomic.Bool
	c := testkit.Config()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("missing saved credential")
		}
		snap := engine.Build(c, "fixture", r.URL.Query().Get("observer"), nil, nil, time.Now().UTC())
		if foreign.Load() {
			snap.Project = "foreign"
			snap.Topology.Project = "foreign"
		}
		json.NewEncoder(w).Encode(snap)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "credentials.json")
	creds := client.Credentials{Schema: 1, Active: "default", Profiles: map[string]client.Profile{"default": {Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: strings.Repeat("a", 64)}}}}}
	if err := client.Save(path, creds); err != nil {
		t.Fatal(err)
	}
	r := request()
	r.Source = "client"
	r.Lang = ""
	r.Credentials = path
	r.Observer = "home"
	r.Project = c.Project
	source, e := OpenSource(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	initial, err := source.Fetch(context.Background())
	if err != nil || initial.Snapshot.ViewObserver != "home" || initial.AllSnapshot == nil || len(initial.AllSnapshot.Checks) <= len(initial.Snapshot.Checks) {
		t.Fatal(initial, err)
	}
	foreign.Store(true)
	refused, err := source.Fetch(context.Background())
	if err != nil || refused.Snapshot.Project != c.Project {
		t.Fatal(refused, err)
	}
	if refused.CollectorRead.State != "error" || refused.CollectorRead.Code != "project_mismatch" || refused.CollectorRead.LastSuccessAt != initial.CollectorRead.LastSuccessAt {
		t.Fatal("project mismatch changed successful-read history", refused.CollectorRead)
	}
	select {
	case e := <-source.Errors:
		if e.Code != "project_mismatch" {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("project guard did not report an error")
	}
}

func TestAllErrorsLeaveRunningRequest(t *testing.T) {
	for _, code := range []string{"invalid_request", "unsupported_protocol", "credentials_unavailable", "profile_not_found", "project_mismatch"} {
		t.Run(code, func(t *testing.T) {
			lines := make(chan Line, 2)
			r := request()
			lines <- line(r)
			r.RequestID = "rejected"
			if code == "invalid_request" {
				r.Template.Bar.Row = "absent"
			}
			if code == "unsupported_protocol" {
				r.Schema = 2
			}
			lines <- line(r)
			close(lines)
			var frames []monitor.Panel
			open := func(_ context.Context, r Request) (Source, *Error) {
				if r.RequestID == "rejected" {
					return Source{}, failure(code, "rejected request")
				}
				return Source{Initial: monitor.Input{Unavailable: true}, Fetch: func(ctx context.Context) (monitor.Input, error) { <-ctx.Done(); return monitor.Input{}, ctx.Err() }}, nil
			}
			if err := Run(context.Background(), lines, open, func(p monitor.Panel) error { frames = append(frames, p); return nil }); err != nil {
				t.Fatal(err)
			}
			if len(frames) != 3 || frames[1].Error == nil || frames[1].Error.Code != code || frames[1].RequestID != "rejected" || frames[2].RequestID != "one" || frames[2].ConfigRevision != frames[0].ConfigRevision {
				t.Fatal(frames)
			}
		})
	}
}

func TestScopedPreviewAndLanguages(t *testing.T) {
	for _, lang := range []string{"en", "ja", "zh"} {
		r := request()
		r.Lang = lang
		r.Observer = "obs-mac"
		source, e := OpenSource(context.Background(), r)
		if e != nil {
			t.Fatal(e)
		}
		if source.Initial.Snapshot.ViewObserver != "obs-mac" || source.Initial.AllSnapshot == nil || len(source.Initial.AllSnapshot.Checks) <= len(source.Initial.Snapshot.Checks) {
			t.Fatal(lang)
		}
		for _, c := range source.Initial.Snapshot.Checks {
			if c.Observer != "obs-mac" {
				t.Fatal(c)
			}
		}
	}
}

// A service card's samples are its checks' history, read once from the
// collector the snapshot came from under the measurement it names, then
// extended from each snapshot.
func TestSourcesReadServiceCardHistoryOnce(t *testing.T) {
	r := request()
	r.Template.Rows = append(r.Template.Rows, monitor.Row{ID: "blog", Metric: "service.card", Visual: "card", Select: monitor.Selector{Service: "blog"}})
	source, e := OpenSource(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	card := monitor.Build(monitor.Config{Source: "preview", Template: r.Template}, source.Initial, time.Now()).Rows[1].Value.(monitor.ServiceCard)
	for _, s := range card.SeenFrom {
		for _, x := range s.Checks {
			if len(x.Samples.Slots) < 2 {
				t.Fatalf("the first preview frame lacks %s's history: %+v", x.ID, x.Samples)
			}
		}
	}

	c := testkit.Config()
	var histories atomic.Int32
	var snap atomic.Pointer[model.Snapshot]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/history" {
			histories.Add(1)
			s := snap.Load()
			id := r.URL.Query().Get("check")
			h := client.History{Project: s.Project, Revision: s.Revision, Check: id}
			for _, st := range s.Checks {
				if st.ID == id {
					h.Contract = st.Contract
				}
			}
			for i := range 3 {
				o := testkit.Observation(c, id, uint64(i+1), time.Now().UTC().Add(-time.Duration(30-i)*time.Second), "ok")
				o.Contract = h.Contract
				h.Entries = append(h.Entries, o)
			}
			json.NewEncoder(w).Encode(h)
			return
		}
		s := engine.Build(c, "fixture", "", []model.Observation{testkit.Observation(c, "home-http", 9, time.Now().UTC().Add(-time.Second), "ok")}, nil, time.Now().UTC())
		snap.Store(&s)
		json.NewEncoder(w).Encode(s)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "credentials.json")
	creds := client.Credentials{Schema: 1, Active: "default", Profiles: map[string]client.Profile{"default": {Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: strings.Repeat("a", 64)}}}}}
	if err := client.Save(path, creds); err != nil {
		t.Fatal(err)
	}
	r = request()
	r.Source, r.Lang, r.Credentials = "client", "", path
	r.Template.Rows = append(r.Template.Rows, monitor.Row{ID: "web", Metric: "service.card", Visual: "card", Select: monitor.Selector{Service: "web"}})
	r.Template.Rows = append(r.Template.Rows, monitor.Row{ID: "check", Metric: "check.card", Visual: "card", Select: monitor.Selector{Check: "home-http"}})
	source, e = OpenSource(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	in, err := source.Fetch(context.Background())
	if err != nil || histories.Load() != 2 || len(in.Samples["home-http"]) != 4 || len(in.Samples["cloud-http"]) != 3 {
		t.Fatal("history not read for the card's checks, or not extended", histories.Load(), in.Samples, err)
	}
	if in, err = source.Fetch(context.Background()); err != nil || histories.Load() != 2 || len(in.Samples["home-http"]) < 4 {
		t.Fatal("history read again", histories.Load(), err)
	}
}
