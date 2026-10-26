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
	r.RotationRecovered("delivery", "0123456789abcdef", model.TransportEvent{At: now, Stage: "complete", HTTPStatus: 202, DurationMS: 24, ConnectionKnown: true, Attempts: 2, RetryOutcome: "recovered", PreviousCode: "connection_closed"}, now)
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
