package management

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Receipt struct {
	CommandID     string    `json:"command_id"`
	Deployment    string    `json:"deployment_id"`
	CommandDigest string    `json:"command_digest"`
	Sequence      uint64    `json:"sequence"`
	Generation    uint64    `json:"generation"`
	PayloadDigest string    `json:"payload_sha256"`
	State         string    `json:"state"`
	Code          string    `json:"code,omitempty"`
	At            time.Time `json:"at"`
}

func (r Receipt) Digest() string { return Digest(Marshal(r)) }

type ChannelState struct {
	Quiesced            bool      `json:"quiesced,omitempty"`
	QuiesceUntil        time.Time `json:"quiesce_until,omitempty"`
	PreviousDigest      string    `json:"previous_digest,omitempty"`
	PreviousConstraints string    `json:"previous_constraints,omitempty"`
	ActiveConstraints   string    `json:"active_constraints,omitempty"`
	PreparedConstraints string    `json:"prepared_constraints,omitempty"`
	Sequence            uint64    `json:"sequence"`
	ActiveGeneration    uint64    `json:"active_generation"`
	ActiveDigest        string    `json:"active_digest,omitempty"`
	Prepared            *Receipt  `json:"prepared,omitempty"`
	Last                *Receipt  `json:"last,omitempty"`
}
type State struct {
	Role         string       `json:"role"`
	Observer     string       `json:"observer,omitempty"`
	Schema       int          `json:"schema"`
	Installation string       `json:"installation_id"`
	Controller   string       `json:"controller_id"`
	Project      string       `json:"project"`
	Epoch        uint64       `json:"authority_epoch"`
	TimeFloor    time.Time    `json:"time_floor"`
	Config       ChannelState `json:"config"`
	Updates      ChannelState `json:"updates"`
}
type Store struct {
	failed bool
	mu     sync.Mutex
	dir    string
	state  State
	unlock func() error
}

func Open(dir string, p Policy, epoch uint64) (*Store, error) {
	if !filepath.IsAbs(dir) || epoch == 0 {
		return nil, errors.New("management_state_path_invalid")
	}
	if err := MakePrivateDirectory(dir, false); err != nil {
		return nil, err
	}
	if err := trustedPath(dir, false, false, false); err != nil {
		return nil, err
	}
	unlock, err := secure.Lock(filepath.Join(dir, "writer.lock"))
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, unlock: unlock, state: State{Role: p.Role, Observer: p.Observer, Schema: 1, Installation: p.Installation, Controller: p.Controller.ID, Project: p.Project, Epoch: epoch}}
	b, err := ReadLocal(filepath.Join(dir, "state.json"), 1<<20, false, true)
	if err == nil {
		if err = Decode(b, &s.state, 1<<20); err == nil {
			v := s.state
			if v.Schema != 1 || v.Role != p.Role || v.Observer != p.Observer || v.Installation != p.Installation || v.Controller != p.Controller.ID || v.Project != p.Project || v.Epoch != epoch {
				err = errors.New("management_state_identity_mismatch")
			}
		}
	} else if os.IsNotExist(err) {
		err = s.write(s.state)
	}
	if err != nil {
		unlock()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.unlock() }
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	var copy State
	_ = Decode(Marshal(s.state), &copy, 1<<20)
	return copy
}
func (s *Store) write(v State) error {
	receipts := []Receipt{}
	if v.Config.Last != nil {
		receipts = append(receipts, *v.Config.Last)
	}
	if v.Updates.Last != nil {
		receipts = append(receipts, *v.Updates.Last)
	}
	err := Checkpoint(filepath.Join(s.dir, "state.json"), Marshal(v), Audit{Component: "agent", Installation: v.Installation, Receipts: receipts})
	if err != nil {
		s.failed = true
		if CheckpointCommitted(err) {
			s.state = v
		}
	}
	return err
}
func channel(s *State, name string) *ChannelState {
	if name == "config" {
		return &s.Config
	}
	return &s.Updates
}

// Begin fsyncs authorization and replay protection before exposing an accepted action.
func (s *Store) Begin(c Command, e Envelope, now time.Time) (Receipt, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return Receipt{}, false, errors.New("management_storage_requires_recovery")
	}
	if c.Controller != s.state.Controller || c.Epoch != s.state.Epoch || c.Project != s.state.Project || c.Installation != s.state.Installation || c.Role != s.state.Role || c.Observer != s.state.Observer {
		return Receipt{}, false, errors.New("management_authority_transition_requires_enrollment")
	}
	next := s.state
	ch := channel(&next, c.Channel())
	hash := Digest(Marshal(e))
	if now.Before(next.TimeFloor.Add(-30 * time.Second)) {
		return Receipt{}, false, errors.New("management_clock_rollback")
	}
	if ch.Last != nil && c.Sequence == ch.Last.Sequence {
		if ch.Last.CommandID != c.ID || ch.Last.CommandDigest != hash {
			return Receipt{}, false, errors.New("command_identity_conflict")
		}
		return *ch.Last, true, nil
	}
	if c.Sequence <= ch.Sequence {
		return Receipt{}, false, errors.New("stale_command")
	}
	if c.Base != ch.ActiveGeneration {
		return Receipt{}, false, errors.New("generation_conflict")
	}
	if c.Kind == "config.activate" || c.Kind == "update.activate" || c.Kind == "config.quiesce" {
		pr := ch.Prepared
		if pr == nil || pr.State != "prepared" || pr.Digest() != c.Prepared || pr.Generation != c.Generation || pr.PayloadDigest != c.Digest || pr.Deployment != c.Deployment {
			return Receipt{}, false, errors.New("prepared_receipt_mismatch")
		}
	}
	r := Receipt{c.ID, c.Deployment, hash, c.Sequence, c.Generation, c.Digest, "accepted", "", now.UTC()}
	ch.Sequence = c.Sequence
	ch.Last = &r
	if now.After(next.TimeFloor) {
		next.TimeFloor = now.UTC()
	}
	if err := s.write(next); err != nil {
		return Receipt{}, false, err
	}
	s.state = next
	return r, false, nil
}
func (s *Store) Finish(c Command, state, code string, now time.Time, constraints ...string) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state
	ch := channel(&next, c.Channel())
	if ch.Last == nil || ch.Last.CommandID != c.ID || ch.Last.Sequence != c.Sequence {
		return Receipt{}, errors.New("command_not_current")
	}
	r := *ch.Last
	r.State = state
	r.Code = code
	r.At = now.UTC()
	ch.Last = &r
	switch state {
	case "prepared":
		ch.Prepared = &r
		if len(constraints) > 0 {
			ch.PreparedConstraints = constraints[0]
		}
	case "active":
		ch.Quiesced = false
		ch.QuiesceUntil = time.Time{}
		ch.PreviousDigest = ch.ActiveDigest
		ch.PreviousConstraints = ch.ActiveConstraints
		ch.ActiveGeneration = c.Generation
		ch.ActiveDigest = c.Digest
		ch.ActiveConstraints = ch.PreparedConstraints
		ch.PreparedConstraints = ""
		ch.Prepared = nil
	case "canceled":
		ch.Quiesced = false
		ch.QuiesceUntil = time.Time{}
		ch.Prepared = nil
	}
	if err := s.write(next); err != nil {
		return Receipt{}, err
	}
	s.state = next
	return r, nil
}
func (s *Store) PutBlob(raw []byte, digest string) error {
	if len(raw) > 4<<20 || !HashOK(digest) || Digest(raw) != digest {
		return errors.New("configuration_digest_mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[string]bool{digest: true, s.state.Config.ActiveDigest: true, s.state.Config.ActiveConstraints: true, s.state.Config.PreviousDigest: true, s.state.Config.PreviousConstraints: true, s.state.Config.PreparedConstraints: true}
	if s.state.Config.Last != nil {
		keep[s.state.Config.Last.PayloadDigest] = true
	}
	if s.state.Config.Prepared != nil {
		keep[s.state.Config.Prepared.PayloadDigest] = true
	}
	entries, e := os.ReadDir(s.dir)
	if e != nil {
		return e
	}
	count := 0
	for _, f := range entries {
		n := f.Name()
		if len(n) != 69 || n[64:] != ".json" || !HashOK(n[:64]) {
			continue
		}
		if !keep[n[:64]] {
			if e = os.Remove(filepath.Join(s.dir, n)); e != nil {
				return e
			}
		} else {
			count++
		}
	}
	if count >= 8 {
		if _, e := os.Stat(filepath.Join(s.dir, digest+".json")); e != nil {
			return errors.New("configuration_cache_budget")
		}
	}
	return WriteLocal(filepath.Join(s.dir, digest+".json"), raw)
}
func (s *Store) Blob(digest string) ([]byte, error) {
	if !HashOK(digest) {
		return nil, errors.New("invalid_configuration_digest")
	}
	b, e := ReadLocal(filepath.Join(s.dir, digest+".json"), 4<<20, false, true)
	if e != nil {
		return nil, e
	}
	if Digest(b) != digest {
		return nil, errors.New("configuration_digest_mismatch")
	}
	return b, nil
}

// ImportUpdateReceipt preserves the helper's exact receipt identity. Rewriting
// its timestamp would break the activation command's prepared-receipt binding.
func (s *Store) ImportUpdateReceipt(c Command, r Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state
	ch := &next.Updates
	if c.Channel() != "update" || ch.Last == nil || ch.Last.CommandID != r.CommandID || ch.Last.CommandDigest != r.CommandDigest || ch.Last.Sequence != r.Sequence || r.Generation != c.Generation || r.PayloadDigest != c.Digest || r.Deployment != c.Deployment {
		return errors.New("helper_receipt_mismatch")
	}
	switch r.State {
	case "accepted", "prepared", "healthy", "failed", "rejected", "canceled", "rolled_back", "manual_recovery_required":
	default:
		return errors.New("invalid_helper_receipt")
	}
	ch.Last = &r
	if r.State == "prepared" {
		ch.Prepared = &r
	}
	if r.State == "healthy" {
		ch.ActiveGeneration = c.Generation
		ch.ActiveDigest = c.Digest
		ch.Prepared = nil
	}
	if r.State == "canceled" || r.State == "rolled_back" || r.State == "failed" {
		ch.Prepared = nil
	}
	if e := s.write(next); e != nil {
		return e
	}
	s.state = next
	return nil
}

// RevertConfig requires the store writer lock, so the monitoring process must
// be stopped. Replay floors and update state are deliberately retained.
func (s *Store) RevertConfig() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state
	next.Config.ActiveDigest = ""
	next.Config.ActiveConstraints = ""
	next.Config.PreviousDigest = ""
	next.Config.PreviousConstraints = ""
	next.Config.Prepared = nil
	next.Config.PreparedConstraints = ""
	if e := s.write(next); e != nil {
		return e
	}
	s.state = next
	return nil
}
