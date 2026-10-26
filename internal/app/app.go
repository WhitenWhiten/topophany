// Package app wires the independent server, executor and remote client roles.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/runner"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type App struct {
	In                      io.Reader
	Out, Err                io.Writer
	TerminalIn, TerminalOut *os.File
	Now                     func() time.Time // the clock the read commands judge freshness by
	Reload                  <-chan os.Signal // stands in for SIGHUP when set
}

func New() *App {
	return &App{In: os.Stdin, Out: os.Stdout, Err: secure.Terminal(os.Stderr), TerminalIn: os.Stdin, TerminalOut: os.Stdout, Now: time.Now}
}

const help = `Topophany — where your network stands, seen from where, and on what evidence

Get started
  topo preview                      the whole UI on synthetic evidence; x switches scenarios
  topo init --dir DIR               a local demo: a topology, credentials and a collector to run
  topo login                        save and verify a connection to a collector
  topo                              open the TUI on the saved connection

Read what the TUI shows
  topo sessions --json              list running local viewers
  topo diagnose --session ID --json inspect exactly what that viewer displays
  topo status                       the Overview as text; its exit status says it too
  topo show TAB [ID]                a tab, or the evidence behind one entry in it
  topo history CHECK                a check's recent samples
  topo history CHECK --step 1h      how a check fared, hour by hour, from its rollups
  topo --json                       one authenticated snapshot as JSON
  topo --source direct --profile ID  Beszel/Komari resource TUI without a collector
  topo sources list --json           list direct profiles (independent of client profiles)
  topo sources inspect|save|remove --json-stdin  bounded direct configuration API
  topo --token-stdin                read a temporary bearer token from stdin
  topo --credential-file FILE       read a temporary token from a private file

Run
  topo serve                        run a collector, optionally with checks of its own
  topo worker                       run a reviewed topology's checks from this machine
  topo once                         run local checks once; write observations as JSON

Describe your network
  topo edit                         add, edit and delete topology entities in a TUI
  topo validate                     check a topology; print its revision
  topo address                      inspect or edit address definitions; report changed checks
  topo export                       export a redacted topology

Deploy
  topo init --role ROLE             make a private server, worker or client bundle
  topo install                      install a reviewed bundle (--dry-run, --rollback DIR)
  topo capacity                     preflight collector storage before a migration
  topo token                        make a random bearer token and its hash

Settings
  topo monitor                      configure and run persistent display panels
  topo monitor stream               stream requested panels as NDJSON; --capabilities lists support
  topo monitor stream --profiles     list saved client profiles
  topo preferences show|set|reset   the TUI's saved startup defaults
  topo version                      print the version

status, show and history print what the TUI shows, as text or with --json;
TAB is overview, topology, servers, services, paths, checks, incidents,
observers or map. topo status exits 0 when all is clear, 2 when something
needs attention, 3 when nothing current is known and 4 when no collector
answers. 'topo preview [--scenario N] status' (or show, history) reads the
synthetic evidence instead; 'topo preview scenarios' lists the scenarios.
'topo preview --lang LANG' writes the built-in topology and the scenarios in
another language; by default they follow LC_ALL, LC_MESSAGES or LANG.

Direct also supports --json, status, show and monitor configure/run. It reads
CPU, memory and the primary disk; it does not establish network reachability.
One-shot reads wait at most 15s (--wait overrides); history explicitly fails
with history_unsupported_in_direct_mode. --direct-root ABS_PATH overrides only
the private direct configuration root. See docs/DIRECT.md for configuration API 1.

In the TUI: 1–9 or Tab switch tabs, ↑↓ or jk move, Enter shows the evidence,
/ filters, v switches the observer you view from, m z f p change the map,
r refreshes, comma opens Settings, ? About, q quits; the mouse works too.
In 'topo edit': a add, e edit, d delete, u undo, Ctrl-S save. In 'topo
preview': x scenario, D debug overlay; evidence is synthetic and the
topology is read-only.

Startup display flags (topo, preview and edit) override the saved defaults
for one launch: --page PAGE, --map-mode auto|virtual|geo|network,
--groups-collapsed, --uniform-cards, --service-cards, --full-map and
--nerd-font-icons (each also takes =false), and --preferences PATH. In the
TUI, Settings applies a change to this session or saves it for future
launches; or, for example:
  topo preferences set --page servers --uniform-cards=true

Credentials and preferences live in the OS user config directory, under
topophany/. 'topo COMMAND --help' lists a command's options. Monitoring
never changes routes, proxy policies, TUN settings or services.
`

func (a *App) flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(a.Err)
	return f
}

func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.view(ctx, args)
	}
	switch args[0] {
	case "help", "--help", "-h":
		_, e := io.WriteString(a.Out, help)
		return e
	case "version", "--version":
		_, e := fmt.Fprintln(a.Out, "Topophany "+model.Version)
		return e
	case "serve":
		return a.serve(ctx, args[1:])
	case "worker":
		return a.worker(ctx, args[1:])
	case "sessions", "diagnose":
		return a.diagnostics(args[0], args[1:])
	case "sources":
		return a.sources(ctx, args[1:])
	case "monitor":
		return a.monitor(ctx, args[1:])
	case "preferences":
		return a.preferences(args[1:])
	case "login":
		return a.login(ctx, args[1:])
	case "init":
		return a.init(args[1:])
	case "install":
		return a.install(ctx, args[1:])
	case "edit":
		return a.edit(ctx, args[1:])
	case "preview":
		return a.preview(ctx, args[1:])
	case "status", "show", "history":
		return a.read(ctx, args[0], args[1:], nil)
	case "validate", "export", "once":
		return a.topologyCommand(ctx, args[0], args[1:])
	case "address":
		return a.address(args[1:])
	case "capacity":
		return a.capacity(args[1:])
	case "token":
		return a.token(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			return a.view(ctx, args)
		}
		return fmt.Errorf("unknown command %q; use topo --help", secure.Text(args[0]))
	}
}

func parsed(f *flag.FlagSet, args []string) error {
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() > 0 {
		return errors.New("unexpected positional arguments")
	}
	return nil
}

// printJSON writes v as indented JSON, for a program or a terminal to
// read. encoding/json escapes C0 controls in strings but writes DEL, the
// C1 controls and bidi controls as they are, and a terminal may act on a
// C1 control as on the escape sequence it stands for; each is written as
// a \u escape instead, which reads back as the same string. The only
// control left is the newline between lines, as one in a string is
// escaped already.
func printJSON(w io.Writer, v any) error {
	raw, err := secure.MarshalJSON(v, "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(raw, '\n'))
	return err
}

func (a *App) token(args []string) error {
	f := a.flags("token")
	if e := parsed(f, args); e != nil {
		return e
	}
	t, e := secure.Token()
	if e != nil {
		return e
	}
	return printJSON(a.Out, map[string]string{"token": t, "token_sha256": secure.Hash(t)})
}

func (a *App) topologyCommand(ctx context.Context, cmd string, args []string) error {
	f := a.flags(cmd)
	path := f.String("config", "topology.json", "reviewed local topology JSON")
	observer := f.String("observer", "", "local execution context (required for once)")
	if e := parsed(f, args); e != nil {
		return e
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	switch cmd {
	case "validate":
		return printJSON(a.Out, map[string]any{"project": c.Project, "revision": config.Revision(c), "contracts": config.Contracts(c), "nodes": len(c.Nodes), "checks": len(c.Checks), "valid": true})
	case "export":
		return printJSON(a.Out, config.Public(c))
	case "once":
		r, e := runner.New(c, *observer, 1, probe.NewRunner(c, *observer).Run, func(v model.Observation) error { return json.NewEncoder(a.Out).Encode(v) })
		if e != nil {
			return e
		}
		batch := func(vs []model.Observation) error {
			for _, v := range vs {
				if e := json.NewEncoder(a.Out).Encode(v); e != nil {
					return e
				}
			}
			return nil
		}
		r.WithSources(probe.NewSourceRuntime(probe.ProductionSourceAdapters()), batch, 4)
		return r.Once(ctx)
	}
	return nil
}

func (a *App) init(args []string) error {
	for _, arg := range args {
		if arg == "--role" || strings.HasPrefix(arg, "--role=") || arg == "--interactive" {
			return a.setup(args)
		}
	}
	f := a.flags("init")
	dir := f.String("dir", "./topophany-demo", "NEW demo directory (refuses existing files)")
	if e := parsed(f, args); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(filepath.Clean(*dir)), 0700); e != nil {
		return e
	}
	// Claim the final directory exclusively: a separate existence check and
	// MkdirAll permit two initializers to overwrite each other's credentials.
	if e := os.Mkdir(*dir, 0700); e != nil {
		if errors.Is(e, os.ErrExist) {
			return errors.New("init refuses an existing directory; choose a new --dir")
		}
		return e
	}
	viewer, e := secure.Token()
	if e != nil {
		return e
	}
	worker, e := secure.Token()
	if e != nil {
		return e
	}
	c := DemoConfig()
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	if e = secure.AtomicWrite(filepath.Join(*dir, "topology.json"), append(b, '\n'), 0600); e != nil {
		return e
	}
	auth := api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(viewer), Role: "viewer"}, {TokenSHA256: secure.Hash(worker), Role: "worker", Observer: "demo"}}}
	if e = api.SaveAuth(filepath.Join(*dir, "auth.json"), auth); e != nil {
		return e
	}
	for name, token := range map[string]string{"credentials.json": viewer, "worker-credentials.json": worker} {
		cr := client.Credentials{Schema: 1, Active: "demo", Profiles: map[string]client.Profile{"demo": {Project: c.Project, Servers: []client.Endpoint{{URL: "http://127.0.0.1:7447", Token: token}}}}}
		if e = client.Save(filepath.Join(*dir, name), cr); e != nil {
			return e
		}
	}
	fmt.Fprintf(a.Out, "Demo created at %s (only loopback probes; fresh private credentials).\n\nCollector:\n  topo serve --config %s --auth %s --data %s --observer demo\n\nIn another terminal:\n  topo --credentials %s\n\nDo not run a second executor with observer=demo concurrently.\n", *dir, filepath.Join(*dir, "topology.json"), filepath.Join(*dir, "auth.json"), filepath.Join(*dir, "data"), filepath.Join(*dir, "credentials.json"))
	return nil
}

func DemoConfig() model.Config {
	c := model.Config{Schema: 1, Project: "topophany-demo", Nodes: []model.Node{{ID: "collector", Name: "Local collector", Group: "Demo", Kind: "host"}, {ID: "home", Name: "Home workstation", Group: "Demo", Kind: "host", Notes: "Registered identity, deliberately not monitored by this local-only demo."}}, Observers: []model.Observer{{ID: "demo", Name: "Local process", Node: "collector", Context: "native local process; explicit loopback probe", StaleSeconds: 30}}, Endpoints: []model.Endpoint{{ID: "api-loopback", Node: "collector", Address: "http://127.0.0.1:7447/healthz", Network: "loopback", Observer: "demo"}}, Services: []model.Service{{ID: "panel", Name: "Topophany collector", Node: "collector", Endpoints: []string{"api-loopback"}}, {ID: "notes-only", Name: "Notes-only service", Node: "home", Notes: "No probe configured. Unknown is intentional; never counted as uptime."}}, Paths: []model.Path{{ID: "loopback", Name: "Local API access", Observer: "demo", Nodes: []string{"collector"}}}, Checks: []model.Check{{ID: "api-health", Name: "Collector health response", Observer: "demo", Endpoint: "api-loopback", Service: "panel", Path: "loopback", Kind: "http", IntervalSeconds: 5, TimeoutSeconds: 2, StaleSeconds: 15, FailureThreshold: 2, RecoveryThreshold: 2, HTTP: model.HTTPOptions{Status: []int{200}, Contains: `"status":"ok"`}}}}
	config.Defaults(&c)
	return c
}
