package api

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

const (
	// DefaultIngressWindow is how long a handshake failure is remembered.
	// Delivery retries are minutes apart, so a short window would miss a
	// burst that has just stopped.
	DefaultIngressWindow = 30 * time.Minute
	// maxIngressSources bounds the sources kept individually: a scanner can
	// present many addresses. Failures of sources beyond it still count.
	maxIngressSources = 256
	// shownIngressSources bounds what a snapshot shows.
	shownIngressSources = 8
	// ingressBuckets is the resolution of the rolling window. Each source,
	// and the total, keeps one count per bucket instead of a lifetime count.
	ingressBuckets = 60
	// tlsHandshakePrefix is net/http's own wording for a failed handshake
	// on the HTTP server's connection. Only lines with it count.
	tlsHandshakePrefix = "http: TLS handshake error from "
)

type ingressBucket struct {
	at    int64 // the bucket's start, in Unix nanoseconds
	count int
}

type ingressSource struct {
	buckets [ingressBuckets]ingressBucket
	last    time.Time
}

// IngressCounter counts TLS handshake failures by source over a rolling
// window. It observes the HTTP server's error log rather than the sockets:
// net/http does the handshake, and its error line is the only place the
// failure is reported. A failed handshake is evidence that an observer's
// delivery did not arrive; it is not evidence about anything the observer
// watches, and it cannot be attributed to an observer because the observer
// identifies itself only after the handshake succeeds.
type IngressCounter struct {
	mu      sync.Mutex
	window  time.Duration
	width   time.Duration
	now     func() time.Time
	sources map[string]*ingressSource
	// total counts every failure in the window, including failures from
	// addresses the source map could not keep.
	failureKinds map[string]*[ingressBuckets]ingressBucket
	total        [ingressBuckets]ingressBucket
	last         time.Time
	// lastReject is when an address beyond maxIngressSources last failed.
	// It makes Truncated a statement about the current window, not history.
	lastReject time.Time
}

func NewIngressCounter(window time.Duration) *IngressCounter {
	if window <= 0 {
		window = DefaultIngressWindow
	}
	width := window / ingressBuckets
	if width < time.Nanosecond {
		width = time.Nanosecond
	}
	return &IngressCounter{window: window, width: width, now: time.Now, sources: map[string]*ingressSource{}, failureKinds: map[string]*[ingressBuckets]ingressBucket{}}
}

// Writer passes every byte to next and counts the handshake failures among
// them. Give it to the HTTP server's ErrorLog: log.Logger writes one message
// per call, so a message is one line.
func (c *IngressCounter) Writer(next func(string)) io.Writer {
	return &ingressWriter{counter: c, next: next}
}

type ingressWriter struct {
	counter *IngressCounter
	next    func(string)
}

func (w *ingressWriter) Write(p []byte) (int, error) {
	line := strings.TrimSuffix(string(p), "\n")
	w.counter.observe(line)
	if w.next != nil {
		if handshakeOffset(line) >= 0 {
			w.next("collector ingress stage=tls code=" + handshakeKind(line))
		} else {
			w.next(line)
		}
	}
	return len(p), nil
}

// observe counts line when it is the HTTP server reporting a failed TLS
// handshake, and ignores every other line. The line arrives as log.Logger
// formatted it, so a timestamp can come first.
func (c *IngressCounter) observe(line string) {
	if c == nil || c.width <= 0 {
		return
	}
	at := handshakeOffset(line)
	if at < 0 {
		return
	}
	rest := line[at+len(tlsHandshakePrefix):]
	// "<host>:<port>: <error>"; the address is everything before the first
	// ": " after the port, and IPv6 addresses keep their brackets.
	cut := strings.Index(rest, ": ")
	if cut < 0 {
		return
	}
	host, port, err := net.SplitHostPort(rest[:cut])
	if err != nil || host == "" {
		return
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	// Pruning is O(sources); do it only when the map is full, so a normal
	// line costs one bucket update. Expired entries stay until a new source
	// needs their slot; Summary counts only buckets inside the window.
	if len(c.sources) >= maxIngressSources {
		c.prune(now)
	}
	c.add(&c.total, now)
	kind := handshakeKind(line)
	if c.failureKinds[kind] == nil {
		c.failureKinds[kind] = new([ingressBuckets]ingressBucket)
	}
	c.add(c.failureKinds[kind], now)
	c.last = now
	if s := c.sources[host]; s != nil {
		c.add(&s.buckets, now)
		s.last = now
		return
	}
	if len(c.sources) >= maxIngressSources {
		// Do not evict a live source to make room: a scanner can rotate
		// addresses, and losing the observer whose delivery is failing is
		// the one outcome this counter exists to prevent. Count the failure
		// in the total and say the source list is truncated.
		c.lastReject = now
		return
	}
	s := &ingressSource{}
	c.add(&s.buckets, now)
	s.last = now
	c.sources[host] = s
}

// handshakeOffset returns where net/http's handshake message starts: at the
// beginning, or after the one standard log timestamp the collector adds.
// A message that merely quotes the prefix later is not a handshake report.
func handshakeOffset(line string) int {
	if strings.HasPrefix(line, tlsHandshakePrefix) {
		return 0
	}
	const stamp = "2006/01/02 15:04:05 "
	if len(line) > len(stamp) && strings.HasPrefix(line[len(stamp):], tlsHandshakePrefix) {
		if _, err := time.Parse("2006/01/02 15:04:05", line[:len(stamp)-1]); err == nil {
			return len(stamp)
		}
	}
	return -1
}

// add records one failure at t in a bucket. Callers hold c.mu.
func (c *IngressCounter) add(buckets *[ingressBuckets]ingressBucket, t time.Time) {
	at := t.UnixNano() / int64(c.width)
	slot := int(at % ingressBuckets)
	if slot < 0 {
		slot += ingressBuckets
	}
	if buckets[slot].at != at {
		buckets[slot] = ingressBucket{at: at}
	}
	buckets[slot].count++
}

// sum counts the failures in buckets whose start falls inside the window
// ending at now. The bucket width is window/ingressBuckets; a bucket that
// starts before the cutoff is dropped even though its tail may overlap the
// window, so the count has one bucket's resolution at the old edge.
func (c *IngressCounter) sum(buckets *[ingressBuckets]ingressBucket, now time.Time) int {
	cutoff := now.Add(-c.window).UnixNano()
	end := now.UnixNano()
	total := 0
	for _, b := range buckets {
		if b.count == 0 {
			continue
		}
		start := b.at * int64(c.width)
		if start >= cutoff && start <= end {
			total += b.count
		}
	}
	return total
}

// prune drops sources whose last failure fell out of the window. Callers
// hold c.mu.
func (c *IngressCounter) prune(now time.Time) {
	for host, s := range c.sources {
		if now.Sub(s.last) > c.window {
			delete(c.sources, host)
		}
	}
}

// pseudonymousIngress replaces each source address with a short keyed
// digest. Viewer snapshots use it unless the collector explicitly exposes
// addresses: a viewer token is read-only, not a licence to learn every
// observer's or scanner's address. The counts and last-seen times stay
// usable, and the same address keeps the same digest for the collector's
// lifetime; the key changes on restart, so the digest cannot be reversed
// by enumerating the small IPv4 space offline.
func pseudonymousIngress(in *model.Ingress, salt []byte) *model.Ingress {
	if in == nil {
		return nil
	}
	out := *in
	out.Sources = slices.Clone(in.Sources)
	out.FailureTypes = maps.Clone(in.FailureTypes)
	for i := range out.Sources {
		mac := hmac.New(sha256.New, salt)
		mac.Write([]byte(out.Sources[i].Address))
		out.Sources[i].Address = "source-" + hex.EncodeToString(mac.Sum(nil)[:6])
	}
	return &out
}

// Summary is the window as a snapshot shows it, or nil when nothing failed
// inside it. Sources are the busiest first; SourceCount counts every source
// the counter could track, and Truncated says some were not tracked.
func (c *IngressCounter) Summary(now time.Time) *model.Ingress {
	if c == nil || c.width <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	failures := c.sum(&c.total, now)
	if failures == 0 {
		return nil
	}
	out := &model.Ingress{WindowSeconds: int(c.window / time.Second), Failures: failures, Last: c.last, FailureTypes: map[string]int{}}
	for kind, buckets := range c.failureKinds {
		if n := c.sum(buckets, now); n > 0 {
			out.FailureTypes[kind] = n
		}
	}
	for host, s := range c.sources {
		count := c.sum(&s.buckets, now)
		if count == 0 {
			continue
		}
		out.SourceCount++
		out.Sources = append(out.Sources, model.IngressSource{Address: host, Count: count, Last: s.last})
	}
	out.Truncated = !c.lastReject.IsZero() && now.Sub(c.lastReject) <= c.window
	slices.SortFunc(out.Sources, func(a, b model.IngressSource) int {
		if a.Count != b.Count {
			return cmp.Compare(b.Count, a.Count)
		}
		return strings.Compare(a.Address, b.Address)
	})
	if len(out.Sources) > shownIngressSources {
		out.Sources = out.Sources[:shownIngressSources]
	}
	return out
}

// Match only bounded protocol categories, never retain the raw TLS error line.
func handshakeKind(line string) string {
	line = strings.ToLower(line)
	switch {
	case strings.Contains(line, "timeout"), strings.Contains(line, "deadline exceeded"):
		return "tls_timeout"
	case strings.Contains(line, "eof"), strings.Contains(line, "connection reset"), strings.Contains(line, "closed network"):
		return "connection_closed"
	case strings.Contains(line, "certificate"):
		return "certificate_rejected"
	case strings.Contains(line, "first record"), strings.Contains(line, "record header"), strings.Contains(line, "unsupported"):
		return "invalid_tls"
	default:
		return "handshake_other"
	}
}
