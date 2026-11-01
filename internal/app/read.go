package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/rollup"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// Exit statuses besides 0, success, and 1, a command that failed.
const (
	ExitAttention   = 2 // topo status: something needs attention
	ExitNoEvidence  = 3 // topo status: nothing current is known
	ExitUnreachable = 4 // no collector answered
)

// ExitError ends topo with Code, reporting Err first when there is one.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "exit status " + strconv.Itoa(e.Code)
}

func (e *ExitError) Unwrap() error { return e.Err }

// reader is where the read commands get evidence.
type reader struct {
	snapshot  func(ctx context.Context, observer string) (model.Snapshot, string, error)
	history   func(ctx context.Context, check string, limit int) (client.History, string, error)
	historyAt func(context.Context, client.HistoryView, int) (client.History, string, error)
	rollups   func(ctx context.Context, check, step, span string) (client.Rollups, string, error)
}

// unreachable gives a read that no collector answered its exit status.
func unreachable(e error) error {
	if errors.Is(e, client.ErrNoCollector) {
		return &ExitError{ExitUnreachable, e}
	}
	return e
}

func remote(c *client.Client) reader {
	return reader{
		historyAt: c.HistoryAt,
		snapshot: func(ctx context.Context, observer string) (model.Snapshot, string, error) {
			s, server, e := c.Snapshot(ctx, observer)
			return s, server, unreachable(e)
		},
		history: func(ctx context.Context, check string, limit int) (client.History, string, error) {
			h, server, e := c.History(ctx, check, limit)
			return h, server, unreachable(e)
		},
		rollups: func(ctx context.Context, check, step, span string) (client.Rollups, string, error) {
			r, server, e := c.Rollups(ctx, check, step, span)
			return r, server, unreachable(e)
		},
	}
}

// previewed is a read under topo preview: the synthetic evidence, the
// moment it is read at, and the defaults its own flags give --observer and
// --json.
type previewed struct {
	config   model.Config
	sim      *preview.Sim
	now      time.Time
	observer string
	json     bool
}

func (p *previewed) reader() reader {
	const server = "synthetic"
	return reader{
		snapshot: func(_ context.Context, observer string) (model.Snapshot, string, error) {
			known := observer == ""
			for _, o := range p.config.Observers {
				known = known || o.ID == observer
			}
			if !known {
				return model.Snapshot{}, "", fmt.Errorf("unknown observer %q", observer)
			}
			s, e := p.sim.Snapshot(observer, p.now)
			if e != nil {
				return s, "", &ExitError{ExitUnreachable, e}
			}
			return s, server, nil
		},
		history: func(_ context.Context, check string, limit int) (client.History, string, error) {
			if _, ok := config.Check(p.config, check); !ok {
				return client.History{}, "", fmt.Errorf("no check %q", check)
			}
			entries, e := p.sim.History(check, limit, p.now)
			if e != nil {
				return client.History{}, "", &ExitError{ExitUnreachable, e}
			}
			// As a collector would serve them: public, with no revision.
			contract := config.PublicContracts(p.config)[check]
			for i := range entries {
				entries[i].Contract, entries[i].Revision = contract, ""
			}
			return client.History{Project: p.config.Project, Revision: config.PublicRevision(p.config), Check: check, Contract: contract, Entries: entries}, server, nil
		},
		rollups: func(_ context.Context, check, step, span string) (client.Rollups, string, error) {
			if _, ok := config.Check(p.config, check); !ok {
				return client.Rollups{}, "", fmt.Errorf("no check %q", check)
			}
			width, length, e := layout(step, span)
			if e != nil {
				return client.Rollups{}, "", e
			}
			total, slots, since, e := p.sim.Rollups(check, width, length, p.now)
			if e != nil {
				return client.Rollups{}, "", &ExitError{ExitUnreachable, e}
			}
			return client.Rollups{Project: p.config.Project, Revision: config.PublicRevision(p.config), Check: check, Contract: config.PublicContracts(p.config)[check], GeneratedAt: p.now.UTC(), Step: int64(width / time.Second), Since: since, Total: total, Slots: slots}, server, nil
		},
	}
}

// layout reads a step and a span as a collector does for /v1/rollups.
func layout(step, span string) (time.Duration, time.Duration, error) {
	width, ok := rollup.Steps[step]
	if !ok {
		return 0, 0, errors.New("--step must be 5m, 1h or 1d")
	}
	length := rollup.DefaultSpan(width)
	if span != "" {
		var e error
		if length, e = rollup.ParseSpan(span); e != nil {
			return 0, 0, errors.New("--span " + e.Error())
		}
	}
	if e := rollup.Layout(width, length); e != nil {
		return 0, 0, e
	}
	return width, length, nil
}

// tabNames are the names show takes, in the TUI's order, then the map.
var tabNames = func() []string {
	out := []string{}
	for _, t := range view.Tabs {
		out = append(out, strings.ToLower(t))
	}
	return append(out, "map")
}()

const mapTab = -1

func tabIndex(name string) (int, bool) {
	for i, t := range tabNames {
		if strings.EqualFold(t, name) {
			if t == "map" {
				return mapTab, true
			}
			return i, true
		}
	}
	return 0, false
}

// parseMixed parses flags that may come before, between or after the
// positional arguments, and returns those. IDs never start with "-".
func parseMixed(f *flag.FlagSet, args []string) ([]string, error) {
	pos := []string{}
	for {
		if e := f.Parse(args); e != nil {
			return nil, e
		}
		if f.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, f.Arg(0))
		args = f.Args()[1:]
	}
}

// read runs status, show or history against the collector the connection
// flags choose or, under topo preview, against its synthetic evidence.
func (a *App) read(ctx context.Context, cmd string, args []string, pv *previewed) error {
	f := a.flags(cmd)
	mode := addDirectFlags(f)
	var connect func() (*client.Client, error)
	observerDefault, jsonDefault := "", false
	if pv == nil {
		connect = a.connection(f)
	} else {
		observerDefault, jsonDefault = pv.observer, pv.json
	}
	observer := new(string)
	if cmd != "history" {
		observer = f.String("observer", observerDefault, "observer view (default all)")
	}
	asJSON := f.Bool("json", jsonDefault, "print the view as JSON")
	limit := new(int)
	step, span := new(string), new(string)
	if cmd == "history" {
		limit = f.Int("limit", 20, "samples to read, newest first (1..500)")
		step = f.String("step", "", "read rollups in slots of 5m, 1h or 1d instead of samples")
		span = f.String("span", "", "how far back rollups reach, such as 24h, 7d or 90d (default a day of 5m, a week of 1h, 90 days of 1d)")
	}
	pos, e := parseMixed(f, args)
	if e != nil {
		return e
	}
	tab := 0
	switch cmd {
	case "problems":
		if len(pos) > 1 {
			return errors.New("usage: topo problems [GROUP_ID] [--json]")
		}
	case "status":
		if len(pos) != 0 {
			return errors.New("usage: topo status [--observer ID] [--json]")
		}
	case "show":
		ok := len(pos) == 1 || len(pos) == 2
		if ok {
			tab, ok = tabIndex(pos[0])
		}
		if !ok || tab == mapTab && len(pos) == 2 {
			return errors.New("usage: topo show TAB [ID] [--observer ID] [--json]; TAB is one of " + strings.Join(tabNames, ", ") + " (the map takes no ID)")
		}
	case "history":
		if *span != "" && *step == "" {
			*step = "1h"
		}
		if len(pos) != 1 || *limit < 1 || *limit > 500 {
			return errors.New("usage: topo history CHECK [--limit 1..500 | --step 5m|1h|1d [--span 7d]] [--json]")
		}
		if *step != "" {
			if _, _, e := layout(*step, *span); e != nil {
				return e
			}
		}
	}
	if pv == nil {
		if e := mode.validate(f); e != nil {
			return e
		}
	} else if *mode.source != "client" {
		return errors.New("preview cannot be combined with --source")
	}
	var r reader
	if pv == nil && *mode.source == "direct" {
		if cmd == "history" {
			return &direct.Error{Code: "history_unsupported_in_direct_mode", Message: "History is not supported in direct mode."}
		}
		s, _, e := a.openDirect(ctx, f, mode)
		if e != nil {
			return e
		}
		defer s.Close()
		a.waitDirect(ctx, s, mode)
		r = a.directReader(s)
	} else if pv == nil {
		c, e := connect()
		if e != nil {
			return e
		}
		defer c.Close()
		r = remote(c)
	} else {
		r = pv.reader()
	}
	now := a.now
	if pv != nil {
		now = func() time.Time { return pv.now }
	}
	if cmd == "history" && *step != "" {
		return a.rollups(ctx, r, pos[0], *step, *span, *asJSON, now)
	}
	if cmd == "history" {
		return a.history(ctx, r, pos[0], *limit, *asJSON, now)
	}
	snap, server, e := r.snapshot(ctx, *observer)
	if e != nil {
		return e
	}
	at := now()
	v := view.View{Snapshot: snap, Now: at, Cached: view.Expired(snap, at)}
	h := header{Project: snap.Project, Instance: snap.Instance, Server: server, Observer: snap.ViewObserver, GeneratedAt: snap.GeneratedAt, Cached: v.Cached}
	if snap.Direct != nil {
		status, _ := v.SourcesStatus()
		h.Source = "direct"
		h.Profile = snap.Direct.Profile
		h.ProfileRevision = snap.Direct.ProfileRevision
		h.DatasetID = snap.Direct.DatasetID
		h.Sources = &status
	}
	if pv != nil {
		h.Scenario, _, _ = strings.Cut(pv.sim.Scenarios()[pv.sim.Scenario()], "\t")
	}
	switch {
	case cmd == "problems":
		id := ""
		if len(pos) > 0 {
			id = pos[0]
		}
		return a.problems(v, h, id, *asJSON)
	case cmd == "status":
		return a.status(v, h, *asJSON)
	case tab == mapTab:
		return a.showMap(v, h, *asJSON)
	case len(pos) == 2:
		return a.showEntry(ctx, r, v, h, tab, pos[1], *asJSON)
	}
	return a.showTab(v, h, tab, *asJSON)
}

// short is the head of a digest, as revisions and contracts are shown.
func short(digest string) string { return digest[:min(12, len(digest))] }

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// scenarios lists the preview's scenarios, the one in effect marked.
func (a *App) scenarios(args []string, ids, list []string, current int, jsonDefault bool) error {
	f := a.flags("scenarios")
	asJSON := f.Bool("json", jsonDefault, "print the scenarios as JSON")
	if e := parsed(f, args); e != nil {
		return e
	}
	type scenario struct {
		Index   int    `json:"index"`
		ID      string `json:"id"`
		Name    string `json:"name"`
		Detail  string `json:"detail"`
		Current bool   `json:"current"`
	}
	out := []scenario{}
	for i, s := range list {
		name, detail, _ := strings.Cut(s, "\t")
		out = append(out, scenario{i, ids[i], name, detail, i == current})
	}
	if *asJSON {
		return printJSON(a.Out, out)
	}
	t := &text{w: a.Out}
	cells := [][]string{{"#", "SCENARIO", "WHAT HAPPENS"}}
	for _, s := range out {
		mark := strconv.Itoa(s.Index)
		if s.Current {
			mark += "*"
		}
		cells = append(cells, []string{mark, s.Name, s.Detail})
	}
	t.table(cells)
	return t.err
}
