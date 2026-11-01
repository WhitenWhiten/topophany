package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type runtimeRecord struct {
	Schema   int       `json:"schema"`
	Role     string    `json:"role"`
	PID      int       `json:"pid"`
	Version  string    `json:"version"`
	Project  string    `json:"project"`
	Observer string    `json:"observer,omitempty"`
	Revision string    `json:"private_revision"`
	At       time.Time `json:"observed_at"`
	Until    time.Time `json:"valid_until"`
}
type runtimePublisher struct {
	mu     sync.Mutex
	path   string
	record runtimeRecord
	last   time.Time
	closed bool
	warned bool
	log    func(string)
}

func newRuntimePublisher(dir, role, observer string, c model.Config, log func(string)) *runtimePublisher {
	p := &runtimePublisher{path: filepath.Join(dir, "local-runtime.json"), record: runtimeRecord{Schema: 1, Role: role, PID: os.Getpid(), Version: model.Version, Project: c.Project, Observer: observer, Revision: config.Revision(c)}, log: log}
	p.Tick(time.Now())
	return p
}
func (p *runtimePublisher) Set(c model.Config) {
	revision := config.Revision(c)
	p.mu.Lock()
	p.record.Revision = revision
	p.last = time.Time{}
	p.mu.Unlock()
	p.Tick(time.Now())
}
func (p *runtimePublisher) Tick(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || !p.last.IsZero() && now.Sub(p.last) < 5*time.Second {
		return
	}
	r := p.record
	r.At = now.UTC()
	r.Until = r.At.Add(15 * time.Second)
	data, err := json.Marshal(r)
	if err == nil {
		err = secure.AtomicWrite(p.path, data, 0600)
	}
	if err != nil {
		if !p.warned && p.log != nil {
			p.log("local runtime diagnostics unavailable; monitoring continues")
		}
		p.warned = true
		return
	}
	p.warned = false
	p.last = now
}
func (p *runtimePublisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	_ = os.Remove(p.path)
}

func (a *App) runtimeStatus(args []string) error {
	f := a.flags("runtime")
	dir := f.String("data", "", "local collector or worker data directory")
	path := f.String("config", "", "optional reviewed topology to compare with the running process")
	jsonOut := f.Bool("json", false, "private local runtime report (not a shareable diagnostic export)")
	if err := parsed(f, args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("runtime requires --data DIRECTORY")
	}
	data, err := secure.PrivateRead(filepath.Join(*dir, "local-runtime.json"))
	if err != nil {
		return err
	}
	var r runtimeRecord
	if err = secure.Decode(bytes.NewReader(data), &r, 16384); err != nil {
		return err
	}
	if r.Schema != 1 || r.Role != "worker" && r.Role != "collector" || len(r.Revision) != 64 || r.PID <= 0 || r.Until.Sub(r.At) != 15*time.Second {
		return errors.New("invalid local runtime record")
	}
	now := a.now()
	current := !r.At.After(now.Add(5*time.Second)) && now.Before(r.Until)
	code := 0
	if !current {
		code = 3
	}
	var matches *bool
	if *path != "" {
		c, err := config.Load(*path)
		if err != nil {
			return err
		}
		match := current && c.Project == r.Project && config.Revision(c) == r.Revision
		matches = &match
		if current && !match {
			code = 2
		}
	}
	result := struct {
		runtimeRecord
		Current bool  `json:"lease_current"`
		Matches *bool `json:"matches_config,omitempty"`
	}{r, current, matches}
	if *jsonOut {
		err = printJSON(a.Out, result)
	} else {
		fmt.Fprintf(a.Out, "%s pid=%d project=%s observer=%s · lease current=%t · observed %s\nPrivate revision: %s\n", r.Role, r.PID, secure.Text(r.Project), secure.Text(r.Observer), current, r.At.Format(time.RFC3339), r.Revision)
		if matches != nil {
			fmt.Fprintf(a.Out, "Matches reviewed file: %t\n", *matches)
		}
	}
	if err != nil {
		return err
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}
