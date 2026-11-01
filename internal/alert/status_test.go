package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExplicitNotificationTestAndPrivateStatus(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["event"] != "notification.test" {
			t.Error("test forged an incident")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	w := Webhook{ID: "ops", Format: "json", URL: server.URL + "/?secret=canary", Bearer: "private-token"}
	result, err := TestDelivery(context.Background(), w, "test")
	if err != nil || calls != 1 || result.LastSuccess.IsZero() {
		t.Fatal(result, err, calls)
	}
	path := filepath.Join(t.TempDir(), "alert-queue.json")
	n := &Notifier{Path: path, queues: map[string]*queue{"ops": {Sent: 2, Dropped: 1, Waiting: []queued{{Queued: time.Now(), Event: Event{Text: "private event details"}}}}}}
	n.recordAttempt("ops", 204, "")
	n.save()
	report, err := ReadStatus(path)
	if err != nil || len(report.Webhooks) != 1 || report.Webhooks[0].Pending != 1 || report.Webhooks[0].Delivery.LastSuccess.IsZero() {
		t.Fatal(report, err)
	}
	data, _ := os.ReadFile(path + ".status.json")
	for _, secret := range []string{server.URL, "canary", "private-token", "private event details"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("status contains private event material")
		}
	}
	// The canonical queue format remains readable without new status fields.
	var stored saved
	raw, _ := os.ReadFile(path)
	if json.Unmarshal(raw, &stored) != nil || stored.Schema != 1 {
		t.Fatal("changed queue format")
	}
}
