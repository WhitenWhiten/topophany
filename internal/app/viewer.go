package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

func (a *App) view(ctx context.Context, args []string) error {
	f := a.flags("topo")
	panelFlags := addMonitorFlags(f)
	connect := a.connection(f)
	mode := addDirectFlags(f)
	observer := f.String("observer", "", "observer view (default all)")
	jsonOut := f.Bool("json", false, "print one snapshot without terminal control")
	startupFlags := addPreferenceFlags(f)
	device := deviceFlag(f)
	noColor := f.Bool("no-color", os.Getenv("NO_COLOR") != "", "disable ANSI colors")
	if e := parsed(f, args); e != nil {
		return e
	}
	if _, e := withDevice(tui.Startup{}, *device); e != nil {
		return e
	}
	if e := mode.validate(f); e != nil {
		return e
	}
	if *mode.source == "direct" {
		return a.viewDirect(ctx, f, mode, *observer, *jsonOut, *noColor, *device, panelFlags, startupFlags)
	}
	c, e := connect()
	if e != nil {
		return e
	}
	defer c.Close()
	if *jsonOut && *panelFlags.name != "" {
		return errors.New("--monitor requires a running TUI; use monitor run for standalone output")
	}
	if *jsonOut {
		snap, _, e := c.Snapshot(ctx, *observer)
		if e != nil {
			return e
		}
		return printJSON(a.Out, snap)
	}
	if a.TerminalIn == nil || a.TerminalOut == nil {
		return errors.New("interactive terminal unavailable; use --json")
	}
	startup, err := startupFlags.startup()
	if err != nil {
		return err
	}
	if startup, err = withDevice(startup, *device); err != nil {
		return err
	}
	panel, path, e := panelFlags.load("client", c.Project(), *observer)
	if e != nil {
		return e
	}
	if panel == nil {
		return tui.Run(ctx, c, *observer, *noColor, a.TerminalIn, a.TerminalOut, startup)
	}
	saved, e := monitorClient(*panel)
	if e != nil {
		return e
	}
	same := saved.Project() == c.Project() && slices.Equal(saved.URLs(), c.URLs())
	saved.Close()
	if !same {
		return errors.New("monitor saved connection differs from TUI connection")
	}
	feed := &monitor.Feed{}
	feed.Set(monitor.Input{Unavailable: true})
	stop, e := a.attachMonitor(ctx, panel, path, func(context.Context) (monitor.Input, error) { return feed.Get(), nil })
	if e != nil {
		return e
	}
	defer stop()
	return tui.RunObserved(ctx, c, *observer, *noColor, a.TerminalIn, a.TerminalOut, func(s model.Snapshot, e error) { feed.Set(monitor.Input{Snapshot: s, Unavailable: e != nil}) }, startup)
}

// edit opens the local topology editor. It never contacts a collector: the
// reviewed file is the only source of truth, and changes apply on restart.
func (a *App) edit(ctx context.Context, args []string) error {
	f := a.flags("edit")
	path := f.String("config", "topology.json", "local topology JSON to edit (created on first save if missing)")
	project := f.String("project", "my-network", "project ID for a new topology file")
	apply := f.String("apply", "", "reviewed replacement topology; batch local edits")
	expected := f.String("expect-revision", "", "required previous revision when applying")
	expectedCandidate := f.String("expect-candidate-revision", "", "reviewed replacement revision from --dry-run")
	dry := f.Bool("dry-run", false, "validate replacement and report measurement changes without writing")
	startupFlags := addPreferenceFlags(f)
	noColor := f.Bool("no-color", os.Getenv("NO_COLOR") != "", "disable ANSI colors")
	if e := parsed(f, args); e != nil {
		return e
	}
	c, e := config.Load(*path)
	if errors.Is(e, os.ErrNotExist) {
		if !config.ID(*project) {
			return errors.New("invalid --project for a new topology")
		}
		c, e = model.Config{Schema: model.ConfigSchema, Project: *project}, nil
	}
	if e != nil {
		return fmt.Errorf("%s: %w (fix the JSON, or run topo validate for details)", *path, e)
	}
	if *apply != "" {
		return a.applyEdit(*path, c, *apply, *expected, *expectedCandidate, *dry)
	}
	if *dry || *expected != "" || *expectedCandidate != "" {
		return errors.New("--dry-run and --expect-revision require --apply")
	}
	if a.TerminalIn == nil || a.TerminalOut == nil {
		return errors.New("interactive terminal unavailable")
	}
	startup, err := startupFlags.startup()
	if err != nil {
		return err
	}
	return tui.RunEditor(ctx, *path, c, *noColor, a.TerminalIn, a.TerminalOut, startup)
}

// preview opens the TUI on synthetic evidence. It never dials a collector.
// --scenario is applied in the past, so the view opens already settled;
// switching with x inside the TUI happens at the current time, and health
// follows the real confirmation and staleness rules as new samples arrive.
func (a *App) preview(ctx context.Context, args []string) error {
	f := a.flags("preview")
	path := f.String("config", "", "topology to render with synthetic evidence (default: built-in complex homelab)")
	observer := f.String("observer", "", "initial observer view (default: all observers)")
	scenario := f.Int("scenario", 0, "initial scenario index, already in effect; x in the TUI lists names")
	printTopo := f.Bool("print-topology", false, "write the built-in topology JSON to stdout and exit")
	dump := f.Bool("json", false, "print one synthetic snapshot and exit")
	startupFlags := addPreferenceFlags(f)
	device := deviceFlag(f)
	noColor := f.Bool("no-color", os.Getenv("NO_COLOR") != "", "disable ANSI colors")
	lang := f.String("lang", localeLanguage(), "language of the built-in topology and the scenarios: "+strings.Join(preview.Languages(), ", "))
	if e := f.Parse(args); e != nil {
		return e
	}
	if _, e := withDevice(tui.Startup{}, *device); e != nil {
		return e
	}
	if preview.Language(*lang) != *lang {
		return fmt.Errorf("--lang %q is not one of %s", secure.Text(*lang), strings.Join(preview.Languages(), ", "))
	}
	if *printTopo {
		if f.NArg() > 0 {
			return errors.New("--print-topology takes no command")
		}
		b, e := preview.PresetJSON(*lang)
		if e != nil {
			return e
		}
		_, e = a.Out.Write(b)
		return e
	}
	var c model.Config
	var e error
	if *path == "" {
		c, e = preview.Preset(*lang)
	} else {
		c, e = config.Load(*path)
	}
	if e != nil {
		return e
	}
	now := a.now()
	sim := preview.New(c, now, *lang)
	names := sim.Scenarios()
	if *scenario < 0 || *scenario >= len(names) {
		return fmt.Errorf("scenario %d out of range (0..%d)", *scenario, len(names)-1)
	}
	if *scenario != 0 {
		sim.SetScenario(*scenario, now.Add(-30*time.Minute))
	}
	if f.NArg() > 0 {
		switch cmd, rest := f.Arg(0), f.Args()[1:]; cmd {
		case "status", "show", "history", "problems":
			return a.read(ctx, cmd, rest, &previewed{config: c, sim: sim, now: now, observer: *observer, json: *dump})
		case "scenarios":
			return a.scenarios(rest, sim.ScenarioIDs(), names, sim.Scenario(), *dump)
		default:
			return fmt.Errorf("unknown preview command %q; use status, show, history or scenarios", secure.Text(cmd))
		}
	}
	if *dump {
		snap, e := sim.Snapshot(*observer, now)
		if e != nil {
			return e
		}
		return printJSON(a.Out, snap)
	}
	if a.TerminalIn == nil || a.TerminalOut == nil {
		return errors.New("interactive terminal unavailable")
	}
	startup, err := startupFlags.startup()
	if err != nil {
		return err
	}
	if startup, err = withDevice(startup, *device); err != nil {
		return err
	}
	return tui.RunPreview(ctx, sim, func(_ context.Context, view string) (model.Snapshot, string, error) {
		snap, e := sim.Snapshot(view, time.Now())
		return snap, "synthetic", e
	}, func(_ context.Context, check string, limit int) ([]model.Observation, error) {
		return sim.History(check, limit, time.Now())
	}, *observer, *noColor, a.TerminalIn, a.TerminalOut, startup)
}

// localeLanguage is the preview language the environment asks for: the
// first of LC_ALL, LC_MESSAGES and LANG that is set decides, as it does for
// messages, and a locale no catalog is written in asks for the default.
func localeLanguage() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(name); v != "" {
			if lang := preview.Language(v); lang != "" {
				return lang
			}
			break
		}
	}
	return preview.DefaultLanguage
}
