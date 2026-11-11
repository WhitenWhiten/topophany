package probe

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestDarwinNativeHostMatchesRunningProcess(t *testing.T) {
	name := filepath.Base(os.Args[0])
	if len(name) > 15 {
		t.Skip("test executable exceeds process_name limit")
	}
	c := testkit.Config()
	c.Checks = []model.Check{{ID: "host", Observer: "home", Kind: "host", Host: &model.HostOptions{
		DiskPath: t.TempDir(), ProcessName: name, MinProcesses: 1,
	}}}
	config.Defaults(&c)
	o := NewRunner(c, "home").Run(context.Background(), c, c.Checks[0])
	matches, err := strconv.Atoi(o.Facts["process_matching_count"])
	if o.Outcome != "ok" || err != nil || matches < 1 || o.Facts["memory_total_bytes"] == "" {
		t.Fatal(o, err)
	}
}

func TestDarwinNativeCountersAgainstSystemTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name := filepath.Base(os.Args[0])
	if len(name) > 15 {
		t.Skip("test executable exceeds process_name limit")
	}
	s, err := sampleHostRuntime(ctx, []string{name})
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUSampleMS != 200 || s.CPUPercent < 0 || s.CPUPercent > 100 {
		t.Fatal(s.HostSample)
	}
	if s.Swap.TotalBytes == nil || s.Swap.UsedBytes == nil ||
		s.Load.Avg1m == nil || s.Load.Avg5m == nil || s.Load.Avg15m == nil ||
		s.network.interfaces == nil {
		t.Fatal("native swap, load or network counters unavailable", s.HostSample, s.network)
	}
	vm, err := exec.CommandContext(ctx, "/usr/bin/vm_stat").Output()
	if err != nil {
		t.Fatal(err)
	}
	pageRE := regexp.MustCompile(`page size of ([0-9]+) bytes`)
	pageMatch := pageRE.FindStringSubmatch(string(vm))
	if len(pageMatch) != 2 {
		t.Fatal("missing page size")
	}
	page, _ := strconv.ParseUint(pageMatch[1], 10, 64)
	var pages uint64
	found := 0
	for _, line := range strings.Split(string(vm), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && (key == "Pages free" || key == "Pages inactive" || key == "Pages speculative") {
			n, e := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
			if e != nil {
				t.Fatal(e)
			}
			pages += n
			found++
		}
	}
	expected := pages * page
	// Counters move between reads, especially while other tests build binaries.
	if found != 3 || math.Abs(float64(expected)-float64(s.MemoryAvailable)) > float64(max(s.MemoryTotal/20, 64<<20)) {
		t.Fatal("memory differs from vm_stat", s.MemoryAvailable, expected)
	}
	ps, err := exec.CommandContext(ctx, "/bin/ps", "-A", "-c", "-o", "pid=,stat=,rss=,comm=").Output()
	if err != nil {
		t.Fatal(err)
	}
	processes, zombies, matching := 0, 0, 0
	var rss uint64
	for _, line := range strings.Split(string(ps), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		processes++
		if strings.HasPrefix(fields[1], "Z") {
			zombies++
			continue
		}
		if strings.Join(fields[3:], " ") == name {
			matching++
			n, e := strconv.ParseUint(fields[2], 10, 64)
			if e != nil {
				t.Fatal(e)
			}
			rss += n * 1024
		}
	}
	match := s.matches[name]
	if abs(s.Processes-processes) > max(10, processes/20) || abs(s.Zombies-zombies) > 5 || match.count != matching || math.Abs(float64(match.rss)-float64(rss)) > 32<<20 {
		t.Fatalf("native processes=%d zombies=%d matching=%+v; ps processes=%d zombies=%d matching=%d rss=%d", s.Processes, s.Zombies, match, processes, zombies, matching, rss)
	}
	t.Logf("CPU %.2f%% / %dms; memory native=%d vm_stat=%d; processes native=%d ps=%d; selected RSS native=%d ps=%d", s.CPUPercent, s.CPUSampleMS, s.MemoryAvailable, expected, s.Processes, processes, match.rss, rss)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
