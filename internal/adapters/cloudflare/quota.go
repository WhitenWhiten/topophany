package cloudflare

import (
	"context"
	"github.com/WhitenWhiten/topophany/internal/adapters"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Quota is shared by all API reads in a runner, including different tokens.
// Cloudflare's user quota spans tokens. The topology allocates a conservative
// share to each observer; other applications are accommodated by server headers.
// No request is retried within a sample. State contains no credentials.
type Quota struct {
	mu             sync.Mutex
	spacing        time.Duration
	next, cooldown time.Time
	failures       int
}

// NewQuota allocates 900 requests / five minutes across the topology's checks,
// leaving headroom under the default 1200 user limit. This assumes one active
// worker per observer and cannot coordinate unrelated deployments.
func NewQuota(localChecks, totalChecks int) *Quota {
	if localChecks < 1 {
		localChecks = 1
	}
	if totalChecks < localChecks {
		totalChecks = localChecks
	}
	return &Quota{spacing: time.Second * time.Duration(totalChecks) / time.Duration(3*localChecks)}
}

func (q *Quota) Wait(ctx context.Context) error {
	if q == nil {
		return nil
	}
	for {
		q.mu.Lock()
		now := time.Now()
		if now.Before(q.cooldown) {
			q.mu.Unlock()
			return adapters.Local("adapter_api", "api_rate_limited")
		}
		delay := q.next.Sub(now)
		if delay <= 0 {
			q.next = now.Add(q.spacing)
			q.mu.Unlock()
			return nil
		}
		q.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return adapters.Local("adapter_api", "api_rate_limited")
		case <-timer.C:
		}
	}
}

func (q *Quota) Observe(status int, headers http.Header) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	delay := time.Duration(0)
	if status == 429 || status >= 500 {
		q.failures = min(q.failures+1, 5)
		delay = min(time.Duration(1<<uint(q.failures-1))*30*time.Second, 5*time.Minute)
	} else if status == 200 {
		q.failures = 0
	}
	if status == 429 || status == 503 {
		if seconds, err := strconv.Atoi(headers.Get("Retry-After")); err == nil && seconds >= 0 {
			delay = max(delay, time.Duration(min(seconds, 3600))*time.Second)
		} else if at, err := http.ParseTime(headers.Get("Retry-After")); err == nil {
			delay = max(delay, min(at.Sub(now), time.Hour))
		}
	}
	// Each service item independently names remaining requests and reset seconds.
	// Unknown/malformed items are ignored; never export raw header values.
	for _, item := range strings.Split(headers.Get("Ratelimit"), ",") {
		remaining, reset := -1, -1
		for _, part := range strings.Split(item, ";") {
			pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(pair) != 2 {
				continue
			}
			n, err := strconv.Atoi(pair[1])
			if err != nil || n < 0 {
				continue
			}
			switch pair[0] {
			case "r":
				remaining = n
			case "t":
				reset = n
			}
		}
		if remaining == 0 && reset >= 0 {
			delay = max(delay, time.Duration(min(reset, 3600))*time.Second)
		}
	}
	if delay > 0 {
		q.cooldown = maxTime(q.cooldown, now.Add(delay))
	}
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
