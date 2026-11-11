package probe

import (
	"context"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

var hostKernel = syscall.NewLazyDLL("kernel32.dll")
var globalMemoryStatus = hostKernel.NewProc("GlobalMemoryStatusEx")
var diskFreeSpace = hostKernel.NewProc("GetDiskFreeSpaceExW")

type windowsMemoryStatus struct {
	Length, Load                                                                                         uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile, TotalVirtual, AvailVirtual, AvailExtendedVirtual uint64
}

func sampleHostRuntime(ctx context.Context, names []string) (hostRuntime, error) {
	s := newHostRuntime(names)
	if ctx.Err() != nil {
		return s, local("host", "host_sampling_canceled")
	}
	a, e := windowsSystemTimes(ctx)
	if e != nil {
		return s, e
	}
	start := time.Now()
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return s, local("host", "host_sampling_canceled")
	case <-timer.C:
	}
	b, e := windowsSystemTimes(ctx)
	if e != nil {
		return s, e
	}
	s.CPUSampleMS = time.Since(start).Milliseconds()
	s.CPUPercent, e = windowsCPUPercent(a, b)
	if e != nil {
		return s, e
	}
	var memory windowsMemoryStatus
	memory.Length = uint32(unsafe.Sizeof(memory))
	ok, _, _ := globalMemoryStatus.Call(uintptr(unsafe.Pointer(&memory)))
	if ok == 0 {
		return s, hostReadError("memory")
	}
	s.MemoryTotal, s.MemoryAvailable, s.MemoryBasis = memory.TotalPhys, memory.AvailPhys, "windows_AvailPhys"
	s.Swap = windowsSwapUsage()
	s.network = windowsNetwork()
	if e = windowsProcesses(ctx, &s); e != nil {
		return s, e
	}
	return s, nil
}
func readDisk(path string) (HostDisk, error) {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return HostDisk{}, hostReadError("disk")
	}
	name, e := syscall.UTF16PtrFromString(absolute)
	if e != nil {
		return HostDisk{}, hostReadError("disk")
	}
	var available, total, volumeFree uint64
	ok, _, _ := diskFreeSpace.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&volumeFree)))
	if ok == 0 {
		return HostDisk{}, hostReadError("disk")
	}
	// Total and available are caller-relative under Windows disk quotas.
	// Preserve the volume-wide free counter separately instead of mixing bases.
	return HostDisk{Total: total, Free: available, Available: available, Basis: "windows_caller_quota", VolumeFree: volumeFree}, nil
}
