package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type destination struct {
	attempt, lastFailure model.TransportEvent
	attemptID            uint64
	// endpoint and http are immutable after construction. Read them by index
	// or pointer: copying the whole destination also reads Tick's retry state.
	endpoint      client.Endpoint
	address       int
	http          *http.Client
	next          time.Time
	failures      int
	isolating     bool
	isolationID   string
	isolationNext int
	// Storage backpressure delays samples, not the executor's liveness.
	separateBeat bool
	// A collector that refuses a request carrying delivery telemetry may be
	// refusing the telemetry, not the samples: one older than it, or one
	// that finds it invalid, as when the clocks disagree. The request goes
	// again without it (probing) before any sample is blamed, and if that
	// is accepted, heartbeats go without it until bareUntil.
	probing                               bool
	bareUntil                             time.Time
	stage, serverCode                     string
	httpStatus, retryAfterSeconds         int
	code                                  string
	diagnostic                            string
	lastAck, lastContact                  time.Time
	lastBeat                              time.Time
	beatRevision, beatBoot, beatContracts string
	quarantined                           int64
}

// bareFor is how long heartbeats go without delivery telemetry once a
// collector has refused it; it is offered again after, as the collector
// may have been upgraded.
const bareFor = time.Hour

type Delivery struct {
	destinations      []destination
	Now               func() time.Time
	Project           string
	mu                sync.Mutex
	reports           map[string]model.DeliveryStatus
	heartbeatInterval time.Duration
	revision          string // what the worker runs; a reload changes it mid-round
}
type Result struct {
	Diagnostic  model.TransportEvent
	Code        string // stable diagnostic code for logs; retained during retry backoff
	Recipient   string
	Accepted    int
	Quarantined int
	Error       string
}

func NewDelivery(p client.Profile, revision string) (*Delivery, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	d := &Delivery{Now: time.Now, Project: p.Project, revision: revision}
	for _, ep := range p.Servers {
		h, e := client.HTTP(ep)
		if e != nil {
			d.Close()
			return nil, e
		}
		d.destinations = append(d.destinations, destination{endpoint: ep, http: h})
	}
	return d, nil
}
func (d *Delivery) Recipients() []string {
	r := []string{}
	for i := range d.destinations {
		r = append(r, Recipient(d.destinations[i].endpoint.URL))
	}
	return r
}
func (d *Delivery) Close() {
	for i := range d.destinations {
		d.destinations[i].http.CloseIdleConnections()
	}
}

// SetRevision says what topology the worker now runs, as after a reload.
func (d *Delivery) SetRevision(revision string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.revision = revision
}

// SetHeartbeatInterval bounds idle heartbeat traffic independently of sample
// delivery. Zero preserves a heartbeat on every round for embedded callers.
func (d *Delivery) SetHeartbeatInterval(interval time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.heartbeatInterval = max(interval, 0)
}

func (d *Delivery) beatDue(dest *destination, revision string, hb *model.Heartbeat, now time.Time) bool {
	d.mu.Lock()
	interval := d.heartbeatInterval
	d.mu.Unlock()
	return dest.lastBeat.IsZero() || now.Before(dest.lastBeat) || now.Sub(dest.lastBeat) >= interval ||
		dest.beatRevision != revision || dest.beatBoot != hb.Boot || dest.beatContracts != hb.Contracts
}

func (d *Delivery) keptBeat(dest *destination, revision string, hb *model.Heartbeat) {
	dest.lastBeat = d.Now()
	dest.beatRevision, dest.beatBoot, dest.beatContracts = revision, hb.Boot, hb.Contracts
}

func (d *Delivery) currentRevision() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.revision
}

// Tick is single-owner, and not to be run beside Run. It delivers one round
// to every collector at once and waits for them all.
func (d *Delivery) Tick(ctx context.Context, q *Queue, hb *model.Heartbeat) []Result {
	results := make([]Result, len(d.destinations))
	var wg sync.WaitGroup
	for i := range d.destinations {
		wg.Go(func() { results[i] = d.send(ctx, q, i, hb) })
	}
	wg.Wait()
	return results
}

// Run delivers to every collector from a loop of its own, a round each
// interval once any backoff has passed, until ctx ends. A collector slow
// to answer or not answering holds up only its own next round; the others
// keep receiving on time. beat gives the heartbeat for a round, and report
// is handed each round's result, one at a time.
func (d *Delivery) Run(ctx context.Context, q *Queue, interval time.Duration, beat func() *model.Heartbeat, report func(Result)) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range d.destinations {
		wg.Go(func() {
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
				d.drain(ctx, q, i, beat, func(r Result) {
					mu.Lock()
					defer mu.Unlock()
					report(r)
				})
			}
		})
	}
	wg.Wait()
}

// A successful round can drain up to four 1 MiB requests. Continuations are
// paced at ten requests/second and limited to a one-second window. Failures,
// including 429, leave send's existing destination-specific backoff intact.
// Tick deliberately remains a single round for diagnostic/embedded callers.
func (d *Delivery) drain(ctx context.Context, q *Queue, i int, beat func() *model.Heartbeat, report func(Result)) {
	r := d.send(ctx, q, i, beat())
	if ctx.Err() != nil {
		return
	}
	report(r)
	window, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for requests := 1; requests < 4; requests++ {
		if r.Error != "" || r.Accepted == 0 {
			return
		}
		count, _, _ := q.deliveryStats(r.Recipient)
		if count == 0 {
			return
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-window.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		r = d.send(window, q, i, beat())
		if ctx.Err() != nil {
			return
		}
		report(r)
	}
}

// send delivers one round to destination i: what is pending for it, with
// hb when it describes the topology the batch was queued under. Only the
// goroutine delivering to i touches its retry state.
func (d *Delivery) send(ctx context.Context, q *Queue, i int, hb *model.Heartbeat) (result Result) {
	dest := &d.destinations[i]
	rid := Recipient(dest.endpoint.URL)
	result = Result{Recipient: rid}
	serial := dest.attemptID
	defer func() {
		if serial != dest.attemptID {
			result.Diagnostic = dest.attempt
		}
		result.Code = dest.diagnostic
		if result.Code == "" || dest.code == "local_ack_failed" || dest.code == "local_quarantine_failed" || dest.code == "local_read_failed" {
			result.Code = dest.code
		}
		if result.Error == "" && result.Diagnostic.RetryOutcome == "recovered" {
			result.Code = "connection_rotation_recovered"
			if result.Diagnostic.AddressAttempts > 1 {
				result.Code = "delivery_failover_recovered"
			}
		}
		d.publish(q, dest, result)
	}()
	revision := d.currentRevision()
	now := d.Now()
	if now.Before(dest.next) {
		if dest.separateBeat && hb != nil {
			if e := d.heartbeat(ctx, dest, revision, hb); e != nil {
				dest.recordFailure(e)
				dest.separateBeat = false // an outage follows the normal retry clock
				result.Error = "collector heartbeat unavailable"
			}
		}
		return
	}
	itemLimit := 100
	if dest.isolating {
		itemLimit = 1
	}
	items, readErr := q.ReadPending(rid, itemLimit, 1<<20)
	if readErr != nil {
		dest.code = "local_read_failed"
		result.Error = "local outbox payload unavailable or corrupt"
		return
	}
	if dest.isolating && len(items) == 1 && len(items[0].Batch.Observations) > 1 {
		if hb != nil {
			_ = d.heartbeat(ctx, dest, revision, hb)
		}
		return d.sendIsolated(ctx, q, dest, items[0], result)
	}
	if len(items) == 0 {
		dest.isolating = false
		dest.separateBeat = false
	}
	if len(items) == 0 && hb != nil && !d.beatDue(dest, revision, hb, now) {
		return
	}
	batch := model.Batch{Schema: model.BatchSchema, Project: d.Project, Revision: revision, Observations: []model.Observation{}}
	ids := []string{}
	if len(items) > 0 {
		// Items queued before an upgrade or a reload travel with
		// their own schema and revision, never mixed with others.
		batch.Schema = items[0].Batch.Schema
		batch.Project = items[0].Batch.Project
		batch.Revision = items[0].Batch.Revision
		header, _ := json.Marshal(batch)
		wireBytes := len(header)
		for _, v := range items {
			if v.Batch.Schema != batch.Schema || v.Batch.Project != batch.Project || v.Batch.Revision != batch.Revision || len(batch.Observations)+len(v.Batch.Observations) > 100 {
				continue
			}
			// Encode each item once, rather than re-encoding the growing
			// batch up to a hundred times while draining a backlog.
			wire, _ := json.Marshal(v.Batch.Observations)
			added := len(wire) - 2 // strip this item's array brackets
			if len(batch.Observations) > 0 {
				added++ // comma between the existing and appended observations
			}
			// Leave room for the optional heartbeat and HTTP decoder budget.
			if wireBytes+added > (1<<20)-4096 {
				continue
			}
			wireBytes += added
			batch.Observations = append(batch.Observations, v.Batch.Observations...)
			ids = append(ids, v.ID)
		}
	}
	if hb != nil && batch.Revision == revision && batch.Schema == model.BatchSchema {
		h := d.beat(dest, hb, now)
		batch.Heartbeat = &h
	}
	if len(batch.Observations) == 0 && batch.Heartbeat == nil {
		return
	}
	telemetry := batch.Heartbeat != nil && len(batch.Heartbeat.Delivery) > 0
	probed := dest.probing
	e := d.post(ctx, dest, batch)
	if e == nil {
		dest.lastContact = d.Now()
		if batch.Heartbeat != nil {
			d.keptBeat(dest, revision, batch.Heartbeat)
		}
		dest.code = ""
		dest.diagnostic = ""
	} else {
		dest.code = deliveryCode(e)
		dest.diagnostic = diagnosticCode(e)
	}
	retryAfter := dest.recordFailure(e)
	var he *client.HTTPError
	permanent := errors.As(e, &he) && he.DeliveryAction() == client.RejectSample
	switch {
	case telemetry && permanent:
		// Not yet the samples' fault: they go again at once, bare.
		dest.probing = true
		dest.next = time.Time{}
		result.Error = he.Error() + "; resending without delivery telemetry"
		return
	case probed && e == nil:
		// The telemetry was what the collector refused.
		dest.probing = false
		dest.bareUntil = d.Now().Add(bareFor)
	case probed && permanent:
		// Refused bare too, so the samples are; the telemetry was not.
		dest.probing = false
	}
	if e == nil {
		if e = q.Ack(ids, rid); e != nil {
			dest.code = "local_ack_failed"
			result.Error = "local acknowledgement persistence failed"
			return
		}
		// Accepted: whatever was refused before has been dealt with, so
		// items travel together again.
		dest.failures = 0
		dest.next = time.Time{}
		dest.isolating = false
		dest.separateBeat = false
		result.Accepted = len(batch.Observations)
		return
	}
	dest.separateBeat = errors.As(e, &he) && he.DeliveryAction() == client.StorageBackpressure
	if dest.separateBeat && hb != nil {
		if d.heartbeat(ctx, dest, revision, hb) != nil {
			dest.separateBeat = false
		}
	}
	if permanent {
		if len(ids) > 1 || len(batch.Observations) > 1 {
			// A single expired or invalid item rejects the entire request.
			// Retry original durable items separately before quarantining
			// anything, so valid neighbouring samples are still delivered.
			dest.isolating = true
			dest.next = time.Time{}
			result.Error = "combined batch rejected; retrying individual outbox items"
			return
		}
		if qe := q.Quarantine(ids, rid, he.Error()); qe != nil {
			dest.code = "local_quarantine_failed"
			result.Error = "quarantine persistence failed"
		} else {
			// The collector answered and the refused item is set aside:
			// that is progress, not an outage, so the next item goes at
			// the next tick rather than after a backoff (a reload can
			// leave many samples of an old contract).
			result.Quarantined = len(batch.Observations)
			result.Error = he.Error()
			dest.failures = 0
			dest.next = time.Time{}
			return
		}
	} else {
		result.Error = e.Error()
		if he != nil {
			result.Error = he.Error()
		}
	}
	dest.failures++
	n := dest.failures
	if n > 5 {
		n = 5
	}
	// The backoff counts from when the attempt ended, which for one that
	// timed out is long after it began.
	delay := time.Duration(1<<n) * time.Second
	dest.next = d.Now().Add(max(delay+time.Duration(rand.IntN(1000))*time.Millisecond, retryAfter))
	return
}

// heartbeat consumes no pending item and never acknowledges observations.
// It always describes the current worker, even while old samples wait.
func (d *Delivery) heartbeat(ctx context.Context, dest *destination, revision string, hb *model.Heartbeat) error {
	if !d.beatDue(dest, revision, hb, d.Now()) {
		return nil
	}
	h := d.beat(dest, hb, d.Now())
	err := d.post(ctx, dest, model.Batch{Schema: model.BatchSchema, Project: d.Project, Revision: revision, Heartbeat: &h})
	if err == nil {
		dest.lastContact = d.Now()
		d.keptBeat(dest, revision, &h)
	}
	return err
}

// beat is the heartbeat to send dest: hb with the delivery telemetry,
// unless dest has refused it lately or is being asked whether it did.
func (d *Delivery) beat(dest *destination, hb *model.Heartbeat, now time.Time) model.Heartbeat {
	h := model.CloneHeartbeat(*hb)
	if !dest.probing && !now.Before(dest.bareUntil) {
		h.Delivery = d.Statuses()
		// Enriched diagnostics must not consume the heartbeat's hard wire budget.
		for i := len(h.Delivery) - 1; i >= 0; i-- {
			raw, _ := json.Marshal(h)
			if len(raw) <= model.MaxHeartbeatBytes {
				break
			}
			h.Delivery[i].LastFailure = model.TransportEvent{}
		}
		for i := len(h.Delivery) - 1; i >= 0; i-- {
			raw, _ := json.Marshal(h)
			if len(raw) <= model.MaxHeartbeatBytes {
				break
			}
			h.Delivery[i].Attempt = model.TransportEvent{}
		}

	}
	return h
}
