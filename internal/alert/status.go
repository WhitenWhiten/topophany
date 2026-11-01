package alert

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

type DeliveryState struct {
	LastAttempt time.Time `json:"last_attempt,omitzero"`
	LastSuccess time.Time `json:"last_success,omitzero"`
	HTTPStatus  int       `json:"http_status,omitempty"`
	Code        string    `json:"code,omitempty"`
}
type WebhookStatus struct {
	Mode            string        `json:"mode,omitempty"`
	ResumeAt        time.Time     `json:"resume_at,omitzero"`
	PrimaryInstance string        `json:"primary_instance,omitempty"`
	ID              string        `json:"id"`
	Pending         int           `json:"pending"`
	Sent            int           `json:"sent"`
	Dropped         int           `json:"dropped"`
	Tries           int           `json:"oldest_attempts"`
	Oldest          time.Time     `json:"oldest_queued,omitzero"`
	Delivery        DeliveryState `json:"delivery"`
}
type StatusReport struct {
	Schema       int             `json:"schema"`
	AsOf         time.Time       `json:"as_of"`
	QueueDurable bool            `json:"queue_durable"`
	SnapshotOnly bool            `json:"snapshot_only"`
	Webhooks     []WebhookStatus `json:"webhooks"`
}

func (n *Notifier) statusLocked(at time.Time) StatusReport {
	out := StatusReport{Schema: 1, AsOf: at, QueueDurable: !n.unwritten, SnapshotOnly: true, Webhooks: []WebhookStatus{}}
	for id, q := range n.queues {
		s := WebhookStatus{ID: id, Pending: len(q.Waiting), Sent: q.Sent, Dropped: q.Dropped, Delivery: n.delivery[id]}
		if h := n.hooks[id]; h != nil {
			s.Mode = "ready"
			s.PrimaryInstance = h.PrimaryInstance
			if h.PrimaryInstance != "" && h.PrimaryInstance != n.Instance {
				s.Mode = "standby"
			}
			if until := h.quietDeadline(at); !until.IsZero() {
				s.ResumeAt = until
				if s.Mode != "standby" {
					s.Mode = "quiet"
				}
			}
		}
		if len(q.Waiting) > 0 {
			s.Oldest = q.Waiting[0].Queued
			s.Tries = q.Waiting[0].Tries
		}
		out.Webhooks = append(out.Webhooks, s)
	}
	sort.Slice(out.Webhooks, func(i, j int) bool { return out.Webhooks[i].ID < out.Webhooks[j].ID })
	return out
}
func (n *Notifier) recordAttempt(id string, status int, code string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.delivery == nil {
		n.delivery = map[string]DeliveryState{}
	}
	d := n.delivery[id]
	d.LastAttempt = time.Now().UTC()
	d.HTTPStatus = status
	d.Code = code
	if code == "" && status/100 == 2 {
		d.LastSuccess = d.LastAttempt
	}
	n.delivery[id] = d
}
func readStatus(path string) (StatusReport, error) {
	data, err := secure.PrivateRead(path)
	if err != nil {
		return StatusReport{}, err
	}
	var result StatusReport
	if json.Unmarshal(data, &result) != nil || result.Schema != 1 || len(result.Webhooks) > maxWebhooks {
		return StatusReport{}, errors.New("invalid alert status report")
	}
	return result, nil
}

// ReadStatus never starts deliveries, rewrites the queue or returns event bodies.
func ReadStatus(queuePath string) (StatusReport, error) {
	report, err := readStatus(queuePath + ".status.json")
	if err == nil {
		return report, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return report, err
	}
	n := &Notifier{Path: queuePath}
	if err := n.Load(); err != nil {
		return report, err
	}
	at := time.Time{}
	if st, err := os.Stat(queuePath); err == nil {
		at = st.ModTime()
	}
	return n.statusLocked(at), nil
}

// TestDelivery sends exactly one explicitly requested test, without changing the
// incident ledger or retry queue. The caller must validate the webhook first.
func TestDelivery(ctx context.Context, w Webhook, project string) (DeliveryState, error) {
	n := &Notifier{Project: project, Instance: "notification-test"}
	at := time.Now().UTC()
	status, code := n.post(ctx, w, Event{Kind: "notification.test", At: at, Title: "Topophany notification test", Text: "This is an explicit notification test, not an incident."})
	result := DeliveryState{LastAttempt: at, HTTPStatus: status, Code: code}
	if code == "" && status/100 == 2 {
		result.LastSuccess = at
		return result, nil
	}
	return result, errors.New("notification test was not accepted; inspect the returned status and code")
}
