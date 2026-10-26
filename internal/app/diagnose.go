package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/WhitenWhiten/topophany/internal/session"
)

func (a *App) diagnostics(command string, args []string) error {
	f := a.flags(command)
	id := f.String("session", "", "running viewer ID from topo sessions")
	asJSON := f.Bool("json", false, "emit the exact viewer diagnostic state as JSON")
	rootDefault, err := session.DefaultRoot()
	if err != nil {
		return err
	}
	root := f.String("sessions-dir", rootDefault, "private local session directory")
	if err = parsed(f, args); err != nil {
		return err
	}
	now := a.Now
	if now == nil {
		now = time.Now
	}
	if command == "sessions" {
		if *id != "" {
			return errors.New("use topo diagnose --session ID")
		}
		states, err := session.List(*root, now())
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(a.Out, states)
		}
		for _, state := range states {
			fmt.Fprintf(a.Out, "%s  pid=%d  %s  observer=%s  %s\n", state.ID, state.PID, state.Project, state.Observer, state.Server)
		}
		return nil
	}
	if *id == "" {
		return errors.New("--session is required; use topo sessions to find a running viewer")
	}
	state, err := session.Read(*root, *id, now())
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a.Out, state)
	}
	fmt.Fprintf(a.Out, "Session %s  live=%t\nCollector: %s\nObserver: %s\nSnapshot: %s  cached=%t\nTab: %s  search=%q\nError: %s\nNotice: %s\nHistory: %s  %s\n", state.ID, state.Live, state.Server, state.Observer, state.SnapshotTime.Format(time.RFC3339), state.Cached, state.Tab, state.Filters.Search, state.Error, state.Notice.Text, state.History.Check, state.History.Error)
	for _, event := range state.Events {
		fmt.Fprintf(a.Out, "%s  %s  %s  %s\n", event.At.Format(time.RFC3339), event.Kind, event.Code, event.Message)
	}
	return nil
}
