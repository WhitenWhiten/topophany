package probe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLinuxCPUAccountingAndReset(t *testing.T) {
	a, err := parseLinuxCPU("cpu 100 20 30 400 50 10 20 5 80 10\ncpu0 1 2 3 4")
	if err != nil || a.total != 635 || a.idle != 450 {
		t.Fatal(a, err)
	}
	got, err := cpuDelta(a, cpuTicks{total: 735, idle: 525})
	if err != nil || got != 25 {
		t.Fatal(got, err)
	}
	for _, b := range []cpuTicks{a, {total: 634, idle: 449}, {total: 735, idle: 449}, {total: 735, idle: 551}} {
		if _, err := cpuDelta(a, b); err == nil {
			t.Fatal("counter reset accepted", b)
		}
	}
	if _, err := parseLinuxCPU("cpu 1 2 broken"); err == nil {
		t.Fatal("malformed CPU accepted")
	}
}

func TestLinuxProcessExitAfterOpen(t *testing.T) {
	p := exec.Command("sleep", "30")
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Process.Kill(); _ = p.Wait() })
	f, err := os.Open(fmt.Sprintf("/proc/%d/stat", p.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := p.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = p.Wait()
	// Reopening this descriptor keeps the exited process's procfs inode.
	// This reproduces read(2)'s ESRCH, unlike a missing fixture file (ENOENT).
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "123"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "123", "stat")
	if err := os.Symlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()), path); err != nil {
		t.Fatal(err)
	}
	if _, err := readHostFile(path, 8192); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("expected real procfs ESRCH, got %v", err)
	}
	s := newHostRuntime([]string{"sleep"})
	if err := linuxProcesses(context.Background(), root, &s); err != nil || s.Processes != 0 || s.matches["sleep"].count != 0 {
		t.Fatal("exited process invalidated snapshot", s, err)
	}
}

func TestLinuxCPUReadsOnlyBoundedAggregateLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat")
	write := func(raw string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("cpu 100 20 30 400 50 10 20 5 80 10\n" + strings.Repeat("cpu0 1 2 3 4\n", 100000))
	if s, err := readLinuxCPU(path); err != nil || s.total != 635 || s.idle != 450 {
		t.Fatal(s, err)
	}
	write(strings.Repeat("1", 4096) + "\n")
	if _, err := readLinuxCPU(path); err == nil {
		t.Fatal("oversized aggregate line accepted")
	}
}

func TestLinuxMemoryAccounting(t *testing.T) {
	total, avail, err := parseLinuxMemory("MemTotal: 1000 kB\nMemFree: 100 kB\nMemAvailable: 250 kB\n")
	if err != nil || total != 1024000 || avail != 256000 {
		t.Fatal(total, avail, err)
	}
	for _, raw := range []string{"MemTotal: 1000 kB", "MemTotal: 1 kB\nMemAvailable: 2 kB", "MemTotal: 1000 B\nMemAvailable: 2 kB", "MemTotal: 18446744073709551615 kB\nMemAvailable: 0 kB"} {
		if _, _, err := parseLinuxMemory(raw); err == nil {
			t.Fatal("invalid memory accepted", raw)
		}
	}
}

func TestLinuxSwapAndNetworkCounters(t *testing.T) {
	swap := parseLinuxSwap("MemTotal: 10000 kB\nSwapTotal: 4096 kB\nSwapFree: 1024 kB\n")
	if swap.TotalBytes == nil || *swap.TotalBytes != 4<<20 || swap.UsedBytes == nil || *swap.UsedBytes != 3<<20 {
		t.Fatal("incorrect swap usage", swap)
	}
	const counters = "18446744073709551614 0 0 0 0 0 0 0 1024 0 0 0 0 0 0 0\n"
	nics := parseLinuxNetwork("Inter-|   Receive | Transmit\n face |bytes |bytes\n  lo: " + counters + " eth0: " + counters)
	if got := nics["eth0"]; len(nics) != 1 || got.rx != ^uint64(0)-1 || got.tx != 1024 {
		t.Fatal("incorrect network counters", nics)
	}
}

func processStat(pid int, name, state string, rss int) string {
	return fmt.Sprintf("%d (%s) %s %s %d 0\n", pid, name, state, strings.Repeat("0 ", 20), rss)
}

func TestLinuxProcessSnapshotAndDisappearingProcess(t *testing.T) {
	root := t.TempDir()
	for i, p := range []struct {
		name, state string
		rss         int
	}{{"topo", "S", 10}, {"topo", "Z", 0}, {"space ) name", "R", 20}} {
		dir := filepath.Join(root, fmt.Sprint(i+1))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(processStat(i+1, p.name, p.state, p.rss)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "999"), 0700); err != nil {
		t.Fatal(err)
	} // Exited before stat read.
	s := newHostRuntime([]string{"topo"})
	if err := linuxProcesses(context.Background(), root, &s); err != nil {
		t.Fatal(err)
	}
	if s.Processes != 3 || s.Zombies != 1 || s.matches["topo"].count != 1 || s.matches["topo"].rss != uint64(10*os.Getpagesize()) {
		t.Fatal(s)
	}
	if name, _, rss, err := parseLinuxProcess(processStat(1, "space ) name", "R", 20)); err != nil || name != "space ) name" || rss != 20 {
		t.Fatal(name, rss, err)
	}
	if _, _, _, err := parseLinuxProcess("1 (bad) S 0"); err == nil {
		t.Fatal("malformed process accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := linuxProcesses(ctx, root, &hostRuntime{}); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestLinuxHostNativeSampling(t *testing.T) {
	s, err := sampleLinuxRuntime(context.Background(), nil, "/proc")
	if err != nil || s.MemoryTotal == 0 || s.Processes < 1 || s.CPUPercent < 0 || s.CPUPercent > 100 || s.CPUSampleMS < 200 {
		t.Fatal(s, err)
	}
	if s.Swap.TotalBytes == nil || s.Swap.UsedBytes == nil ||
		s.Load.Avg1m == nil || s.Load.Avg5m == nil || s.Load.Avg15m == nil ||
		s.network.interfaces == nil {
		t.Fatal("native swap, load or network counters unavailable", s.HostSample, s.network)
	}
	if _, err := sampleLinuxRuntime(context.Background(), nil, t.TempDir()); err == nil {
		t.Fatal("missing procfs accepted")
	}
	if _, err := sampleDisk(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing filesystem accepted")
	}
}
