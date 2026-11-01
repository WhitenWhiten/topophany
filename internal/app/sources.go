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
	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type sourceEnvelope struct {
	Schema    int           `json:"schema"`
	RequestID string        `json:"request_id"`
	OK        bool          `json:"ok"`
	Data      any           `json:"data,omitempty"`
	Error     *direct.Error `json:"error,omitempty"`
}

func (a *App) sources(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "export" {
		return a.exportDirect(args[1:])
	}
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	f := a.flags("sources " + cmd)
	f.SetOutput(io.Discard)
	root := f.String("direct-root", "", "absolute private direct configuration root")
	asJSON := f.Bool("json", false, "print JSON (list)")
	stdin := f.Bool("json-stdin", false, "read one bounded JSON request through EOF")
	out := sourceEnvelope{Schema: 1}
	code := 0
	finish := func(err error) error {
		if len(out.RequestID) > 64 || !utf8.ValidString(out.RequestID) {
			out.RequestID = ""
		}
		if err != nil {
			out.Error = direct.AsError(err)
			out.OK = false
			if code == 0 {
				code = 1
			}
			fmt.Fprintln(a.Err, out.Error.Message)
		} else {
			out.OK = true
		}
		b, e := secure.MarshalJSON(out, "")
		if e != nil || len(b)+1 > direct.MaxOutput {
			out = sourceEnvelope{Schema: 1, RequestID: out.RequestID, Error: &direct.Error{Code: "state_budget", Message: "Direct response exceeds its budget."}}
			b, _ = secure.MarshalJSON(out, "")
			code = 1
		}
		_, e = a.Out.Write(append(b, '\n'))
		if e != nil {
			return e
		}
		if code != 0 {
			return &ExitError{Code: code}
		}
		return nil
	}
	if e := parsed(f, args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		code = 2
		return finish(&direct.Error{Code: "invalid_request", Message: "Invalid sources command options."})
	}
	if cmd != "list" && cmd != "inspect" && cmd != "save" && cmd != "remove" || cmd == "list" && (!*asJSON || *stdin) || cmd != "list" && (!*stdin || *asJSON) {
		code = 2
		return finish(&direct.Error{Code: "invalid_request", Message: "Use sources list --json or inspect/save/remove --json-stdin."})
	}
	s, e := direct.NewStore(*root)
	if e != nil {
		code = 2
		return finish(e)
	}
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if cmd == "list" {
		profiles, e := s.List()
		if e != nil {
			return finish(e)
		}
		public := []any{}
		for _, p := range profiles {
			entries := []any{}
			for _, v := range p.Sources {
				entries = append(entries, map[string]any{"source": v.Source, "auth_mode": v.AuthMode, "selected_objects": v.Selected, "nodes": v.Nodes})
			}
			public = append(public, map[string]any{"profile": p.Profile, "project": p.Project, "profile_revision": p.Revision, "dataset_id": p.Dataset, "sources": entries})
		}
		out.Data = map[string]any{"profiles": public}
		return finish(nil)
	}
	r, e := direct.Decode(a.In)
	out.RequestID = r.RequestID
	if e != nil {
		code = 2
		return finish(e)
	}
	if cmd == "remove" {
		if !config.ID(r.SourceID) || len(r.SourceID) > 64 || len(r.BaseRevision) > 128 {
			code = 2
			return finish(&direct.Error{Code: "invalid_request", Message: "Invalid source ID or base revision."})
		}
		p, e := s.Remove(commandCtx, r)
		if e != nil {
			return finish(e)
		}
		out.Data = map[string]any{"profile": p.Profile, "profile_revision": p.Revision, "dataset_id": p.Dataset, "source_id": r.SourceID}
		return finish(nil)
	}
	if e = r.Source.Validate(); e != nil || r.Auth.Mode == "" {
		code = 2
		return finish(&direct.Error{Code: "invalid_request", Message: "Invalid direct source or authentication union."})
	}
	if cmd == "save" {
		p, e := s.Load(r.Profile)
		if e != nil && direct.AsError(e).Code != "profile_not_found" {
			return finish(e)
		}
		if p.Revision != r.BaseRevision {
			return finish(&direct.Error{Code: "profile_conflict", Message: "Profile changed; list profiles before retrying."})
		}
		if len(r.Selected) == 0 {
			return finish(&direct.Error{Code: "selection_invalid", Message: "Select at least one accessible system."})
		}
		if len(r.Selected) > 20 {
			return finish(&direct.Error{Code: "selection_limit", Message: "Select at most 20 systems per source."})
		}
	}
	cred, h, e := s.Authenticate(commandCtx, r)
	if e != nil {
		return finish(e)
	}
	defer h.Close()
	systems, truncated, e := h.Discover(commandCtx, cred)
	if e != nil {
		return finish(e)
	}
	if cmd == "inspect" {
		out.Data = map[string]any{"source": r.Source, "systems": systems, "truncated": truncated, "limits": map[string]int{"max_selected": 20}, "warnings": []string{}}
		return finish(nil)
	}
	p, nodes, e := s.Save(commandCtx, r, cred, systems)
	if e != nil {
		return finish(e)
	}
	out.Data = map[string]any{"profile": p.Profile, "profile_revision": p.Revision, "dataset_id": p.Dataset, "source_id": r.Source.ID, "nodes": nodes}
	return finish(nil)
}
