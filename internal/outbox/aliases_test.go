package outbox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestAuthenticatedAliasesDeliverOnceAndFailOver(t *testing.T) {
	c := testkit.Config()
	origin, st := collector(t, c, nil)
	var posts atomic.Int32
	alias := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
		}
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, origin.URL+r.URL.RequestURI(), r.Body)
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			w.WriteHeader(503)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer alias.Close()
	p := client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: origin.URL, Token: workerToken}, {URL: alias.URL, Token: workerToken}}}
	resolved, notes, err := client.ResolveDeliveryTargets(context.Background(), p)
	if err != nil || len(resolved.Servers) != 1 || len(notes) != 1 {
		t.Fatal(resolved, notes, err)
	}
	d, err := NewDelivery(resolved, config.Revision(c))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	q := queue(t)
	// This simulates an old two-URL outbox; migration leaves one recipient.
	if err = q.Add(makeBatch(1), []string{Recipient(origin.URL), Recipient(alias.URL)}); err != nil {
		t.Fatal(err)
	}
	if err = d.ConsolidateAliases(q); err != nil {
		t.Fatal(err)
	}
	if r := d.Tick(context.Background(), q, nil); r[0].Accepted != 1 {
		t.Fatal(r)
	}
	if n, _ := q.Stats(); n != 0 {
		t.Fatal("alias receipt stranded", n)
	}
	if rows, _ := st.View(); len(rows) != 1 {
		t.Fatal(rows)
	}
	if posts.Load() != 0 {
		t.Fatal("alias also received a POST")
	}
	// Prefer the alias, then prove an unavailable address switches to its peer.
	d.destinations[0].address = 1
	alias.Close()
	if err = q.Add(makeBatch(2), d.Recipients()); err != nil {
		t.Fatal(err)
	}
	if r := d.Tick(context.Background(), q, nil); r[0].Accepted != 1 {
		t.Fatal(r)
	}
}
func TestIndependentCollectorsWithSameDisplayNameRemainReplicas(t *testing.T) {
	c := testkit.Config()
	a, _ := collector(t, c, nil)
	b, _ := collector(t, c, nil)
	p := client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: a.URL, Token: workerToken}, {URL: b.URL, Token: workerToken}}}
	resolved, _, err := client.ResolveDeliveryTargets(context.Background(), p)
	if err != nil || len(resolved.Servers) != 2 {
		t.Fatal(resolved, err)
	}
}
