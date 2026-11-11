package probe

import (
	"context"
	"encoding/binary"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Use the syscall package's libc entry point, including its stack switching
// and pointer lifetime handling. The linker explicitly supports this symbol.
//
//go:linkname darwinCall6 syscall.syscall6
//go:uintptrescapes
func darwinCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, syscall.Errno)

//go:cgo_import_dynamic topo_mach_host_self mach_host_self "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic topo_host_statistics host_statistics "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic topo_host_statistics64 host_statistics64 "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic topo_sysctl sysctl "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic topo_proc_pidinfo proc_pidinfo "/usr/lib/libSystem.B.dylib"

func hostSelfPC() uintptr
func hostStatisticsPC() uintptr
func hostStatistics64PC() uintptr
func hostSysctlPC() uintptr
func hostProcInfoPC() uintptr

// Retain one host send right for the process lifetime rather than acquiring
// a fresh right on every sample.
var darwinHostPort = sync.OnceValue(func() uintptr {
	port, _, _ := darwinCall6(hostSelfPC(), 0, 0, 0, 0, 0, 0)
	return port
})

func darwinCPUTicks() ([4]uint32, error) {
	var ticks [4]uint32
	count := uint32(len(ticks))
	r, _, _ := darwinCall6(hostStatisticsPC(), darwinHostPort(), 3, uintptr(unsafe.Pointer(&ticks)), uintptr(unsafe.Pointer(&count)), 0, 0)
	if r != 0 || count != 4 {
		return ticks, hostReadError("cpu")
	}
	return ticks, nil
}

func sampleHostRuntime(ctx context.Context, names []string) (hostRuntime, error) {
	s := newHostRuntime(names)
	var err error
	s.CPUPercent, err = sampleDarwinCPU(ctx, darwinCPUTicks, waitDarwinSample)
	if err != nil {
		return s, err
	}
	s.CPUSampleMS = 200
	mem, err := syscall.Sysctl("hw.memsize")
	// Sysctl strips one trailing zero byte, including from binary counters.
	if err != nil || len(mem) < 7 || len(mem) > 8 {
		return s, hostReadError("memory")
	}
	var memory [8]byte
	copy(memory[:], mem)
	s.MemoryTotal = binary.LittleEndian.Uint64(memory[:])
	if s.MemoryTotal == 0 {
		return s, local("host", "invalid_memory_counters")
	}
	// Request the stable revision-zero VM prefix; later kernel extensions are
	// unnecessary. Its count is in 32-bit words, while its counters need alignment.
	var vm [12]uint64
	count := uint32(24)
	r, _, _ := darwinCall6(hostStatistics64PC(), darwinHostPort(), 4, uintptr(unsafe.Pointer(&vm)), uintptr(unsafe.Pointer(&count)), 0, 0)
	if r != 0 || count < 24 {
		return s, hostReadError("memory")
	}
	page := uint64(syscall.Getpagesize())
	s.MemoryAvailable, err = darwinAvailable(unsafe.Slice((*byte)(unsafe.Pointer(&vm)), 96), page, s.MemoryTotal)
	if err != nil {
		return s, err
	}
	s.MemoryBasis = "macos_estimated_free_inactive_speculative"
	s.Swap, s.Load = darwinSwapUsage(), darwinLoadAverages()
	s.network = darwinNetwork(ctx)
	raw, err := darwinProcesses(ctx)
	if err != nil {
		return s, err
	}
	err = parseDarwinProcessTable(raw, &s, func(pid int32) (uint64, error) {
		if ctx.Err() != nil {
			return 0, local("host", "host_sampling_canceled")
		}
		var task [12]uint64 // proc_taskinfo: resident_size is the second uint64
		n, _, _ := darwinCall6(hostProcInfoPC(), uintptr(pid), 4, 0, uintptr(unsafe.Pointer(&task)), 96, 0)
		if n != 96 {
			return 0, hostReadError("processes")
		}
		return task[1], nil
	})
	return s, err
}

func darwinSwapUsage() model.SwapReport {
	raw, err := syscall.Sysctl("vm.swapusage")
	// xsw_usage stores total, available and used as uint64 bytes.
	if err != nil || len(raw) < 24 {
		return model.SwapReport{}
	}
	total, used := binary.LittleEndian.Uint64([]byte(raw[:8])), binary.LittleEndian.Uint64([]byte(raw[16:24]))
	if used > total {
		return model.SwapReport{}
	}
	return model.SwapReport{TotalBytes: &total, UsedBytes: &used}
}

func darwinLoadAverages() model.LoadReport {
	raw, err := syscall.Sysctl("vm.loadavg")
	// On 64-bit Darwin the three fixpt_t values precede a padded long
	// scale. Sysctl may trim the final zero byte.
	var data [24]byte
	if err != nil || len(raw) < len(data)-1 || len(raw) > len(data) {
		return model.LoadReport{}
	}
	copy(data[:], raw)
	scale := int64(binary.LittleEndian.Uint64(data[16:24]))
	if scale <= 0 {
		return model.LoadReport{}
	}
	one := float64(binary.LittleEndian.Uint32(data[0:4])) / float64(scale)
	five := float64(binary.LittleEndian.Uint32(data[4:8])) / float64(scale)
	fifteen := float64(binary.LittleEndian.Uint32(data[8:12])) / float64(scale)
	return model.LoadReport{Avg1m: &one, Avg5m: &five, Avg15m: &fifteen}
}

func darwinNetwork(ctx context.Context) hostNetworkSample {
	mib := [6]int32{syscall.CTL_NET, syscall.AF_ROUTE, 0, 0, syscall.NET_RT_IFLIST2, 0}
	for range 3 {
		if ctx.Err() != nil {
			break
		}
		var size uintptr
		_, _, err := darwinCall6(hostSysctlPC(), uintptr(unsafe.Pointer(&mib)), uintptr(len(mib)), 0, uintptr(unsafe.Pointer(&size)), 0, 0)
		if err != 0 || size == 0 || size > 1<<20 {
			break
		}
		data := make([]byte, size)
		_, _, err = darwinCall6(hostSysctlPC(), uintptr(unsafe.Pointer(&mib)), uintptr(len(mib)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
		if err == syscall.ENOMEM {
			continue
		}
		if err == 0 && size <= uintptr(len(data)) {
			return hostNetworkSample{at: time.Now(), interfaces: parseDarwinNetwork(data[:size])}
		}
		break
	}
	return hostNetworkSample{}
}

func parseDarwinNetwork(data []byte) map[string]hostInterfaceCounters {
	interfaces := map[string]hostInterfaceCounters{}
	for len(data) > 0 {
		if len(data) < 4 {
			return nil
		}
		size := int(binary.LittleEndian.Uint16(data[:2]))
		if size < 4 || size > len(data) || data[2] != syscall.RTM_VERSION {
			return nil
		}
		msg := data[:size]
		data = data[size:]
		if msg[3] != syscall.RTM_IFINFO2 {
			continue
		}
		// if_msghdr2 and if_data64 offsets from <net/if.h> and <net/if_var.h>.
		if len(msg) < 160 || len(interfaces) >= maxHostInterfaces {
			return nil
		}
		if binary.LittleEndian.Uint32(msg[8:12])&syscall.IFF_LOOPBACK != 0 {
			continue
		}
		id := strconv.FormatUint(uint64(binary.LittleEndian.Uint16(msg[12:14])), 10)
		if _, exists := interfaces[id]; exists {
			return nil
		}
		interfaces[id] = hostInterfaceCounters{
			rx: binary.LittleEndian.Uint64(msg[96:104]),
			tx: binary.LittleEndian.Uint64(msg[104:112]),
		}
	}
	return interfaces
}

func darwinProcesses(ctx context.Context) ([]byte, error) {
	mib := [3]int32{1, 14, 0} // CTL_KERN, KERN_PROC, KERN_PROC_ALL
	// A process may appear between the sizing and reading calls. Retry within
	// the original 8 MiB read budget, with bounded attempts and spare records.
	for range 3 {
		if ctx.Err() != nil {
			return nil, local("host", "host_sampling_canceled")
		}
		var size uintptr
		_, _, err := darwinCall6(hostSysctlPC(), uintptr(unsafe.Pointer(&mib)), 3, 0, uintptr(unsafe.Pointer(&size)), 0, 0)
		if err != 0 {
			return nil, hostReadError("processes")
		}
		const budget = 8 << 20
		if size == 0 || size > budget {
			return nil, local("host", "invalid_process_counters")
		}
		capacity := min(size+16*darwinProcSize, budget)
		data := make([]byte, capacity)
		size = capacity
		_, _, err = darwinCall6(hostSysctlPC(), uintptr(unsafe.Pointer(&mib)), 3, uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
		if err == syscall.ENOMEM {
			continue
		}
		if err != 0 || size > capacity {
			return nil, hostReadError("processes")
		}
		return data[:size], nil
	}
	return nil, hostReadError("processes")
}
