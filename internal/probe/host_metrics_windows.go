package probe

import (
	"math"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"github.com/WhitenWhiten/topophany/internal/model"
)

var hostIPHelper = syscall.NewLazyDLL("iphlpapi.dll")
var getIfTable2 = hostIPHelper.NewProc("GetIfTable2")
var freeMibTable = hostIPHelper.NewProc("FreeMibTable")
var enumPageFiles = hostKernel.NewProc("K32EnumPageFilesW")

// MIB_IF_ROW2, retaining only the fields needed for interface accounting.
type windowsIfRow struct {
	luid          uint64
	_             [1120]byte // InterfaceIndex through Mtu
	interfaceType uint32
	_             [76]byte // TunnelType through ReceiveLinkSpeed
	inOctets      uint64
	_             [8]uint64 // remaining receive counters
	outOctets     uint64
	_             [8]uint64 // remaining transmit counters
}

type windowsIfTable struct {
	count uint32
	_     uint32 // MIB_IF_ROW2 alignment
	rows  [1]windowsIfRow
}

func windowsNetwork() hostNetworkSample {
	if getIfTable2.Find() != nil || freeMibTable.Find() != nil {
		return hostNetworkSample{}
	}
	var table *windowsIfTable
	status, _, _ := getIfTable2.Call(uintptr(unsafe.Pointer(&table)))
	if status != 0 || table == nil {
		return hostNetworkSample{}
	}
	defer freeMibTable.Call(uintptr(unsafe.Pointer(table)))
	if table.count > maxHostInterfaces {
		return hostNetworkSample{}
	}
	s := hostNetworkSample{at: time.Now(), interfaces: make(map[string]hostInterfaceCounters, table.count)}
	for _, row := range unsafe.Slice(&table.rows[0], table.count) {
		const softwareLoopback = 24
		if row.interfaceType != softwareLoopback {
			id := strconv.FormatUint(row.luid, 10)
			if _, exists := s.interfaces[id]; exists {
				return hostNetworkSample{}
			}
			s.interfaces[id] = hostInterfaceCounters{rx: row.inOctets, tx: row.outOctets}
		}
	}
	return s
}

// ENUM_PAGE_FILE_INFORMATION contains page counts, not bytes. The callback
// reads only the counters and never retains page file names.
type windowsPageFileInfo struct {
	size, reserved    uint32
	total, used, peak uintptr
}

type windowsSwapPages struct {
	total, used uint64
	invalid     bool
}

var collectWindowsSwap = syscall.NewCallback(func(pages *windowsSwapPages, file *windowsPageFileInfo, _ *uint16) uintptr {
	if file == nil || uintptr(file.size) < unsafe.Sizeof(*file) || file.used > file.total {
		pages.invalid = true
		return 0
	}
	total, used := uint64(file.total), uint64(file.used)
	if total > math.MaxUint64-pages.total || used > math.MaxUint64-pages.used {
		pages.invalid = true
		return 0
	}
	pages.total += total
	pages.used += used
	return 1
})

func windowsSwapUsage() model.SwapReport {
	if enumPageFiles.Find() != nil {
		return model.SwapReport{}
	}
	var pages windowsSwapPages
	ok, _, _ := enumPageFiles.Call(collectWindowsSwap, uintptr(unsafe.Pointer(&pages)))
	if ok == 0 || pages.invalid {
		return model.SwapReport{}
	}
	pageSize := uint64(os.Getpagesize())
	total, totalErr := bytesFromUnits(pages.total, pageSize)
	used, usedErr := bytesFromUnits(pages.used, pageSize)
	if totalErr != nil || usedErr != nil {
		return model.SwapReport{}
	}
	return model.SwapReport{TotalBytes: &total, UsedBytes: &used}
}
