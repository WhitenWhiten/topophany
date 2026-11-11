package direct

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/adapters/cfservermonitor"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type System struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Hub struct {
	redact  discoverySecrets
	source  SourceConfig
	client  *http.Client
	RetryAt time.Time
	Status  int
	Bytes   int
}

func NewHub(s SourceConfig) (*Hub, error) {
	if e := s.Validate(); e != nil {
		return nil, e
	}
	if s.Adapter != "beszel" {
		return nil, fail("invalid_request")
	}
	u, _ := url.Parse(s.URL)
	tc, e := probe.TLSConfig(u.Hostname(), s.CAFile)
	if e != nil {
		return nil, fail("tls_error")
	}
	tr := &http.Transport{TLSClientConfig: tc, Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10, ResponseHeaderTimeout: time.Duration(s.Timeout) * time.Second}
	return &Hub{source: s, client: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (h *Hub) Close() { h.client.CloseIdleConnections() }

// call permits only fixed authentication endpoints or users/systems GETs.
// Collection uses the existing GET-only source.IO, never this POST capability.
func (h *Hub) call(ctx context.Context, method, path, token string, body any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.source.Timeout)*time.Second)
	defer cancel()
	allowed := method == "POST" && (path == "users/auth-with-password" || path == "users/auth-refresh") || method == "GET" && (strings.HasPrefix(path, "systems/records?") || strings.HasPrefix(path, "users/records/") && objectID(strings.TrimPrefix(path, "users/records/")))
	if !allowed {
		return fail("invalid_request")
	}
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.source.URL, "/")+"/api/collections/"+path, bytes.NewReader(b))
	if e != nil {
		return fail("invalid_request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		if !safeToken(token) {
			return fail("auth_required")
		}
		req.Header.Set("Authorization", token)
	}
	resp, e := h.client.Do(req)
	if e != nil {
		if strings.HasPrefix(secure.ErrorCode(e), "tls_") {
			return fail("tls_error")
		}
		return fail("connection_failed")
	}
	defer resp.Body.Close()
	h.Status = resp.StatusCode
	if resp.StatusCode == 429 {
		h.RetryAt = retryAfter(resp.Header.Get("Retry-After"), time.Now())
		e := fail("rate_limited")
		e.RetryAt = h.RetryAt
		return e
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	h.Bytes += len(data)
	if e != nil {
		return fail("connection_failed")
	}
	if len(data) > 1<<20 || h.Bytes > 8<<20 {
		return fail("discovery_limit")
	}
	// Only closed response markers are inspected; upstream messages never leave.
	var marker struct {
		MFAID string `json:"mfaId"`
		Data  struct {
			MFAID string `json:"mfaId"`
			Code  string `json:"code"`
		} `json:"data"`
		Code string `json:"code"`
	}
	_ = json.Unmarshal(data, &marker)
	if marker.MFAID != "" || marker.Data.MFAID != "" || marker.Code == "mfa_required" {
		return fail("mfa_required")
	}
	if marker.Code == "auth_method_unsupported" {
		return fail("auth_method_unsupported")
	}
	switch resp.StatusCode {
	case 200:
	case 401:
		return fail("auth_required")
	case 403:
		return fail("forbidden")
	case 400:
		if path == "users/auth-with-password" {
			return fail("auth_required")
		}
		return fail("upstream_incompatible")
	case 404, 405:
		return fail("auth_method_unsupported")
	default:
		if resp.StatusCode >= 500 {
			return fail("connection_failed")
		}
		return fail("upstream_incompatible")
	}
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media != "application/json" || !utf8.Valid(data) || secure.JSONBudget(data) != nil || json.Unmarshal(data, out) != nil {
		return fail("upstream_incompatible")
	}
	return nil
}
func retryAfter(value string, now time.Time) time.Time {
	if n, e := strconv.Atoi(value); e == nil && n >= 0 {
		return now.Add(time.Duration(min(n, 86400*365)) * time.Second)
	}
	if at, e := http.ParseTime(value); e == nil && at.After(now) {
		return at.UTC()
	}
	return now.Add(time.Minute)
}

type claims struct {
	ID          string `json:"id"`
	Collection  string `json:"collectionId"`
	Type        string `json:"type"`
	Exp         int64  `json:"exp"`
	Refreshable *bool  `json:"refreshable"`
}

func tokenClaims(token string) claims {
	var c claims
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		b, e := base64.RawURLEncoding.DecodeString(parts[1])
		if e == nil && len(b) <= 4096 {
			_ = json.Unmarshal(b, &c)
		}
	}
	return c
}

type authReply struct {
	Token  string `json:"token"`
	Record struct {
		ID         string `json:"id"`
		Collection string `json:"collectionName"`
	} `json:"record"`
}

func (h *Hub) authenticate(ctx context.Context, a Auth, saved *Credential) (Credential, error) {
	h.redact = discoverySecrets{a.Identity, a.Password, a.Token}
	c := Credential{Mode: a.Mode}
	token := a.Token
	if a.Mode == "env" {
		v, e := secure.SecretEnv(a.Name)
		if e != nil || !safeToken(v) {
			return c, fail("auth_required")
		}
		token = v
		c.Env = a.Name
	}
	if a.Mode == "saved" {
		if saved == nil {
			return c, fail("source_not_found")
		}
		c = *saved
		token = c.Token
		if c.Mode == "env" {
			v, e := secure.SecretEnv(c.Env)
			if e != nil {
				return c, fail("auth_required")
			}
			token = v
		}
	}
	h.redact = append(h.redact, token)
	var reply authReply
	if a.Mode == "password" {
		if e := h.call(ctx, "POST", "users/auth-with-password", "", map[string]string{"identity": a.Identity, "password": a.Password}, &reply); e != nil {
			return c, e
		}
		c.Renewable = true
	} else {
		cl := tokenClaims(token)
		if cl.Refreshable != nil && !*cl.Refreshable || cl.Type == "apiKey" {
			if !objectID(cl.ID) {
				return c, fail("auth_method_unsupported")
			}
			var record struct {
				ID string `json:"id"`
			}
			// A public users view cannot prove authentication. Require that the
			// same record is inaccessible anonymously before accepting this
			// nonrenewable token's authenticated own-record response.
			var anonymous struct {
				ID string `json:"id"`
			}
			anonymousErr := h.call(ctx, "GET", "users/records/"+cl.ID, "", nil, &anonymous)
			if anonymousErr == nil {
				return c, fail("auth_method_unsupported")
			}
			switch AsError(anonymousErr).Code {
			case "auth_required", "forbidden", "auth_method_unsupported":
			default:
				return c, anonymousErr
			}

			if e := h.call(ctx, "GET", "users/records/"+cl.ID, token, nil, &record); e != nil {
				return c, e
			}
			if record.ID != cl.ID {
				return c, fail("upstream_incompatible")
			}
			reply.Token = token
			reply.Record.ID = record.ID
			reply.Record.Collection = "users"
			c.Renewable = false
		} else {
			if e := h.call(ctx, "POST", "users/auth-refresh", token, map[string]string{}, &reply); e != nil {
				return c, e
			}
			c.Renewable = true
		}
	}
	if !safeToken(reply.Token) || !objectID(reply.Record.ID) || reply.Record.Collection != "" && reply.Record.Collection != "users" {
		return c, fail("upstream_incompatible")
	}
	h.redact = append(h.redact, reply.Token)
	c.RetryAt = time.Time{}
	c.Subject = "users:" + reply.Record.ID
	if saved != nil && c.Subject != saved.Subject {
		return c, fail("auth_required")
	}
	if claims := tokenClaims(reply.Token); claims.Refreshable != nil && !*claims.Refreshable || claims.Type == "apiKey" {
		c.Renewable = false
	}
	if c.Mode != "env" {
		c.Token = reply.Token
	}
	return c, nil
}

// Connection owns command-local discovery. Collection uses source.Runtime.
type Connection interface {
	Discover(context.Context, Credential) ([]System, bool, error)
	Close()
}

func (s *Store) Authenticate(ctx context.Context, r Request) (Credential, Connection, error) {
	if r.Source.AllowsAnonymous() {
		return s.authenticateBearer(r)
	}
	if r.Auth.Mode == "anonymous" {
		return Credential{}, nil, fail("auth_method_unsupported")
	}
	h, e := NewHub(r.Source)
	if e != nil {
		return Credential{}, nil, e
	}
	var saved *Credential
	if r.Auth.Mode == "saved" {
		p, e := s.Load(r.Profile)
		if e != nil {
			h.Close()
			return Credential{}, nil, e
		}
		entry, ok := findEntry(p, r.Source.ID)
		if !ok {
			h.Close()
			return Credential{}, nil, fail("source_not_found")
		}
		if entry.Source.URL != r.Source.URL || entry.Source.Adapter != r.Source.Adapter {
			h.Close()
			return Credential{}, nil, fail("invalid_request")
		}
		c, e := s.credential(entry.Credential)
		if e != nil {
			h.Close()
			return Credential{}, nil, e
		}
		saved = &c
	}
	c, e := h.authenticate(ctx, r.Auth, saved)
	if e != nil {
		h.Close()
		return c, nil, e
	}
	return c, h, nil
}
func (h *Hub) Discover(ctx context.Context, c Credential) ([]System, bool, error) {
	token := c.Token
	if c.Mode == "env" {
		var e error
		token, e = secure.SecretEnv(c.Env)
		if e != nil {
			return nil, false, fail("auth_required")
		}
	}
	secrets := append(append(discoverySecrets(nil), h.redact...), token)
	systems := []System{}
	seen := map[string]bool{}
	for page := 1; page <= 10; page++ {
		var p struct {
			Page       int      `json:"page"`
			PerPage    int      `json:"perPage"`
			TotalPages int      `json:"totalPages"`
			TotalItems int      `json:"totalItems"`
			Items      []System `json:"items"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "perPage": {"100"}, "sort": {"id"}, "fields": {"id,name"}}
		if e := h.call(ctx, "GET", "systems/records?"+q.Encode(), token, nil, &p); e != nil {
			return nil, false, e
		}
		if p.Page != page || p.PerPage != 100 || p.TotalItems < 0 || p.TotalPages < 0 || p.Items == nil || len(p.Items) > 100 || p.TotalPages > 0 && page > p.TotalPages || p.TotalPages != (p.TotalItems+99)/100 {
			return nil, false, fail("upstream_incompatible")
		}
		for _, v := range p.Items {
			if !objectID(v.ID) || seen[v.ID] || secrets.conflictsID(v.ID) {
				return nil, false, fail("upstream_incompatible")
			}
			seen[v.ID] = true
			v.Name = secrets.cleanName(v.Name, v.ID)
			systems = append(systems, v)
		}
		if page >= p.TotalPages {
			return systems, false, nil
		}
		if len(p.Items) == 0 {
			return nil, false, fail("upstream_incompatible")
		}
	}
	return systems, true, nil
}

// Token rereads credentials on every collection. A per-reference lock merges
// concurrent refreshes. The root lock is held only for the final version check
// and atomic write, so other sources and removals do not wait for network I/O.
func (s *Store) Token(ctx context.Context, profile string, entry Entry, now time.Time) (string, error) {
	c, e := s.credential(entry.Credential)
	if e != nil {
		return "", e
	}
	if entry.Source.AllowsAnonymous() {
		token, err := bearerToken(entry.Source.Adapter, c, entry.Subject)
		if err == nil && entry.Source.Adapter == cfservermonitor.Name && cfsmJWTExpired(token, now) {
			return "", fail("auth_required")
		}
		return token, err
	}
	if c.Mode == "env" {
		v, e := secure.SecretEnv(c.Env)
		if e != nil || !safeToken(v) {
			return "", fail("auth_required")
		}
		if cl := tokenClaims(v); cl.ID != "" && "users:"+cl.ID != entry.Subject {
			return "", fail("auth_required")
		}
		return v, nil
	}
	cl := tokenClaims(c.Token)
	if cl.Exp == 0 {
		return c.Token, nil
	}
	expires := time.Unix(cl.Exp, 0)
	if !c.Renewable {
		// A nonrenewable token cannot be refreshed ahead of time, so it stays
		// usable until it expires rather than until a refresh window opens.
		if now.Before(expires) {
			return c.Token, nil
		}
		return "", fail("auth_required")
	}
	if now.Before(expires.Add(-time.Minute)) {
		return c.Token, nil
	}
	if c.RetryAt.After(now) {
		e := fail("rate_limited")
		e.RetryAt = c.RetryAt
		return "", e
	}
	return s.renew(ctx, profile, entry, c)
}

// Refresh is used at most once after a 401 for an acquisition source. It is
// distinct from expiry-driven renewal and cannot turn persistent 401s into a loop.
func (s *Store) Refresh(ctx context.Context, profile string, entry Entry, sent string) (string, error) {
	if entry.Source.Adapter != "beszel" {
		return "", fail("auth_required")
	}
	c, e := s.credential(entry.Credential)
	if e != nil {
		return "", e
	}
	if c.RetryAt.After(time.Now()) {
		e := fail("rate_limited")
		e.RetryAt = c.RetryAt
		return "", e
	}
	if c.Mode == "env" || !c.Renewable {
		return "", fail("auth_required")
	}
	if c.Token != sent {
		return c.Token, nil
	}
	return s.renew(ctx, profile, entry, c)
}
func (s *Store) renew(ctx context.Context, profile string, entry Entry, c Credential) (string, error) {
	unlockRef, e := s.lockFile(ctx, "refresh-"+entry.Credential+".lock")
	if e != nil {
		return "", e
	}
	defer unlockRef()
	p, e := s.Load(profile)
	if e != nil {
		return "", e
	}
	current, ok := findEntry(p, entry.Source.ID)
	if !ok || current.Credential != entry.Credential || current.Subject != entry.Subject || current.Source.URL != entry.Source.URL {
		return "", fail("source_not_found")
	}
	latest, e := s.credential(entry.Credential)
	if e != nil {
		return "", e
	}
	if latest.RetryAt.After(time.Now()) {
		e := fail("rate_limited")
		e.RetryAt = latest.RetryAt
		return "", e
	}
	if latest.Version != c.Version {
		return latest.Token, nil
	}
	h, e := NewHub(entry.Source)
	if e != nil {
		return "", e
	}
	defer h.Close()
	fresh, e := h.authenticate(ctx, Auth{Mode: "saved"}, &latest)
	if e != nil {
		if AsError(e).Code == "rate_limited" {
			cooldown := latest
			cooldown.RetryAt = AsError(e).RetryAt
			if _, writeErr := s.commitCredential(ctx, profile, entry, latest, cooldown); writeErr != nil {
				return "", writeErr
			}
		}
		return "", e
	}
	return s.commitCredential(ctx, profile, entry, latest, fresh)
}
func (s *Store) commitCredential(ctx context.Context, profile string, entry Entry, latest, fresh Credential) (string, error) {
	unlock, e := s.lock(ctx)
	if e != nil {
		return "", e
	}
	defer unlock()
	p, e := s.Load(profile)
	if e != nil {
		return "", e
	}
	current, ok := findEntry(p, entry.Source.ID)
	if !ok || current.Credential != entry.Credential || current.Subject != entry.Subject || current.Source.URL != entry.Source.URL {
		return "", fail("source_not_found")
	}
	committed, e := s.credential(entry.Credential)
	if e != nil {
		return "", e
	}
	if committed.Version != latest.Version {
		if committed.RetryAt.After(time.Now()) {
			e := fail("rate_limited")
			e.RetryAt = committed.RetryAt
			return "", e
		}
		return committed.Token, nil
	}
	fresh.Version = newID()
	b, _ := json.Marshal(fresh)
	if s.Write(filepath.Join(s.Root, "credential-"+entry.Credential+".json"), append(b, '\n'), 0600) != nil {
		return "", fail("credential_storage_error")
	}
	return fresh.Token, nil
}
