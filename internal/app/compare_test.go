package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestComparisonPinsWindowsAndRejectsIgnoredAnchor(t *testing.T) {
	c := testkit.Config()
	st, err := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Observers: observerIDs(c)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token := strings.Repeat("v", 64)
	backend := api.New(c, "comparison", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(token), Role: "viewer"}}})
	var ignore atomic.Bool
	handler := backend.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ignore.Load() {
			q := r.URL.Query()
			q.Del("until")
			r.URL.RawQuery = q.Encode()
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	anchor := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	args := []string{"compare", "--checks", "home-http,home-ssh", "--around", anchor.Format(time.RFC3339), "--span", "1h", "--json", "--credentials", filepath.Join(t.TempDir(), "none"), "--server", server.URL, "--project", c.Project, "--credential-file", credentialFile(t, token)}
	out, _, err := invoke("", args...)
	if err != nil {
		t.Fatal(err, out)
	}
	var result struct{ Series []ComparedHistory }
	if json.Unmarshal([]byte(out), &result) != nil || len(result.Series) != 4 {
		t.Fatal(out)
	}
	if !result.Series[0].History.WindowEnd.Equal(result.Series[1].History.WindowEnd) || !result.Series[2].History.WindowEnd.Equal(result.Series[3].History.WindowEnd) {
		t.Fatal("comparison windows differ")
	}
	fractional := anchor.Add(123456789 * time.Nanosecond)
	fixed := append([]string(nil), args...)
	for i, arg := range fixed {
		if arg == "--around" {
			fixed[i], fixed[i+1] = "--until", fractional.Format(time.RFC3339Nano)
		}
	}
	out, _, err = invoke("", fixed...)
	if err != nil || json.Unmarshal([]byte(out), &result) != nil || !result.Series[0].History.WindowEnd.Equal(fractional) {
		t.Fatal("fractional fixed end was not preserved", err, out)
	}
	ignore.Store(true)
	if _, _, err := invoke("", args...); err == nil || !strings.Contains(err.Error(), "fixed history window") {
		t.Fatal("old collector silently accepted", err)
	}
}
