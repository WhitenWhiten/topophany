package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/management"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/runner"
)

// A collector or worker reloads its reviewed topology on SIGHUP, as a
// restart would but without one: an invalid file, a different project or
// a vanished observer is refused and the running topology kept. Only
// checks whose measurement contract changed start afresh. Windows has no
// SIGHUP; restart there instead.

// hangups delivers the requests to reload: SIGHUP, or App.Reload in tests.
func (a *App) hangups() (<-chan os.Signal, func()) {
	if a.Reload != nil {
		return a.Reload, func() {}
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}

// probing is a scheduler running in the background until stopped.
type probing struct {
	*runner.Scheduler
	cancel context.CancelFunc
	done   chan struct{}
}

// background runs s under ctx, counted in wg. A failure of its own, not its
// being stopped, reaches failures.
func background(ctx context.Context, wg *sync.WaitGroup, s *runner.Scheduler, failures chan<- error) *probing {
	sub, cancel := context.WithCancel(ctx)
	p := &probing{s, cancel, make(chan struct{})}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(p.done)
		if e := s.Run(sub); e != nil && sub.Err() == nil {
			select {
			case failures <- e:
			default:
			}
		}
	}()
	return p
}

// stop ends the scheduler and waits for it; samples in flight are dropped,
// as on shutdown.
func (p *probing) stop() {
	if p != nil {
		p.cancel()
		<-p.done
	}
}

// reloaded reads a topology again for a process running c: it must keep
// c's project and, for a process that probes, declare its observer.
func reloaded(path string, c model.Config, observer string) (model.Config, error) {
	next, e := config.Load(path)
	switch {
	case e != nil:
		return next, e
	case next.Project != c.Project:
		return next, fmt.Errorf("project %q cannot become %q without a restart", c.Project, next.Project)
	case observer != "" && !declares(next, observer):
		return next, fmt.Errorf("observer %q is no longer declared", observer)
	}
	return next, nil
}

func declares(c model.Config, observer string) bool {
	for _, o := range c.Observers {
		if o.ID == observer {
			return true
		}
	}
	return false
}

// changes says what a reload from old to next means for the checks: which
// measure something new and so start afresh, and how many come and go.
func changes(old, next model.Config) string {
	before, after := config.Contracts(old), config.Contracts(next)
	changed := []string{}
	added, removed := 0, 0
	for id, contract := range after {
		if was, ok := before[id]; !ok {
			added++
		} else if was != contract {
			changed = append(changed, id)
		}
	}
	for id := range before {
		if _, ok := after[id]; !ok {
			removed++
		}
	}
	sort.Strings(changed)
	out := "every check measures what it did"
	if len(changed) > 0 {
		out = fmt.Sprintf("%d %s something new and start afresh: %s", len(changed), map[bool]string{true: "check measures", false: "checks measure"}[len(changed) == 1], strings.Join(changed, ", "))
	}
	if added+removed > 0 {
		out += fmt.Sprintf("; %d added, %d removed", added, removed)
	}
	return out
}

// prepareScheduler wires and validates the complete execution plan before a
// caller publishes the new topology or stops its old scheduler. Both native
// observations and source fan-outs use the same durability sink.
func prepareScheduler(c model.Config, observer string, workers int, sink runner.BatchSink, agents ...*management.Agent) (*runner.Scheduler, error) {
	run := probe.NewRunner(c, observer).Run
	guards := make([]func() error, len(agents))
	for i, agent := range agents {
		guards[i] = agent.Guard(c, observer)
	}
	guarded := func(ctx context.Context, c model.Config, x model.Check) model.Observation {
		for _, guard := range guards {
			if e := guard(); e != nil {
				evidence := "measured"
				if x.Kind == "host" || x.Kind == "mihomo" || x.Kind == "cloudflare" || x.Kind == "cloudflared" || x.Kind == "tailscale" || x.Kind == "wireguard" {
					evidence = "reported"
				}
				return model.Observation{Check: x.ID, SampledAt: time.Now().UTC(), Outcome: "error", Evidence: evidence, Code: "management_policy_denied", Stage: "setup", Detail: "local management policy refused execution"}
			}
		}
		for _, agent := range agents {
			ctx = managedNetwork(ctx, agent)
		}
		return run(ctx, c, x)
	}
	s, err := runner.New(c, observer, workers, guarded, func(v model.Observation) error { return sink([]model.Observation{v}) })
	if err != nil {
		return nil, err
	}
	var rt runner.SourceRuntime = probe.NewSourceRuntime(probe.ProductionSourceAdapters())
	for i, agent := range agents {
		if agent != nil {
			rt = managedSources{runtime: rt, agent: agent, guard: guards[i]}
		}
	}
	s.WithSources(rt, sink, 4)
	if err = s.ValidateExecution(); err != nil {
		return nil, err
	}
	return s, nil
}
