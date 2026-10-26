package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/app"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/session"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

func TestLiveTUIAndCLISessionDiagnosticsAgree(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	c := testkit.Config()
	st, err := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token := strings.Repeat("c", 64)
	handler := api.New(c, "s2", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(token), Role: "viewer"}}}).Handler()
	var down atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	remote, err := tui.NewRemote(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: token}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	wait := func(condition func(session.State) bool) {
		t.Helper()
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			remote.Frame(time.Now(), 100, 30, true)
			if condition(remote.Diagnostics()) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("viewer did not reach requested state", remote.Diagnostics())
	}
	wait(func(s session.State) bool { return !s.SnapshotTime.IsZero() })
	remote.Key("/", time.Now())
	remote.Key("a", time.Now())
	remote.Key("enter", time.Now())
	down.Store(true)
	remote.Key("r", time.Now())
	wait(func(s session.State) bool { return s.Error != "" })
	root, _ := session.DefaultRoot()
	states, err := session.List(root, time.Now())
	if err != nil || len(states) != 1 {
		t.Fatal(states, err)
	}
	var out bytes.Buffer
	a := &app.App{Out: &out, Err: &out, Now: time.Now}
	if err = a.Run(context.Background(), []string{"diagnose", "--session", states[0].ID, "--json"}); err != nil {
		t.Fatal(err)
	}
	var got session.State
	if json.Unmarshal(out.Bytes(), &got) != nil {
		t.Fatal(out.String())
	}
	screen := remote.Diagnostics()
	if got.Error != screen.Error || got.Cached != screen.Cached || got.Server != screen.Server || got.Filters != screen.Filters || got.SnapshotTime != screen.SnapshotTime || len(got.Events) != len(screen.Events) {
		t.Fatal("CLI differs from displayed TUI", got, screen)
	}
	down.Store(false)
	remote.Key("r", time.Now())
	wait(func(s session.State) bool { return s.Error == "" })
	if len(remote.Diagnostics().Events) < 3 {
		t.Fatal("success erased error/retry/recovery")
	}
}
