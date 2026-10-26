// Package session exposes a bounded, private copy of a running viewer's state.
// It never reads credentials or stores topology, samples, request/response bodies.
package session

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

const Retention = 30 * time.Minute
const MaxEvents = 64

type Event struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Code    string    `json:"code,omitempty"`
	Message string    `json:"message,omitempty"`
}
type Filters struct {
	Search        string `json:"search,omitempty"`
	Application   string `json:"application,omitempty"`
	Environment   string `json:"environment,omitempty"`
	Service       string `json:"service,omitempty"`
	AttentionOnly bool   `json:"attention_only"`
}
type Notice struct {
	Visible bool      `json:"visible"`
	Text    string    `json:"text"`
	Error   bool      `json:"error"`
	At      time.Time `json:"at,omitzero"`
}
type History struct {
	Check   string `json:"check"`
	Error   string `json:"error"`
	Samples int    `json:"samples"`
}
type State struct {
	Requests     []model.TransportEvent `json:"requests,omitempty"`
	Schema       int                    `json:"schema"`
	ID           string                 `json:"session"`
	PID          int                    `json:"pid"`
	StartedAt    time.Time              `json:"started_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
	Live         bool                   `json:"live"`
	Project      string                 `json:"project"`
	Server       string                 `json:"server"`
	Collectors   []string               `json:"collectors"`
	Observer     string                 `json:"observer"`
	Filters      Filters                `json:"filters"`
	Tab          string                 `json:"tab"`
	Selection    string                 `json:"selection"`
	SnapshotTime time.Time              `json:"snapshot_time,omitzero"`
	Instance     string                 `json:"instance"`
	Revision     string                 `json:"revision"`
	Cached       bool                   `json:"cached"`
	Fetching     bool                   `json:"fetching"`
	Loading      string                 `json:"loading"`
	Error        string                 `json:"error"`
	Notice       Notice                 `json:"notice"`
	History      History                `json:"history"`
	Events       []Event                `json:"events"`
}

type Writer struct {
	root, id string
	started  time.Time
	last     []byte
	written  time.Time
}

func DefaultRoot() (string, error) {
	root, err := os.UserCacheDir()
	return filepath.Join(root, "topophany", "sessions"), err
}
func New(root string) (*Writer, error) {
	if err := secure.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	id, err := secure.Token()
	if err != nil {
		return nil, err
	}
	return &Writer{root: root, id: id[:24], started: time.Now()}, nil
}
func (w *Writer) ID() string { return w.id }
func (w *Writer) Close()     { _ = os.Remove(filepath.Join(w.root, w.id+".json")) }
func (w *Writer) Publish(state State, now time.Time) error {
	state.Schema, state.ID, state.PID, state.StartedAt, state.Live = 1, w.id, os.Getpid(), w.started, true
	// State changes publish immediately; unchanged frames refresh the lease at 1 Hz.
	state.UpdatedAt = time.Time{}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if string(body) == string(w.last) && now.Sub(w.written) < time.Second {
		return nil
	}
	state.UpdatedAt = now
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(raw) > 128<<10 {
		return errors.New("session diagnostics exceed budget")
	}
	if err = secure.AtomicWrite(filepath.Join(w.root, w.id+".json"), raw, 0600); err != nil {
		return err
	}
	w.last, w.written = body, now
	return nil
}
func Read(root, id string, now time.Time) (State, error) {
	var state State
	if len(id) != 24 || strings.Trim(id, "0123456789abcdef") != "" {
		return state, errors.New("session must be the ID from topo sessions")
	}
	raw, err := secure.PrivateRead(filepath.Join(root, id+".json"))
	if err != nil {
		return state, err
	}
	if len(raw) > 128<<10 || json.Unmarshal(raw, &state) != nil || state.Schema != 1 || state.ID != id || len(state.Events) > MaxEvents || len(state.Requests) > MaxEvents {
		return State{}, errors.New("invalid session diagnostic record")
	}
	state.Live = now.Sub(state.UpdatedAt) <= 10*time.Second && !state.UpdatedAt.After(now.Add(time.Second))
	return state, nil
}
func List(root string, now time.Time) ([]State, error) {
	out := []State{}
	dir, err := os.Open(root)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(257)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > 256 {
		return nil, errors.New("session directory exceeds 256 entries; remove stale session files")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		state, err := Read(root, id, now)
		if err != nil {
			continue
		}
		if state.Live {
			out = append(out, state)
		} else if now.Sub(state.UpdatedAt) > Retention {
			_ = os.Remove(filepath.Join(root, entry.Name()))
		}
	}
	slices.SortFunc(out, func(a, b State) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
