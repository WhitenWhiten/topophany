package direct

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/view"
)

const komariObject = "d4c8d9a1-4ec5-4c1b-9b95-4c1c8f930b0d"
const komariKey = "synthetic.komari.direct-key.123456789"

func komariFixture(t *testing.T) (Request, time.Time, *atomic.Int32) {
	t.Helper()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	var state atomic.Int32
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" || r.URL.RawQuery != "" {
			t.Error("unexpected method or query", r.Method, r.URL.Path)
			w.WriteHeader(405)
			return
		}
		if state.Load() == 1 {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/admin/client/list":
			if r.Header.Get("Authorization") != "Bearer "+komariKey {
				w.WriteHeader(401)
				return
			}
			fmt.Fprintf(w, `[{"uuid":%q,"name":"Synthetic Komari","token":"agent-secret-canary"}]`, komariObject)
		case "/api/nodes":
			if r.Header.Get("Authorization") != "" {
				t.Error("public discovery received a credential")
			}
			fmt.Fprintf(w, `{"status":"success","data":[{"uuid":%q,"name":"Public Komari"}]}`, komariObject)
		case "/api/recent/" + komariObject:
			if got := r.Header.Get("Authorization"); got != "" && got != "Bearer "+komariKey {
				w.WriteHeader(401)
				return
			}
			fmt.Fprintf(w, `{"status":"success","data":[{"uuid":%q,"updated_at":%q,"cpu":{"usage":25,"cores":4},"ram":{"total":9007199254740993,"used":4503599627370496},"disk":{"total":1000,"used":250}}]}`, komariObject, now.Format(time.RFC3339Nano))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(h.Close)
	r := Request{Schema: 1, Profile: "desktop", Source: SourceConfig{ID: "km-example", Name: "Komari", Adapter: "komari", URL: h.URL, Interval: 30, Timeout: 8, Stale: 180, MaxAge: 180}, Auth: Auth{Mode: "token", Token: komariKey}, Selected: []string{komariObject}}
	return r, now, &state
}

func TestKomariDirectCredentialModesAndResourceSession(t *testing.T) {
	for _, mode := range []string{"token", "env", "anonymous"} {
		t.Run(mode, func(t *testing.T) {
			r, now, state := komariFixture(t)
			st := testStore(t)
			r.Auth = Auth{Mode: mode}
			switch mode {
			case "token":
				r.Auth.Token = komariKey
			case "env":
				t.Setenv("TOPO_KOMARI_DIRECT_TEST", komariKey)
				r.Auth.Name = "TOPO_KOMARI_DIRECT_TEST"
			}
			p := saveFixture(t, st, r)
			c, err := p.Config()
			if err != nil || c.Checks[0].External.PrimaryDisk != "aggregate" || (c.Sources[0].CredentialEnv == "") != (mode == "anonymous") {
				t.Fatal("invalid generated Komari binding", err)
			}
			stored, err := st.credential(p.Sources[0].Credential)
			if err != nil || stored.Renewable || mode == "env" && stored.Token != "" {
				t.Fatal("API key credential was renewable or persisted an environment value", err)
			}
			r.Auth = Auth{Mode: "saved"}
			r.BaseRevision = p.Revision
			updated := saveFixture(t, st, r)
			if updated.Dataset != p.Dataset {
				t.Fatal("saved authentication changed dataset identity")
			}
			s, err := NewSession(context.Background(), st, updated, Options{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			s.Wait(ctx)
			cancel()
			snap, err := s.Snapshot("", now)
			if err != nil || len(snap.External) != 1 || snap.External[0].Freshness != "fresh" || snap.External[0].Report == nil {
				s.Close()
				t.Fatal("no current Komari resources", err, snap.SourcesStatus)
			}
			res := snap.External[0].Report.Resource
			if *res.CPU.Percent != 25 || *res.Memory.TotalBytes != 9007199254740993 || res.Disks[0].ID != "aggregate" || *res.Disks[0].Percent != 25 {
				t.Fatal("resource mapping lost data", res)
			}
			v := view.View{Snapshot: snap, Now: now}
			if v.DirectConnection() != "Komari direct" || !v.SelectResources()[p.Sources[0].Nodes[0].ID].Selected.Current {
				t.Fatal("Komari resource projection is unavailable")
			}
			stale, err := s.Snapshot("", now.Add(181*time.Second))
			if err != nil || stale.External[0].Freshness != "stale" {
				t.Fatal("old Komari sample stayed current", err)
			}
			s.Close()
			state.Store(1)
			s, err = NewSession(context.Background(), st, updated, Options{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			s.Wait(ctx)
			cancel()
			failed, err := s.Snapshot("", now)
			s.Close()
			if err != nil || failed.SourcesStatus.Sources[0].Code == nil || *failed.SourcesStatus.Sources[0].Code != "auth_required" {
				t.Fatal("401 did not stop with a source authentication failure", err)
			}
		})
	}
}

func TestKomariDirectSavedCredentialBoundariesAndMixedProfile(t *testing.T) {
	r, _, _ := komariFixture(t)
	st := testStore(t)
	t.Setenv("TOPO_KOMARI_DIRECT_TEST", komariKey)
	r.Auth = Auth{Mode: "env", Name: "TOPO_KOMARI_DIRECT_TEST"}
	p := saveFixture(t, st, r)
	t.Setenv("TOPO_KOMARI_DIRECT_TEST", "different-synthetic-key")
	if _, err := st.Token(context.Background(), p.Profile, p.Sources[0], time.Now()); AsError(err).Code != "auth_required" {
		t.Fatal("environment key changed identity silently", err)
	}
	t.Setenv("TOPO_KOMARI_DIRECT_TEST", komariKey)
	r.Auth = Auth{Mode: "saved"}
	r.Source.Adapter = "beszel"
	if _, _, err := st.Authenticate(context.Background(), r); AsError(err).Code != "invalid_request" {
		t.Fatal("saved credential crossed adapters", err)
	}
	r.Source.Adapter = "komari"
	r.Source.URL += "/other"
	if _, _, err := st.Authenticate(context.Background(), r); AsError(err).Code != "invalid_request" {
		t.Fatal("saved credential crossed origins", err)
	}
	h := fixtureHub(t)
	br := testRequest(h)
	br.BaseRevision = p.Revision
	mixed := saveFixture(t, st, br)
	if len(mixed.Sources) != 2 || mixed.Sources[0].Source.Adapter != "komari" || mixed.Sources[1].Source.Adapter != "beszel" {
		t.Fatal("saving Beszel did not preserve the Komari source")
	}
	snap := LoadingSnapshot(mixed, "", time.Now())
	if got := (view.View{Snapshot: snap, Now: time.Now()}).DirectConnection(); got != "Direct resources" {
		t.Fatal("mixed sources mislabeled", got)
	}
	data, err := os.ReadFile(filepath.Join(st.Root, "profiles.json"))
	if err != nil || strings.Contains(string(data), komariKey) || strings.Contains(string(data), "agent-secret-canary") {
		t.Fatal("profile exposed private upstream credentials", err)
	}
}

func TestKomariDirectRejectsBadKeyAndUnsupportedAuth(t *testing.T) {
	r, _, _ := komariFixture(t)
	st := testStore(t)
	r.Auth.Token = "invalid-synthetic-api-key"
	c, h, err := st.Authenticate(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, _, err := h.Discover(context.Background(), c); AsError(err).Code != "auth_required" {
		t.Fatal("invalid key fell back to public discovery", err)
	}
	if _, err := os.Stat(st.Root); !os.IsNotExist(err) {
		t.Fatal("failed discovery wrote credentials", err)
	}
	r.Auth = Auth{Mode: "password", Identity: "synthetic", Password: "synthetic-password"}
	if _, _, err := st.Authenticate(context.Background(), r); AsError(err).Code != "auth_method_unsupported" {
		t.Fatal("Komari entered the PocketBase password flow", err)
	}
	// Even a key shaped like an expired PocketBase JWT is opaque to Komari.
	key := jwt(time.Now().Add(-time.Hour), true)
	cred := Credential{Mode: "token", Token: key, Subject: bearerSubject("komari", key)}
	if got, err := bearerToken("komari", cred, cred.Subject); err != nil || got != key {
		t.Fatal("opaque API key was interpreted as a PocketBase JWT", err)
	}
}
