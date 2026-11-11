package monitor

import (
	"encoding/json"
	"fmt"
	"slices"
)

// All monitor consumers share one wire budget, including the trailing newline.
const MaxPanelBytes = 1 << 20
const maxRowValueBytes = 64 << 10
const panelValueBudget = MaxPanelBytes / 2

func rowValueBudget(metric string) int {
	if metric == "servers.list" {
		// A list can carry readings and deadlines for 256 servers.
		return 256 << 10
	}
	return maxRowValueBytes
}

// Limit values before cloning, hashing or serializing a whole panel. The other
// half of the budget is reserved for template metadata, deadlines and scalars.
func boundedValue(v Value, remaining *int) Value {
	if v.Value == nil {
		return v
	}
	b, err := json.Marshal(v.Value)
	if err != nil || len(b) > rowValueBudget(v.Metric) || len(b) > *remaining {
		v = missing(v, "frame_budget_exceeded")
		v.Freshness = "unavailable"
		return v
	}
	*remaining -= len(b)
	return v
}

func boundPanel(p Panel) Panel {
	p.Rows = slices.Clone(p.Rows)
	remaining := panelValueBudget
	for i, row := range p.Rows {
		p.Rows[i] = boundedValue(row, &remaining)
	}
	return p
}

// MarshalPanel emits bounded JSON for both persistent files and stream frames.
// No full-panel marshal is attempted until rich values have been budgeted.
func MarshalPanel(p Panel) ([]byte, error) {
	b, err := json.Marshal(boundPanel(p))
	if err != nil {
		return nil, err
	}
	if len(b)+1 > MaxPanelBytes {
		return nil, fmt.Errorf("panel exceeds 1 MiB")
	}
	return append(b, '\n'), nil
}
