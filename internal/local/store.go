package local

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Entry is a saved check and how it authenticates. Credential names the
// private file holding its secret; it is never printed.
type Entry struct {
	Check      Check  `json:"check"`
	AuthMode   string `json:"auth_mode"`
	Credential string `json:"credential_ref,omitempty"`
}

// Profile is every saved check under one opaque revision. An empty store has
// the empty revision.
type Profile struct {
	Schema   int     `json:"schema"`
	Revision string  `json:"profile_revision"`
	Checks   []Entry `json:"checks"`
}

// Find returns the saved entry with id.
func (p Profile) Find(id string) (Entry, bool) {
	for _, e := range p.Checks {
		if e.Check.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

type credential struct {
	Token string `json:"token"`
}

// Store is a private configuration root: checks.json for the checks and one
// 0600 credential-REF.json per secret, written atomically under a lock.
type Store struct {
	Root  string
	Write func(string, []byte, os.FileMode) error
}

// DefaultRoot is topophany/local in the OS user configuration directory,
// beside the direct profiles' topophany/direct.
func DefaultRoot() (string, error) {
	d, e := os.UserConfigDir()
	if e != nil {
		return "", fail("storage_error")
	}
	return filepath.Join(d, "topophany", "local"), nil
}

func NewStore(root string) (*Store, error) {
	if root == "" {
		var e error
		if root, e = DefaultRoot(); e != nil {
			return nil, e
		}
	}
	if !filepath.IsAbs(root) {
		return nil, fail("invalid_request")
	}
	return &Store{Root: filepath.Clean(root), Write: secure.AtomicWrite}, nil
}

func (s *Store) private(create bool) error {
	if create {
		if e := secure.MkdirAll(s.Root, 0700); e != nil {
			return fail("storage_error")
		}
	}
	st, e := os.Lstat(s.Root)
	if errors.Is(e, os.ErrNotExist) && !create {
		return e
	}
	if e != nil || !st.IsDir() || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
		return fail("storage_error")
	}
	return nil
}

func (s *Store) lock(ctx context.Context) (func(), error) {
	if e := s.private(true); e != nil {
		return nil, e
	}
	for {
		unlock, e := secure.Lock(filepath.Join(s.Root, "state.lock"))
		if e == nil {
			return func() { _ = unlock() }, nil
		}
		select {
		case <-ctx.Done():
			return nil, fail("storage_error")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Load reads the saved checks; a missing root or file is an empty store.
func (s *Store) Load() (Profile, error) {
	p := Profile{Schema: 1, Checks: []Entry{}}
	if e := s.private(false); errors.Is(e, os.ErrNotExist) {
		return p, nil
	} else if e != nil {
		return p, e
	}
	b, e := secure.PrivateRead(filepath.Join(s.Root, "checks.json"))
	if errors.Is(e, os.ErrNotExist) {
		return p, nil
	}
	if e != nil || secure.JSONBudget(b) != nil || json.Unmarshal(b, &p) != nil || p.Schema != 1 || p.Revision == "" || len(p.Checks) > MaxChecks {
		return Profile{Schema: 1, Checks: []Entry{}}, fail("storage_error")
	}
	seen := map[string]bool{}
	for _, entry := range p.Checks {
		valid := entry.Check.Validate() == nil && !seen[entry.Check.ID]
		valid = valid && (entry.AuthMode == "token" && config.ID(entry.Credential) || entry.AuthMode == "none" && entry.Credential == "")
		if !valid {
			return Profile{Schema: 1, Checks: []Entry{}}, fail("storage_error")
		}
		seen[entry.Check.ID] = true
	}
	if p.Checks == nil {
		p.Checks = []Entry{}
	}
	return p, nil
}

// Secret reads an entry's stored secret; an entry without one has "". Use
// SavedSecret or Snapshot when the profile may be changed concurrently.
func (s *Store) Secret(e Entry) (string, error) {
	if e.AuthMode != "token" {
		return "", nil
	}
	if !config.ID(e.Credential) || s.private(false) != nil {
		return "", fail("credential_storage_error")
	}
	b, err := secure.PrivateRead(filepath.Join(s.Root, "credential-"+e.Credential+".json"))
	var c credential
	if err != nil || json.Unmarshal(b, &c) != nil || !validToken(c.Token) {
		return "", fail("credential_storage_error")
	}
	return c.Token, nil
}

// Secrets reads every stored secret by check ID. Use Snapshot when the
// profile may be changed concurrently.
func (s *Store) Secrets(p Profile) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range p.Checks {
		if e.AuthMode != "token" {
			continue
		}
		token, err := s.Secret(e)
		if err != nil {
			return nil, err
		}
		out[e.Check.ID] = token
	}
	return out, nil
}

// readSnapshot retries a credential read if a concurrent write replaced the
// profile before deleting its old credential. Credential files are immutable:
// every new token gets a new reference, so a successful read belongs to p even
// if a writer commits a newer profile concurrently.
func (s *Store) readSnapshot(ctx context.Context, read func(Profile) error) (Profile, error) {
	p, err := s.Load()
	if err != nil {
		return p, err
	}
	for {
		err = read(p)
		var localErr *Error
		if err == nil || !errors.As(err, &localErr) || localErr.Code != "credential_storage_error" {
			return p, err
		}
		next, loadErr := s.Load()
		if loadErr != nil {
			return next, loadErr
		}
		if next.Revision == p.Revision {
			return p, err
		}
		if ctx.Err() != nil {
			return next, fail("storage_error")
		}
		p = next
	}
}

// Snapshot reads one profile and its corresponding secrets. A nonempty
// revision must match before its credentials are read.
func (s *Store) Snapshot(ctx context.Context, revision string) (Profile, map[string]string, error) {
	var secrets map[string]string
	p, err := s.readSnapshot(ctx, func(p Profile) error {
		if revision != "" && revision != p.Revision {
			return fail("profile_conflict")
		}
		var e error
		secrets, e = s.Secrets(p)
		return e
	})
	if err != nil {
		return p, nil, err
	}
	return p, secrets, nil
}

// SavedSecret reads the secret for the same check ID and address from a
// consistent profile. Checks saved without a secret return an empty string.
func (s *Store) SavedSecret(ctx context.Context, c Check) (string, error) {
	var token string
	_, err := s.readSnapshot(ctx, func(p Profile) error {
		saved, ok := p.Find(c.ID)
		if !ok || c.ID == "" || saved.Check.URL != c.Normalized().URL {
			return fail("saved_auth_mismatch")
		}
		var e error
		token, e = s.Secret(saved)
		return e
	})
	return token, err
}

func (s *Store) commit(p Profile) error {
	b, e := json.Marshal(p)
	if e != nil || len(b) > MaxOutput {
		return fail("state_budget")
	}
	if s.Write(filepath.Join(s.Root, "checks.json"), append(b, '\n'), 0600) != nil {
		return fail("storage_error")
	}
	return nil
}

func (s *Store) forget(ref string) {
	if config.ID(ref) {
		_ = os.Remove(filepath.Join(s.Root, "credential-"+ref+".json"))
	}
}

func newID() string {
	v, e := secure.Token()
	if e != nil {
		panic("system random unavailable")
	}
	return v[:32]
}

// NewCheckID is a fresh opaque check ID.
func NewCheckID() string { return "lc-" + newID()[:16] }

// Save replaces or adds c at base. auth "token" stores token in a new private
// file; "none" removes any stored secret; "saved" keeps the stored secret of
// the check with this ID at the same address.
func (s *Store) Save(ctx context.Context, base string, c Check, auth Auth) (Profile, error) {
	unlock, e := s.lock(ctx)
	if e != nil {
		return Profile{}, e
	}
	defer unlock()
	p, e := s.Load()
	if e != nil {
		return p, e
	}
	if p.Revision != base {
		return p, fail("profile_conflict")
	}
	index := -1
	for i, entry := range p.Checks {
		if entry.Check.ID == c.ID {
			index = i
		}
	}
	if index < 0 && len(p.Checks) >= MaxChecks {
		return p, fail("check_limit")
	}
	var old Entry
	if index >= 0 {
		old = p.Checks[index]
	}
	next := Entry{Check: c.Normalized(), AuthMode: auth.Mode}
	switch auth.Mode {
	case "saved":
		if index < 0 || old.Check.URL != next.Check.URL {
			return p, fail("saved_auth_mismatch")
		}
		next.AuthMode, next.Credential = old.AuthMode, old.Credential
	case "token":
		next.Credential = newID()
		b, _ := json.Marshal(credential{Token: auth.Token})
		if s.Write(filepath.Join(s.Root, "credential-"+next.Credential+".json"), append(b, '\n'), 0600) != nil {
			return p, fail("credential_storage_error")
		}
	case "none":
	default:
		return p, fail("invalid_request")
	}
	checks := append([]Entry(nil), p.Checks...)
	if index < 0 {
		checks = append(checks, next)
	} else {
		checks[index] = next
	}
	updated := Profile{Schema: 1, Revision: newID(), Checks: checks}
	if e = s.commit(updated); e != nil {
		// An atomic writer may report a directory sync failure after the
		// rename. Keep the new secret rather than risk a visible
		// configuration that points to a removed one.
		return p, e
	}
	if old.Credential != "" && old.Credential != next.Credential {
		s.forget(old.Credential)
	}
	return updated, nil
}

// Remove deletes the check with id at base, and its stored secret.
func (s *Store) Remove(ctx context.Context, base, id string) (Profile, error) {
	unlock, e := s.lock(ctx)
	if e != nil {
		return Profile{}, e
	}
	defer unlock()
	p, e := s.Load()
	if e != nil {
		return p, e
	}
	if p.Revision != base {
		return p, fail("profile_conflict")
	}
	for i, entry := range p.Checks {
		if entry.Check.ID != id {
			continue
		}
		checks := append(append([]Entry{}, p.Checks[:i]...), p.Checks[i+1:]...)
		updated := Profile{Schema: 1, Revision: newID(), Checks: checks}
		if e = s.commit(updated); e != nil {
			return p, e
		}
		s.forget(entry.Credential)
		return updated, nil
	}
	return p, fail("not_found")
}
