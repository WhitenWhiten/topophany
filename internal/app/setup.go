package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

// startSetup routes by the user's task. It never starts a service or a probe.
func (a *App) startSetup(ctx context.Context, args []string) error {
	f := a.flags("setup")
	task := f.String("task", "", "website, direct or network")
	root := f.String("direct-root", "", "private direct configuration root")
	secretFile := f.String("credential-file", "", "private source token/password file; never a command-line secret")
	if err := parsed(f, args); err != nil {
		return err
	}
	scanner := bufio.NewScanner(io.LimitReader(a.In, 64<<10))
	scanner.Buffer(make([]byte, 1024), 4096)
	ask := func(label string) (string, error) {
		fmt.Fprint(a.Err, label+": ")
		if !scanner.Scan() {
			return "", errors.New("setup input ended; no additional changes were applied")
		}
		return strings.TrimSpace(scanner.Text()), nil
	}
	if *task == "" {
		fmt.Fprintln(a.Out, "Choose what to do:\n  website — monitor an HTTP(S) application from this machine\n  direct  — read Beszel or Komari resources\n  network — prepare a reviewed multi-location deployment")
		choice, err := ask("Task")
		if err != nil {
			return err
		}
		*task = choice
	}
	switch *task {
	case "website":
		address, err := ask("Website health URL (expects HTTP 200)")
		if err != nil {
			return err
		}
		dir, err := ask("New configuration directory")
		if err != nil {
			return err
		}
		if dir == "" || address == "" {
			return errors.New("choose a new configuration directory")
		}
		return a.init([]string{"--url", address, "--dir", dir})
	case "network":
		fmt.Fprintln(a.Out, "Use the same reviewed topology on each collector and worker. Setup prepares an installation bundle; review it before installing.")
		role, err := ask("Role (server/worker/client)")
		if err != nil {
			return err
		}
		if role != "server" && role != "worker" && role != "client" {
			return errors.New("choose server, worker or client")
		}
		flags := []string{"--role", role}
		fields := []struct{ label, flag string }{{"New bundle directory", "--dir"}}
		if role != "client" {
			fields = append(fields, struct{ label, flag string }{"Reviewed topology JSON", "--config"})
		}
		if role == "server" {
			fields = append(fields, struct{ label, flag string }{"Collector HTTPS URL", "--server"})
		} else {
			fields = append(fields, struct{ label, flag string }{"Enrollment credentials file", "--credentials"}, struct{ label, flag string }{"Trusted CA PEM path", "--ca-file"})
		}
		if role == "worker" {
			fields = append(fields, struct{ label, flag string }{"Observer ID", "--observer"})
		}
		for _, field := range fields {
			value, err := ask(field.label)
			if err != nil {
				return err
			}
			if value == "" {
				return fmt.Errorf("%s is required", field.label)
			}
			flags = append(flags, field.flag, value)
		}
		return a.setup(flags)
	case "direct":
		return a.setupDirect(ctx, *root, *secretFile, ask)
	default:
		return errors.New("choose website, direct or network")
	}
}

func (a *App) setupDirect(ctx context.Context, root, secretFile string, ask func(string) (string, error)) error {
	r := direct.Request{Schema: 1, Source: direct.SourceConfig{Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}}
	for _, field := range []struct {
		label string
		value *string
	}{
		{"Profile name", &r.Profile}, {"Source ID", &r.Source.ID}, {"Source display name", &r.Source.Name},
		{"Source type (beszel/komari)", &r.Source.Adapter}, {"Source URL", &r.Source.URL},
	} {
		v, err := ask(field.label)
		if err != nil {
			return err
		}
		*field.value = v
	}
	if !config.ID(r.Profile) {
		return errors.New("invalid profile name")
	}
	if r.Source.Adapter == "komari" {
		r.Source.Interval = 30
	}
	if err := r.Source.Validate(); err != nil {
		return err
	}
	mode, err := ask("Authentication (token/password/anonymous)")
	if err != nil {
		return err
	}
	r.Auth.Mode = mode
	switch mode {
	case "password":
		if r.Source.Adapter != "beszel" {
			return errors.New("Komari requires token or anonymous authentication")
		}
		r.Auth.Identity, err = ask("Beszel account")
		if err != nil {
			return err
		}
	case "token":
	case "anonymous":
		if r.Source.Adapter != "komari" {
			return errors.New("anonymous reads require Komari")
		}
	default:
		return errors.New("choose token, password or anonymous")
	}
	if mode != "anonymous" {
		var secret string
		if secretFile != "" {
			b, e := secure.PrivateRead(secretFile)
			if e != nil {
				return errors.New("private credential file unavailable")
			}
			secret = strings.TrimSpace(string(b))
		} else {
			if a.TerminalIn == nil || a.TerminalOut == nil {
				return errors.New("use --credential-file outside a terminal")
			}
			fmt.Fprintln(a.Err, "Credential (not echoed):")
			secret, err = tui.ReadSecret(ctx, a.TerminalIn, a.TerminalOut)
			if err != nil {
				return err
			}
		}
		if mode == "password" {
			r.Auth.Password = secret
		} else {
			r.Auth.Token = secret
		}
	}
	store, err := direct.NewStore(root)
	if err != nil {
		return err
	}
	p, err := store.Load(r.Profile)
	if err != nil && direct.AsError(err).Code != "profile_not_found" {
		return err
	}
	r.BaseRevision = p.Revision
	// Existing entries are edited through the versioned sources API, never
	// silently replaced by a first-time setup session.
	for _, entry := range p.Sources {
		if entry.Source.ID == r.Source.ID {
			return errors.New("source already exists; edit it with topo sources or Data sources settings")
		}
	}
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	fmt.Fprintln(a.Err, "Connecting and discovering accessible servers…")
	cred, hub, err := store.Authenticate(commandCtx, r)
	if err != nil {
		cancel()
		return err
	}
	defer hub.Close()
	systems, truncated, err := hub.Discover(commandCtx, cred)
	cancel()
	if err != nil {
		return err
	}
	if truncated {
		return errors.New("source discovery was truncated; use a smaller source before selecting servers")
	}
	for _, system := range systems {
		fmt.Fprintf(a.Out, "%s  %s\n", secure.Text(system.ID), secure.Text(system.Name))
	}
	ids, err := ask("Server IDs to save (comma-separated, at most 20)")
	if err != nil {
		return err
	}
	for _, id := range strings.Split(ids, ",") {
		r.Selected = append(r.Selected, strings.TrimSpace(id))
	}
	saveCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	saved, _, err := store.Save(saveCtx, r, cred, systems)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Saved profile %s. Open: topo --source direct --profile %s\nResource reads do not establish server availability; direct mode has no stored history.\n", saved.Profile, saved.Profile)
	if root != "" {
		fmt.Fprintf(a.Out, "Use the same private configuration root: --direct-root %q\n", root)
	}
	return nil
}
