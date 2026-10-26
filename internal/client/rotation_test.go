package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestPostRotationRetryPreservesSamplesAndIsBounded(t *testing.T) {
	for _, alwaysClose := range []bool{false, true} {
		t.Run(map[bool]string{true: "bounded", false: "recovered"}[alwaysClose], func(t *testing.T) {
			c := testkit.Config()
			batch := model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", 1, time.Now(), "ok")}}
			var received []model.Batch
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				var b model.Batch
				json.NewDecoder(r.Body).Decode(&b)
				received = append(received, b)
				if alwaysClose || len(received) == 1 {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				w.WriteHeader(202)
				json.NewEncoder(w).Encode(map[string]any{"project": b.Project, "revision": b.Revision, "accepted": len(b.Observations)})
			}))
			defer server.Close()
			diagnostic, err := PostBatchDetailed(context.Background(), server.Client(), Endpoint{URL: server.URL, Token: token}, batch)
			if diagnostic.Attempts != 2 || diagnostic.PreviousCode != "connection_closed" || (!alwaysClose && diagnostic.RetryOutcome != "recovered") {
				t.Fatal(diagnostic)
			}
			mu.Lock()
			defer mu.Unlock()
			if (err != nil) != alwaysClose || len(received) != 2 || !reflect.DeepEqual(received[0], received[1]) {
				t.Fatalf("requests=%d err=%v", len(received), err)
			}
		})
	}
}
