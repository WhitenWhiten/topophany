package model

import (
	"math"
	"strconv"
)

// ResourceMetrics are optional host readings shared by native and external
// resource samples. Nil scalars represent missing readings, including in
// samples written before these fields were added.
type ResourceMetrics struct {
	Network NetworkReport `json:"network,omitzero"`
	Swap    SwapReport    `json:"swap,omitzero"`
	Load    LoadReport    `json:"load,omitzero"`
}

type NetworkReport struct {
	RXBytesPerSecond *float64 `json:"rx_bytes_per_second,omitempty"`
	TXBytesPerSecond *float64 `json:"tx_bytes_per_second,omitempty"`
}

type SwapReport struct {
	TotalBytes *uint64 `json:"total_bytes,omitempty"`
	UsedBytes  *uint64 `json:"used_bytes,omitempty"`
}

type LoadReport struct {
	Avg1m  *float64 `json:"avg_1m,omitempty"`
	Avg5m  *float64 `json:"avg_5m,omitempty"`
	Avg15m *float64 `json:"avg_15m,omitempty"`
}

func (m ResourceMetrics) Clone() ResourceMetrics {
	for _, p := range []**float64{
		&m.Network.RXBytesPerSecond, &m.Network.TXBytesPerSecond,
		&m.Load.Avg1m, &m.Load.Avg5m, &m.Load.Avg15m,
	} {
		if *p != nil {
			value := **p
			*p = &value
		}
	}
	for _, p := range []**uint64{&m.Swap.TotalBytes, &m.Swap.UsedBytes} {
		if *p != nil {
			value := **p
			*p = &value
		}
	}
	return m
}

// SetFacts projects the readings into the resource facts shared by the host
// check and the selected external resource view.
func (m ResourceMetrics) SetFacts(facts map[string]string) {
	for _, f := range []struct {
		key   string
		value *float64
	}{
		{"network_rx_bytes_per_second", m.Network.RXBytesPerSecond},
		{"network_tx_bytes_per_second", m.Network.TXBytesPerSecond},
		{"load_avg_1m", m.Load.Avg1m},
		{"load_avg_5m", m.Load.Avg5m},
		{"load_avg_15m", m.Load.Avg15m},
	} {
		if f.value != nil {
			facts[f.key] = strconv.FormatFloat(*f.value, 'g', -1, 64)
		}
	}
	if m.Swap.TotalBytes != nil {
		facts["swap_total_bytes"] = strconv.FormatUint(*m.Swap.TotalBytes, 10)
	}
	if m.Swap.UsedBytes != nil {
		facts["swap_used_bytes"] = strconv.FormatUint(*m.Swap.UsedBytes, 10)
	}
}

func ResourceMetricsFromFacts(facts map[string]string) ResourceMetrics {
	byteCount := func(key string) *uint64 {
		value, err := strconv.ParseUint(facts[key], 10, 64)
		if err != nil {
			return nil
		}
		return &value
	}
	swap := SwapReport{TotalBytes: byteCount("swap_total_bytes"), UsedBytes: byteCount("swap_used_bytes")}
	if swap.TotalBytes != nil && swap.UsedBytes != nil && *swap.UsedBytes > *swap.TotalBytes {
		swap.UsedBytes = nil
	}
	return ResourceMetrics{
		Network: NetworkReport{
			RXBytesPerSecond: parseResourceNumber(facts["network_rx_bytes_per_second"]),
			TXBytesPerSecond: parseResourceNumber(facts["network_tx_bytes_per_second"]),
		},
		Swap: swap,
		Load: LoadReport{
			Avg1m:  parseResourceNumber(facts["load_avg_1m"]),
			Avg5m:  parseResourceNumber(facts["load_avg_5m"]),
			Avg15m: parseResourceNumber(facts["load_avg_15m"]),
		},
	}
}

// ParseLoadAverages takes the 1, 5 and 15 minute values in that order.
func ParseLoadAverages(values []string) LoadReport {
	if len(values) != 3 {
		return LoadReport{}
	}
	return LoadReport{
		Avg1m:  parseResourceNumber(values[0]),
		Avg5m:  parseResourceNumber(values[1]),
		Avg15m: parseResourceNumber(values[2]),
	}
}

func parseResourceNumber(raw string) *float64 {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.Signbit(value) || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}
