package management

import (
	"context"
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func ContractDigest(c model.Config, role, observer string) string {
	if role == "collector" {
		return Digest(Marshal(config.Contracts(c)))
	}
	return config.ObserverContracts(c, observer)
}
func (a *Agent) Paused() bool { return a != nil && a.pausedFlag.Load() }

func (a *Agent) requestOperation(ctx context.Context, operation string) error {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r := ApplyRequest{Operation: operation, Context: bounded, Done: make(chan error, 1)}
	select {
	case a.Changes <- r:
	case <-bounded.Done():
		return bounded.Err()
	}
	return <-r.Done
}
func (a *Agent) quiesce(ctx context.Context, c Command, p Policy) (Receipt, error) {
	a.pauseMu.Lock()
	defer a.pauseMu.Unlock()
	if a.paused {
		return a.store.Finish(c, "rejected", "already_quiescing", time.Now())
	}
	if e := a.requestOperation(ctx, "pause"); e != nil {
		return a.store.Finish(c, "failed", "configuration_pause_failed", time.Now())
	}
	a.paused = true
	a.pausedFlag.Store(true)
	a.pauseUntil = time.Now().Add(min(time.Duration(c.LeaseSeconds)*time.Second, time.Until(c.Expires)))
	a.pauseSession = p.Config.Session
	if a.Pending != nil {
		_, a.quarantineFloor = a.Pending()
	}
	r, e := a.store.Finish(c, "quiescing", "", time.Now())
	if e == nil {
		e = a.store.SetQuiesce(false, a.pauseUntil.UTC(), "")
	}
	if e != nil {
		_ = a.requestOperation(context.WithoutCancel(ctx), "resume")
		a.paused = false
		a.pausedFlag.Store(false)
	}
	return r, e
}
func (a *Agent) resume(ctx context.Context) error {
	a.pauseMu.Lock()
	defer a.pauseMu.Unlock()
	if !a.paused {
		return nil
	}
	if e := a.requestOperation(ctx, "resume"); e != nil {
		return e
	}
	a.paused = false
	a.pausedFlag.Store(false)
	return a.store.SetQuiesce(false, time.Time{}, "configuration_pause_canceled")
}
func (a *Agent) consumePause() error {
	a.pauseMu.Lock()
	defer a.pauseMu.Unlock()
	if !a.paused || !time.Now().Before(a.pauseUntil) || !a.store.Snapshot().Config.Quiesced {
		return errors.New("configuration_not_quiesced")
	}
	a.paused = false
	a.pausedFlag.Store(false)
	return nil
}
func (a *Agent) checkPause(ctx context.Context) {
	a.pauseMu.Lock()
	defer a.pauseMu.Unlock()
	if !a.paused {
		return
	}
	p, e := LoadPolicy(a.path)
	reason := ""
	if e != nil || !p.Config.Accept || p.Config.Session != a.pauseSession {
		reason = "configuration_pause_canceled"
	} else if !time.Now().Before(a.pauseUntil) {
		reason = "configuration_pause_expired"
	}
	pending, quarantine := 0, a.quarantineFloor
	if a.Pending != nil {
		pending, quarantine = a.Pending()
	}
	if quarantine != a.quarantineFloor {
		reason = "configuration_drain_quarantined"
	}
	if reason != "" {
		if a.requestOperation(ctx, "resume") == nil {
			a.paused = false
			a.pausedFlag.Store(false)
			_ = a.store.SetQuiesce(false, time.Time{}, reason)
		}
		return
	}
	if pending == 0 && !a.store.Snapshot().Config.Quiesced {
		_ = a.store.SetQuiesce(true, a.pauseUntil.UTC(), "")
	}
}
func (s *Store) SetQuiesce(ready bool, until time.Time, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state
	next.Config.Quiesced = ready
	next.Config.QuiesceUntil = until
	if next.Config.Last != nil && (next.Config.Last.State == "quiescing" || next.Config.Last.State == "quiesced") {
		r := *next.Config.Last
		r.At = time.Now().UTC()
		if ready {
			r.State = "quiesced"
		}
		if code != "" {
			r.State = "failed"
			r.Code = code
		}
		next.Config.Last = &r
	}
	if e := s.write(next); e != nil {
		return e
	}
	s.state = next
	return nil
}
func (s *Store) ClearInterruptedPause() error {
	snapshot := s.Snapshot()
	if snapshot.Config.QuiesceUntil.IsZero() {
		return nil
	}
	return s.SetQuiesce(false, time.Time{}, "configuration_pause_interrupted")
}
