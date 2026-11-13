package outbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Quarantine has its own global budget and per-recipient shares. Oldest evidence
// is retired first; pending deliveries are never removed by this retention rule.
// At most one extra record can be present during an interrupted atomic write.
func (q *Queue) trimQuarantine(recipient, keep string, extra int64) error {
	type entry struct {
		name, recipient string
		size            int64
		at              time.Time
	}
	dir := filepath.Join(q.dir, "quarantine")
	files, err := queueReadDir(dir)
	if err != nil {
		return err
	}
	entries := []entry{}
	sizes := map[string]int64{}
	counts := map[string]int{}
	count := 0
	total := int64(0)
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		name := f.Name()
		if len(name) != 86 || name[64] != '-' || strings.Trim(name[:64]+name[65:81], "0123456789abcdef") != "" || f.Type()&os.ModeSymlink != 0 || f.IsDir() {
			return errors.New("invalid quarantine entry")
		}
		info, err := queueInfo(f)
		if err != nil {
			return err
		}
		id := name[65:81]
		entries = append(entries, entry{name, id, info.Size(), info.ModTime()})
		sizes[id] += info.Size()
		counts[id]++
		count++
		total += info.Size()
	}
	if keep != "" {
		found := false
		for _, v := range entries {
			if v.name == keep {
				found = true
				break
			}
		}
		if !found {
			counts[recipient]++
			count++
		}
	}
	sizes[recipient] += extra
	total += extra
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].at.Equal(entries[j].at) {
			return entries[i].name < entries[j].name
		}
		return entries[i].at.Before(entries[j].at)
	})
	perRecipient := max(q.max/8, 2<<20)
	global := max(q.max, 16<<20)
	removed := false
	for _, v := range entries {
		if v.name == keep {
			continue
		}
		if sizes[v.recipient] <= perRecipient && total <= global && counts[v.recipient] <= 1024 && count <= 8192 {
			continue
		}
		if err := queueRemove(filepath.Join(dir, v.name)); err != nil {
			return err
		}
		sizes[v.recipient] -= v.size
		counts[v.recipient]--
		count--
		total -= v.size
		q.used -= v.size
		q.quarantineUsed -= v.size
		removed = true
	}
	if removed {
		if err := secure.SyncDir(dir); err != nil {
			return err
		}
	}
	if total > global || sizes[recipient] > perRecipient {
		return errors.New("quarantine record exceeds independent budget")
	}
	return nil
}

// quarantineItem publishes forensic evidence before its delivery is consumed.
// Caller holds q.mu; retries replace the same deterministic file.
func (q *Queue) quarantineItem(v Item, recipient, reason string) error {
	file := filepath.Join(q.dir, "quarantine", v.ID+"-"+recipient+".json")
	b, e := json.Marshal(struct {
		Item      Item   `json:"item"`
		Recipient string `json:"recipient"`
		Reason    string `json:"reason"`
	}{v, recipient, reason})
	if e != nil {
		return e
	}
	old := int64(0)
	if st, e := queueStat(file); e == nil {
		old = st.Size()
	}
	if e := q.trimQuarantine(recipient, filepath.Base(file), int64(len(b))-old); e != nil {
		return e
	}
	if e = queueWrite(file, b, 0600); e != nil {
		return e
	}
	q.used = q.used - old + int64(len(b))
	q.quarantineEvents++
	q.quarantineUsed += int64(len(b)) - old
	return nil
}

// QuarantinePart preserves one refused observation without acknowledging its
// parent. After a crash the parent replays; collector deduplication and the
// deterministic quarantine identity make that safe.
func (q *Queue) QuarantinePart(parent string, index int, recipient, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("outbox closed")
	}
	if q.ackError != nil {
		return q.ackError
	}
	meta, ok := q.items[parent]
	if !ok || !slices.Contains(meta.Pending, recipient) {
		return errors.New("pending item unavailable")
	}
	v, err := q.loadItem(parent)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(v.Batch.Observations) {
		return errors.New("invalid observation index")
	}
	v.Batch.Observations = v.Batch.Observations[index : index+1]
	raw, err := json.Marshal(v.Batch)
	if err != nil {
		return err
	}
	v.ID = secure.Hash(string(raw))
	v.Pending = []string{recipient}
	v.SHA256 = itemHash(v)
	reason = secure.Text(reason)
	if len(reason) > 256 {
		reason = reason[:256]
	}
	return q.quarantineItem(v, recipient, reason)
}
