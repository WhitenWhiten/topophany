package tui

import (
	"strings"
	"testing"
)

var otherReadings = map[string]string{
	"network_rx_bytes_per_second": "1834500", "network_tx_bytes_per_second": "312700",
	"swap_total_bytes": "4294967296", "swap_used_bytes": "536870912",
	"load_avg_1m": "0.42", "load_avg_5m": "0.37", "load_avg_15m": "0.31",
}

// A card says the other readings in one row, each under the panel it goes
// with: the load under the CPU, the swap under the memory, the network
// under the disk.
func TestCardSaysOtherReadingsUnderTheirPanels(t *testing.T) {
	ns := panelWidths(42)
	if got, want := spansText(readingCells(otherReadings, ns, panelGap)), "load    0.42  swap     12%  ↓1.7M    ↑305K"; got != want {
		t.Fatalf("readings row %q, want %q", got, want)
	}
	// Windows reports no load averages, and a host may have no swap.
	windows := map[string]string{"network_rx_bytes_per_second": "2048", "network_tx_bytes_per_second": "0", "swap_total_bytes": "0", "swap_used_bytes": "0"}
	if got, want := spansText(readingCells(windows, ns, panelGap)), "              swap    none  ↓2.0K       ↑0"; got != want {
		t.Fatalf("readings row %q, want %q", got, want)
	}
	if readingCells(map[string]string{"cpu_percent": "12"}, ns, panelGap) != nil {
		t.Fatal("a host reporting none of the readings has a row for them")
	}
}

// Laid open, each reading has a line of its own in the charts' register.
func TestOpenCardSaysOtherReadingsInFull(t *testing.T) {
	got := []string{}
	for _, l := range readingLines(otherReadings, 8, fg(cMuted)) {
		got = append(got, spansText(l))
	}
	want := []string{"NETWORK ↓ 1.7 MiB/s   ↑ 305.4 KiB/s", "SWAP    12%  0.5/4.0 GiB", "LOAD    0.42  0.37  0.31   1 · 5 · 15 min"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("readings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
