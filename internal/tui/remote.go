//go:build !js || collector

package tui

import (
	"context"
	"runtime"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/session"
)

// Remote is the native read-only viewer driven by browser events. Only this
// owner's calls mutate its Model; background reads return through bounded
// channels and never block keyboard handling or frame rendering.
type Remote struct {
	diagnostics *session.Writer
	m           *Model
	client      *client.Client
	ctx         context.Context
	cancel      context.CancelFunc
	results     chan remoteSnapshot
	samples     sampler
	encoder     cellEncoder
	requested   time.Time
	painted     time.Time
	busy        bool
	closed      bool
}

type remoteSnapshot struct {
	observer string
	snapshot model.Snapshot
	server   string
	err      error
	took     time.Duration
}

func NewRemote(profile client.Profile, reconnectHint string) (*Remote, error) {
	c, err := client.New(profile)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Model{Collectors: c.URLs(), Project: c.Project(), Colors: colorTrue,
		Now: time.Now(), Loading: "connecting to the collector", NerdFont: true}
	m.requestDiagnostics = c.Diagnostics
	m.initializeStartup(nil)
	m.ReconnectHint = secure.Text(reconnectHint[:min(len(reconnectHint), 1024)])
	m.NerdFont = true
	r := &Remote{m: m, client: c, ctx: ctx, cancel: cancel, results: make(chan remoteSnapshot, 1)}
	if runtime.GOOS != "js" {
		root, err := session.DefaultRoot()
		if err != nil {
			r.Close()
			return nil, err
		}
		r.diagnostics, err = session.New(root)
		if err != nil {
			r.Close()
			return nil, err
		}
	}
	r.samples = sampler{results: make(chan fetchedSamples, 1), readAt: collectorHistory(c)}
	return r, nil
}

func (r *Remote) Close() {
	if !r.closed {
		r.closed = true
		r.cancel()
		r.client.Close()
		if r.diagnostics != nil {
			r.diagnostics.Close()
		}
	}
}

func (r *Remote) Key(key string, now time.Time) {
	if r.closed {
		return
	}
	r.m.Now = now
	switch cmd := r.m.Key(key); cmd {
	case "quit":
		r.Close()
	case "refresh":
		r.requested = time.Time{}
	case "view", "observe":
		// v steps to the next observer; a touch screen's menu has already
		// picked one.
		if cmd == "view" {
			r.m.NextObserver()
		}
		r.m.Snapshot = model.Snapshot{Topology: r.m.Snapshot.Topology}
		r.m.Error, r.m.Loading = "", "loading the observer view"
		r.requested = time.Time{}
	}
}

func (r *Remote) request(now time.Time) {
	if r.busy || r.closed {
		return
	}
	r.busy, r.m.Fetching, r.requested = true, true, now
	if r.m.Error != "" {
		r.m.diagnosticEvent("retry", "snapshot_retry", "retrying snapshot read")
	}
	observer := r.m.Observer
	go func() {
		start := time.Now()
		snap, server, err := r.client.Snapshot(r.ctx, observer)
		select {
		case r.results <- remoteSnapshot{observer, snap, server, err, time.Since(start)}:
		case <-r.ctx.Done():
		}
	}()
}

func (r *Remote) receive() bool {
	changed := false
	select {
	case got := <-r.results:
		r.busy, r.m.Fetching, changed = false, false, true
		if got.observer != r.m.Observer {
			r.requested = time.Time{}
			break
		}
		r.m.Loading = ""
		r.m.perf.fetch, r.m.perf.fetches = got.took, r.m.perf.fetches+1
		if got.err != nil {
			r.m.keepFetchError(got.err)
		} else {
			r.m.Snapshot, r.m.Server = got.snapshot, got.server
			r.m.keepFetchError(nil)
		}
		r.samples.request(r.ctx, r.m, true)
	default:
	}
	select {
	case got := <-r.samples.results:
		changed = true
		r.samples.keep(r.m, got)
	default:
	}
	return changed
}

func (r *Remote) Frame(now time.Time, cols, rows int, force bool) *DemoFrame {
	m := r.m
	cols, rows = min(240, max(30, cols)), min(90, max(10, rows))
	resized := m.Width != cols || m.Height != rows
	m.Width, m.Height, m.Now = cols, rows, now
	changed := false
	if !r.closed {
		changed = r.receive()
		if r.requested.IsZero() || now.Sub(r.requested) >= retryEvery {
			r.request(now)
		}
		r.samples.request(r.ctx, m, false)
	}
	if !force && !resized && !changed && (r.closed || now.Sub(r.painted) < m.Animating()) {
		return nil
	}
	cv := m.draw()
	if len(r.encoder.styles) > 8192 {
		r.encoder = cellEncoder{}
	}
	f := &DemoFrame{CellFrame: r.encoder.encode(cv), Title: m.title(), Observer: m.Observer,
		Tab: m.Tab, Searching: m.Searching, Closed: r.closed, Device: m.deviceID()}
	if force || resized || changed {
		f.Text = strings.Join(cv.Lines(colorNone), "\n")
	}
	if r.diagnostics != nil {
		if err := r.diagnostics.Publish(r.m.Diagnostics(), now); err != nil {
			r.m.say("Session diagnostics write failed", true)
		}
	}
	r.painted = now
	return f
}

func (r *Remote) Measured(render time.Duration, bytes int, at time.Time) {
	r.m.measured(render, bytes, at)
}

// Diagnostics is the same state published for native session inspection.
func (r *Remote) Diagnostics() session.State { return r.m.Diagnostics() }
