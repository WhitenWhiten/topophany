package logging

import (
	"bytes"
	"github.com/WhitenWhiten/topophany/internal/model"
	"strings"
	"testing"
	"time"
)

func TestRecoveredRotationIsInformationalAndStructured(t *testing.T) {
	var output bytes.Buffer
	now := time.Now()
	r := &Repeater{Out: &output}
	r.RetryRecovered("delivery", "0123456789abcdef", model.TransportEvent{At: now, Stage: "complete", HTTPStatus: 202, DurationMS: 24, ConnectionKnown: true, Attempts: 2, RetryOutcome: "recovered", PreviousCode: "connection_closed"}, now)
	text := output.String()
	if !strings.Contains(text, "event=connection_rotation_recovered") || strings.Contains(text, "event=error") || !strings.Contains(text, "duration_ms=24") || !strings.Contains(text, "retry_outcome=recovered") {
		t.Fatal(text)
	}
	output.Reset()
	r.UpdateDiagnostic("delivery", "collector", "tls_timeout", model.TransportEvent{At: now, Stage: "private-token", Code: "tls_timeout"}, now)
	if strings.Contains(output.String(), "private-token") {
		t.Fatal(output.String())
	}
}

func TestRecoveredFailoverIsInformationalAndPreservesCause(t *testing.T) {
	var output bytes.Buffer
	now := time.Now()
	r := &Repeater{Out: &output}
	r.Update("delivery", "collector", "tcp_failure", now)
	output.Reset()
	r.RetryRecovered("delivery", "collector", model.TransportEvent{At: now, Stage: "complete", HTTPStatus: 202, Attempts: 2, AddressAttempts: 2, RetryOutcome: "recovered", PreviousCode: "tcp_failure", PreviousStage: "tcp"}, now)
	text := output.String()
	for _, want := range []string{"event=delivery_failover_recovered", "code=tcp_failure", "attempts=2", "address_attempts=2"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing failover metadata", want, text)
		}
	}
	if strings.Contains(text, "connection_rotation") || strings.Contains(text, "event=error") {
		t.Fatal("failover misreported as a rotation or outage", text)
	}
	output.Reset()
	r.Update("delivery", "collector", "", now)
	if output.Len() != 0 {
		t.Fatal("recovery did not clear previous failure", output.String())
	}
}
