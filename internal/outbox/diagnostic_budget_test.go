package outbox

import (
	"encoding/json"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
	"time"
)

func TestEnrichedDeliveryTelemetryKeepsHeartbeatBudget(t *testing.T) {
	now := time.Now().UTC()
	event := model.TransportEvent{At: now, Stage: "ack", Code: "ack_identity_mismatch", HTTPStatus: 202, DurationMS: 12000, Attempts: 2, RetryOutcome: "failed", PreviousCode: "connection_closed", PreviousStage: "response", ConnectionKnown: true, ConnectionReused: true}
	d := &Delivery{Now: func() time.Time { return now }, reports: map[string]model.DeliveryStatus{}}
	for i := range 8 {
		rid := fmt.Sprintf("%016x", i)
		d.reports[rid] = model.DeliveryStatus{Collector: rid, ReportedAt: now, LastAck: now, LastContact: now, RetryAt: now.Add(time.Minute), Code: "ack_identity_mismatch", Stage: "ack", HTTPStatus: 202, PendingRecords: 1000000, PendingBytes: 1 << 40, QuarantineBytes: 1 << 40, QuarantinedSamples: 1000000, Attempt: event, LastFailure: event}
	}
	beat := d.beat(&destination{}, &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: now}, now)
	raw, err := json.Marshal(beat)
	if err != nil || len(raw) > model.MaxHeartbeatBytes || len(beat.Delivery) != 8 || !model.ValidDelivery(beat.Delivery, now) {
		t.Fatal(len(raw), beat, err)
	}
}
