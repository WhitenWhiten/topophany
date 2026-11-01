package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/local"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type localEnvelope struct {
	Schema    int          `json:"schema"`
	RequestID string       `json:"request_id"`
	OK        bool         `json:"ok"`
	Data      any          `json:"data,omitempty"`
	Error     *local.Error `json:"error,omitempty"`
}

// local configures this machine's own runtime checks with the same envelope,
// bounds and exit codes as topo sources. Secrets go to private files and are
// never printed.
func (a *App) local(ctx context.Context, args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	f := a.flags("local " + cmd)
	f.SetOutput(io.Discard)
	root := f.String("local-root", "", "absolute private local check configuration root")
	asJSON := f.Bool("json", false, "print JSON (list)")
	stdin := f.Bool("json-stdin", false, "read one bounded JSON request through EOF")
	out := localEnvelope{Schema: 1}
	code := 0
	finish := func(err error) error {
		if len(out.RequestID) > 64 || !utf8.ValidString(out.RequestID) {
			out.RequestID = ""
		}
		if err != nil {
			out.Error = local.AsError(err)
			out.OK = false
			out.Data = nil
			if code == 0 {
				code = 1
			}
			fmt.Fprintln(a.Err, out.Error.Message)
		} else {
			out.OK = true
		}
		b, e := secure.MarshalJSON(out, "")
		if e != nil || len(b)+1 > local.MaxOutput {
			out = localEnvelope{Schema: 1, RequestID: out.RequestID, Error: &local.Error{Code: "state_budget", Message: "The local response exceeds its budget."}}
			b, _ = secure.MarshalJSON(out, "")
			code = 1
		}
		if _, e = a.Out.Write(append(b, '\n')); e != nil {
			return e
		}
		if code != 0 {
			return &ExitError{Code: code}
		}
		return nil
	}
	usage := func(message string) error {
		code = 2
		return finish(&local.Error{Code: "invalid_request", Message: message})
	}
	if e := parsed(f, args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return usage("Invalid local command options.")
	}
	if cmd != "list" && cmd != "inspect" && cmd != "save" && cmd != "remove" || cmd == "list" && (!*asJSON || *stdin) || cmd != "list" && (!*stdin || *asJSON) {
		return usage("Use local list --json or inspect/save/remove --json-stdin.")
	}
	s, e := local.NewStore(*root)
	if e != nil {
		code = 2
		return finish(e)
	}
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if cmd == "list" {
		p, e := s.Load()
		if e != nil {
			return finish(e)
		}
		checks := []any{}
		for _, entry := range p.Checks {
			checks = append(checks, map[string]any{"check": entry.Check, "auth_mode": entry.AuthMode})
		}
		out.Data = map[string]any{"profile_revision": p.Revision, "checks": checks}
		return finish(nil)
	}
	r, e := local.Decode(a.In)
	out.RequestID = r.RequestID
	if e != nil {
		code = 2
		return finish(e)
	}
	if cmd == "remove" {
		if !config.ID(r.CheckID) || len(r.CheckID) > 64 {
			return usage("Invalid check ID or base revision.")
		}
		p, e := s.Remove(commandCtx, r.BaseRevision, r.CheckID)
		if e != nil {
			return finish(e)
		}
		out.Data = map[string]any{"profile_revision": p.Revision, "check_id": r.CheckID}
		return finish(nil)
	}
	if r.Auth.Mode == "" {
		return usage("Invalid local check or authentication union.")
	}
	if e = r.Check.ValidateDraft(); e != nil {
		code = 2
		return finish(e)
	}
	check := r.Check.Normalized()
	if cmd == "save" && check.ID == "" {
		check.ID = local.NewCheckID()
	}
	token := r.Auth.Token
	if r.Auth.Mode == "saved" {
		if token, e = s.SavedSecret(commandCtx, check); e != nil {
			return finish(e)
		}
	}
	inspection, e := local.Inspect(commandCtx, check, token)
	if e != nil {
		return finish(e)
	}
	if cmd == "inspect" {
		out.Data = inspection
		return finish(nil)
	}
	auth := r.Auth
	auth.Token = token
	p, e := s.Save(commandCtx, r.BaseRevision, check, auth)
	if e != nil {
		return finish(e)
	}
	out.Data = map[string]any{"profile_revision": p.Revision, "check_id": check.ID}
	return finish(nil)
}
