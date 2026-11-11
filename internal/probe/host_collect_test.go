package probe

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestHostChecksShareRuntimeButKeepDiskPolicyAndTimestamp(t *testing.T) {
	now := time.Now().UTC()
	collected := now.Add(-200 * time.Millisecond)
	c := newHostCollector([]string{"topo", "other"})
	c.now = func() time.Time { return now }
	reads := 0
	c.read = func(_ context.Context, names []string) (hostRuntime, error) {
		reads++
		s := newHostRuntime(names)
		s.HostSample = hostFixture()
		s.SampledAt = collected
		if err := s.addProcess("topo", false, 2, 1024); err != nil {
			t.Fatal(err)
		}
		if err := s.addProcess("other", false, 4, 1024); err != nil {
			t.Fatal(err)
		}
		return s, nil
	}
	disks := 0
	c.disk = func(_ context.Context, path string) (HostDisk, error) {
		disks++
		d := hostFixture().Disk
		if path == "/data" {
			d.Free, d.Available = 900, 900
		}
		return d, nil
	}
	r := Runner{HostSample: c.sample}
	x := model.Check{ID: "root", Observer: "home", Kind: "host", TimeoutSeconds: 8, Host: &model.HostOptions{ProcessName: "topo", MaxDiskPercent: 80}}
	a := r.Run(context.Background(), testkit.Config(), x)
	x.ID, x.Host = "data", &model.HostOptions{DiskPath: "/data", ProcessName: "other", MaxDiskPercent: 80}
	b := r.Run(context.Background(), testkit.Config(), x)
	if reads != 1 || disks != 2 || a.Outcome != "policy" || b.Outcome != "ok" || a.Contract == b.Contract {
		t.Fatal(reads, disks, a, b)
	}
	if a.SampledAt != collected || b.SampledAt != collected || a.Facts["process_matching_rss_bytes"] != "2048" || b.Facts["process_matching_rss_bytes"] != "4096" {
		t.Fatal("shared sample was re-timestamped or process aggregates mixed", a, b)
	}
	a.Facts["cpu_used_percent"] = "changed"
	if b.Facts["cpu_used_percent"] != "25.00" {
		t.Fatal("observation maps are shared")
	}
	now = now.Add(hostReuse)
	c.read = func(context.Context, []string) (hostRuntime, error) {
		reads++
		return hostRuntime{}, hostReadError("cpu")
	}
	if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "error" || o.Facts["cpu_used_percent"] != "" || reads != 2 {
		t.Fatal("expired counters hid a failed refresh", o, reads)
	}
	c.read = func(_ context.Context, names []string) (hostRuntime, error) {
		reads++
		s := newHostRuntime(names)
		s.HostSample = hostFixture()
		return s, nil
	}
	if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "ok" || reads != 3 {
		t.Fatal("failed refresh was cached", o, reads)
	}
}

func TestHostNetworkRatesAcrossCollections(t *testing.T) {
	now := time.Now()
	c := newHostCollector(nil)
	c.now = func() time.Time { return now }
	c.disk = func(context.Context, string) (HostDisk, error) { return hostFixture().Disk, nil }
	counters := map[string]hostInterfaceCounters{
		"nic0": {rx: 100, tx: 0},
		"nic1": {rx: 200, tx: 100},
	}
	c.read = func(context.Context, []string) (hostRuntime, error) {
		s := hostRuntime{HostSample: hostFixture()}
		s.SampledAt = now
		s.Swap = model.SwapReport{TotalBytes: new(uint64(2048)), UsedBytes: new(uint64(512))}
		s.Load = model.ParseLoadAverages([]string{"1.25", "0.5", "0"})
		s.network = hostNetworkSample{at: now, interfaces: counters}
		return s, nil
	}
	r := Runner{HostSample: c.sample}
	x := model.Check{ID: "host", Observer: "home", Kind: "host", TimeoutSeconds: 8}
	collect := func() model.Observation {
		t.Helper()
		o := r.Run(t.Context(), testkit.Config(), x)
		if o.Outcome != "ok" || o.Facts["swap_used_bytes"] != "512" || o.Facts["load_avg_15m"] != "0" {
			t.Fatal("host resource facts missing", o)
		}
		return o
	}
	first := collect()
	if first.Facts["network_rx_bytes_per_second"] != "" {
		t.Fatal("first sample fabricated a rate", first)
	}
	now = now.Add(2 * time.Second)
	counters = map[string]hostInterfaceCounters{"nic0": {rx: 300, tx: 0}, "nic1": {rx: 400, tx: 100}}
	second := collect()
	if second.Facts["network_rx_bytes_per_second"] != "200" || second.Facts["network_tx_bytes_per_second"] != "0" {
		t.Fatal("incorrect network rates", second)
	}
	if shared := collect(); shared.Facts["network_rx_bytes_per_second"] != "200" || !shared.SampledAt.Equal(second.SampledAt) {
		t.Fatal("shared runtime sample lost its rate or timestamp", shared)
	}
	now = now.Add(2 * time.Second)
	counters = map[string]hostInterfaceCounters{"nic0": {rx: 0, tx: 0}, "nic1": {rx: 5000, tx: 100}}
	if got := collect(); got.Facts["network_rx_bytes_per_second"] != "" || got.Facts["network_tx_bytes_per_second"] != "" {
		t.Fatal("reset was hidden by a different interface's traffic", got)
	}
	now = now.Add(2 * time.Second)
	counters = map[string]hostInterfaceCounters{"nic0": {rx: 400, tx: 0}, "nic2": {rx: 5000, tx: 100}}
	if got := collect(); got.Facts["network_rx_bytes_per_second"] != "" {
		t.Fatal("changed interface set produced a rate", got)
	}
	now = now.Add(2 * time.Second)
	counters = map[string]hostInterfaceCounters{"nic0": {rx: 600, tx: 0}, "nic2": {rx: 5000, tx: 300}}
	if got := collect(); got.Facts["network_rx_bytes_per_second"] != "100" || got.Facts["network_tx_bytes_per_second"] != "100" {
		t.Fatal("new interface set did not establish a baseline", got)
	}
}

func TestHostConcurrentChecksCollectOnce(t *testing.T) {
	c := newHostCollector([]string{"topo"})
	var reads atomic.Int32
	c.read = func(_ context.Context, names []string) (hostRuntime, error) {
		reads.Add(1)
		s := newHostRuntime(names)
		s.HostSample = hostFixture()
		return s, s.addProcess("topo", false, 1, 1024)
	}
	c.disk = func(context.Context, string) (HostDisk, error) { return hostFixture().Disk, nil }
	// Freeze cache time so this assertion is independent of race-detector speed.
	now := time.Now()
	c.now = func() time.Time { return now }
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			s, err := c.sample(context.Background(), model.HostOptions{ProcessName: "topo"})
			if err != nil || s.Matching != 1 || s.MatchingRSS != 1024 {
				t.Error(s, err)
			}
		})
	}
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatal("duplicate runtime collection", reads.Load())
	}
}

func TestHostCanceledCollectionAndWaitersDoNotPoisonCache(t *testing.T) {
	c := newHostCollector(nil)
	started := make(chan struct{})
	var reads atomic.Int32
	c.read = func(ctx context.Context, _ []string) (hostRuntime, error) {
		if reads.Add(1) == 1 {
			close(started)
			<-ctx.Done()
		}
		return hostRuntime{HostSample: hostFixture()}, nil
	}
	c.disk = func(context.Context, string) (HostDisk, error) { return hostFixture().Disk, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.sample(ctx, model.HostOptions{}); done <- err }()
	hostWait(t, started)
	// A waiting check has its own deadline, independent of the active collector.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer waitCancel()
	waitDone := make(chan error, 1)
	go func() { _, err := c.sample(waitCtx, model.HostOptions{}); waitDone <- err }()
	if err := hostWait(t, waitDone); err == nil {
		t.Fatal("waiting check ignored its deadline")
	}
	cancel()
	if err := hostWait(t, done); err == nil {
		t.Fatal("canceled collection succeeded")
	}
	if _, err := c.sample(context.Background(), model.HostOptions{}); err != nil || reads.Load() != 2 {
		t.Fatal("canceled counters were cached", err, reads.Load())
	}
}

func TestHostProcessAggregatesRejectOverflow(t *testing.T) {
	s := newHostRuntime([]string{"topo"})
	if err := s.addProcess("unselected", false, ^uint64(0), 1024); err != nil || len(s.matches) != 1 {
		t.Fatal("unselected process retained", s, err)
	}
	if err := s.addProcess("topo", true, ^uint64(0), 1024); err != nil {
		t.Fatal("zombie RSS counted", err)
	}
	if err := s.addProcess("topo", false, ^uint64(0), 1); err != nil {
		t.Fatal(err)
	}
	if err := s.addProcess("topo", false, 1, 1); err == nil {
		t.Fatal("RSS sum overflow accepted", s)
	}
}
