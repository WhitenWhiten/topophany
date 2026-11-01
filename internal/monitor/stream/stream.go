package stream

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/monitor"
)

// Source is an owned, prepared source. Open must honor ctx; Fetch follows the
// same five-second deadline contract as monitor.Run. Close releases resources.
type Source struct {
	Project string
	Errors  <-chan *Error
	Initial monitor.Input
	// Prepare validates a replacement's first read before the active source is
	// released. The first request still emits Initial immediately. A result
	// returned only because its context expired is treated as unavailable
	// evidence, not as a validated input.
	Prepare func(context.Context) (monitor.Input, *Error)
	Fetch   monitor.Fetch
	Close   func()
}
type Open func(context.Context, Request) (Source, *Error)
type Emit func(monitor.Panel) error

// Run serializes request changes, source errors and outgoing frames. Replacing
// a request cancels and joins its runtime before publishing the new revision.
// A rejected line never changes the running source or its cadence.
func Run(ctx context.Context, lines <-chan Line, open Open, emit Emit) error {
	return runClock(ctx, lines, open, emit, time.Now, prepareBudget)
}

// RunClock uses one evaluation/publication clock for embedding tests.
func RunClock(ctx context.Context, lines <-chan Line, open Open, emit Emit, now func() time.Time) error {
	return runClock(ctx, lines, open, emit, now, prepareBudget)
}

// runClock also exposes the replacement preparation budget to embedding
// tests. A non-positive budget disables the internal deadline; the caller's
// context still bounds preparation.
func runClock(ctx context.Context, lines <-chan Line, open Open, emit Emit, now func() time.Time, budget time.Duration) (result error) {
	if now == nil {
		now = time.Now
	}
	instance := now().UTC().Format("20060102T150405.000000000")
	var sequence, revision uint64
	var active Request
	var c monitor.Config
	var last monitor.Panel
	var digest string
	var frames <-chan monitor.Panel
	var sourceErrors <-chan *Error
	var cancel context.CancelFunc
	var done <-chan struct{}
	var unsubscribe func()
	var refresh chan struct{}
	var lastRetry time.Time
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			unsubscribe()
			cancel = nil
			frames = nil
			refresh = nil
			sourceErrors = nil
		}
	}
	defer stop()
	send := func(p monitor.Panel) error {
		sequence++
		p.Instance = instance
		p.Sequence = sequence
		p.Name = "stream"
		return emit(p.Clone())
	}
	reject := func(id string, e *Error) error {
		// Invalid identifiers and validation messages are untrusted request data.
		// Bound them before JSON escaping, including errors with no row payload.
		if len(id) > 64 || !utf8.ValidString(id) {
			id = ""
		}
		bounded := *e
		bounded.Message = boundedText(e.Message, 1024)
		now := now().UTC()
		return send(monitor.Panel{Schema: 1, Name: "stream", RequestID: id, Lifecycle: "error", Error: &bounded, GeneratedAt: now, PublisherExpiresAt: now.Add(30 * time.Second), Rows: []monitor.Value{}})
	}
	publish := func(p monitor.Panel) error {
		if p.Lifecycle != "running" {
			return nil
		}
		p.RequestID = active.RequestID
		if monitor.ShouldPublish(last, digest, p, c.Every()) {
			if err := send(p); err != nil {
				return err
			}
			last = p
			digest = monitor.ContentDigest(p)
		}
		return nil
	}
	stopAttempted := false
	stopped := func() error {
		stopAttempted = true
		stop()
		now := now().UTC()
		p := last
		p.Schema = 1
		p.Name = "stream"
		p.RequestID = active.RequestID
		p.Lifecycle = "stopped"
		p.Error = nil
		p.Rows = []monitor.Value{}
		p.GeneratedAt = now
		p.PublisherExpiresAt = now
		return send(p)
	}
	defer func() {
		// A normal frame may reach the pipe before its acknowledgment races
		// cancellation. That path must still join the source and attempt the
		// final frame, using the sink's bounded shutdown window.
		if !stopAttempted && ctx.Err() != nil && errors.Is(result, ctx.Err()) {
			result = stopped()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return stopped()
		case line, ok := <-lines:
			if !ok {
				return stopped()
			}
			request, e := Decode(line)
			if e != nil {
				if err := reject(request.RequestID, e); err != nil {
					return err
				}
				continue
			}
			if cancel != nil && request.Source == "client" && request.RetryNonce != "" && sameClientRequest(active, request) {
				// A retry is a hint for the existing source, not a replacement.
				// Repeated nonces acknowledge the request without another read.
				if request.RetryNonce != active.RetryNonce && (lastRetry.IsZero() || now().Sub(lastRetry) >= 5*time.Second) {
					lastRetry = now()
					select {
					case refresh <- struct{}{}:
					default:
					}
				}
				active = request
				last.RequestID = request.RequestID
				// Acknowledge without renewing either publication or evidence
				// clocks. The current runtime publishes the actual read result.
				if err := send(last); err != nil {
					return err
				}
				continue
			}
			source, e := open(ctx, request)
			if e != nil {
				if err := reject(request.RequestID, e); err != nil {
					return err
				}
				continue
			}
			if source.Prepare != nil && cancel != nil {
				var outputErr error
				source.Initial, e, outputErr = prepareReplacement(ctx, source, frames, &sourceErrors, publish, func(e *Error) error { return reject(active.RequestID, e) }, budget)
				if e != nil || ctx.Err() != nil || outputErr != nil {
					if source.Close != nil {
						source.Close()
					}
					if outputErr != nil {
						return outputErr
					}
					if ctx.Err() != nil {
						return stopped()
					}
					if err := reject(request.RequestID, e); err != nil {
						return err
					}
					continue
				}
			}
			stop()
			revision++
			active = request
			lastRetry = time.Time{}
			refresh = make(chan struct{}, 1)
			refreshes := refresh
			sourceErrors = source.Errors
			project := source.Project
			if project == "" {
				project = request.Project
			}
			c = monitor.Config{Schema: 1, Name: "stream", Revision: fmt.Sprintf("%s-%d", instance, revision), Source: request.Source, Project: project, Observer: request.Observer, Interval: request.Interval, Template: request.Template}
			runCtx, runCancel := context.WithCancel(ctx)
			hub := &monitor.Hub{}
			ch, unsub, _ := hub.Subscribe()
			ready := make(chan struct{})
			cancel, done, unsubscribe, frames = runCancel, ready, unsub, ch
			// Build the prepared first frame synchronously: even buffered EOF cannot
			// overtake it. Run publishes that same evidence before it starts polling.
			first := monitor.Build(c, source.Initial, now())
			first.RequestID = active.RequestID
			first.PublisherExpiresAt = first.GeneratedAt.Add(30 * time.Second)
			// The first frame may already be visible when its acknowledgment
			// races cancellation. Retain its configuration for the final frame.
			last = first
			if err := send(first); err != nil {
				runCancel()
				unsub()
				cancel = nil
				if source.Close != nil {
					source.Close()
				}
				return err
			}
			digest = monitor.ContentDigest(first)
			go func() {
				defer close(ready)
				if source.Close != nil {
					defer source.Close()
				}
				monitor.RunClockWithRefresh(runCtx, c, source.Fetch, nil, hub, nil, now, refreshes, source.Initial)
			}()
		case e, ok := <-sourceErrors:
			if !ok {
				sourceErrors = nil
			} else if e != nil {
				if err := reject(active.RequestID, e); err != nil {
					return err
				}
			}
		case p := <-frames:
			if err := publish(p); err != nil {
				return err
			}
		}
	}
}

func sameClientRequest(a, b Request) bool {
	a.RequestID, b.RequestID, a.RetryNonce, b.RetryNonce = "", "", "", ""
	return reflect.DeepEqual(a, b)
}

func boundedText(s string, limit int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= limit {
		return s
	}
	s = s[:limit-3]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "..."
}

// WriteFrame makes exactly one write, then flushes sinks that support flushing.
// Oversized display values are withheld, preserving the template and deadlines.
func WriteFrame(w interface{ Write([]byte) (int, error) }, p monitor.Panel) error {
	data, err := monitor.MarshalPanel(p)
	if err != nil {
		return err
	}
	n, err := w.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("short frame write")
	}
	if f, ok := w.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}
