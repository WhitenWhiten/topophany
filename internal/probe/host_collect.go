package probe

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/adapters/mihomo"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// NewRunner shares local runtime counters among this observer's host checks.
// Construct a new runner on topology reload so newly selected process names
// cannot be evaluated against a snapshot collected for the old configuration.
func NewRunner(c model.Config, observer string) Runner {
	names := []string{}
	localCF, totalCF := 0, 0
	for _, x := range c.Checks {
		if x.Kind == "cloudflare" && !x.Disabled && !x.Maintenance {
			totalCF++
			if x.Observer == observer {
				localCF++
			}
		}
		if x.Observer == observer && x.Kind == "host" && !x.Disabled && !x.Maintenance && x.Host != nil {
			names = append(names, x.Host.ProcessName)
		}
	}
	return Runner{CloudflareQuota: cloudflare.NewQuota(localCF, totalCF), MihomoCache: &mihomo.SnapshotCache{}, HostSample: newHostCollector(names).sample}
}

type processUsage struct {
	count int
	rss   uint64
}

type hostRuntime struct {
	HostSample
	// Retain aggregates only for explicitly configured names, never an inventory.
	matches map[string]processUsage
}

func newHostRuntime(names []string) hostRuntime {
	s := hostRuntime{HostSample: HostSample{SampledAt: time.Now().UTC()}, matches: map[string]processUsage{}}
	for _, name := range names {
		if name != "" {
			s.matches[name] = processUsage{}
		}
	}
	return s
}

func (s *hostRuntime) addProcess(name string, zombie bool, rss, unit uint64) error {
	s.Processes++
	if zombie {
		s.Zombies++
		return nil
	}
	if match, ok := s.matches[name]; ok {
		n, err := bytesFromUnits(rss, unit)
		if err != nil || n > math.MaxUint64-match.rss {
			return local("host", "invalid_process_counters")
		}
		match.count++
		match.rss += n
		// A parsed name can be a substring of the entire ps output. Updating a
		// string map key may replace its backing storage, so copy just the name.
		s.matches[strings.Clone(name)] = match
	}
	return nil
}

// Keep reuse shorter than the minimum configured interval. Disk capacity and
// assertions remain per check. The original collection timestamp is never renewed.
const hostReuse = 500 * time.Millisecond

type hostCollector struct {
	names   []string
	gate    chan struct{}
	read    func(context.Context, []string) (hostRuntime, error)
	disk    func(context.Context, string) (HostDisk, error)
	now     func() time.Time
	cached  hostRuntime
	expires time.Time
}

func newHostCollector(names []string) *hostCollector {
	return &hostCollector{names: names, gate: make(chan struct{}, 1), read: sampleHostRuntime, disk: sampleDisk, now: time.Now}
}

func (c *hostCollector) runtime(ctx context.Context) (hostRuntime, error) {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return hostRuntime{}, local("host", "host_sampling_canceled")
	}
	defer func() { <-c.gate }()
	if ctx.Err() != nil {
		return hostRuntime{}, local("host", "host_sampling_canceled")
	}
	if c.now().Before(c.expires) {
		return c.cached, nil
	}
	s, err := c.read(ctx, c.names)
	if ctx.Err() != nil {
		return hostRuntime{}, local("host", "host_sampling_canceled")
	}
	if err != nil {
		return hostRuntime{}, err
	}
	c.cached, c.expires = s, c.now().Add(hostReuse)
	return s, nil
}

func (c *hostCollector) sample(ctx context.Context, h model.HostOptions) (HostSample, error) {
	r, err := c.runtime(ctx)
	if err != nil {
		return HostSample{}, err
	}
	s := r.HostSample
	if h.ProcessName != "" {
		match, ok := r.matches[h.ProcessName]
		if !ok {
			return HostSample{}, local("host", "host_process_not_configured")
		}
		s.Matching, s.MatchingRSS = match.count, match.rss
	}
	s.Disk, err = c.disk(ctx, h.DiskPath)
	return s, err
}
