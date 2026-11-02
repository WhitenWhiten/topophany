package stream

import (
	"context"
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/preview"
)

// OpenSource prepares saved client credentials, synthetic evidence, a direct
// profile or this machine's local checks from their default roots.
// Embedding callers may supply their own Open function to Run instead.
func OpenSource(ctx context.Context, r Request) (Source, *Error) {
	if r.Source == "local" {
		m := NewLocalManager("", time.Now)
		src, e := m.Open(ctx, r)
		if e != nil {
			m.Close()
			return src, e
		}
		closeHandle := src.Close
		src.Close = func() { closeHandle(); m.Close() }
		return src, nil
	}
	if r.Source == "direct" {
		m := NewDirectManager("", time.Now)
		src, e := m.Open(ctx, r)
		if e != nil {
			m.Close()
			return src, e
		}
		closeHandle := src.Close
		src.Close = func() { closeHandle(); m.Close() }
		return src, nil
	}
	return openSource(ctx, r, time.Now)
}
func openSource(ctx context.Context, r Request, now func() time.Time) (Source, *Error) {
	if r.Source == "preview" {
		c, err := preview.Preset(r.Lang)
		if err != nil {
			return Source{}, failure("invalid_request", "preview language unavailable")
		}
		sim := preview.New(c, now(), r.Lang)
		if r.Scenario >= len(sim.Scenarios()) {
			return Source{}, failure("invalid_request", "unknown preview scenario")
		}
		if r.Observer != "" {
			found := false
			for _, o := range c.Observers {
				found = found || o.ID == r.Observer
			}
			if !found {
				return Source{}, failure("invalid_request", "unknown preview observer")
			}
		}
		if r.Project != "" && r.Project != c.Project {
			return Source{}, failure("project_mismatch", "preview project does not match request")
		}
		sim.SetScenario(r.Scenario, now().Add(-30*time.Minute))
		book := monitor.NewSampleBook(r.Template)
		fetch := func(ctx context.Context) (monitor.Input, error) {
			if err := ctx.Err(); err != nil {
				return monitor.Input{}, err
			}
			at := now()
			s, err := sim.Snapshot(r.Observer, at)
			in := monitor.Input{Snapshot: s, Unavailable: err != nil}
			if err == nil && r.Observer != "" {
				all, e := sim.Snapshot("", at)
				if e == nil {
					in.AllSnapshot = &all
				}
			}
			if err == nil {
				in.Samples = book.Fill(ctx, s, "", -1, func(_ context.Context, id string) ([]model.Observation, error) {
					return sim.History(id, monitor.CardSamples, at)
				})
			}
			return in, err
		}
		initial, err := fetch(ctx)
		if err != nil {
			initial = monitor.Input{Unavailable: true}
		}
		return Source{Project: c.Project, Initial: initial, Fetch: fetch}, nil
	}
	path := r.Credentials
	if path == "" {
		var err error
		path, err = client.DefaultPath()
		if err != nil {
			return Source{}, failure("credentials_unavailable", "credential file unavailable")
		}
	}
	creds, err := client.Load(path)
	if err != nil {
		return Source{}, failure("credentials_unavailable", "credential file unavailable")
	}
	profile := r.Profile
	if profile == "" {
		profile = creds.Active
	}
	p, ok := creds.Profiles[profile]
	if !ok {
		return Source{}, failure("profile_not_found", "credential profile not found")
	}
	if r.Project != "" && p.Project != r.Project {
		return Source{}, failure("project_mismatch", "profile project does not match request")
	}
	cl, err := client.New(p)
	if err != nil {
		return Source{}, failure("credentials_unavailable", "credential profile unavailable")
	}
	sourceErrors := make(chan *Error, 1)
	reading := monitor.CollectorRead{State: "waiting"}
	initialReading := reading
	previous := monitor.Input{Unavailable: true, CollectorRead: &initialReading}
	book := monitor.NewSampleBook(r.Template)
	fetch := func(ctx context.Context) (monitor.Input, error) {
		started := time.Now()
		attempted := now().UTC()
		s, all, server, err := cl.SourcedViews(ctx, r.Observer)
		reading = collectorRead(reading, attempted, now().UTC(), time.Since(started), err, cl.Diagnostics())
		// Every result owns its diagnostics; subsequent reads cannot mutate a
		// previously published input, including the initial waiting frame.
		status := reading
		if errors.Is(err, client.ErrProjectMismatch) {
			select {
			case sourceErrors <- failure("project_mismatch", "snapshot project does not match request"):
			default:
			}
			in := previous
			in.CollectorRead = &status
			return in, nil
		}
		if err != nil {
			// A missed read does not invalidate evidence already acquired.
			// Keep its original clocks while reporting the failed acquisition;
			// Build will withhold it when those clocks actually expire. No
			// fallback across authentication, identity or malformed responses.
			if !previous.Unavailable && (errors.Is(err, client.ErrNoCollector) || errors.Is(err, context.DeadlineExceeded)) {
				in := previous
				in.CollectorRead = &status
				return in, nil
			}
			return monitor.Input{Unavailable: true, CollectorRead: &status}, err
		}
		in := monitor.Input{Snapshot: s, AllSnapshot: all, CollectorRead: &status}
		// History is read from the collector the snapshot came from, under
		// the measurement it names, or not at all.
		in.Samples = book.Fill(ctx, s, server+" "+s.Revision, monitor.HistoryReads, func(ctx context.Context, id string) ([]model.Observation, error) {
			h, _, err := cl.HistoryAt(ctx, client.SnapshotHistory(s, server, id), monitor.CardSamples)
			return h.Entries, err
		})
		previous = in
		return in, nil
	}
	prepare := func(ctx context.Context) (monitor.Input, *Error) {
		in, err := fetch(ctx)
		select {
		case e := <-sourceErrors:
			return monitor.Input{}, e
		default:
		}
		if err != nil {
			// An unreachable collector is a valid, unavailable source; a
			// positively identified foreign project is a rejected replacement.
			in.Unavailable = true
		}
		return in, nil
	}
	return Source{Project: p.Project, Initial: previous, Prepare: prepare, Fetch: fetch, Close: cl.Close, Errors: sourceErrors}, nil
}
