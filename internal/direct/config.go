// Package direct owns private external-source profiles and process-local collection.
// It never reads collector credentials or starts a metrics service.
package direct

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/adapters/beszel"
	"github.com/WhitenWhiten/topophany/internal/adapters/cfservermonitor"
	"github.com/WhitenWhiten/topophany/internal/adapters/komari"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/source"
)

const MaxInput = 256 << 10
const MaxOutput = 1 << 20
const Observer = "direct-reader"
const MaxProfiles = 64

type Error struct {
	RetryAt   time.Time `json:"-"`
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func fail(code string) *Error {
	_, retry := failurePolicy(code)
	return &Error{Code: code, Message: messages[code], Retryable: retry}
}

var messages = map[string]string{
	"invalid_request": "Invalid direct request.", "profile_not_found": "Direct profile not found.", "source_not_found": "Direct source not found.", "profile_conflict": "The profile has changed. List profiles before retrying.", "profile_unavailable": "Direct profile is unavailable.", "auth_required": "Authentication is required; the source did not accept this credential.", "forbidden": "The source denied permission for this operation.", "mfa_required": "The Hub requires an additional authentication step; use a token.", "auth_method_unsupported": "This authentication method is not supported by the source.", "connection_failed": "The source could not be reached.", "tls_error": "The source TLS configuration or certificate could not be verified.", "rate_limited": "The source rate limited this request.", "upstream_incompatible": "The source response is not supported by this adapter.", "selection_invalid": "A selected system is invalid or not accessible.", "selection_limit": "The selection exceeds the direct profile budget.", "discovery_limit": "System discovery exceeded its bounded budget.", "storage_error": "Direct profile storage is unavailable.", "credential_storage_error": "Private credential storage is unavailable.", "state_budget": "The direct memory or configuration budget was exceeded.", "history_unsupported_in_direct_mode": "History is not supported in direct mode.",
}

func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return fail("profile_unavailable")
}

type SourceConfig struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Adapter       string `json:"adapter"`
	URL           string `json:"url"`
	CAFile        string `json:"ca_file"`
	AllowInsecure bool   `json:"allow_insecure,omitempty"`
	Interval      int    `json:"interval_seconds"`
	Timeout       int    `json:"timeout_seconds"`
	Stale         int    `json:"stale_seconds"`
	MaxAge        int    `json:"max_age_seconds"`
}

// AdapterNames returns the adapters supported by direct profiles.
func AdapterNames() []string { return []string{"beszel", "komari", cfservermonitor.Name} }

func (s SourceConfig) AllowsAnonymous() bool {
	return s.Adapter == "komari" || s.Adapter == cfservermonitor.Name
}

func (s *SourceConfig) UnmarshalJSON(b []byte) error {
	type plain SourceConfig
	p := plain{Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}
	if err := Exact(b, &p); err != nil {
		return err
	}
	*s = SourceConfig(p)
	return nil
}
func (s SourceConfig) model(objects int) model.Source {
	return model.Source{ID: s.ID, Name: s.Name, Adapter: s.Adapter, Observer: Observer, Target: s.URL, CAFile: s.CAFile, AllowInsecure: s.AllowInsecure, CredentialEnv: "TOPO_DIRECT_RESOLVED", IntervalSeconds: s.Interval, TimeoutSeconds: s.Timeout, StaleSeconds: s.Stale, MaxPages: max(1, objects), MaxObjects: max(1, objects), MaxResponseBytes: 1 << 20, MaxReportBytes: model.MaxReportBytes}
}
func (s SourceConfig) Validate() error {
	if !config.ID(s.ID) || len(s.ID) > 64 || strings.TrimSpace(s.Name) == "" || len(s.Name) > 128 || secure.Text(s.Name) != s.Name || len(s.CAFile) > 1024 || secure.Text(s.CAFile) != s.CAFile || s.CAFile != "" && !filepath.IsAbs(s.CAFile) {
		return fail("invalid_request")
	}
	if s.Interval < 1 || s.Interval > 86400 || s.Timeout < 1 || s.Timeout > config.MaxTimeoutSeconds || s.Stale < s.Interval || s.Stale > 604800 || s.MaxAge < 1 || s.MaxAge > 604800 {
		return fail("invalid_request")
	}
	var adapter source.Adapter
	switch s.Adapter {
	case "beszel":
		adapter = beszel.Adapter{}
	case "komari":
		adapter = komari.Adapter{}
	case cfservermonitor.Name:
		adapter = cfservermonitor.Adapter{}
	default:
		return fail("invalid_request")
	}
	if adapter.Validate(s.model(1)) != nil {
		return fail("invalid_request")
	}
	return nil
}

func (s SourceConfig) validObject(id string) bool {
	switch s.Adapter {
	case "komari":
		return komari.ValidObjectID(id)
	case cfservermonitor.Name:
		return cfservermonitor.ValidObjectID(id)
	default:
		return s.Adapter == "beszel" && objectID(id)
	}
}

func (s SourceConfig) primaryDisk() string {
	if s.Adapter == "komari" || s.Adapter == cfservermonitor.Name {
		return "aggregate"
	}
	return "root"
}

type Auth struct {
	Mode     string `json:"mode"`
	Identity string `json:"identity,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Name     string `json:"name,omitempty"`
}

func (a *Auth) UnmarshalJSON(b []byte) error {
	type plain Auth
	var p plain
	if err := Exact(b, &p); err != nil {
		return err
	}
	*a = Auth(p)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	allowed := map[string]bool{"mode": true}
	switch a.Mode {
	case "password":
		allowed["identity"] = true
		allowed["password"] = true
		if a.Identity == "" || a.Password == "" || len(a.Identity) > 256 || len(a.Password) > 4096 {
			return fail("invalid_request")
		}
	case "token":
		allowed["token"] = true
		if !safeToken(a.Token) {
			return fail("invalid_request")
		}
	case "env":
		allowed["name"] = true
		if !config.SecretEnvName(a.Name) || a.Name == "" {
			return fail("invalid_request")
		}
	case "saved", "anonymous":
	default:
		return fail("invalid_request")
	}
	for _, k := range []string{"identity", "password", "token", "name"} {
		if _, ok := m[k]; ok && !allowed[k] {
			return fail("invalid_request")
		}
	}
	return nil
}

type Request struct {
	Schema       int          `json:"schema"`
	RequestID    string       `json:"request_id"`
	Profile      string       `json:"profile"`
	BaseRevision string       `json:"base_revision"`
	Source       SourceConfig `json:"source"`
	Auth         Auth         `json:"auth"`
	Selected     []string     `json:"selected_objects"`
	SourceID     string       `json:"source_id"`
}

func Decode(r io.Reader) (Request, error) {
	var req Request
	b, e := io.ReadAll(io.LimitReader(r, MaxInput+1))
	if e != nil || len(b) > MaxInput || Exact(b, &req) != nil || req.Schema != 1 || len(req.RequestID) > 64 || !config.ID(req.Profile) {
		return req, fail("invalid_request")
	}
	return req, nil
}

// Exact ignores unknown extension keys but rejects wrong known types, nulls,
// case aliases and duplicate keys. It never embeds raw parser errors in output.
func Exact(b []byte, v any) error {
	if !utf8.Valid(b) || secure.JSONBudget(b) != nil {
		return fail("invalid_request")
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil || m == nil {
		return fail("invalid_request")
	}
	t := reflect.TypeOf(v).Elem()
	filtered := map[string]json.RawMessage{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key := strings.Split(f.Tag.Get("json"), ",")[0]
		if key == "" {
			continue
		}
		if raw, ok := m[key]; ok {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return fail("invalid_request")
			}
			filtered[key] = raw
		}
	}
	clean, _ := json.Marshal(filtered)
	if json.Unmarshal(clean, v) != nil {
		return fail("invalid_request")
	}
	return nil
}
func objectID(id string) bool {
	if len(id) != 15 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func safeToken(s string) bool {
	if len(s) < 1 || len(s) > 1024 {
		return false
	}
	for _, c := range s {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

type Node struct {
	ID       string `json:"id"`
	ObjectID string `json:"object_id"`
	Name     string `json:"name"`
}
type Entry struct {
	Source     SourceConfig `json:"source"`
	AuthMode   string       `json:"auth_mode"`
	Selected   []string     `json:"selected_objects"`
	Nodes      []Node       `json:"nodes"`
	Credential string       `json:"credential_ref"`
	Subject    string       `json:"subject"`
}
type Profile struct {
	Profile  string  `json:"profile"`
	Project  string  `json:"project"`
	Revision string  `json:"profile_revision"`
	Dataset  string  `json:"dataset_id"`
	Sources  []Entry `json:"sources"`
}
type database struct {
	Schema   int       `json:"schema"`
	Profiles []Profile `json:"profiles"`
}
type Credential struct {
	RetryAt   time.Time `json:"retry_at,omitzero"`
	Version   string    `json:"version"`
	Mode      string    `json:"mode"`
	Token     string    `json:"token,omitempty"`
	Env       string    `json:"env,omitempty"`
	Subject   string    `json:"subject"`
	Renewable bool      `json:"renewable"`
}
type Store struct {
	Root  string
	Write func(string, []byte, os.FileMode) error
}

func NewStore(root string) (*Store, error) {
	if root == "" {
		d, e := os.UserConfigDir()
		if e != nil {
			return nil, fail("storage_error")
		}
		root = filepath.Join(d, "topophany", "direct")
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
	return s.lockFile(ctx, "state.lock")
}
func (s *Store) lockFile(ctx context.Context, name string) (func(), error) {
	for {
		unlock, e := secure.Lock(filepath.Join(s.Root, name))
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
func (s *Store) read() (database, error) {
	d := database{Schema: 1, Profiles: []Profile{}}
	if e := s.private(false); errors.Is(e, os.ErrNotExist) {
		return d, nil
	} else if e != nil {
		return d, e
	}
	b, e := secure.PrivateRead(filepath.Join(s.Root, "profiles.json"))
	if errors.Is(e, os.ErrNotExist) {
		return d, nil
	}
	if e != nil || secure.JSONBudget(b) != nil || json.Unmarshal(b, &d) != nil || d.Schema != 1 || len(d.Profiles) > MaxProfiles {
		return d, fail("storage_error")
	}
	ids := map[string]bool{}
	for _, p := range d.Profiles {
		if !config.ID(p.Profile) || !config.ID(p.Project) || p.Revision == "" || p.Dataset == "" || ids[p.Profile] {
			return d, fail("storage_error")
		}
		ids[p.Profile] = true
		if _, e := p.Config(); e != nil {
			return d, fail("storage_error")
		}
		for _, src := range p.Sources {
			if !config.ID(src.Credential) {
				return d, fail("storage_error")
			}
		}
	}
	return d, nil
}
func (s *Store) Load(profile string) (Profile, error) {
	d, e := s.read()
	if e != nil {
		return Profile{}, e
	}
	for _, p := range d.Profiles {
		if p.Profile == profile {
			return p, nil
		}
	}
	return Profile{}, fail("profile_not_found")
}
func (s *Store) List() ([]Profile, error) {
	d, e := s.read()
	if e != nil {
		return nil, e
	}
	return d.Profiles, nil
}
func (s *Store) credential(ref string) (Credential, error) {
	var c Credential
	if s.private(false) != nil {
		return c, fail("credential_storage_error")
	}
	if !config.ID(ref) {
		return c, fail("credential_storage_error")
	}
	b, e := secure.PrivateRead(filepath.Join(s.Root, "credential-"+ref+".json"))
	if e != nil || json.Unmarshal(b, &c) != nil || c.Version == "" || c.Subject == "" || c.Mode != "env" && c.Mode != "anonymous" && !safeToken(c.Token) {
		return c, fail("credential_storage_error")
	}
	if c.Mode == "anonymous" && (c.Subject != anonymousSubject("komari") && c.Subject != anonymousSubject(cfservermonitor.Name) ||
		c.Token != "" || c.Env != "" || c.Renewable) {
		return c, fail("credential_storage_error")
	}
	return c, nil
}
func newID() string {
	v, e := secure.Token()
	if e != nil {
		panic("system random unavailable")
	}
	return v[:32]
}
func findEntry(p Profile, id string) (Entry, bool) {
	for _, e := range p.Sources {
		if e.Source.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}
func (p Profile) Config() (model.Config, error) {
	c := model.Config{Schema: model.ConfigSchema, Project: p.Project, Observers: []model.Observer{{ID: Observer, Name: "Direct", Context: "local direct executor", StaleSeconds: 90}}}
	if len(p.Sources) > 64 {
		return c, fail("selection_limit")
	}
	seen := map[string]bool{}
	total := 0
	for _, e := range p.Sources {
		if e.Source.Validate() != nil || seen[e.Source.ID] || len(e.Selected) < 1 || len(e.Selected) > 20 || len(e.Nodes) != len(e.Selected) {
			return c, fail("selection_invalid")
		}
		seen[e.Source.ID] = true
		total += len(e.Selected)
		src := e.Source.model(len(e.Selected))
		if e.AuthMode == "anonymous" {
			if !e.Source.AllowsAnonymous() || e.Subject != anonymousSubject(e.Source.Adapter) {
				return c, fail("selection_invalid")
			}
			src.CredentialEnv = ""
		}
		c.Sources = append(c.Sources, src)
		objects := map[string]bool{}
		for i, obj := range e.Selected {
			n := e.Nodes[i]
			id := e.Source.ID + "." + obj
			check := id + ".resource"
			if !e.Source.validObject(obj) || objects[obj] || n.ID != id || n.ObjectID != obj || n.Name == "" || len(n.Name) > 128 || secure.Text(n.Name) != n.Name {
				return c, fail("selection_invalid")
			}
			objects[obj] = true
			c.Nodes = append(c.Nodes, model.Node{ID: id, Name: n.Name, Kind: "host", ResourceChecks: []string{check}})
			c.Checks = append(c.Checks, model.Check{ID: check, Name: n.Name, Kind: "external", Observer: Observer, External: &model.ExternalBinding{Source: src.ID, Object: obj, Report: model.ReportTypeResource, Node: id, MaxAgeSeconds: e.Source.MaxAge, PrimaryDisk: e.Source.primaryDisk()}})
		}
	}
	if total > 256 {
		return c, fail("selection_limit")
	}
	config.Defaults(&c)
	if config.Validate(c) != nil {
		return c, fail("selection_invalid")
	}
	return c, nil
}
func datasetKey(p Profile) string {
	parts := []string{p.Profile}
	for _, e := range p.Sources {
		objs := append([]string(nil), e.Selected...)
		sort.Strings(objs)
		parts = append(parts, e.Source.ID, e.Source.Adapter, e.Source.URL, e.Subject, strings.Join(objs, ","))
	}
	return strings.Join(parts, "\x00")
}
func (s *Store) commit(d database) error {
	b, e := json.Marshal(d)
	if e != nil || len(b) > MaxOutput {
		return fail("state_budget")
	}
	if s.Write(filepath.Join(s.Root, "profiles.json"), append(b, '\n'), 0600) != nil {
		return fail("storage_error")
	}
	return nil
}
func (s *Store) Save(ctx context.Context, r Request, cred Credential, systems []System) (Profile, []Node, error) {
	unlock, e := s.lock(ctx)
	if e != nil {
		return Profile{}, nil, e
	}
	defer unlock()
	d, e := s.read()
	if e != nil {
		return Profile{}, nil, e
	}
	idx := -1
	p := Profile{Profile: r.Profile, Project: "direct-" + secure.Hash(r.Profile)[:24], Sources: []Entry{}}
	for i, v := range d.Profiles {
		if v.Profile == r.Profile {
			idx = i
			p = v
			break
		}
	}
	if p.Revision != r.BaseRevision {
		return p, nil, fail("profile_conflict")
	}
	if idx < 0 && len(d.Profiles) >= MaxProfiles {
		return p, nil, fail("state_budget")
	}
	names := map[string]string{}
	for _, sys := range systems {
		names[sys.ID] = sys.Name
	}
	objects := map[string]bool{}
	nodes := []Node{}
	selected := []string{}
	if len(r.Selected) == 0 {
		return p, nil, fail("selection_invalid")
	}
	for _, id := range r.Selected {
		if !r.Source.validObject(id) || names[id] == "" {
			return p, nil, fail("selection_invalid")
		}
		if objects[id] {
			continue
		}
		objects[id] = true
		selected = append(selected, id)
		nodes = append(nodes, Node{r.Source.ID + "." + id, id, names[id]})
	}
	if len(selected) > 20 {
		return p, nil, fail("selection_limit")
	}
	oldKey := datasetKey(p)
	ref := newID()
	entry := Entry{Source: r.Source, AuthMode: cred.Mode, Selected: selected, Nodes: nodes, Credential: ref, Subject: cred.Subject}
	found := false
	for i, v := range p.Sources {
		if v.Source.ID == r.Source.ID {
			p.Sources[i] = entry
			found = true
			break
		}
	}
	if !found {
		p.Sources = append(p.Sources, entry)
	}
	if _, e = p.Config(); e != nil {
		return p, nil, e
	}
	if p.Dataset == "" || oldKey != datasetKey(p) {
		p.Dataset = newID()
	}
	p.Revision = newID()
	if idx < 0 {
		d.Profiles = append(d.Profiles, p)
	} else {
		d.Profiles[idx] = p
	}
	cred.Version = newID()
	b, _ := json.Marshal(cred)
	path := filepath.Join(s.Root, "credential-"+ref+".json")
	if s.Write(path, append(b, '\n'), 0600) != nil {
		return p, nil, fail("credential_storage_error")
	}
	if e = s.commit(d); e != nil {
		// Keep the new credential on an uncertain replacement result.
		// An atomic writer may report a directory sync failure after rename.
		// Never leave a visible configuration pointing to a removed secret.
		return p, nil, e
	}
	return p, nodes, nil
}
func (s *Store) Remove(ctx context.Context, r Request) (Profile, error) {
	unlock, e := s.lock(ctx)
	if e != nil {
		return Profile{}, e
	}
	defer unlock()
	d, e := s.read()
	if e != nil {
		return Profile{}, e
	}
	for i, p := range d.Profiles {
		if p.Profile != r.Profile {
			continue
		}
		if p.Revision != r.BaseRevision {
			return p, fail("profile_conflict")
		}
		for j, v := range p.Sources {
			if v.Source.ID != r.SourceID {
				continue
			}
			p.Sources = append(p.Sources[:j:j], p.Sources[j+1:]...)
			p.Revision = newID()
			p.Dataset = newID()
			d.Profiles[i] = p
			if e = s.commit(d); e != nil {
				return p, e
			}
			return p, nil
		}
		return p, fail("source_not_found")
	}
	return Profile{}, fail("profile_not_found")
}
