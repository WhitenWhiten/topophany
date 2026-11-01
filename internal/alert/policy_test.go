package alert

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQuietDeadlineAndServiceMembership(t *testing.T) {
	now := time.Now()
	c := model.Config{Services: []model.Service{{ID: "web"}, {ID: "db"}}, Checks: []model.Check{{ID: "http", Service: "web"}, {ID: "sql", Service: "db"}}}
	w := Webhook{ID: "ops", Format: "json", URL: "https://example.invalid", Services: []string{"web"}, Windows: []QuietWindow{{Start: now.Add(-time.Minute), End: now.Add(time.Minute)}}}
	if err := Validate(Config{Schema: 1, Webhooks: []Webhook{w}}, c); err != nil {
		t.Fatal(err)
	}
	w = BindServices(Config{Webhooks: []Webhook{w}}, c).Webhooks[0]
	if w.quietDeadline(now).IsZero() || !w.quietDeadline(now.Add(2*time.Minute)).IsZero() {
		t.Fatal("maintenance boundary")
	}
	for _, id := range []string{"http", "sql"} {
		event := Event{Kind: "incident.opened", Incident: &model.IncidentRecord{Checks: []string{id}}}
		if w.wantsEvent(event) != (id == "http") {
			t.Fatal("service routing", id)
		}
	}
	if w.wantsEvent(Event{Kind: "observer.silent"}) {
		t.Fatal("service route accepted unrelated observer event")
	}
	w.Windows = append(w.Windows, w.Windows[0])
	if Validate(Config{Schema: 1, Webhooks: []Webhook{w}}, c) == nil {
		t.Fatal("overlap accepted")
	}
}
func TestStandbyPromotionAndQuietDelivery(t *testing.T) {
	sent := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing event key")
		}
		sent <- struct{}{}
		w.WriteHeader(204)
	}))
	defer server.Close()
	n := &Notifier{Instance: "secondary", queues: map[string]*queue{"ops": {Waiting: []queued{{Queued: time.Now(), Event: Event{Kind: "incident.opened", At: time.Now()}, Observer: "home"}}}}}
	defer n.Close()
	w := Webhook{ID: "ops", Format: "json", URL: server.URL, PrimaryInstance: "primary"}
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{w}})
	select {
	case <-sent:
		t.Fatal("standby sent notification")
	case <-time.After(30 * time.Millisecond):
	}
	w.PrimaryInstance = "secondary"
	w.QuietUntil = time.Now().Add(100 * time.Millisecond)
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{w}})
	select {
	case <-sent:
		t.Fatal("quiet notification sent early")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("quiet notification did not resume")
	}
}
func TestEventKeysTrackEvidenceNotCollectorClock(t *testing.T) {
	e := Event{Kind: "incident.opened", At: time.Now(), Incident: &model.IncidentRecord{Scope: "home/path/main", Since: time.Now(), Checks: []string{"a", "b"}}}
	first := EventKey(e, "project")
	e.At = e.At.Add(time.Minute)
	e.Incident.Checks = []string{"b", "a"}
	if EventKey(e, "project") != first {
		t.Fatal("same evidence key changed")
	}
	e.Kind = "incident.closed"
	if EventKey(e, "project") == first {
		t.Fatal("open and close collided")
	}
}
