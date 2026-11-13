// Package outbox stores immutable observations until every configured collector
// has acknowledged them. Permanent rejections enter a separate, rotating forensic budget.
package outbox

import (
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

var ErrFull = errors.New("outbox byte or record budget exhausted; execution must stop")

// DegradedError reports destinations whose new evidence could not be queued.
// Other destinations have already been durably queued; callers must continue
// delivery. Add returns ErrFull, never this, when no destination had room.
type DegradedError struct{ Recipients []string }

func (e *DegradedError) Error() string {
	return fmt.Sprintf("outbox destination quota exhausted; new evidence dropped for %v", e.Recipients)
}

type Item struct {
	Schema  int         `json:"schema"`
	ID      string      `json:"id"`
	Batch   model.Batch `json:"batch"`
	Pending []string    `json:"pending"`
	SHA256  string      `json:"sha256"`
}

// Options bounds disk use and the in-memory index independently.
type Options struct {
	MaxBytes   int64
	MaxRecords int
}
type itemMeta struct {
	Pending []string
	At      time.Time
}

type Queue struct {
	quarantineEvents uint64
	mu               sync.Mutex
	dir              string
	project          string
	max              int64
	maxRecords       int
	used             int64
	quarantineUsed   int64
	items            map[string]itemMeta
	pending          map[string]*pendingIndex
	itemSizes        map[string]int64
	recipientBytes   map[string]int64
	unlock           func() error
	closed           bool
	ackFile          *os.File
	ackBytes         int64
	acked            map[string]bool
	ackError         error
}

func itemHash(v Item) string { v.SHA256 = ""; b, _ := json.Marshal(v); return secure.Hash(string(b)) }
func Open(dir, project string, maxBytes int64) (*Queue, error) {
	return OpenWithOptions(dir, project, Options{MaxBytes: maxBytes})
}

func OpenWithOptions(dir, project string, options Options) (*Queue, error) {
	maxBytes := options.MaxBytes
	if options.MaxRecords == 0 {
		options.MaxRecords = 20000
	}
	if options.MaxRecords < 1 || options.MaxRecords > 1000000 {
		return nil, errors.New("outbox record limit must be 1..1000000")
	}
	if maxBytes < 1024 {
		return nil, errors.New("outbox budget must be at least 1024 bytes")
	}
	// A record is queued once it and pending are synced, which makes
	// neither pending nor the directories above it durable: those are
	// synced as they are made.
	if e := secure.MkdirAll(filepath.Join(dir, "pending"), 0700); e != nil {
		return nil, e
	}
	if e := secure.MkdirAll(filepath.Join(dir, "quarantine"), 0700); e != nil {
		return nil, e
	}
	unlock, e := secure.Lock(filepath.Join(dir, "writer.lock"))
	if e != nil {
		return nil, e
	}
	q := &Queue{dir: dir, project: project, max: maxBytes, maxRecords: options.MaxRecords, items: map[string]itemMeta{}, pending: map[string]*pendingIndex{}, itemSizes: map[string]int64{}, recipientBytes: map[string]int64{}, unlock: unlock}
	ok := false
	defer func() {
		if !ok {
			if q.ackFile != nil {
				q.ackFile.Close()
			}
			unlock()
		}
	}()
	for _, sub := range []string{"pending", "quarantine"} {
		entries, e := queueReadDir(filepath.Join(dir, sub))
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil, errors.New("invalid outbox entry")
			}
			info, e := queueInfo(entry)
			if e != nil {
				return nil, e
			}
			if !info.Mode().IsRegular() {
				return nil, errors.New("invalid outbox entry")
			}
			q.used += info.Size()
			if q.used-q.quarantineUsed > maxBytes && sub == "pending" {
				return nil, ErrFull
			}
			if info.Size() > 2<<20 {
				return nil, errors.New("oversized outbox record")
			}
			if sub == "quarantine" {
				q.quarantineUsed += info.Size()
				continue
			}
			if len(q.items) >= q.maxRecords {
				return nil, ErrFull
			}
			b, e := queueRead(filepath.Join(dir, sub, entry.Name()), 2<<20)
			if e != nil {
				return nil, e
			}
			var v Item
			if e = json.Unmarshal(b, &v); e != nil || v.Schema != 1 || v.SHA256 != itemHash(v) || entry.Name() != v.ID+".json" || v.Batch.Project != project || !validItem(v) {
				return nil, errors.New("outbox corruption or project mismatch")
			}
			q.account(v, info.Size())
		}
	}
	if q.used-q.quarantineUsed > maxBytes {
		return nil, ErrFull
	}
	if e := q.openAcks(); e != nil {
		return nil, e
	}
	if e := q.trimQuarantine("", "", 0); e != nil {
		return nil, e
	}
	ok = true
	return q, nil
}
func validItem(v Item) bool {
	if len(v.Batch.Observations) == 0 || len(v.Batch.Observations) > 100 || len(v.Pending) == 0 || len(v.Pending) > 8 {
		return false
	}
	raw, e := json.Marshal(v.Batch)
	if e != nil || v.ID != secure.Hash(string(raw)) {
		return false
	}
	seen := map[string]bool{}
	for _, r := range v.Pending {
		if len(r) != 16 || strings.Trim(r, "0123456789abcdef") != "" || seen[r] {
			return false
		}
		seen[r] = true
	}
	return true
}
func (q *Queue) file(id string) string { return filepath.Join(q.dir, "pending", id+".json") }
func (q *Queue) write(v Item) error {
	v.SHA256 = itemHash(v)
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > 1<<20 {
		return errors.New("outbox record exceeds 1 MiB")
	}
	old := int64(0)
	if st, e := queueStat(q.file(v.ID)); e == nil {
		old = st.Size()
	}
	if q.used-q.quarantineUsed-old+int64(len(b)) > q.max {
		return ErrFull
	}
	if e = queueWrite(q.file(v.ID), b, 0600); e != nil {
		return e
	}
	q.used = q.used - old + int64(len(b))
	q.account(v, int64(len(b)))
	return nil
}

// account updates independent recipient reservations on durable record changes.
func (q *Queue) account(v Item, size int64) {
	old := q.items[v.ID]
	for _, r := range old.Pending {
		q.recipientBytes[r] -= q.itemSizes[v.ID]
		if !slices.Contains(v.Pending, r) {
			q.indexRecipient(r, Item{ID: v.ID}, false)
		}
	}
	for _, r := range v.Pending {
		q.recipientBytes[r] += size
		if !slices.Contains(old.Pending, r) {
			q.indexRecipient(r, v, true)
		}
	}
	at := time.Time{}
	if len(v.Batch.Observations) > 0 {
		at = v.Batch.Observations[0].SampledAt
	}
	q.items[v.ID], q.itemSizes[v.ID] = itemMeta{Pending: slices.Clone(v.Pending), At: at}, size
}

func (q *Queue) Add(b model.Batch, recipients []string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("outbox closed")
	}
	if q.ackError != nil {
		return q.ackError
	}
	if b.Project != q.project || len(b.Observations) == 0 || len(b.Observations) > 100 || len(recipients) == 0 || len(recipients) > 8 {
		return errors.New("outbox needs matching project, observations and recipients")
	}
	for _, r := range recipients {
		if len(r) != 16 || strings.Trim(r, "0123456789abcdef") != "" {
			return errors.New("invalid recipient ID")
		}
	}
	raw, e := json.Marshal(b)
	if e != nil {
		return e
	}
	if len(raw) > (1<<20)-8192 {
		return errors.New("outbox batch exceeds transport budget")
	}
	id := secure.Hash(string(raw))
	if _, ok := q.items[id]; ok {
		return nil
	}
	if q.maxRecords < len(recipients) {
		return errors.New("outbox record budget must cover every recipient")
	}
	if len(q.items) >= q.maxRecords {
		return ErrFull
	}
	// The same immutable batch may be explicitly queued again after delivery.
	// Remove old ACK identities durably before publishing its new record.
	if q.acked[id] {
		if e := q.compactAcks(); e != nil {
			return e
		}
	}
	v := Item{Schema: 1, ID: id, Batch: model.CloneBatch(b), Pending: append([]string(nil), recipients...)}
	sort.Strings(v.Pending)
	for i := 1; i < len(v.Pending); i++ {
		if v.Pending[i] == v.Pending[i-1] {
			return errors.New("duplicate recipient")
		}
	}
	// Charge each recipient the full record size, even though bytes are shared.
	// This conservative reservation guarantees the sum cannot exceed the global
	// budget and is reconstructed from pending records after every restart.
	v.SHA256 = itemHash(v)
	encoded, _ := json.Marshal(v)
	share := q.max / int64(len(recipients))
	recordShare := q.maxRecords / len(recipients)
	admitted, rejected := []string{}, []string{}
	for _, r := range v.Pending {
		records := 0
		if pending := q.pending[r]; pending != nil {
			records = pending.Len()
		}
		if records >= recordShare || q.recipientBytes[r]+int64(len(encoded)) > share {
			rejected = append(rejected, r)
		} else {
			admitted = append(admitted, r)
		}
	}
	// With room at no recipient the record is kept for none: that is a
	// full outbox, however many recipients there are, not a degraded one.
	if len(admitted) == 0 {
		return ErrFull
	}
	v.Pending = admitted
	if err := q.write(v); err != nil {
		return err
	}
	if len(rejected) > 0 {
		return &DegradedError{Recipients: rejected}
	}
	return nil
}

// Pending is the earliest sampled items still owed to recipient, at most
// limit of them (all when limit is not positive). Per-recipient ordered indexes
// bound each round's work to the selected items rather than the whole backlog.
func (q *Queue) Pending(recipient string, limit int) []Item {
	return q.PendingBytes(recipient, limit, 0)
}

// PendingBytes bounds a delivery's detached copy by encoded record bytes as
// well as count. Always include the first item so a large record can progress.
// Zero maxBytes preserves Pending's unrestricted diagnostic behavior.
func (q *Queue) PendingBytes(recipient string, limit int, maxBytes int64) []Item {
	out, _ := q.ReadPending(recipient, limit, maxBytes)
	return out
}

// ReadPending loads only the selected, byte-bounded batch. A corrupt or missing
// payload is an explicit local error; no delivery or ACK may consume that batch.
func (q *Queue) ReadPending(recipient string, limit int, maxBytes int64) ([]Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, errors.New("outbox closed")
	}
	ids := q.pendingIDs(recipient, limit, maxBytes)
	out := make([]Item, 0, len(ids))
	for _, id := range ids {
		v, err := q.loadItem(id)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (q *Queue) pendingIDs(recipient string, limit int, maxBytes int64) []string {
	out := []string{}
	p := q.pending[recipient]
	if p == nil {
		return out
	}
	var size int64
	entries := []pendingEntry{}
	for p.Len() > 0 && (limit <= 0 || len(out) < limit) {
		entry := p.entries[0]
		n := q.itemSizes[entry.id]
		if maxBytes > 0 && len(out) > 0 && size+n > maxBytes {
			break
		}
		entries = append(entries, heap.Pop(p).(pendingEntry))
		out = append(out, entry.id)
		size += n
	}
	for _, entry := range entries {
		heap.Push(p, entry)
	}
	return out
}

func (q *Queue) loadItem(id string) (Item, error) {
	raw, err := queueRead(q.file(id), 2<<20)
	if err != nil {
		return Item{}, err
	}
	var v Item
	if json.Unmarshal(raw, &v) != nil || v.Schema != 1 || v.ID != id || v.SHA256 != itemHash(v) || v.Batch.Project != q.project || !validItem(v) {
		return Item{}, errors.New("outbox payload corruption")
	}
	v.Pending = slices.Clone(q.items[id].Pending)
	v.SHA256 = itemHash(v)
	return v, nil
}

func (q *Queue) Ack(ids []string, recipient string) (err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("outbox closed")
	}
	if q.ackError != nil {
		return q.ackError
	}
	// Keep each log record within the transport's 100-item batch bound.
	for start := 0; start < len(ids); start += 100 {
		partial := []string{}
		seen := map[string]bool{}
		for _, id := range ids[start:min(start+100, len(ids))] {
			if v, ok := q.items[id]; ok && len(v.Pending) > 1 && slices.Contains(v.Pending, recipient) && !seen[id] {
				partial = append(partial, id)
				seen[id] = true
			}
		}
		if e := q.logAcks(partial, recipient); e != nil {
			return e
		}
	}
	removed := false
	defer func() { err = q.syncRemoved(removed, err) }()
	for _, id := range ids {
		deleted, e := q.ack(id, recipient)
		removed = removed || deleted
		if e != nil {
			return e
		}
	}
	return nil
}

// Sync even after a later deletion fails. A sync failure can replay already
// acknowledged samples after restart; collector deduplication makes that safe.
func (q *Queue) syncRemoved(removed bool, err error) error {
	if removed {
		return errors.Join(err, secure.SyncDir(filepath.Join(q.dir, "pending")))
	}
	return err
}

func (q *Queue) ack(id, recipient string) (bool, error) {
	v, ok := q.items[id]
	if !ok {
		return false, nil
	}
	pending := []string{}
	found := false
	for _, r := range v.Pending {
		if r == recipient {
			found = true
		} else {
			pending = append(pending, r)
		}
	}
	if !found {
		return false, nil
	}
	if len(pending) == 0 {
		st, e := queueStat(q.file(id))
		if e != nil {
			return false, e
		}
		if e = queueRemove(q.file(id)); e != nil {
			return false, e
		}
		q.used -= st.Size()
		q.account(Item{ID: id}, 0)
		delete(q.items, id)
		delete(q.itemSizes, id)
		return true, nil
	}
	return false, q.logAcks([]string{id}, recipient)
}
func (q *Queue) Quarantine(ids []string, recipient, reason string) (err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("outbox closed")
	}
	if q.ackError != nil {
		return q.ackError
	}
	if len(recipient) != 16 || strings.Trim(recipient, "0123456789abcdef") != "" {
		return errors.New("invalid quarantine recipient")
	}
	removed := false
	defer func() { err = q.syncRemoved(removed, err) }()
	reason = secure.Text(reason)
	if len(reason) > 256 {
		reason = reason[:256]
	}
	for _, id := range ids {
		meta, ok := q.items[id]
		if !ok || !slices.Contains(meta.Pending, recipient) {
			continue
		}
		v, e := q.loadItem(id)
		if e != nil {
			return e
		}
		if e = q.quarantineItem(v, recipient, reason); e != nil {
			return e
		}
		deleted, e := q.ack(id, recipient)
		removed = removed || deleted
		if e != nil {
			return e
		}
	}
	return nil
}
func (q *Queue) Stats() (int, int64) { q.mu.Lock(); defer q.mu.Unlock(); return len(q.items), q.used }
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	return errors.Join(q.ackFile.Close(), q.unlock())
}
func Recipient(url string) string { return secure.Hash(url)[:16] }
func (q *Queue) String() string {
	n, b := q.Stats()
	return fmt.Sprintf("outbox: %d pending records, %d bytes including quarantine", n, b)
}

// ManagementDrainStats distinguishes acknowledgments from new quarantine during a pause.
func (q *Queue) ManagementDrainStats() (int, uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items), q.quarantineEvents
}
