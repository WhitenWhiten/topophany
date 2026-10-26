package outbox

import (
	"context"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// One destination owns the receipt stream for all of its addresses. Only
// authenticated matching identities earn a POST; DNS/IP never merge targets.
func (d *Delivery) post(ctx context.Context, dest *destination, b model.Batch) error {
	ep := dest.endpoint
	addresses := append([]string{ep.URL}, ep.AlternateURLs...)
	var last error
	for offset := range addresses {
		at := (dest.address + offset) % len(addresses)
		candidate := ep
		candidate.URL = addresses[at]
		if ep.CollectorID != "" {
			if _, err := client.IdentifyWorker(ctx, dest.http, candidate, d.Project); err != nil {
				last = err
				continue
			}
		}
		err := client.PostBatch(ctx, dest.http, candidate, b)
		if err == nil {
			dest.address = at
			return nil
		}
		last = err
		switch client.FailureCode(err) {
		case "dns_failure", "dns_timeout", "tcp_failure", "tcp_timeout", "tls_failure", "tls_timeout", "connection_closed", "response_timeout", "response_read_timeout", "transport_failure":
		default:
			return err
		}
		if ctx.Err() != nil {
			return err
		}
	}
	return last
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
