package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Fetch func(context.Context) (Input, error)

// Run isolates fetches and file writes from the sampling/ingestion path. All
// queues have capacity one; only display frames may be coalesced. A nil output
// publishes only in memory, for an embedding consumer. An optional prepared
// input lets a source with immediate evidence publish it in the first frame.
func Run(ctx context.Context, c Config, fetch Fetch, out *Output, hub *Hub, report func(string), initial ...Input) {
	RunClock(ctx, c, fetch, out, hub, report, time.Now, initial...)
}

// RunClock injects the projection clock; scheduling remains independent.
func RunClock(ctx context.Context, c Config, fetch Fetch, out *Output, hub *Hub, report func(string), now func() time.Time, initial ...Input) {
	RunClockWithRefresh(ctx, c, fetch, out, hub, report, now, nil, initial...)
}

// RunClockWithRefresh accepts coalesced refresh hints. A hint never starts a
// concurrent fetch; hints arriving during a fetch are satisfied by that fetch.
// The caller owns the channel and must not close it while the runtime is live.
func RunClockWithRefresh(ctx context.Context, c Config, fetch Fetch, out *Output, hub *Hub, report func(string), now func() time.Time, refresh <-chan struct{}, initial ...Input) {
	if now == nil {
		now = time.Now
	}
	if hub == nil {
		hub = &Hub{}
	}
	if report == nil {
		report = func(string) {}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var frames <-chan Panel
	var unsubscribe func()
	if out != nil {
		var err error
		frames, unsubscribe, err = hub.Subscribe()
		if err != nil {
			report("monitor subscriber limit reached")
			return
		}
	}
	incoming := make(chan Input, 1)
	producerDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(min(c.Every(), 5*time.Second))
		defer tick.Stop()
		for {
			request, done := context.WithTimeout(ctx, 5*time.Second)
			in, e := fetch(request)
			done()
			// The channel has capacity one. Do not queue a redundant fetch when
			// a reader asks for freshness while a request is already in flight.
			select {
			case <-refresh:
			default:
			}
			if e != nil {
				in.Unavailable = true
			}
			select {
			case <-incoming:
			default:
			}
			select {
			case incoming <- in:
			case <-ctx.Done():
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			case <-refresh:
			}
		}
	})
	if out != nil {
		wg.Go(func() {
			defer unsubscribe()
			failed := false
			var failures uint64
			var lastReport time.Time
			write := func(p Panel, force bool) {
				if e := out.Write(p, c.Every(), force); e != nil {
					failures++
					if !failed || time.Since(lastReport) >= time.Minute {
						report(fmt.Sprintf("monitor output unavailable; retrying; failed_writes=%d", failures))
						lastReport = time.Now()
					}
					failed = true
				} else if failed {
					report("monitor output recovered")
					failed = false
					failures = 0
				}
			}
			for {
				select {
				case <-ctx.Done():
					<-producerDone
					if p, ok := hub.Latest(); ok {
						write(p, true)
					}
					return
				case p := <-frames:
					write(p, false)
				}
			}
		})
	}
	instance := now().UTC().Format("20060102T150405.000000000")
	var sequence uint64
	in := Input{Unavailable: true}
	if len(initial) > 0 {
		in = initial[0]
	}
	publish := func() {
		sequence++
		p := Build(c, in, now())
		p.Instance = instance
		p.Sequence = sequence
		p.PublisherExpiresAt = p.GeneratedAt.Add(30 * time.Second)
		hub.Publish(p)
	}
	publish()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			if p, ok := hub.Latest(); ok {
				p.Sequence++
				p.GeneratedAt = now().UTC()
				p.PublisherExpiresAt = p.GeneratedAt
				p.Lifecycle = "stopped"
				for i := range p.Rows {
					p.Rows[i] = missing(p.Rows[i], "publisher_stopped")
				}
				hub.Publish(p)
			}
			close(producerDone)
			wg.Wait()
			return
		case in = <-incoming:
			publish()
		case <-tick.C:
			publish()
		}
	}
}
