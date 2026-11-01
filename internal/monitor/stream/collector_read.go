package stream

import (
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
)

func collectorRead(previous monitor.CollectorRead, attempted, completed time.Time, duration time.Duration, err error, events []model.TransportEvent) monitor.CollectorRead {
	r := monitor.CollectorRead{State: "ok", AttemptedAt: attempted, CompletedAt: completed,
		LastSuccessAt: previous.LastSuccessAt, DurationMS: max(0, duration.Milliseconds())}
	if err == nil {
		r.LastSuccessAt = completed
		return r
	}
	r.State = "error"
	r.ConsecutiveFailures = previous.ConsecutiveFailures + 1
	if r.ConsecutiveFailures == 0 { // saturate rather than wrap
		r.ConsecutiveFailures = previous.ConsecutiveFailures
	}
	r.Code = client.FailureCode(err)
	r.Stage, _, _, _ = client.FailureDetail(err)
	// Failover may wrap a transport error without retaining its type. Reuse
	// the client's bounded, sanitized trace from this read only. Never echo
	// error strings, endpoints, response bodies or credentials.
	if r.Stage == "" {
		for i := len(events) - 1; i >= 0; i-- {
			e := events[i]
			if !e.At.Before(attempted) && e.Code != "" {
				r.Stage, r.Code = e.Stage, e.Code
				break
			}
		}
	}
	if errors.Is(err, client.ErrProjectMismatch) {
		r.Stage, r.Code = "protocol", "project_mismatch"
	}
	return r
}
