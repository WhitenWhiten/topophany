//go:build webui

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func decodeWeb(t *testing.T, w *httptest.ResponseRecorder) webProjection {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var p webProjection
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestWebViewerBoundary(t *testing.T) {
	for _, path := range []string{"/v1/web/view", "/v1/web/events", "/v1/web/history?check=home-http"} {
		for _, tc := range []struct {
			token  string
			status int
		}{{"", 401}, {workerToken, 403}, {"wrong", 401}} {
			t.Run(path+tc.token[:min(2, len(tc.token))], func(t *testing.T) {
				_, h, _ := webFixture(t)
				w := request(h, "GET", path, tc.token, "", nil)
				if w.Code != tc.status {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
	for _, token := range []string{"", workerToken} {
		_, h, _ := webFixture(t)
		w := request(h, "POST", "/v1/web/validate", token, "application/json", []byte(`{}`))
		if w.Code == 200 {
			t.Fatal("unauthorized validation")
		}
	}
}
func TestWebProjectionUsesTerminalView(t *testing.T) {
	for _, observer := range []string{"", "home", "cloud"} {
		for tab := 0; tab < 9; tab++ {
			t.Run(fmt.Sprintf("%s/%d", observer, tab), func(t *testing.T) {
				srv, h, b := webFixture(t)
				if e := srv.Store.Put(b); e != nil {
					t.Fatal(e)
				}
				p := decodeWeb(t, request(h, "GET", fmt.Sprintf("/v1/web/view?tab=%d&observer=%s", tab, observer), viewerToken, "", nil))
				raw, status, code := srv.cachedSnapshot(srv.generation(), observer, secure.Hash(viewerToken))
				if status != 200 {
					t.Fatal(status, code)
				}
				var snap model.Snapshot
				_ = json.Unmarshal(raw, &snap)
				v := view.View{Snapshot: snap, Now: srv.Now()}
				if p.Verdict != v.Verdict() || p.Counts != v.Counts() {
					t.Fatal("different verdict or counts", p.Verdict, v.Verdict())
				}
				if tab < 8 {
					want := v.List(tab, "")
					if tab == view.Services || tab == view.Checks {
						want = v.GroupedList(tab, view.GroupOptions{})
					}
					if !reflect.DeepEqual(p.Rows, want) {
						t.Fatalf("different rows/details in tab %d", tab)
					}
				}
				if p.Schema != 1 || p.GeneratedAt.IsZero() {
					t.Fatal("missing contract metadata")
				}
			})
		}
	}
}
func TestWebQueryBoundsAndPagination(t *testing.T) {
	for _, query := range []string{"tab=9", "limit=0", "limit=201", "offset=-1", "observer=absent", "by=evil", "map=evil"} {
		_, h, _ := webFixture(t)
		w := request(h, "GET", "/v1/web/view?"+query, viewerToken, "", nil)
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	_, h, _ := webFixture(t)
	p := decodeWeb(t, request(h, "GET", "/v1/web/view?tab=5&limit=1&offset=1", viewerToken, "", nil))
	if len(p.Rows) > 1 || p.Offset != 1 {
		t.Fatal(p)
	}
}
func TestWebHistoryAndDetail(t *testing.T) {
	srv, h, b := webFixture(t)
	if err := srv.Store.Put(b); err != nil {
		t.Fatal(err)
	}
	p := decodeWeb(t, request(h, "GET", "/v1/web/view?tab=5&kind=check&id=home-http", viewerToken, "", nil))
	if p.Detail == nil || p.Detail.ID != "home-http" || len(p.Detail.Details) == 0 || len(p.Checks) != 1 {
		t.Fatal(p.Detail, p.Checks)
	}
	w := request(h, "GET", "/v1/web/history?check=home-http", viewerToken, "", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var history struct {
		Strip   view.Strip          `json:"strip"`
		Entries []model.Observation `json:"entries"`
	}
	if json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history.Entries) != 1 || history.Strip.Samples != 1 {
		t.Fatal(w.Body.String())
	}
}
func TestWebDraftValidationDoesNotMutateOrReadFiles(t *testing.T) {
	srv, h, _ := webFixture(t)
	before := config.Revision(srv.generation().config)
	raw, _ := json.Marshal(srv.generation().config)
	w := request(h, "POST", "/v1/web/validate", viewerToken, "application/json", raw)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"valid":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if config.Revision(srv.generation().config) != before {
		t.Fatal("draft changed collector")
	}
	for _, body := range [][]byte{[]byte(`{"schema":1,"unknown":true}`), []byte(`{}`), bytes.Repeat([]byte(" "), (4<<20)+1)} {
		_, h, _ := webFixture(t)
		w = request(h, "POST", "/v1/web/validate", viewerToken, "application/json", body)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
func TestWebStreamCancellationAndRevocation(t *testing.T) {
	srv, _, _ := webFixture(t)
	server := httptest.NewServer(srv.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/web/events", nil)
	r.Header.Set("Authorization", "Bearer "+viewerToken)
	resp, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal(resp.StatusCode)
	}
	// Revoked viewer must receive no second frame even though its request was
	// authenticated before the reload.
	next := srv.generation().auth
	next.Tokens = []Principal{{secure.Hash(workerToken), "worker", "home"}}
	if err := srv.Reload(srv.generation().config, next); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if _, err = body.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if strings.Count(body.String(), "event: view") != 1 {
		t.Fatal("revoked stream continued", body.String())
	}
}

func TestWebProjectionRedactsPrivateExecutionConfig(t *testing.T) {
	srv, h, _ := webFixture(t)
	c := config.Clone(srv.Config)
	const secret = "private-web-header-do-not-expose"
	c.Checks[0].HTTP.RequestHeaders = map[string]string{"Authorization": "Bearer " + secret}
	if err := srv.Reload(c, srv.Auth); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/web/view?tab=1", "/v1/web/view?tab=5&kind=check&id=home-http"} {
		w := request(h, "GET", path, viewerToken, "", nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), secret) {
			t.Fatal(path, w.Code, "private execution setting exposed")
		}
	}
	if c.Checks[0].HTTP.RequestHeaders["Authorization"] != "Bearer "+secret {
		t.Fatal("runtime config was mutated")
	}
}
func TestWebGroupingBeforePagination(t *testing.T) {
	_, h, _ := webFixture(t)
	collapsed := decodeWeb(t, request(h, "GET", "/v1/web/view?tab=5&by=service&collapsed=true&limit=1", viewerToken, "", nil))
	if len(collapsed.Rows) != 1 || collapsed.Rows[0].Kind != "group" {
		t.Fatal(collapsed.Rows)
	}
	// Paging a collapsed group must never leak orphaned members on later pages.
	next := decodeWeb(t, request(h, "GET", "/v1/web/view?tab=5&by=service&collapsed=true&limit=1&offset=1", viewerToken, "", nil))
	if len(next.Rows) > 0 && next.Rows[0].Kind != "group" {
		t.Fatal(next.Rows)
	}
}

func webFixture(t *testing.T) (*Server, http.Handler, model.Batch) {
	srv, _, b := fixture(t)
	srv.WebEnabled = true
	return srv, srv.Handler(), b
}
func TestWebAPIAbsentUntilEnabled(t *testing.T) {
	_, h, _ := fixture(t)
	w := request(h, "GET", "/v1/web/view", viewerToken, "", nil)
	if w.Code != 404 {
		t.Fatal("disabled Web API is exposed", w.Code)
	}
}
