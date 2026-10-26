// Package client is independent of the TUI. Read failover never becomes write
// failover, and every successful response is checked against a pinned project.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Endpoint struct {
	URL           string `json:"url"`
	Token         string `json:"token,omitempty"`
	TokenEnv      string `json:"token_env,omitempty"`
	CAFile        string `json:"ca_file,omitempty"`
	AllowInsecure bool   `json:"allow_insecure,omitempty"`
}
type Profile struct {
	Project string     `json:"project"`
	Servers []Endpoint `json:"servers"`
}
type Credentials struct {
	Schema   int                `json:"schema"`
	Active   string             `json:"active"`
	Profiles map[string]Profile `json:"profiles"`
}

func DefaultPath() (string, error) {
	d, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "topophany", "credentials.json"), nil
}
func Load(path string) (Credentials, error) {
	c, e := readCredentials(path)
	if e != nil {
		return c, e
	}
	if c.Schema != 1 || len(c.Profiles) == 0 || !config.ID(c.Active) {
		return c, errors.New("invalid credential schema or active profile")
	}
	if _, ok := c.Profiles[c.Active]; !ok {
		return c, errors.New("active profile missing")
	}
	for k, p := range c.Profiles {
		if !config.ID(k) {
			return c, errors.New("invalid profile name")
		}
		if e = p.Validate(); e != nil {
			return c, e
		}
	}
	return c, nil
}

func readCredentials(path string) (Credentials, error) {
	var c Credentials
	b, e := secure.PrivateRead(path)
	if e != nil {
		return c, e
	}
	if e = secure.Decode(strings.NewReader(string(b)), &c, 1<<20); e != nil {
		return c, e
	}
	return c, nil
}
func Save(path string, c Credentials) error {
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	return secure.AtomicWrite(path, append(b, '\n'), 0600)
}
func (p Profile) Validate() error {
	if !config.ID(p.Project) || len(p.Servers) == 0 || len(p.Servers) > 8 {
		return errors.New("profile needs project and 1..8 collector URLs")
	}
	seen := map[string]bool{}
	for _, s := range p.Servers {
		if e := secure.URL(s.URL, s.AllowInsecure); e != nil {
			return e
		}
		if seen[s.URL] {
			return errors.New("duplicate collector URL")
		}
		seen[s.URL] = true
		if s.Token != "" && s.TokenEnv != "" {
			return errors.New("choose token or token_env, not both")
		}
		if s.TokenEnv == "" {
			if e := secure.CheckToken(s.Token); e != nil {
				return e
			}
		}
	}
	return nil
}

type Client struct {
	topologyMu sync.Mutex
	topologies map[string]cachedTopology
	profile    Profile
	transports []*http.Client
	Now        func() time.Time
}

func New(p Profile) (*Client, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	c := &Client{profile: p, Now: time.Now}
	for _, ep := range p.Servers {
		h, e := HTTP(ep)
		if e != nil {
			c.Close()
			return nil, e
		}
		c.transports = append(c.transports, h)
	}
	return c, nil
}

// URLs are the collectors the client asks, in the order it asks them.
func (c *Client) URLs() []string {
	out := []string{}
	for _, ep := range c.profile.Servers {
		out = append(out, ep.URL)
	}
	return out
}

// Project is the project every answer must be of.
func (c *Client) Project() string { return c.profile.Project }

func (c *Client) Close() {
	for _, h := range c.transports {
		h.CloseIdleConnections()
	}
}
func endpointURL(ep Endpoint, path string, q url.Values) string {
	u, _ := url.Parse(ep.URL)
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	return u.String()
}
func authorize(req *http.Request, ep Endpoint) error {
	token := ep.Token
	if ep.TokenEnv != "" {
		v, e := secure.SecretEnv(ep.TokenEnv)
		if e != nil {
			return e
		}
		token = v
	}
	if e := secure.CheckToken(token); e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Topophany/"+model.Version)
	return nil
}

// ErrProjectMismatch means the peer returned evidence for another project.
var ErrProjectMismatch = errors.New("project identity mismatch; response rejected")

func (c *Client) Snapshot(ctx context.Context, observer string) (model.Snapshot, string, error) {
	return c.snapshot(ctx, observer, true)
}

func (c *Client) snapshot(ctx context.Context, observer string, retryBusy bool) (model.Snapshot, string, error) {
	var empty model.Snapshot
	var skew error // a collector of another release, passed over
	var missed misses
	busy := false
	for i, ep := range c.profile.Servers {
		known := c.knownTopology(ep.URL)
		query := url.Values{"observer": {observer}}
		if known.revision != "" {
			query.Set("topology_revision", known.revision)
		}
		req, e := http.NewRequestWithContext(ctx, "GET", endpointURL(ep, "/v1/snapshot", query), nil)
		if e != nil {
			return empty, "", errors.New("invalid request")
		}
		if e = authorize(req, ep); e != nil {
			return empty, "", e
		}
		attempt, cancel := endpointContext(ctx, len(c.profile.Servers)-i)
		resp, e := c.transports[i].Do(req.WithContext(attempt))
		if e != nil {
			cancel()
			if ctx.Err() != nil {
				return empty, "", ctx.Err()
			}
			missed.add(ep, unreachable(e))
			continue
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, model.MaxSnapshotBytes+1))
		resp.Body.Close()
		cancel()
		if e != nil {
			missed.add(ep, "answer cut off")
			continue
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return empty, "", errors.New("collector authentication/authorization rejected; not falling through silently")
		}
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			// Concurrent readers can meet the collector while it builds a
			// snapshot. Both LAN and overlay addresses may be that same
			// collector, so immediate failover alone cannot clear the lock.
			// Only this explicit, bounded busy response earns one delayed
			// retry, after trying the other configured collectors first.
			if resp.StatusCode == 429 && len(b) <= 1024 {
				var reason struct {
					Error string `json:"error"`
				}
				if json.Unmarshal(b, &reason) == nil && reason.Error == "snapshot_busy" {
					busy = true
				}
			}
			missed.add(ep, unavailable(resp.StatusCode))
			continue
		}
		if resp.StatusCode != 200 {
			return empty, "", fmt.Errorf("collector rejected read (HTTP %d)", resp.StatusCode)
		}
		if len(b) > model.MaxSnapshotBytes {
			return empty, "", errors.New("snapshot budget exceeded")
		}
		var snap model.Snapshot
		if e = decodeSnapshot(b, &snap, known); e != nil {
			// During a rolling upgrade another collector may already
			// speak this client's schema; a malformed snapshot is refused.
			var schema errSchema
			if errors.As(e, &schema) {
				skew = schema
				missed.add(ep, "another release")
				continue
			}
			return empty, "", errors.New("invalid collector snapshot")
		}
		if snap.Project != c.profile.Project || snap.Topology.Project != c.profile.Project {
			return empty, "", ErrProjectMismatch
		}
		if (snap.Schema != model.BatchSchemaContracts && snap.Schema != model.SnapshotSchema) || snap.GeneratedAt.IsZero() {
			return empty, "", errors.New("unsupported snapshot contract")
		}
		if snap.ViewObserver != observer {
			return empty, "", errors.New("observer view mismatch")
		}
		// A serving HTTP process with an old snapshot is not a healthy data source.
		if c.Now().Sub(snap.GeneratedAt) > 30*time.Second || snap.GeneratedAt.After(c.Now().Add(30*time.Second)) {
			missed.add(ep, "snapshot not current — are both clocks right?")
			continue
		}
		c.rememberTopology(ep.URL, snap)
		return snap, ep.URL, nil
	}
	if busy && retryBusy {
		// The server advertises Retry-After: 1. Use a fixed upper bound
		// rather than trusting an unbounded response header. The viewer
		// keeps rendering while this read waits, and cancellation is prompt.
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return empty, "", ctx.Err()
		case <-timer.C:
			return c.snapshot(ctx, observer, false)
		}
	}
	if skew != nil {
		return empty, "", fmt.Errorf("%w with a current, matching snapshot: %v%s", ErrNoCollector, skew, missed)
	}
	return empty, "", fmt.Errorf("%w with a current, matching snapshot%s", ErrNoCollector, missed)
}

// ErrNoCollector is a read that no configured collector answered: each was
// unreachable, failing or, for a snapshot, stale.
var ErrNoCollector = errors.New("no configured collector answered")

// History is a check's raw samples under its current contract, newest
// first. Revision is the topology's and Contract the check's, both public.
type History struct {
	Project  string              `json:"project"`
	Revision string              `json:"revision"`
	Check    string              `json:"check"`
	Contract string              `json:"contract"`
	Entries  []model.Observation `json:"entries"`
}

// History reads up to limit (1..500) of a check's samples, failing over as
// Snapshot does: past unreachable or failing collectors, never past a
// rejection.
func (c *Client) History(ctx context.Context, check string, limit int) (History, string, error) {
	return c.history(ctx, check, limit, nil)
}

func (c *Client) history(ctx context.Context, check string, limit int, snapshot *HistoryView) (History, string, error) {
	var empty History
	if !config.ID(check) || limit < 1 || limit > 500 {
		return empty, "", errors.New("history needs a check ID and a limit of 1..500")
	}
	var missed misses
	for i, ep := range c.profile.Servers {
		if snapshot != nil && ep.URL != snapshot.Server {
			continue
		}
		req, e := http.NewRequestWithContext(ctx, "GET", endpointURL(ep, "/v1/history", url.Values{"check": {check}, "limit": {fmt.Sprint(limit)}}), nil)
		if e != nil {
			return empty, "", errors.New("invalid request")
		}
		if e = authorize(req, ep); e != nil {
			return empty, "", e
		}
		resp, e := c.transports[i].Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return empty, "", ctx.Err()
			}
			missed.add(ep, unreachable(e))
			continue
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, maxHistoryBytes+1))
		resp.Body.Close()
		if e != nil {
			missed.add(ep, "answer cut off")
			continue
		}
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return empty, "", errors.New("collector authentication/authorization rejected; not falling through silently")
		case resp.StatusCode == 404:
			return empty, "", fmt.Errorf("collector has no check %q", check)
		case resp.StatusCode >= 500 || resp.StatusCode == 429:
			missed.add(ep, unavailable(resp.StatusCode))
			continue
		case resp.StatusCode != 200:
			return empty, "", fmt.Errorf("collector rejected read (HTTP %d)", resp.StatusCode)
		case len(b) > maxHistoryBytes:
			return empty, "", errors.New("history budget exceeded")
		}
		var h History
		if e = secure.Decode(bytes.NewReader(b), &h, maxHistoryBytes); e != nil {
			return empty, "", errors.New("invalid collector history")
		}
		if h.Project != c.profile.Project {
			return empty, "", ErrProjectMismatch
		}
		if len(h.Revision) != 64 || len(h.Contract) != 64 || h.Check != check || len(h.Entries) > limit {
			return empty, "", errors.New("invalid collector history")
		}
		// Each entry is held to what the collector held it to when a
		// worker sent it, so nothing drawn from it, such as a latency bar
		// from its duration, meets a value a sample cannot have.
		for _, v := range h.Entries {
			if v.Check != check || v.Project != h.Project || v.Contract != h.Contract || v.Revision != "" || !validEvidence(v) {
				return empty, "", errors.New("invalid collector history")
			}
		}
		if snapshot != nil {
			if e = snapshot.Match(h, ep.URL); e != nil {
				return empty, "", e
			}
		}
		return h, ep.URL, nil
	}
	return empty, "", fmt.Errorf("%w with the history of %s%s", ErrNoCollector, check, missed)
}

// A collector bounds history to 1 MiB of samples; the envelope adds little.
const maxHistoryBytes = 2 << 20

// Rollups are a check's rollups under its current contract, laid out in
// slots of Step seconds, oldest first, with their Total. Since is the
// earliest moment the collector's rollups hold for the check. Revision is
// the topology's and Contract the check's, both public.
type Rollups struct {
	Project     string         `json:"project"`
	Revision    string         `json:"revision"`
	Check       string         `json:"check"`
	Contract    string         `json:"contract"`
	GeneratedAt time.Time      `json:"generated_at"`
	Step        int64          `json:"step_seconds"`
	Since       time.Time      `json:"since,omitzero"`
	Total       model.Rollup   `json:"total"`
	Slots       []model.Rollup `json:"slots"`
}

// Rollups reads a check's rollups in slots of step (5m, 1h or 1d) over the
// span before now, failing over as History does.
func (c *Client) Rollups(ctx context.Context, check, step, span string) (Rollups, string, error) {
	var empty Rollups
	if !config.ID(check) || len(step) > 8 || len(span) > 8 {
		return empty, "", errors.New("rollups need a check ID, a step and a span")
	}
	var missed misses
	for i, ep := range c.profile.Servers {
		req, e := http.NewRequestWithContext(ctx, "GET", endpointURL(ep, "/v1/rollups", url.Values{"check": {check}, "step": {step}, "span": {span}}), nil)
		if e != nil {
			return empty, "", errors.New("invalid request")
		}
		if e = authorize(req, ep); e != nil {
			return empty, "", e
		}
		resp, e := c.transports[i].Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return empty, "", ctx.Err()
			}
			missed.add(ep, unreachable(e))
			continue
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, maxRollupBytes+1))
		resp.Body.Close()
		if e != nil {
			missed.add(ep, "answer cut off")
			continue
		}
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return empty, "", errors.New("collector authentication/authorization rejected; not falling through silently")
		case resp.StatusCode == 404:
			return empty, "", fmt.Errorf("collector has no check %q", check)
		case resp.StatusCode == 400:
			return empty, "", errors.New("collector refused the step or span; steps are 5m, 1h and 1d, spans up to 800d and 999 steps")
		case resp.StatusCode >= 500 || resp.StatusCode == 429:
			missed.add(ep, unavailable(resp.StatusCode))
			continue
		case resp.StatusCode != 200:
			return empty, "", fmt.Errorf("collector rejected read (HTTP %d)", resp.StatusCode)
		case len(b) > maxRollupBytes:
			return empty, "", errors.New("rollups budget exceeded")
		}
		// Like a snapshot, and unlike history, rollups may gain fields in
		// later releases; this client ignores those it does not know.
		var r Rollups
		if secure.JSONBudget(b) != nil || json.Unmarshal(b, &r) != nil {
			return empty, "", errors.New("invalid collector rollups")
		}
		if r.Project != c.profile.Project {
			return empty, "", ErrProjectMismatch
		}
		if len(r.Revision) != 64 || len(r.Contract) != 64 || r.Check != check || r.Step <= 0 || len(r.Slots) > 1000 || !validRollup(r.Total) {
			return empty, "", errors.New("invalid collector rollups")
		}
		for i, slot := range r.Slots {
			if !validRollup(slot) || i > 0 && slot.Start.Before(r.Slots[i-1].Start.Add(time.Duration(r.Slots[i-1].Seconds)*time.Second)) {
				return empty, "", errors.New("invalid collector rollups")
			}
		}
		return r, ep.URL, nil
	}
	return empty, "", fmt.Errorf("%w with the rollups of %s%s", ErrNoCollector, check, missed)
}

// A response of a thousand slots stays well under this.
const maxRollupBytes = 1 << 20

type HTTPError struct {
	Status int
	Code   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("collector HTTP %d: %s", e.Status, e.Code) }

// PostBatch retries one interrupted, deduplicated sample batch. The encoded
// samples (including IDs and timestamps) are reused exactly; heartbeat-only
// requests have no receipt identity and use the ordinary delivery backoff.
func PostBatch(ctx context.Context, h *http.Client, ep Endpoint, b model.Batch) error {
	safe := len(b.Observations) > 0
	for _, v := range b.Observations {
		safe = safe && v.ID != ""
	}
	err := postBatch(ctx, h, ep, b)
	if safe && ctx.Err() == nil && connectionClosed(err) {
		h.CloseIdleConnections()
		return postBatch(ctx, h, ep, b)
	}
	return err
}
func connectionClosed(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}

func postBatch(ctx context.Context, h *http.Client, ep Endpoint, b model.Batch) error {
	body, e := json.Marshal(b)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", endpointURL(ep, "/v1/observations", nil), bytes.NewReader(body))
	if e != nil {
		return errors.New("invalid collector request")
	}
	if e = authorize(req, ep); e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := h.Do(req)
	if e != nil {
		return fmt.Errorf("collector transport interrupted: %w", e)
	}
	defer resp.Body.Close()
	payload, e := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if e != nil {
		return fmt.Errorf("collector acknowledgement interrupted: %w", e)
	}
	if len(payload) > 4096 {
		return errors.New("invalid collector acknowledgement")
	}
	if resp.StatusCode != 202 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(payload, &v)
		code := secure.Text(v.Error)
		if len(code) > 128 {
			code = "invalid_error"
		}
		return &HTTPError{Status: resp.StatusCode, Code: code}
	}
	var ack struct {
		Project  string `json:"project"`
		Revision string `json:"revision"`
		Accepted int    `json:"accepted"`
	}
	if e = json.Unmarshal(payload, &ack); e != nil || ack.Project != b.Project || ack.Revision != b.Revision || ack.Accepted != len(b.Observations) {
		return errors.New("collector acknowledgement identity/count mismatch")
	}
	return nil
}
