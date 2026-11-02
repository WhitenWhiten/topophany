package cloudflare

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestQuotaSharesBudgetAndCancels(t *testing.T) {
	q := NewQuota(2, 8)
	if q.spacing != 4*time.Second/3 {
		t.Fatal(q.spacing)
	}
	if err := q.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := q.Wait(ctx); failureCode(err) != "api_rate_limited" {
		t.Fatal(err)
	}
}

func TestQuotaCooldownHeadersAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		status   int
		header   http.Header
		min, max time.Duration
	}{
		{429, http.Header{"Retry-After": {"120"}}, 119 * time.Second, 121 * time.Second},
		{429, http.Header{"Retry-After": {"999999999"}}, 3599 * time.Second, 3601 * time.Second},
		{200, http.Header{"Ratelimit": {`"default";r=0;t=60`}}, 59 * time.Second, 61 * time.Second},
		{503, http.Header{}, 29 * time.Second, 31 * time.Second},
		{200, http.Header{"Ratelimit": {"garbage"}}, 0, 0},
	} {
		q := NewQuota(1, 1)
		q.Observe(tc.status, tc.header)
		if tc.max == 0 {
			if !q.cooldown.IsZero() {
				t.Fatal("malformed header delayed polling")
			}
			continue
		}
		if delay := time.Until(q.cooldown); delay < tc.min || delay > tc.max {
			t.Fatal(delay)
		}
		if err := q.Wait(context.Background()); failureCode(err) != "api_rate_limited" {
			t.Fatal(err)
		}
		q.cooldown = time.Now().Add(-time.Second)
		if err := q.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	q := NewQuota(1, 1)
	for i := 0; i < 20; i++ {
		q.Observe(429, http.Header{})
	}
	if delay := time.Until(q.cooldown); delay > 5*time.Minute || delay < 299*time.Second {
		t.Fatal(delay)
	}
}
