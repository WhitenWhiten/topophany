package management

import (
	"errors"
	"os"
	"sync"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type constraintFile struct {
	path   string
	system bool
	info   os.FileInfo
	limits *Constraints
}

// executionConstraints checks local authority on every execution, including
// ownership and parent permissions. Only an unchanged, trusted file can reuse
// decoded constraints. Atomic replacement, in-place edits and chmod invalidate
// the cache without a polling delay. Invalid files retain the last valid grant,
// as they do during disconnected/recovery operation.
func (a *Agent) executionConstraints() *Constraints {
	a.constraintMu.Lock()
	defer a.constraintMu.Unlock()
	previous := a.constraints.Load()
	p, err := LoadPolicy(a.path)
	if err != nil || p.Installation != a.identity.Installation || p.Config.ConstraintsFile == "" {
		return previous
	}
	path := p.Config.ConstraintsFile
	if err := ValidateLocalFile(path, p.System, false); err != nil {
		return previous
	}
	info, err := os.Lstat(path)
	if err != nil {
		return previous
	}
	cached := a.constraintFile
	if cached.path == path && cached.system == p.System && cached.info != nil && os.SameFile(cached.info, info) && cached.info.Size() == info.Size() && cached.info.ModTime() == info.ModTime() && cached.info.Mode() == info.Mode() {
		a.constraints.Store(cached.limits)
		return cached.limits
	}
	fresh, err := LoadConstraints(path, p.System)
	if err != nil {
		return previous
	}
	a.constraintFile = constraintFile{path: path, system: p.System, info: info, limits: &fresh}
	a.constraints.Store(&fresh)
	return &fresh
}

// Guard binds one scheduler's immutable topology. Revalidate the whole topology
// only when local constraints change, rather than once per probe or source
// binding. Each call still refreshes local authority and the dial-time policy.
func (a *Agent) Guard(c model.Config, observer string) func() error {
	if a == nil {
		return func() error { return nil }
	}
	c = config.Clone(c)
	var mu sync.Mutex
	var checked *Constraints
	var result error
	return func() error {
		if !a.Managed() {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		limits := a.executionConstraints()
		if limits == nil {
			return errors.New("managed_constraints_unavailable")
		}
		if limits != checked {
			result = limits.Check(c, observer, a.concurrency)
			checked = limits
		}
		return result
	}
}
