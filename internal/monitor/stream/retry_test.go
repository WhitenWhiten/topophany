package stream

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/monitor"
)

func TestClientRetryKeepsSourceAndDeadlines(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lines := make(chan Line)
	frames := make(chan monitor.Panel, 20)
	var opens, closes atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, lines, func(context.Context, Request) (Source, *Error) {
			opens.Add(1)
			return Source{Initial: monitor.Input{Unavailable: true}, Fetch: func(ctx context.Context) (monitor.Input, error) {
				<-ctx.Done()
				return monitor.Input{}, ctx.Err()
			}, Close: func() { closes.Add(1) }}, nil
		}, func(p monitor.Panel) error { frames <- p; return nil })
	}()
	r := request()
	r.Source, r.Lang = "client", ""
	lines <- line(r)
	first := <-frames
	for _, nonce := range []string{"wake", "wake", "open-panel"} {
		r.RequestID, r.RetryNonce = nonce, nonce
		lines <- line(r)
		var p monitor.Panel
		select {
		case p = <-frames:
		case <-ctx.Done():
			t.Fatal("no acknowledgment")
		}
		if p.RequestID != nonce || p.ConfigRevision != first.ConfigRevision || p.PublisherExpiresAt != first.PublisherExpiresAt || p.GeneratedAt != first.GeneratedAt || p.Sequence <= first.Sequence || opens.Load() != 1 || closes.Load() != 0 {
			t.Fatal("retry replaced source or renewed evidence", p, opens.Load(), closes.Load())
		}
	}
	r.Observer = "another"
	lines <- line(r)
	select {
	case p := <-frames:
		if p.ConfigRevision == first.ConfigRevision || opens.Load() != 2 || closes.Load() != 1 {
			t.Fatal("scope change reused source")
		}
	case <-ctx.Done():
		t.Fatal("no replacement")
	}
	close(lines)
	if err := <-done; err != nil || closes.Load() != 2 {
		t.Fatal(err, closes.Load())
	}
}

func TestRetryNonceSourceValidation(t *testing.T) {
	r := request()
	r.RetryNonce = "wake"
	if _, e := Decode(line(r)); e == nil {
		t.Fatal("preview accepted retry")
	}
	r.Source, r.Lang = "client", ""
	if _, e := Decode(line(r)); e != nil {
		t.Fatal(e)
	}
	r.ProfileRevision = "revision"
	if _, e := Decode(line(r)); e == nil {
		t.Fatal("client accepted direct revision")
	}
}
