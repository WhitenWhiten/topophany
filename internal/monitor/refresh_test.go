package monitor

import (
	"context"
	"testing"
	"time"
)

func TestRefreshIsImmediateAndCoalescesInflight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	refresh := make(chan struct{}, 1)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	hub := &Hub{}
	frames, unsub, err := hub.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunClockWithRefresh(ctx, Config{Interval: "1h"}, func(ctx context.Context) (Input, error) {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return Input{}, nil
		}, nil, hub, nil, time.Now, refresh)
	}()
	<-started
	refresh <- struct{}{} // satisfied by the read in progress
	close(release)
	// Wait for publication of the completed read (the initial frame is unavailable).
	for {
		select {
		case <-frames:
			if len(refresh) == 0 {
				goto published
			}
		case <-ctx.Done():
			t.Fatal("read did not complete")
		}
	}
published:
	select {
	case <-started:
		t.Fatal("queued a second read during fetch")
	case <-time.After(100 * time.Millisecond):
	}
	refresh <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh waited for normal poll")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime leaked")
	}
}
