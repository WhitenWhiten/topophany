package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/app"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/outbox"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"github.com/WhitenWhiten/topophany/internal/tui"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func TestRateLimitRecoveryDiagnosticsReachSnapshotCLIAndTUI(t *testing.T) {
	c := testkit.Config()
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	st, err := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	worker, viewer := strings.Repeat("b", 64), strings.Repeat("c", 64)
	backend := api.New(c, "s2", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(worker), Role: "worker", Observer: "home"}, {TokenSHA256: secure.Hash(viewer), Role: "viewer"}}})
	backend.Now = now
	handler := backend.Handler()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && posts.Add(1) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":"observer_heartbeat_rate","private":"sensitive-response"}`)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	d, err := outbox.NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: worker}}}, config.Revision(c))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Now = now
	q, err := outbox.Open(t.TempDir(), c.Project, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err = q.Add(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", 1, now(), "ok")}}, d.Recipients()); err != nil {
		t.Fatal(err)
	}
	beat := func() *model.Heartbeat {
		return &model.Heartbeat{Observer: "home", Boot: "worker", SampledAt: now(), Contracts: config.ObserverContracts(c, "home")}
	}
	result := d.Tick(context.Background(), q, beat())[0]
	if result.Code != "observer_heartbeat_rate" || result.Diagnostic.HTTPStatus != 429 || result.Diagnostic.RetryAfterSeconds != 7 {
		t.Fatal(result)
	}
	clock.Add(int64(10 * time.Second))
	if result = d.Tick(context.Background(), q, beat())[0]; result.Accepted != 1 {
		t.Fatal(result)
	}
	clock.Add(int64(time.Second))
	if result = d.Tick(context.Background(), q, beat())[0]; result.Error != "" {
		t.Fatal(result)
	}
	profile := client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: viewer}}}
	reader, err := client.New(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.Now = now
	snap, _, err := reader.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var delivered model.DeliveryStatus
	for _, o := range snap.Observers {
		if o.ID == "home" && len(o.Delivery) > 0 {
			delivered = o.Delivery[0]
		}
	}
	if delivered.Code != "" || delivered.LastFailure.Code != "rate_limited" || delivered.LastFailure.HTTPStatus != 429 || delivered.Attempt.At.IsZero() {
		t.Fatal(delivered)
	}
	vm := &tui.Model{Snapshot: snap, Now: now(), Tab: view.Observers}
	lines := ""
	for _, r := range vm.Rows() {
		lines += strings.Join(r.Details, "\n")
	}
	if !strings.Contains(lines, "Recent failure: rate_limited") {
		t.Fatal(lines)
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err = client.Save(path, client.Credentials{Schema: 1, Active: "test", Profiles: map[string]client.Profile{"test": profile}}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	a := &app.App{Out: &output, Err: &output, Now: now}
	if err = a.Run(context.Background(), []string{"show", "observers", "home", "--credentials", path}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Recent failure: rate_limited") || strings.Contains(output.String(), "sensitive-response") || strings.Contains(output.String(), worker) {
		t.Fatal(output.String())
	}
}
