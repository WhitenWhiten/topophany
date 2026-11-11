package probe

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func sampleHostRuntime(ctx context.Context, names []string) (hostRuntime, error) {
	return sampleLinuxRuntime(ctx, names, "/proc")
}

func readLinuxCPU(path string) (cpuTicks, error) {
	f, err := os.Open(path)
	if err != nil {
		return cpuTicks{}, hostReadError("cpu")
	}
	defer f.Close()
	// Only the aggregate line is needed. Avoid reading per-CPU and interrupt
	// tables, whose size grows with the machine rather than the measurement.
	line, err := bufio.NewReaderSize(f, 4096).ReadSlice('\n')
	if err != nil {
		return cpuTicks{}, hostReadError("cpu")
	}
	return parseLinuxCPU(string(line))
}

func sampleLinuxRuntime(ctx context.Context, names []string, proc string) (hostRuntime, error) {
	s := newHostRuntime(names)
	readCPU := func() (cpuTicks, error) { return readLinuxCPU(filepath.Join(proc, "stat")) }
	a, err := readCPU()
	if err != nil {
		return s, err
	}
	start := time.Now()
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return s, local("host", "host_sampling_canceled")
	case <-timer.C:
	}
	b, err := readCPU()
	if err != nil {
		return s, err
	}
	s.CPUSampleMS = time.Since(start).Milliseconds()
	s.CPUPercent, err = cpuDelta(a, b)
	if err != nil {
		return s, err
	}
	mem, err := readHostFile(filepath.Join(proc, "meminfo"), 65536)
	if err != nil {
		return s, hostReadError("memory")
	}
	s.MemoryTotal, s.MemoryAvailable, err = parseLinuxMemory(string(mem))
	if err != nil {
		return s, err
	}
	s.MemoryBasis = "linux_MemAvailable"
	if ctx.Err() != nil {
		return s, local("host", "host_sampling_canceled")
	}
	s.Swap = parseLinuxSwap(string(mem))
	if load, readErr := readHostFile(filepath.Join(proc, "loadavg"), 256); readErr == nil {
		values := strings.Fields(string(load))
		if len(values) >= 3 {
			s.Load = model.ParseLoadAverages(values[:3])
		}
	}
	if dev, readErr := readHostFile(filepath.Join(proc, "net", "dev"), 1<<20); readErr == nil {
		s.network = hostNetworkSample{at: time.Now(), interfaces: parseLinuxNetwork(string(dev))}
	}
	err = linuxProcesses(ctx, proc, &s)
	return s, err
}

func readHostFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, local("host", "host_read_budget_exceeded")
	}
	return b, nil
}

type cpuTicks struct{ total, idle uint64 }

func parseLinuxCPU(raw string) (cpuTicks, error) {
	line, _, _ := strings.Cut(raw, "\n")
	f := strings.Fields(line)
	if len(f) < 9 || f[0] != "cpu" {
		return cpuTicks{}, local("host", "invalid_cpu_counters")
	}
	var s cpuTicks
	// Guest and guest_nice are already included in user/nice; do not count twice.
	for i, v := range f[1:9] {
		n, err := counter(v)
		if err != nil || n > math.MaxUint64-s.total {
			return cpuTicks{}, local("host", "invalid_cpu_counters")
		}
		s.total += n
		if i == 3 || i == 4 { // idle plus iowait
			s.idle += n
		}
	}
	return s, nil
}

func cpuDelta(a, b cpuTicks) (float64, error) {
	if b.total <= a.total || b.idle < a.idle || b.idle-a.idle > b.total-a.total {
		return 0, local("host", "cpu_counters_reset_or_unavailable")
	}
	return percentage((b.total-a.total)-(b.idle-a.idle), b.total-a.total), nil
}

func parseLinuxMemory(raw string) (total, available uint64, err error) {
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "MemTotal:" && f[0] != "MemAvailable:" {
			continue
		}
		if len(f) != 3 || f[2] != "kB" || seen[f[0]] {
			return 0, 0, local("host", "invalid_memory_counters")
		}
		seen[f[0]] = true
		n, e := counter(f[1])
		if e != nil {
			return 0, 0, e
		}
		n, e = bytesFromUnits(n, 1024)
		if e != nil {
			return 0, 0, e
		}
		if f[0] == "MemTotal:" {
			total = n
		} else {
			available = n
		}
	}
	if !seen["MemAvailable:"] || total == 0 || available > total {
		return 0, 0, local("host", "invalid_memory_counters")
	}
	return total, available, nil
}

func parseLinuxSwap(raw string) model.SwapReport {
	var total, free *uint64
	for line := range strings.SplitSeq(raw, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "SwapTotal:" && f[0] != "SwapFree:" {
			continue
		}
		if len(f) != 3 || f[2] != "kB" {
			return model.SwapReport{}
		}
		n, err := counter(f[1])
		if err != nil {
			return model.SwapReport{}
		}
		value, err := bytesFromUnits(n, 1024)
		if err != nil {
			return model.SwapReport{}
		}
		if f[0] == "SwapTotal:" && total == nil {
			total = &value
		} else if f[0] == "SwapFree:" && free == nil {
			free = &value
		} else {
			return model.SwapReport{}
		}
	}
	if total == nil || free == nil || *free > *total {
		return model.SwapReport{}
	}
	used := *total - *free
	return model.SwapReport{TotalBytes: total, UsedBytes: &used}
}

func parseLinuxNetwork(raw string) map[string]hostInterfaceCounters {
	lines := strings.SplitN(raw, "\n", 3)
	if len(lines) != 3 {
		return nil
	}
	interfaces := map[string]hostInterfaceCounters{}
	for line := range strings.SplitSeq(strings.TrimSpace(lines[2]), "\n") {
		name, values, found := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if name == "lo" {
			continue
		}
		fields := strings.Fields(values)
		if !found || name == "" || len(fields) != 16 || len(interfaces) >= maxHostInterfaces {
			return nil
		}
		if _, exists := interfaces[name]; exists {
			return nil
		}
		rx, rxErr := counter(fields[0])
		tx, txErr := counter(fields[8])
		if rxErr != nil || txErr != nil {
			return nil
		}
		interfaces[name] = hostInterfaceCounters{rx: rx, tx: tx}
	}
	return interfaces
}

func linuxProcesses(ctx context.Context, proc string, s *hostRuntime) error {
	dir, err := os.Open(proc)
	if err != nil {
		return hostReadError("processes")
	}
	defer dir.Close()
	entries := 0
	for {
		batch, err := dir.Readdirnames(256)
		if err != nil && !errors.Is(err, io.EOF) {
			return hostReadError("processes")
		}
		for _, e := range batch {
			if ctx.Err() != nil {
				return local("host", "host_sampling_canceled")
			}
			entries++
			if entries > 131072 {
				return local("host", "host_process_budget_exceeded")
			}
			pid, parseErr := strconv.ParseUint(e, 10, 32)
			if parseErr != nil || pid == 0 {
				continue
			}
			b, readErr := readHostFile(filepath.Join(proc, e, "stat"), 8192)
			if errors.Is(readErr, os.ErrNotExist) || errors.Is(readErr, syscall.ESRCH) {
				continue // Exit can race both open (ENOENT) and read (ESRCH).
			}
			if readErr != nil {
				return hostReadError("processes")
			}
			comm, state, rss, parseErr := parseLinuxProcess(string(b))
			if parseErr != nil {
				return parseErr
			}
			if err := s.addProcess(comm, state == "Z", rss, uint64(os.Getpagesize())); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func parseLinuxProcess(raw string) (name, state string, rss uint64, err error) {
	a, b := strings.IndexByte(raw, '('), strings.LastIndexByte(raw, ')')
	if a < 1 || b <= a {
		return "", "", 0, local("host", "invalid_process_counters")
	}
	f := strings.Fields(raw[b+1:])
	if len(f) < 22 || len(f[0]) != 1 {
		return "", "", 0, local("host", "invalid_process_counters")
	}
	rss, err = counter(f[21])
	return raw[a+1 : b], f[0], rss, err
}
