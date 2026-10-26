package outbox

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"slices"
	"strings"
	"time"
)

func (q *Queue) deliveryStats(recipient string) (int, int64, int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	if p := q.pending[recipient]; p != nil {
		n = p.Len()
	}
	return n, q.recipientBytes[recipient], q.quarantineUsed
}

// Statuses reads immutable reports without racing destination retry state.
func (d *Delivery) Statuses() []model.DeliveryStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]model.DeliveryStatus, 0, len(d.reports))
	for _, r := range d.reports {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b model.DeliveryStatus) int { return strings.Compare(a.Collector, b.Collector) })
	return out
}
func deliveryCode(err error) string { return client.FailureCode(err) }
func (d *Delivery) publish(q *Queue, dest *destination, result Result) {
	id := Recipient(dest.endpoint.URL)
	n, bytes, quarantine := q.deliveryStats(id)
	if result.Accepted > 0 {
		dest.lastAck = d.Now()
	}
	dest.quarantined += int64(result.Quarantined)
	report := model.DeliveryStatus{Collector: id, ReportedAt: d.Now().UTC(), PendingRecords: n, PendingBytes: bytes, QuarantineBytes: quarantine, QuarantinedSamples: dest.quarantined, LastAck: dest.lastAck, LastContact: dest.lastContact, RetryAt: dest.next, Code: dest.code, Stage: dest.stage, HTTPStatus: dest.httpStatus, ServerCode: dest.serverCode, RetryAfterSeconds: dest.retryAfterSeconds}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reports == nil {
		d.reports = map[string]model.DeliveryStatus{}
	}
	d.reports[id] = report
}

// diagnosticCode preserves actionable protocol failures without logging arbitrary
// response text. Wire delivery health retains its existing broader categories.
func diagnosticCode(err error) string {
	var h *client.HTTPError
	if errors.As(err, &h) && model.SafeDeliveryServerCode(h.Code) != "" {
		return h.Code
	}
	return deliveryCode(err)
}

func (dest *destination) recordFailure(err error) time.Duration {
	dest.code, dest.diagnostic = deliveryCode(err), diagnosticCode(err)
	stage, serverCode, status, retry := client.FailureDetail(err)
	dest.stage, dest.serverCode, dest.httpStatus = stage, serverCode, status
	dest.retryAfterSeconds = int(retry / time.Second)
	return retry
}
