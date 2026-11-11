package probe

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func hostFixture() HostSample {
	return HostSample{CPUPercent: 25, CPUSampleMS: 200, MemoryTotal: 1000, MemoryAvailable: 250,
		MemoryBasis: "fixture", Disk: HostDisk{Total: 1000, Free: 200, Available: 100, Inodes: 100, InodesFree: 50},
		Processes: 20, Zombies: 1, Matching: 2, MatchingRSS: 4096}
}

func TestHostOptionalMetricsStayWithinObservationBounds(t *testing.T) {
	for _, windows := range []bool{false, true} {
		s := hostFixture()
		s.ResourceMetrics = model.ResourceMetrics{
			Network: model.NetworkReport{RXBytesPerSecond: new(4096.0), TXBytesPerSecond: new(0.0)},
			Swap:    model.SwapReport{TotalBytes: new(uint64(1024)), UsedBytes: new(uint64(0))},
			Load:    model.LoadReport{Avg1m: new(1.0), Avg5m: new(2.0), Avg15m: new(3.0)},
		}
		if windows {
			s.Disk.Basis = "windows_caller_quota"
			s.Load = model.LoadReport{} // Windows has no native load averages.
		}
		r := Runner{HostSample: func(context.Context, model.HostOptions) (HostSample, error) { return s, nil }}
		x := model.Check{ID: "host", Observer: "home", Kind: "host", TimeoutSeconds: 8,
			Host: &model.HostOptions{ProcessName: "topo", MinProcesses: 3, MaxCPUPercent: 20, MaxMemoryPercent: 70, MaxDiskPercent: 85}}
		o := r.Run(t.Context(), testkit.Config(), x)
		if o.Outcome != "policy" || o.Facts["network_rx_bytes_per_second"] != "4096" || o.Facts["network_tx_bytes_per_second"] != "0" || o.Facts["swap_used_bytes"] != "0" {
			t.Fatal("optional metrics lost in host observation", o)
		}
		if code := config.ObservationBounds(o, 13*time.Second); code != "" {
			t.Fatal("full host observation cannot be admitted", code, len(o.Facts), o.Facts)
		}
	}
}

func TestHostAssertionsAndDeliverableEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		h          model.HostOptions
		violations string
	}{
		{"collect only", model.HostOptions{}, ""},
		{"threshold equality passes", model.HostOptions{MaxCPUPercent: 25, MaxMemoryPercent: 75, MaxDiskPercent: 90}, ""},
		{"all thresholds", model.HostOptions{MaxCPUPercent: 20, MaxMemoryPercent: 70, MaxDiskPercent: 85, ProcessName: "topo", MinProcesses: 3}, "cpu,memory,disk,process_count"},
		{"too many processes", model.HostOptions{ProcessName: "topo", MaxProcesses: 1}, "process_count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := model.Check{ID: "host", Observer: "home", Kind: "host", TimeoutSeconds: 8, Host: &tc.h}
			r := Runner{HostSample: func(_ context.Context, h model.HostOptions) (HostSample, error) {
				if h.DiskPath != "/" {
					t.Fatal("default filesystem", h)
				}
				return hostFixture(), nil
			}}
			o := r.Run(context.Background(), testkit.Config(), x)
			want := "ok"
			if tc.violations != "" {
				want = "policy"
			}
			if o.Outcome != want || o.Facts["violations"] != tc.violations || o.Evidence != "reported" || o.Connectivity || o.Facts["response_proof"] != "" || o.PathEvidence != "local_host_runtime_reported" {
				t.Fatal(o)
			}
			if o.Facts["disk_used_percent"] != "88.89" || o.Facts["memory_used_percent"] != "75.00" {
				t.Fatal("resource accounting", o.Facts)
			}
			if code := config.ObservationBounds(o, 13*time.Second); code != "" {
				t.Fatal(code, o)
			}
		})
	}
}

func TestHostReadErrorsNeverBecomeTargetFailures(t *testing.T) {
	x := model.Check{ID: "host", Observer: "home", Kind: "host", TimeoutSeconds: 8}
	r := Runner{HostSample: func(context.Context, model.HostOptions) (HostSample, error) {
		return HostSample{}, hostReadError("processes")
	}}
	o := r.Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" || o.Code != "host_processes_unavailable" || o.Connectivity {
		t.Fatal(o)
	}
	for _, invalid := range []func(*HostSample){
		func(s *HostSample) { s.CPUPercent = math.NaN() },
		func(s *HostSample) { s.MemoryAvailable = s.MemoryTotal + 1 },
		func(s *HostSample) { s.Disk.Available = s.Disk.Free + 1 },
	} {
		r.HostSample = func(context.Context, model.HostOptions) (HostSample, error) {
			s := hostFixture()
			invalid(&s)
			return s, nil
		}
		if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "error" {
			t.Fatal(o)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.HostSample = func(context.Context, model.HostOptions) (HostSample, error) { return hostFixture(), nil }
	if o := r.Run(ctx, testkit.Config(), x); o.Outcome != "error" {
		t.Fatal(o)
	}
}

func TestDarwinCounterFixtures(t *testing.T) {
	for _, tc := range []struct {
		before, after [4]uint32
		want          float64
		bad           bool
	}{
		{before: [4]uint32{100, 200, 300, 0}, after: [4]uint32{110, 215, 375, 0}, want: 25},
		{before: [4]uint32{^uint32(0) - 4, 0, 100, 0}, after: [4]uint32{5, 0, 110, 0}, want: 50},
		{bad: true},
	} {
		got, err := darwinCPUPercent(tc.before, tc.after)
		if (err != nil) != tc.bad || !tc.bad && got != tc.want {
			t.Fatal(got, err, tc)
		}
	}
	raw := make([]byte, 96)
	binary.LittleEndian.PutUint32(raw[0:4], 150) // free includes 50 speculative
	binary.LittleEndian.PutUint32(raw[8:12], 200)
	binary.LittleEndian.PutUint32(raw[92:96], 50)
	if got, err := darwinAvailable(raw, 16384, 1<<24); err != nil || got != 350*16384 {
		t.Fatal(got, err)
	}
	for _, data := range [][]byte{nil, raw[:95], raw} {
		if _, err := darwinAvailable(data, 16384, 1); err == nil {
			t.Fatal("invalid memory counters accepted")
		}
	}
	raw = make([]byte, 3*darwinProcSize)
	for i, name := range []string{"topo", "topo", "other"} {
		p := raw[i*darwinProcSize:]
		p[36] = 2
		binary.LittleEndian.PutUint32(p[40:44], uint32(100+i))
		copy(p[243:260], name)
	}
	raw[darwinProcSize+36] = 5
	s := newHostRuntime([]string{"topo"})
	calls := 0
	rss := func(pid int32) (uint64, error) {
		calls++
		if pid != 100 {
			t.Fatal(pid)
		}
		return 1024, nil
	}
	if err := parseDarwinProcessTable(raw, &s, rss); err != nil || s.Processes != 3 || s.Zombies != 1 || s.matches["topo"] != (processUsage{1, 1024}) || calls != 1 {
		t.Fatal(s, err, calls)
	}
	for _, data := range [][]byte{nil, raw[:len(raw)-1], make([]byte, (8<<20)+1)} {
		if err := parseDarwinProcessTable(data, &s, rss); err == nil {
			t.Fatal("invalid process counters accepted")
		}
	}
	if err := parseDarwinProcessTable(raw, &s, func(int32) (uint64, error) { return 0, hostReadError("processes") }); err == nil {
		t.Fatal("RSS failure ignored")
	}
}

func TestDarwinCPUCachedTicksRetry(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cachedReads int
		bad         bool
	}{{"retry", 2, false}, {"exhausted", 6, true}, {"first window", 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			waits := []time.Duration{}
			read := func() ([4]uint32, error) {
				reads++
				if reads <= tc.cachedReads {
					return [4]uint32{100, 100, 100, 0}, nil
				}
				n := uint32(reads - tc.cachedReads)
				return [4]uint32{100 + 10*n, 100, 100 + 30*n, 0}, nil
			}
			wait := func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
			percent, err := sampleDarwinCPU(context.Background(), read, wait)
			if (err != nil) != tc.bad || !tc.bad && percent != 25 {
				t.Fatal(percent, err)
			}
			if reads > 6 {
				t.Fatal("CPU read budget exceeded", reads)
			}
			if tc.cachedReads == 2 && (len(waits) != 3 || waits[0] != 200*time.Millisecond || waits[1] != time.Second || waits[2] != 200*time.Millisecond) {
				t.Fatal(waits)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sampleDarwinCPU(ctx, func() ([4]uint32, error) { return [4]uint32{}, nil }, waitDarwinSample); err == nil {
		t.Fatal("canceled CPU collection continued")
	}
}
