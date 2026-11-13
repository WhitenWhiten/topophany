package management

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// CheckpointError distinguishes a failed post-commit audit/fsync from a write
// that left the previous state in place. Callers must stop new transactions in
// either case, but may not undo runtime state when its new pointer is on disk.
type CheckpointError struct {
	Committed bool
	Cause     error
}

func (e *CheckpointError) Error() string { return "management_checkpoint_failed" }
func (e *CheckpointError) Unwrap() error { return e.Cause }
func CheckpointCommitted(err error) bool {
	var e *CheckpointError
	return errors.As(err, &e) && e.Committed
}

// Audit records contain identities/digests and closed outcomes, never configs,
// credentials or signed CDN URLs. Transaction authority remains in durable state.
type Audit struct {
	Schema       int       `json:"schema"`
	At           time.Time `json:"at"`
	Component    string    `json:"component"`
	Actor        string    `json:"actor,omitempty"`
	Installation string    `json:"installation_id,omitempty"`
	StateDigest  string    `json:"state_sha256"`
	Phase        string    `json:"phase"`
	Receipts     []Receipt `json:"receipts,omitempty"`
}

func AuditWrite(dir string, event Audit) error {
	if e := ValidateDirectory(dir, false); e != nil {
		return e
	}
	name := "audit.jsonl"
	limit := int64(2 << 20)
	backups := 3
	if event.Component == "controller" && event.Installation != "" {
		name = "audit-agent-" + Digest([]byte(event.Installation))[:32] + ".jsonl"
		limit = 256 << 10
		backups = 1
	}
	path := filepath.Join(dir, name)
	event.Schema = 1
	event.At = time.Now().UTC()
	raw := append(Marshal(event), '\n')
	if len(raw) > 128<<10 {
		return errors.New("audit_record_budget")
	}
	if st, e := os.Lstat(path); e == nil {
		if e = ValidateLocalFile(path, false, true); e != nil {
			return e
		}
		if st.Size()+int64(len(raw)) > limit {
			for i := backups; i >= 1; i-- {
				old := path + "." + strconv.Itoa(i)
				next := path + "." + strconv.Itoa(i+1)
				if i == backups {
					if st, e := os.Lstat(old); e == nil {
						if !st.Mode().IsRegular() {
							return errors.New("audit_backup_not_regular")
						}
						if e = os.Remove(old); e != nil {
							return e
						}
					}
					continue
				}
				if _, e := os.Lstat(old); e == nil {
					if e = ValidateLocalFile(old, false, true); e != nil {
						return e
					}
					if e = os.Rename(old, next); e != nil {
						return e
					}
				}
			}
			if e = os.Rename(path, path+".1"); e != nil {
				return e
			}
			if e = secure.SyncDir(dir); e != nil {
				return e
			}
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return secure.SyncDir(dir)
}

// Checkpoint records intent before atomic state replacement and confirmation
// afterwards. Any I/O error must stop further mutations until restart recovery.
func Checkpoint(path string, raw []byte, event Audit) error {
	return checkpoint(path, raw, event, AuditWrite, WriteLocal)
}

func checkpoint(path string, raw []byte, event Audit, audit func(string, Audit) error, write func(string, []byte) error) error {
	event.StateDigest = Digest(raw)
	event.Phase = "intent"
	if e := audit(filepath.Dir(path), event); e != nil {
		return e
	}
	if e := write(path, raw); e != nil {
		// An atomic rename can succeed before its directory fsync reports an
		// error. Preserve the visible pointer; never claim successful durability.
		actual, readErr := ReadLocal(path, 32<<20, false, true)
		return &CheckpointError{Committed: readErr == nil && bytes.Equal(actual, raw), Cause: e}
	}
	event.Phase = "committed"
	if e := audit(filepath.Dir(path), event); e != nil {
		return &CheckpointError{Committed: true, Cause: e}
	}
	return nil
}
