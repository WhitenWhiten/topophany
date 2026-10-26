// Package store implements a single-writer, checksummed, fsync-before-ack
// journal and atomic checkpoints. It requires a local filesystem, never a
// shared NFS/SMB volume. Collectors replicate observations, not database files.
// Besides raw history, the store keeps rollups of every sample it accepts
// (see package rollup), written with each checkpoint and rebuilt from the
// journal after it, so they count each sample exactly once.
package store

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/admission"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/rollup"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

var ErrConflict = errors.New("observation ID reused with different contents")
var ErrCapacity = errors.New("receipt capacity reached; collection paused instead of losing deduplication")
var ErrHeartbeatRate = errors.New("observer heartbeat durability rate exceeded")

// Heartbeat-only writes get one durable update per observer per second, below
// the normal worker's two-second delivery interval. ReceivedAt is collector
// owned and recovered from disk; sender timestamps, boots and revisions cannot
// reset this budget. Heartbeats piggybacked on new observations cost no extra I/O.
const heartbeatWriteInterval = time.Second

// Options bound the store. Revision and Contracts, when set, are the
// topology a collector counts samples under (see config.Counted): only
// their samples keep state tails. Stale is how long each check's sample
// stays current, which is as long as its outcome stands in the rollups.
// Retention is also the ingestion window: a receipt is kept until no
// sample it could match can be accepted, an hour after it.
type Options struct {
	Observers       []string
	ObserverWeights map[string]int
	weightTotal     int64
	Revision        string
	Contracts       map[string]string
	Stale           map[string]time.Duration
	MaxHistory      int
	MaxReceipts     int
	Retention       time.Duration
	CheckpointBytes int64
	MaxJournalBytes int64 // adaptive checkpoint ceiling; bounds restart replay work
	MaxStateBytes   int64
	RollupBytes     int64
	// Bindings and Sources describe external evidence admitted by this store.
	// Direct collectors (local checks) and the API use the same merge. A
	// store with no context refuses external observations instead of
	// guessing a binding's age or subject.
	Bindings      map[string]model.ExternalBinding
	Sources       map[string]model.Source
	ExternalRules map[string]model.ExternalRule
	Now           func() time.Time
	// MonotonicNow supplies elapsed time for live admission; nil uses time.Now.
	MonotonicNow func() time.Time
}

// receiptGrace is how long a receipt outlives the ingestion window.
const receiptGrace = time.Hour

// floorBytes is what a boot's floor costs in its observer's quota.
const floorBytes = 128

// floorShare is the part of an observer's share of the state budget its
// floors may take, one floorShare-th: see shedFloors.
const floorShare = 8

// carryBytes is what a series' carry costs in the state budget.
const carryBytes = 512

type receipt struct {
	Observer string    `json:"observer,omitempty"`
	Hash     digest    `json:"hash"`
	At       time.Time `json:"at"`
}

// diskState is a checkpoint. Floors are, for each boot (observer.boot)
// whose receipts expired, the highest sequence among them: an ID at or
// below its boot's floor stays bound and is never accepted again, while
// the floor is held. FloorsAt says when each floor's receipt was taken; a
// checkpoint from before them has none, and its floors count as oldest.
type diskState struct {
	Schema   int                        `json:"schema"`
	Rows     []model.Observation        `json:"rows"`
	Seen     map[string]receipt         `json:"seen"`
	Beats    map[string]model.Heartbeat `json:"beats"`
	Floors   map[string]uint64          `json:"floors,omitempty"`
	FloorsAt map[string]time.Time       `json:"floors_at,omitempty"`
	Rollups  *rollup.Store              `json:"rollups,omitempty"`
	Carries  map[string]engine.Carry    `json:"carries,omitempty"`
	External *model.ExternalState       `json:"external,omitempty"`
}
type envelope struct {
	SHA256  string          `json:"sha256"`
	Payload json.RawMessage `json:"payload"`
}

// syncFile is the journal durability hook. Tests replace it to prove a
// reader does not wait for the sync; production calls ioaccount.Sync.
var syncFile = func(f *os.File) error { return ioaccount.Sync(f) }

type Store struct {
	// commitMu serializes writers. mu is what readers take; durability IO
	// holds commitMu and not mu, so a slow sync does not stall reads.
	commitMu        sync.Mutex
	mu              sync.Mutex
	version         uint64
	evidenceVersion uint64
	collectorID     string
	dir             string
	file            *os.File
	unlock          func() error
	options         Options
	rows            []model.Observation
	tails           map[string][]model.Observation
	carries         map[string]engine.Carry // what samples trimmed from each tail left
	seen            map[string]receipt
	beats           map[string]model.Heartbeat
	bytes           int64
	checkpointSize  int64
	checkpointDone  chan struct{}
	closed          bool
	poisoned        error
	rowSizes        map[string]int64
	stateBytes      int64
	beatBytes       map[string]int64
	recent          map[string]int
	recentHead      int
	recentAt        time.Time
	rowHashes       map[string]digest
	rowObservers    map[string]string
	scopeBytes      map[string]int64
	scopeReceipts   map[string]int
	floors          map[string]uint64
	floorAt         map[string]time.Time   // when each floor's receipt was taken
	floorQueues     map[string]*floorQueue // by observer, its floors oldest first
	rollups         *rollup.Store
	settled         time.Time // when the rollups last settled
	external        *model.ExternalState
	externalBytes   int64
	// externalSizes and externalSourceBytes are the incremental encoding
	// accounting for the persisted state. They keep a native or single-binding
	// write from serializing the whole external graph; keys are "b:"+check and
	// "s:"+source.
	externalSizes         map[string]int64
	externalSourceBytes   map[string]int64
	externalObserverBytes map[string]int64
	// format is the durable checkpoint schema. It is monotonic: once external
	// data has been committed the store keeps writing schema 2 even after its
	// external state is removed, so an older binary cannot reopen and silently
	// rewrite the directory with its narrower model.
	format      int
	sawExternal bool
	// Both API validation and local writes advance this clock under mu.
	admissionClock admission.Clock

	// Receipts in the order they were taken, from receiptHead on, so the
	// expired ones are found at the front rather than by visiting them all;
	// and what the held rows add up to, in all and by observer. prune keeps
	// these up to date; recount derives them afresh.
	receiptQueue  []string
	receiptHead   int
	rowBytes      int64
	rowScope      map[string]int64
	rowRefs       map[string]int // raw history plus state-tail ownership
	rowsCapacity  int            // includes prefixes released from the history slice
	rowsOrdered   bool
	historyDirty  bool
	historyCounts map[string]int
	strings       stringPool
}

func Open(dir string, o Options) (s *Store, err error) {
	o, err = storageOptions(o)
	if err != nil {
		return nil, err
	}
	// A sample is acknowledged once the journal is synced, which makes
	// neither the journal's name nor the directories above it durable:
	// those are synced as they are made.
	if e := secure.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	unlock, e := secure.Lock(filepath.Join(dir, "writer.lock"))
	if e != nil {
		return nil, e
	}
	s = &Store{dir: dir, unlock: unlock, options: o, tails: map[string][]model.Observation{}, carries: map[string]engine.Carry{}, seen: map[string]receipt{}, beats: map[string]model.Heartbeat{}, rowSizes: map[string]int64{}, rowRefs: map[string]int{}, rowHashes: map[string]digest{}, rowObservers: map[string]string{}, floors: map[string]uint64{}, floorAt: map[string]time.Time{}, floorQueues: map[string]*floorQueue{}, stateBytes: 1024, scopeBytes: map[string]int64{}, beatBytes: map[string]int64{}, scopeReceipts: map[string]int{}, rowScope: map[string]int64{}, rollups: rollup.New(o.RollupBytes), external: model.NewExternalState(), externalSizes: map[string]int64{}, externalSourceBytes: map[string]int64{}, externalObserverBytes: map[string]int64{}, format: 1}
	s.rollups.SetCurrent(s.currentKey)
	s.rollups.SetReorderWindow(o.Retention + 30*time.Second)
	opened := s
	defer func() {
		if err != nil {
			if opened.file != nil {
				opened.file.Close()
			}
			unlock()
		}
	}()
	if e := s.loadCollectorID(); e != nil {
		return nil, e
	}
	if e := s.readCheckpoint(filepath.Join(dir, "checkpoint.json")); e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	s.rebuildExternalAccountingLocked()
	// A reload can replace bindings without writing a new checkpoint. Drop
	// obsolete checkpoint state before replay checks the entity/byte budgets,
	// just as the live reload did before accepting replacement observations.
	if s.options.Contracts != nil {
		s.pruneExternalLocked(s.options.Contracts)
		s.applyExternalRulesLocked()
	}
	path := filepath.Join(dir, "journal.jsonl")
	st, e := os.Lstat(path)
	if e == nil && !st.Mode().IsRegular() {
		return nil, errors.New("journal must be regular file")
	}
	if e == nil && runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
		return nil, errors.New("journal permissions must be 0600 or stricter")
	}
	created := errors.Is(e, os.ErrNotExist)
	s.file, e = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if created {
		if e = secure.SyncDir(dir); e != nil {
			return nil, e
		}
	}
	st, e = s.file.Stat()
	if e != nil {
		return nil, e
	}
	if st.Size() > 256<<20 {
		return nil, errors.New("journal exceeds recovery budget")
	}
	reader := bufio.NewReaderSize(s.file, (2<<20)+1)
	offset := int64(0)
	for {
		line, e := reader.ReadSlice('\n')
		if len(line) > 2<<20 {
			return nil, errors.New("journal record too large")
		}
		if e == io.EOF {
			if len(line) > 0 {
				if er := s.file.Truncate(offset); er != nil {
					return nil, er
				}
				if er := ioaccount.Sync(s.file); er != nil {
					return nil, er
				}
			}
			break
		}
		if e != nil {
			return nil, e
		}
		payload, e := unwrap(bytes.TrimSuffix(line, []byte{'\n'}))
		if e != nil {
			return nil, fmt.Errorf("journal corruption at byte %d", offset)
		}
		var batch model.Batch
		if e = json.Unmarshal(payload, &batch); e != nil {
			return nil, errors.New("invalid journal transaction")
		}
		if e = s.apply(batch, nil, nil); e != nil {
			return nil, e
		}
		offset += int64(len(line))
	}
	s.bytes = offset
	if _, e = s.file.Seek(0, io.SeekEnd); e != nil {
		return nil, e
	}
	s.recount()
	s.prune()
	// A journal may predate a removed binding or an edited contract. The
	// recovery context is installed before Open, so stale external state is
	// pruned here rather than keeping a recovered record current.
	if s.options.Contracts != nil {
		s.pruneExternalLocked(s.options.Contracts)
		s.applyExternalRulesLocked()
	}
	// Data written before the barrier existed is upgraded now, before the
	// store can accept another record. The checkpoint is installed through the
	// same atomic write/fsync path as every other checkpoint.
	if s.sawExternal && s.format < 2 {
		s.format = 2
		if e := s.checkpoint(); e != nil {
			return nil, fmt.Errorf("external checkpoint format upgrade: %w", e)
		}
	}
	return s, nil
}

// recount derives the receipt queue and the accounting of receipts and
// rows from what the store holds, as when it opens.
func (s *Store) recount() {
	s.receiptQueue, s.receiptHead = make([]string, 0, len(s.seen)), 0
	s.scopeReceipts = map[string]int{}
	for id, r := range s.seen {
		if r.Observer == "" {
			r.Observer = receiptObserver(id)
			s.seen[id] = r
		}
		s.scopeReceipts[r.Observer]++
		s.receiptQueue = append(s.receiptQueue, id)
	}
	slices.SortFunc(s.receiptQueue, func(a, b string) int {
		if c := s.seen[a].At.Compare(s.seen[b].At); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	s.queueFloors()
	s.rowBytes, s.rowScope = 0, map[string]int64{}
	for id, size := range s.rowSizes {
		s.rowBytes += size
		s.rowScope[s.rowObservers[id]] += size
	}
	s.rowRefs = make(map[string]int, len(s.rowSizes))
	s.historyCounts = make(map[string]int)
	for _, v := range s.rows {
		s.rowRefs[v.ID]++
		s.historyCounts[v.Observer]++
	}
	for _, tail := range s.tails {
		for _, v := range tail {
			s.rowRefs[v.ID]++
		}
	}
	s.historyDirty = true
	s.recountBytes()
	s.recountRecent(s.options.Now())
}

// receive records the receipt of a sample.
func (s *Store) receive(id string, r receipt) {
	r.Observer = s.strings.intern(r.Observer)
	s.advanceRecent(s.options.Now())
	s.seen[id] = r
	s.recent[r.Observer]++
	s.adjustBytes(r.Observer, 512)
	s.scopeReceipts[r.Observer]++
	// Usually append. A collector clock rollback inserts before later receipts
	// so expiry cursors remain valid without rescanning on ordinary reads.
	i := len(s.receiptQueue)
	if i > s.receiptHead && r.At.Before(s.seen[s.receiptQueue[i-1]].At) {
		i = s.receiptHead + sort.Search(i-s.receiptHead, func(j int) bool {
			return !s.seen[s.receiptQueue[s.receiptHead+j]].At.Before(r.At)
		})
	}
	s.receiptQueue = slices.Insert(s.receiptQueue, i, id)
}
func wrap(v any) ([]byte, error) {
	p, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return json.Marshal(envelope{SHA256: hashBytes(p), Payload: p})
}

// UnmarshalJSON borrows only while unwrap's input is alive. Avoid copying an
// entire checkpoint into json.RawMessage before decoding its state.
type borrowedJSON []byte

func (p *borrowedJSON) UnmarshalJSON(raw []byte) error { *p = raw; return nil }

func hashBytes(p []byte) string {
	h := sha256.Sum256(p)
	return hex.EncodeToString(h[:])
}

func unwrap(b []byte) (json.RawMessage, error) {
	var e struct {
		SHA256  string       `json:"sha256"`
		Payload borrowedJSON `json:"payload"`
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	if e.SHA256 != hashBytes(e.Payload) {
		return nil, errors.New("checksum mismatch")
	}
	return json.RawMessage(e.Payload), nil
}
func observationHash(v model.Observation) digest {
	v.ReceivedAt = time.Time{}
	b, _ := json.Marshal(v)
	return sha256.Sum256(b)
}

// tailKey is a check's series: its samples under one contract, or for
// schema 1 samples under one revision.
func tailKey(v model.Observation) string {
	if v.Contract != "" {
		return v.Contract + "/" + v.Check
	}
	return v.Revision + "/" + v.Check
}

// current reports whether v counts under the topology the store is told
// of; every sample does when it is told of none.
func (s *Store) current(v model.Observation) bool {
	if s.options.Revision == "" && s.options.Contracts == nil {
		return true
	}
	return config.Counted(v, s.options.Contracts, s.options.Revision)
}

// rollKey is the rollup series of a sample: its check under its contract.
// A schema 1 sample counted under the topology the store is told of joins
// its check's contract there; an uncounted one keeps its revision.
func (s *Store) rollKey(v model.Observation) string {
	if v.Contract == "" && v.Revision != "" && v.Revision == s.options.Revision {
		if c := s.options.Contracts[v.Check]; c != "" {
			return c + "/" + v.Check
		}
	}
	return tailKey(v)
}

// currentKey reports whether a rollup series is counted under the
// topology the store is told of. The rollups call it under s.mu.
func (s *Store) currentKey(key string) bool {
	if s.options.Revision == "" && s.options.Contracts == nil {
		return true
	}
	contract, check, ok := strings.Cut(key, "/")
	return ok && s.options.Contracts[check] == contract
}

// roll folds a newly accepted sample into its rollup series, which its
// observer owns. The extra labels are whether an edge answered, the
// worker's first-byte timing and the edge's cache and colo; rollup.ExtraOf
// is the one place rollups drop the Vercel colo, because its reported
// region is not a physical colo.
func (s *Store) roll(v model.Observation) {
	// External reports have their own report statistics. Their collection
	// outcome must not become native availability or their collection
	// duration a probe latency.
	if v.External != nil {
		return
	}
	s.rollups.Add(s.rollKey(v), v.Observer, v.SampledAt, v.Outcome, v.DurationMS, s.options.Stale[v.Check], s.options.Now(), rollup.ExtraOf(v))
}

// bootOf splits an observation ID, observer.boot.sequence, into its boot
// and its sequence.
func bootOf(id string) (string, uint64, bool) {
	i := strings.LastIndexByte(id, '.')
	if i < 0 || strings.IndexByte(id[:i], '.') <= 0 {
		return "", 0, false
	}
	seq, e := strconv.ParseUint(id[i+1:], 10, 64)
	return id[:i], seq, e == nil
}

// expired reports whether id's receipt expired and its boot's floor binds
// it: no sample with that ID is accepted again.
func (s *Store) expired(id string) bool {
	boot, seq, ok := bootOf(id)
	floor, held := s.floors[boot]
	return ok && held && seq <= floor
}

// SetCurrent tells the store the topology it now counts samples under, as
// after a reload: tails of series no longer counted go, and those counted
// again are rebuilt from the history still held. Rollups of series no
// longer counted are kept, and shed first when the rollups are full.
func (s *Store) SetCurrent(revision string, contracts map[string]string, stale map[string]time.Duration) {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	s.evidenceVersion++
	s.options.Revision = revision
	s.options.Contracts = maps.Clone(contracts)
	s.options.Stale = maps.Clone(stale)
	s.stateBytes -= int64(s.discardIncompatibleCarries()) * carryBytes
	s.pruneExternalLocked(s.options.Contracts)
	s.applyExternalRulesLocked()
	rows := s.allRows()
	oldTails := s.tails
	s.tails = map[string][]model.Observation{}
	for _, v := range rows {
		if s.current(v) {
			key := tailKey(v)
			s.tails[key] = append(s.tails[key], v)
			s.rowRefs[v.ID]++
		}
	}
	for _, tail := range oldTails {
		for _, v := range tail {
			s.releaseRow(v.ID)
		}
	}
	for key, tail := range s.tails {
		s.tails[key] = s.trim(key, tail)
	}
	for key := range s.carries {
		if _, ok := s.tails[key]; !ok {
			delete(s.carries, key)
			s.stateBytes -= carryBytes
		}
	}
	s.prune()
}

// discardIncompatibleCarries leaves raw history and tails intact. The caller
// can replay those samples under the new rules; a reclaimed prefix contributes
// no confirmation until the retained evidence confirms it again.
func (s *Store) discardIncompatibleCarries() int {
	removed := 0
	for key, k := range s.carries {
		contract := k.Contract
		if s.options.Contracts != nil {
			contract = s.options.Contracts[k.Check]
		}
		policy := engine.EvidencePolicy{Contract: contract, Stale: s.options.Stale[k.Check]}
		if !k.Compatible(policy) {
			delete(s.carries, key)
			removed++
		}
	}
	return removed
}

type rowEncoding struct {
	Hash  digest
	Bytes int64
}

func (s *Store) addRow(v model.Observation) { s.addEncodedRow(v, nil) }

func (s *Store) addEncodedRow(v model.Observation, encoded *rowEncoding) {
	v = s.compactStrings(v)
	if len(s.rows) > 0 && before(v, s.rows[len(s.rows)-1]) {
		s.rowsOrdered = false
	}
	s.rows = append(s.rows, v)
	if s.historyCounts == nil {
		s.historyDirty = true
	} else {
		s.historyCounts[v.Observer]++
		limit := s.options.MaxHistory
		if len(s.options.Observers) > 0 {
			limit = int(s.options.share(int64(limit), v.Observer))
		}
		if len(s.options.Observers) == 0 && len(s.rows) > limit || len(s.options.Observers) > 0 && s.historyCounts[v.Observer] > limit {
			s.historyDirty = true
		}
	}
	s.rowsCapacity = max(s.rowsCapacity, cap(s.rows))
	s.indexEncodedRow(v, encoded)
}

// indexRow rebuilds accounting and bounded state without copying raw history.
func (s *Store) indexRow(v model.Observation) { s.indexEncodedRow(v, nil) }

func (s *Store) indexEncodedRow(v model.Observation, encoded *rowEncoding) {
	s.rowRefs[v.ID]++
	if _, ok := s.rowSizes[v.ID]; !ok {
		if encoded == nil {
			encoded = &rowEncoding{Hash: observationHash(v), Bytes: observationSize(v) + 1}
		}
		s.rowSizes[v.ID] = encoded.Bytes
		s.rowHashes[v.ID] = encoded.Hash
		s.rowObservers[v.ID] = v.Observer
		s.adjustBytes(v.Observer, s.rowSizes[v.ID])
		s.rowBytes += s.rowSizes[v.ID]
		s.rowScope[v.Observer] += s.rowSizes[v.ID]
	}
	if !s.current(v) {
		return
	}
	key := tailKey(v)
	s.rowRefs[v.ID]++
	tail := append(s.tails[key], v)
	sort.Slice(tail, func(i, j int) bool { return before(tail[i], tail[j]) })
	s.tails[key] = s.trim(key, tail)
}

// tailLength is how many of its latest samples a series keeps for its state.
const tailLength = 40

// trim keeps the latest samples of a sorted tail and folds those it lets
// go into the series' carry, so its state outlives them. A sample its
// carry already holds, such as one arriving late, is not folded again.
func (s *Store) trim(key string, tail []model.Observation) []model.Observation {
	if len(tail) <= tailLength {
		return tail
	}
	k := s.carries[key]
	for _, v := range tail[:len(tail)-tailLength] {
		s.releaseRow(v.ID)
		if v.Contract != "" && v.External == nil && !k.Holds(v) {
			k.Fold(v, s.options.Stale[v.Check])
		}
	}
	if !k.At.IsZero() {
		if _, held := s.carries[key]; !held {
			s.stateBytes += carryBytes
		}
		s.carries[key] = k
	}
	kept := tail[len(tail)-tailLength:]
	if cap(tail) > 2*tailLength {
		out := make([]model.Observation, tailLength, 2*tailLength)
		copy(out, kept)
		return out
	}
	copy(tail, kept)
	clear(tail[tailLength:])
	return tail[:tailLength]
}
func before(a, b model.Observation) bool {
	if a.SampledAt.Equal(b.SampledAt) {
		if a.Observer == b.Observer && a.Boot == b.Boot {
			return a.Sequence < b.Sequence
		}
		return a.ID < b.ID
	}
	return a.SampledAt.Before(b.SampledAt)
}
func (s *Store) apply(b model.Batch, prepared map[string]rowEncoding, external *externalCommit) error {
	for i := range b.Observations {
		if b.Observations[i].External != nil {
			s.sawExternal = true
			break
		}
	}
	s.version++
	if len(b.Observations) > 0 {
		s.evidenceVersion++
	}
	fresh := make([]model.Observation, 0, len(b.Observations))
	for _, v := range b.Observations {
		encoded, ok := prepared[v.ID]
		if !ok {
			encoded = rowEncoding{Hash: observationHash(v), Bytes: observationSize(v) + 1}
		}
		h := encoded.Hash
		if old, ok := s.seen[v.ID]; ok {
			if old.Hash != h {
				return ErrConflict
			}
			continue
		}
		if s.expired(v.ID) {
			// Recovery replays a record whose receipt has since expired.
			continue
		}
		s.receive(v.ID, receipt{Hash: h, At: v.ReceivedAt, Observer: v.Observer})
		s.addEncodedRow(v, &encoded)
		s.roll(v)
		fresh = append(fresh, v)
	}
	if external != nil {
		s.installExternalLocked(external)
	} else if prepared == nil && hasExternalObservation(fresh) {
		// Journal replay has no pending encoding map; live writes were
		// already merged before durability and must not merge twice.
		commit, err := s.mergeExternalLocked(fresh)
		if err != nil {
			return err
		}
		s.installExternalLocked(commit)
	}
	if b.Heartbeat != nil && s.advances(*b.Heartbeat, b.Project, b.Revision) {
		h := *b.Heartbeat
		h.Project, h.Revision = b.Project, b.Revision
		s.replaceBeat(h)
	}
	return nil
}

// advances reports whether heartbeat h, sent under project and revision,
// replaces the one its observer has: it is the first, it comes under
// another project or revision, or it was taken later.
func (s *Store) advances(h model.Heartbeat, project, revision string) bool {
	old, ok := s.beats[h.Observer]
	return !ok || project != old.Project || revision != old.Revision || h.SampledAt.After(old.SampledAt)
}

func hasExternalObservation(observations []model.Observation) bool {
	for i := range observations {
		if observations[i].External != nil {
			return true
		}
	}
	return false
}

func (s *Store) prune() {
	s.advanceRecent(s.options.Now())
	cutoff := s.options.Now().Add(-s.options.Retention)
	if !s.rowsOrdered {
		sort.Slice(s.rows, func(i, j int) bool { return before(s.rows[i], s.rows[j]) })
		s.rowsOrdered = true
	}
	if s.historyDirty || len(s.rows) > 0 && s.rows[0].SampledAt.Before(cutoff) {
		s.pruneHistory(cutoff)
	}
	// A receipt binds an ID to its content for as long as a sample with
	// that ID could still be accepted: the ingestion window and an hour.
	// Then it goes, and its boot's floor keeps the ID bound, so ageing out
	// reopens no observation identity while the floor is held (shedFloors
	// says how long that is). Receipts are taken in order, so those due to
	// go are at the front of the queue; one taken while the clock stood
	// behind waits for those in front of it.
	expiry := s.options.Now().Add(-s.options.Retention - receiptGrace)
	for s.receiptHead < len(s.receiptQueue) {
		id := s.receiptQueue[s.receiptHead]
		r, ok := s.seen[id]
		if ok && !r.At.Before(expiry) {
			break
		}
		s.receiptHead++
		s.receiptQueue[s.receiptHead-1] = ""
		// One whose ID names no boot has no floor to bind it, so it stays.
		if boot, seq, parsed := bootOf(id); ok && parsed {
			s.raiseFloor(boot, seq, r.At)
			delete(s.seen, id)
			s.adjustBytes(r.Observer, -512)
			if s.scopeReceipts[r.Observer]--; s.scopeReceipts[r.Observer] == 0 {
				delete(s.scopeReceipts, r.Observer)
			}
		}
	}
	if s.receiptHead > 1024 && s.receiptHead > len(s.receiptQueue)/2 {
		s.recentHead = max(0, s.recentHead-s.receiptHead)
		s.receiptQueue = append([]string(nil), s.receiptQueue[s.receiptHead:]...)
		s.receiptHead = 0
	}
	for o, q := range s.floorQueues {
		limit := s.floorLimit(o)
		if q.Len() > limit {
			s.shedFloors(o, q.Len()-limit)
		}
	}
	if now := s.options.Now(); now.Sub(s.settled) >= time.Minute || now.Before(s.settled) {
		s.rollups.Settle(now)
		s.settled = now
	}
}

// pruneHistory removes only the expired/overflow prefix for a global quota.
// Weighted allocations select their newest rows in one pass when over quota.
func (s *Store) pruneHistory(cutoff time.Time) {
	start := 0
	if len(s.options.Observers) == 0 && s.historyCounts != nil {
		start = max(0, len(s.rows)-s.options.MaxHistory)
		start = max(start, sort.Search(len(s.rows), func(i int) bool { return !s.rows[i].SampledAt.Before(cutoff) }))
		for _, v := range s.rows[:start] {
			s.releaseRow(v.ID)
			s.historyCounts[v.Observer]--
			if s.historyCounts[v.Observer] == 0 {
				delete(s.historyCounts, v.Observer)
			}
		}
	} else {
		// Select newest rows within each observer's allocation. Release only
		// evicted owners; accounting no longer rebuilds a full live-ID set.
		counts := map[string]int{}
		start = len(s.rows)
		for i := len(s.rows) - 1; i >= 0; i-- {
			v := s.rows[i]
			limit := s.options.MaxHistory
			if len(s.options.Observers) > 0 {
				limit = int(s.options.share(int64(limit), v.Observer))
			}
			count := len(s.rows) - start
			if len(s.options.Observers) > 0 {
				count = counts[v.Observer]
			}
			if !v.SampledAt.Before(cutoff) && count < limit {
				start--
				s.rows[start] = v
				counts[v.Observer]++
			} else {
				s.releaseRow(v.ID)
			}
		}
		s.historyCounts = counts
	}
	if start > 0 {
		s.version++
		s.evidenceVersion++
	}
	clear(s.rows[:start])
	s.rows = s.rows[start:]
	if len(s.rows) == 0 {
		s.rows = nil
		s.rowsCapacity = 0
	} else if s.rowsCapacity > 2*len(s.rows) {
		s.rows = slices.Clone(s.rows)
		s.rowsCapacity = cap(s.rows)
	}
	s.historyDirty = false
}

func (s *Store) allRows() []model.Observation {
	seen := make(map[string]bool, len(s.rows))
	out := make([]model.Observation, 0, len(s.rows)+len(s.tails)*tailLength)
	for _, v := range s.rows {
		seen[v.ID] = true
		out = append(out, v)
	}
	for _, tail := range s.tails {
		for _, v := range tail {
			if !seen[v.ID] {
				seen[v.ID] = true
				out = append(out, v)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return before(out[i], out[j]) })
	return out
}
func (s *Store) checkpoint() error {
	s.settled = s.options.Now()
	s.rollups.Settle(s.settled)
	e := s.writeCheckpoint(filepath.Join(s.dir, "checkpoint.json"))
	if e != nil {
		return e
	}
	// If a crash occurs before truncation, replay is safe because the checkpoint
	// already contains the deduplication receipts and monotonic heartbeats.
	if e = s.file.Truncate(0); e != nil {
		return e
	}
	if _, e = s.file.Seek(0, io.SeekStart); e != nil {
		return e
	}
	if e = ioaccount.Sync(s.file); e != nil {
		return e
	}
	s.bytes = 0
	return nil
}
func (s *Store) Checkpoint() error {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitDurable()
	if s.closed {
		return errors.New("store closed")
	}
	if s.poisoned != nil {
		return s.poisoned
	}
	e := s.checkpoint()
	if e != nil {
		s.poisoned = e
	}
	return e
}

// View returns detached copies so readers cannot mutate persisted evidence.
func (s *Store) View() ([]model.Observation, map[string]model.Heartbeat) {
	s.mu.Lock()
	rows := s.allRows()
	beats := map[string]model.Heartbeat{}
	for k, v := range s.beats {
		beats[k] = model.CloneHeartbeat(v)
	}
	s.mu.Unlock()
	for i := range rows {
		rows[i] = model.CloneObservation(rows[i])
	}
	return rows, beats
}

// History returns at most 1 MiB of newest full observations. A response may
// contain fewer than limit entries; serialization never holds the writer lock.
func (s *Store) History(check string, contracts map[string]string, revision string, limit int) []model.Observation {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	s.mu.Lock()
	cut := s.options.Now().Add(-s.options.Retention)
	out := []model.Observation{}
	size := int64(2)
	for i := len(s.rows) - 1; i >= 0 && len(out) < limit; i-- {
		v := s.rows[i]
		if v.Check == check && config.Counted(v, contracts, revision) && !v.SampledAt.Before(cut) {
			size += s.rowSizes[v.ID]
			if size > 1<<20 {
				break
			}
			out = append(out, v)
		}
	}
	s.mu.Unlock()
	for i := range out {
		out[i] = model.CloneObservation(out[i])
	}
	return out
}

// Availability is how check fared under contract over each of
// rollup.Windows up to now, or nil when its rollups hold nothing. It does
// not wait for the writer.
func (s *Store) Availability(check, contract string, now time.Time) []model.Availability {
	return s.rollups.Availability(contract+"/"+check, now)
}

// Rollups lays out check's rollups under contract from from until now in
// slots step wide (see rollup.Store.Buckets), with their total and the
// earliest moment they hold.
func (s *Store) Rollups(check, contract string, from time.Time, step time.Duration, now time.Time) (model.Rollup, []model.Rollup, time.Time) {
	key := contract + "/" + check
	total, _ := s.rollups.Window(key, from, now, now)
	return total, s.rollups.Buckets(key, from, now, step, now), s.rollups.Since(key)
}

func (s *Store) Close() error {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.waitDurable()
	var e error
	if s.file != nil {
		e = s.file.Close()
	}
	e = errors.Join(e, s.poisoned)
	ue := s.unlock()
	if e != nil {
		return e
	}
	return ue
}

// releaseRow drops accounting only after neither raw history nor a state tail
// owns the observation. Receipts have their own independent retention window.
func (s *Store) releaseRow(id string) {
	if s.rowRefs[id] > 1 {
		s.rowRefs[id]--
		return
	}
	delete(s.rowRefs, id)
	size, observer := s.rowSizes[id], s.rowObservers[id]
	s.adjustBytes(observer, -size)
	s.rowBytes -= size
	s.rowScope[observer] -= size
	if s.rowScope[observer] == 0 {
		delete(s.rowScope, observer)
	}
	delete(s.rowSizes, id)
	delete(s.rowHashes, id)
	delete(s.rowObservers, id)
}
