package outbox

import (
	"context"
	"errors"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"time"
)

// One destination owns the receipt stream for all of its addresses. Only
// authenticated matching identities earn a POST; DNS/IP never merge targets.
func (d *Delivery) post(ctx context.Context, dest *destination, b model.Batch) error {
	dest.attemptID++
	ep := dest.endpoint
	addresses := append([]string{ep.URL}, ep.AlternateURLs...)
	var last error
	for offset := range addresses {
		at := (dest.address + offset) % len(addresses)
		candidate := ep
		candidate.URL = addresses[at]
		if ep.CollectorID != "" {
			start := time.Now()
			if _, err := client.IdentifyWorker(ctx, dest.http, candidate, d.Project); err != nil {
				stage, server, status, retry := client.FailureDetail(err)
				dest.attempt = model.TransportEvent{At: d.Now().UTC(), Stage: stage, Code: client.FailureCode(err), ServerCode: server, HTTPStatus: status, RetryAfterSeconds: int(retry / time.Second), DurationMS: time.Since(start).Milliseconds(), Attempts: 1}
				last = err
				continue
			}
		}
		event, err := client.PostBatchDetailed(ctx, dest.http, candidate, b)
		event.At = d.Now().UTC()
		dest.attempt = event
		if err == nil {
			dest.address = at
			return nil
		}
		last = err
		if !retryAddress(err) {
			return err
		}
		if ctx.Err() != nil {
			return err
		}
	}
	return last
}

func retryAddress(err error) bool {
	switch client.FailureCode(err) {
	case "dns_failure", "dns_timeout", "tcp_failure", "tcp_timeout", "tls_failure", "tls_timeout", "connection_closed", "response_timeout", "response_read_timeout", "transport_failure":
		return true
	case "server_error":
		var h *client.HTTPError
		return errors.As(err, &h) && h.Status >= 500 && h.Status <= 599
	}
	// Authorization, throttling, configuration and storage budgets belong to
	// the recipient, so another address must not bypass their rejection.
	return false
}

// ConsolidateAliases migrates an older URL-addressed outbox before scheduling.
// Samples keep their original IDs; a crash can only replay a deduplicated sample.
func (d *Delivery) ConsolidateAliases(q *Queue) error {
	for i := range d.destinations {
		ep := d.destinations[i].endpoint
		if len(ep.AlternateURLs) == 0 {
			continue
		}
		ids := []string{}
		for _, address := range ep.AlternateURLs {
			ids = append(ids, Recipient(address))
		}
		if err := q.consolidateRecipients(Recipient(ep.URL), ids); err != nil {
			return err
		}
	}
	return nil
}
