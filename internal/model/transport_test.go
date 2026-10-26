package model

import (
	"testing"
	"time"
)

func TestTransportAddressAttemptsAreBoundedAndBackwardCompatible(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		addresses, attempts int
		valid               bool
	}{
		{0, 1, true}, // Events predating address telemetry remain readable.
		{1, 2, true}, // One address with a deduplicated POST retry.
		{8, 16, true},
		{-1, 1, false},
		{9, 16, false},
		{2, 1, false},
	} {
		e := TransportEvent{At: now, Stage: "complete", Attempts: tc.attempts, AddressAttempts: tc.addresses}
		if ValidTransportEvent(e, now) != tc.valid {
			t.Fatal(tc, e)
		}
	}
}
