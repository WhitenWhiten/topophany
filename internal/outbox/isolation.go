package outbox

import (
	"context"
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
)

// Advance one observation per delivery round. The immutable parent remains
// durable until every member has been accepted or individually quarantined.
// The cursor is deliberately ephemeral: restart may replay accepted members,
// whose original observation identities the collector already deduplicates.
func (d *Delivery) sendIsolated(ctx context.Context, q *Queue, dest *destination, item Item, result Result) Result {
	if dest.isolationID != item.ID {
		dest.isolationID, dest.isolationNext = item.ID, 0
	}
	index := dest.isolationNext
	if index < len(item.Batch.Observations) {
		batch := item.Batch
		batch.Observations = batch.Observations[index : index+1]
		batch.Heartbeat = nil
		err := client.PostBatch(ctx, dest.http, dest.endpoint, batch)
		retryAfter := dest.recordFailure(err)
		if err != nil {
			var he *client.HTTPError
			if !errors.As(err, &he) || he.DeliveryAction() != client.RejectSample {
				dest.failures++
				dest.next = d.Now().Add(max(time.Duration(1<<min(dest.failures, 5))*time.Second, retryAfter))
				result.Error = err.Error()
				return result
			}
			if err := q.QuarantinePart(item.ID, index, result.Recipient, he.Error()); err != nil {
				dest.code = "local_quarantine_failed"
				result.Error = "quarantine persistence failed"
				return result
			}
			result.Quarantined = 1
		} else {
			result.Accepted = 1
			dest.lastContact = d.Now()
			dest.code, dest.diagnostic = "", ""
		}
		dest.isolationNext++
		dest.failures, dest.next = 0, time.Time{}
	}
	if dest.isolationNext == len(item.Batch.Observations) {
		if err := q.Ack([]string{item.ID}, result.Recipient); err != nil {
			dest.code = "local_ack_failed"
			result.Error = "local acknowledgement persistence failed"
			return result
		}
		dest.isolating, dest.isolationID, dest.isolationNext = false, "", 0
	}
	return result
}
