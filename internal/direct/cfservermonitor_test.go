package direct

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/view"
)

func TestCFServerMonitorDirectPollsSavedWorker(t *testing.T) {
	const id = "12345678-1234-4234-8234-123456789abc"
	const env = "TOPO_CFSM_DIRECT_TEST_JWT"
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	payload := base64.RawStdEncoding.EncodeToString(fmt.Appendf(nil, `{"sub":"admin","exp":%d}`, expires.Unix()))
	token := "synthetic." + payload + ".sig+part/"
	t.Setenv(env, token)
	firstAt := now.Truncate(time.Millisecond).Add(-time.Second)
	var updated atomic.Bool
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("private Worker received unexpected method or credential")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/monitor/api/config":
			fmt.Fprint(w, `{"authorization":true}`)
		case "/monitor/api/servers":
			fmt.Fprintf(w, `{"servers":[{"id":%q,"name":"Worker server"}]}`, id)
		case "/monitor/api/server":
			if r.URL.RawQuery != "id="+id {
				t.Error("poll selected another server")
			}
			at, cpu := firstAt, 12.5
			if updated.Load() {
				at, cpu = firstAt.Add(time.Second), 37.5
			}
			fmt.Fprintf(w, `{"id":%q,"last_updated":%d,"cpu":%f,"ram_total":1024,"ram_used":512,"disk_total":1000,"disk_used":200}`, id, at.UnixMilli(), cpu)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer h.Close()
	st := testStore(t)
	r := Request{Schema: 1, Profile: "desktop", Source: SourceConfig{
		ID: "cfsm-home", Name: "Home Worker", Adapter: "cf-server-monitor", URL: h.URL + "/monitor",
		Interval: 1, Timeout: 4, Stale: 60, MaxAge: 30,
	}, Auth: Auth{Mode: "env", Name: env}, Selected: []string{id}}
	p := saveFixture(t, st, r)
	entry := p.Sources[0]
	saved, err := st.credential(entry.Credential)
	if err != nil || saved.Token != "" || saved.Env != env || saved.Renewable {
		t.Fatal("environment token was stored or made renewable", err)
	}
	if _, err := st.Token(t.Context(), p.Profile, entry, expires); AsError(err).Code != "auth_required" {
		t.Fatal("expired JWT still used for reads", err)
	}
	r.Auth = Auth{Mode: "saved"}
	r.BaseRevision = p.Revision
	same := saveFixture(t, st, r)
	if same.Dataset != p.Dataset {
		t.Fatal("saved credential changed the dataset")
	}
	r.Source.URL += "/other"
	if _, _, err := st.Authenticate(t.Context(), r); AsError(err).Code != "invalid_request" {
		t.Fatal("saved credential crossed Worker URLs", err)
	}
	r.Source.URL = entry.Source.URL
	r.Source.Adapter = "komari"
	if _, _, err := st.Authenticate(t.Context(), r); AsError(err).Code != "invalid_request" {
		t.Fatal("saved credential crossed adapters", err)
	}
	t.Setenv(env, "synthetic.new.jwt")
	if _, err := st.Token(t.Context(), p.Profile, entry, now); AsError(err).Code != "auth_required" {
		t.Fatal("changed environment credential bypassed verification", err)
	}
	t.Setenv(env, token)

	s, err := NewSession(t.Context(), st, same, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	s.Wait(ctx)
	current := func() (float64, time.Time) {
		snap, err := s.Snapshot("", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		card := (view.View{Snapshot: snap, Now: time.Now()}).SelectResources()[entry.Nodes[0].ID].Selected
		if !card.Current || card.Report == nil || card.Report.Resource == nil ||
			card.Report.Resource.CPU == nil || card.Report.Resource.CPU.Percent == nil ||
			len(card.Report.Resource.Disks) != 1 || card.Report.Resource.Disks[0].ID != "aggregate" {
			t.Fatal("selected Worker has no current CPU or disk evidence")
		}
		return *card.Report.Resource.CPU.Percent, card.Report.ObservedAt
	}
	if cpu, at := current(); cpu != 12.5 || !at.Equal(firstAt) {
		t.Fatalf("first sample = %v at %v", cpu, at)
	}
	updated.Store(true)
	for {
		if cpu, at := current(); cpu == 37.5 && at.Equal(firstAt.Add(time.Second)) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("running direct session never polled the next sample")
		case <-time.After(10 * time.Millisecond):
		}
	}
	stale, err := s.Snapshot("", firstAt.Add(32*time.Second))
	if err != nil || len(stale.External) != 1 || stale.External[0].Freshness != "stale" {
		t.Fatal("polling renewed the upstream sample's data deadline", err)
	}
}
