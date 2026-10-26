package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/logging"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/outbox"
	"github.com/WhitenWhiten/topophany/internal/runner"
)

func (a *App) worker(parent context.Context, args []string) (retErr error) {
	f := a.flags("worker")
	panelFlags := addMonitorFlags(f)
	logs := addLogFlags(f)
	memory := addMemoryFlags(f)
	path := f.String("config", "topology.json", "reviewed LOCAL topology; never downloaded as executable work")
	observer := f.String("observer", "", "this executor's observer identity")
	credPath := f.String("credentials", "worker-credentials.json", "0600 worker credential file")
	profileName := f.String("profile", "", "credential profile (default active)")
	dir := f.String("data", "./worker-data", "exclusive durable outbox directory")
	budget := f.Int64("outbox-bytes", 32<<20, "hard budget for pending observations; quarantine has its own, at least as large")
	maxRecords := f.Int("outbox-records", 20000, "hard pending record limit (1..1000000); bounds the in-memory index")
	heartbeatInterval := f.Duration("heartbeat-interval", 15*time.Second, "idle heartbeat interval (1s..1m), capped at one third of observer stale_seconds")
	workers := f.Int("concurrency", 4, "bounded probe concurrency (1..32)")
	if e := parsed(f, args); e != nil {
		return e
	}
	if *heartbeatInterval < time.Second || *heartbeatInterval > time.Minute {
		return errors.New("heartbeat-interval must be 1s..1m")
	}
	stopMemory, e := memory.apply()
	if e != nil {
		return e
	}
	defer stopMemory()
	finishLog, logErr := a.serviceLog(logs)
	if logErr != nil {
		return logErr
	}
	defer func() { finishLog(retErr) }()
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	panel, panelPath, e := panelFlags.load("worker", c.Project, *observer)
	if e != nil {
		return e
	}
	var localView *monitor.Local
	if panel != nil {
		localView = monitor.NewLocal(c, *observer)
	}
	creds, e := client.Load(*credPath)
	if e != nil {
		return e
	}
	name := *profileName
	if name == "" {
		name = creds.Active
	}
	p, ok := creds.Profiles[name]
	if !ok || p.Project != c.Project {
		return errors.New("worker credentials project/profile mismatch")
	}
	// Authenticate and collapse aliases before allocating independent recipients.
	resolved := p
	var notes []string
	needsIdentity := len(p.Servers) > 1
	for _, ep := range p.Servers {
		needsIdentity = needsIdentity || len(ep.AlternateURLs) > 0 || ep.CollectorID != ""
	}
	if needsIdentity {
		identifyCtx, identifyCancel := context.WithTimeout(parent, 15*time.Second)
		resolved, notes, e = client.ResolveDeliveryTargets(identifyCtx, p)
		identifyCancel()
		if e != nil {
			return e
		}
	}
	for _, note := range notes {
		fmt.Fprintln(a.Err, note)
	}
	d, e := outbox.NewDelivery(resolved, config.Revision(c))
	if e != nil {
		return e
	}
	defer d.Close()
	q, e := outbox.OpenWithOptions(*dir, c.Project, outbox.Options{MaxBytes: *budget, MaxRecords: *maxRecords})
	if e != nil {
		return e
	}
	defer q.Close()
	if e = d.ConsolidateAliases(q); e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	stopPanel, e := a.attachMonitor(ctx, panel, panelPath, func(context.Context) (monitor.Input, error) {
		return monitor.Input{IO: ioaccount.Read(), Snapshot: localView.Snapshot(time.Now()), Delivery: d.Statuses()}, nil
	}, *dir, *path, *credPath)
	if e != nil {
		return e
	}
	defer stopPanel()
	// The checks under a topology; a reload replaces them.
	local := func(c model.Config) (*runner.Scheduler, error) {
		revision := config.Revision(c)
		batch := func(vs []model.Observation) error {
			if panel != nil {
				for _, v := range vs {
					localView.Record(v)
				}
			}
			err := q.Add(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: revision, Observations: vs}, d.Recipients())
			var degraded *outbox.DegradedError
			if errors.As(err, &degraded) {
				fmt.Fprintln(a.Err, degraded)
				return nil
			}
			return err
		}
		return prepareScheduler(c, *observer, *workers, batch)
	}
	s, e := local(c)
	if e != nil {
		return e
	}
	d.SetHeartbeatInterval(idleHeartbeatInterval(c, *observer, *heartbeatInterval))
	failed := make(chan error, 1)
	running := background(ctx, &wg, s, failed)
	contractsDigest := config.ObserverContracts(c, *observer)
	hup, stopHup := a.hangups()
	defer stopHup()
	// reload reads the topology again; samples already queued keep the
	// contracts they were taken under, and collectors judge them by those.
	reload := func() error {
		next, e := reloaded(*path, c, *observer)
		if e != nil {
			return e
		}
		s, e := local(next)
		if e != nil {
			return e
		}
		running.stop()
		d.SetHeartbeatInterval(idleHeartbeatInterval(next, *observer, *heartbeatInterval))
		if localView != nil {
			localView.Reload(next)
		}
		running = background(ctx, &wg, s, failed)
		d.SetRevision(config.Revision(next))
		contractsDigest = config.ObserverContracts(next, *observer)
		fmt.Fprintf(a.Err, "reloaded topology revision %s → %s; %s\n", config.Revision(c)[:12], config.Revision(next)[:12], changes(c, next))
		c = next
		return nil
	}
	fmt.Fprintf(a.Err, "Topophany worker observer=%s project=%s revision=%s; %d collectors\n", *observer, c.Project, config.Revision(c)[:12], len(p.Servers))
	a.logMemory()
	// Each collector is delivered to from a loop of its own, so one that is
	// slow to answer delays no other. The heartbeat they send is the one
	// this loop last made, of the topology it runs now.
	var beat atomic.Pointer[model.Heartbeat]
	heartbeat := func() {
		beat.Store(&model.Heartbeat{Observer: *observer, Boot: running.Boot, SampledAt: time.Now().UTC(), Contracts: contractsDigest})
	}
	heartbeat()
	repeats := &logging.Repeater{Out: a.Err, Every: time.Minute}
	wg.Go(func() {
		d.Run(ctx, q, 2*time.Second, beat.Load, func(r outbox.Result) {
			code := r.Code
			if code == "" && r.Error != "" {
				code = "delivery_retry"
			}
			repeats.Update("delivery", r.Recipient, code, time.Now())
		})
	})
	ioTick := time.NewTicker(time.Minute)
	defer ioTick.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-failed:
			return e
		case <-hup:
			if e := reload(); e != nil {
				fmt.Fprintf(a.Err, "reload refused; still running revision %s: %v\n", config.Revision(c)[:12], e)
			}
			heartbeat()
		case <-ioTick.C:
			a.logIO()
		case <-tick.C:
			heartbeat()
		}
	}
}

// Preserve at least three opportunities to report liveness per stale window.
// Delivery still wakes every two seconds to send new samples promptly.
func idleHeartbeatInterval(c model.Config, observer string, requested time.Duration) time.Duration {
	for _, o := range c.Observers {
		if o.ID == observer {
			return min(requested, time.Duration(o.StaleSeconds)*time.Second/3)
		}
	}
	return requested
}
