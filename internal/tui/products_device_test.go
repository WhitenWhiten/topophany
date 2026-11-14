//go:build !nodevice

package tui

import (
	"os"
	"strings"
	"testing"
)

// On every device, Products mode's Overview is the board in place of the
// map and the list of what needs attention, a finger tall on a phone.
func TestProductsOnEveryDevice(t *testing.T) {
	for _, d := range Devices() {
		for _, detail := range []bool{false, true} {
			m := productsModel(t, "app-origin-down", 240, 110)
			dev, e := DeviceByID(d.ID)
			if e != nil {
				t.Fatal(e)
			}
			m.Device = dev
			m.Detail = detail
			name := d.ID
			if detail {
				name += " detail"
			}
			s := m.screen()
			m.drawScreen(s)
			out := visible(m)
			if os.Getenv("TOPO_PRINT") != "" && !detail {
				t.Logf("%s\n%s", name, out)
			}
			// Opened, a line's evidence takes a phone's page and a tablet's
			// body; the other layouts keep the board beside it.
			if l := s.layout(); !detail || l != layoutCompact && l != layoutRegular {
				if !strings.Contains(out, "PRODUCTS") || !strings.Contains(out, "Snapmeal") {
					t.Errorf("%s: no products board:\n%s", name, out)
				}
			}
			if strings.Contains(out, "NEEDS ATTENTION") || strings.Contains(out, "NETWORK ") {
				t.Errorf("%s: the network Overview shows through:\n%s", name, out)
			}
			if s.layout() == layoutRegular {
				continue // a tablet keeps the terminal's board, a row a line
			}
			rows := map[string]map[int]bool{}
			for _, h := range m.hits {
				if !strings.HasPrefix(h.key, "row:") && !strings.HasPrefix(h.key, "touch:open:") {
					continue
				}
				if rows[h.key] == nil {
					rows[h.key] = map[int]bool{}
				}
				for y := h.y; y < h.y+h.h; y++ {
					rows[h.key][y] = true
				}
			}
			for k, ys := range rows {
				if len(ys) < touchRows {
					t.Errorf("%s: %q is %d rows tall", name, k, len(ys))
				}
			}
		}
	}
}
