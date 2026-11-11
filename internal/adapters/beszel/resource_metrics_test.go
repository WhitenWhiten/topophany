package beszel

import (
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestOptionalMetricsAvailabilityAndWireTypes(t *testing.T) {
	for _, tc := range []struct{ name, raw, key, state string }{
		{"missing rate", ``, "network.rx_bytes_per_second", model.FieldMissing},
		{"null tuple", `,"b":null`, "network.rx_bytes_per_second", model.FieldMissing},
		{"short tuple", `,"b":[1]`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"fraction", `,"b":[0,1.5]`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"overflow", `,"b":[0,18446744073709551616]`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"no fallback", `,"b":[0,-1],"nr":1`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"legacy overflow", `,"nr":1e308`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"invalid load", `,"la":[0,-1,null]`, "load.avg_5m", model.FieldInvalid},
		{"null load", `,"la":[0,-1,null]`, "load.avg_15m", model.FieldMissing},
		{"wrong load shape", `,"la":{}`, "load.avg_1m", model.FieldInvalid},
		{"swap overrun", `,"swap":[1,2]`, "swap.used_bytes", model.FieldInvalid},
		{"legacy swap overrun before rounding", `,"s":0.0000000001,"su":0.0000000002`, "swap.used_bytes", model.FieldInvalid},
		{"explicit null usage", `,"s":2,"su":null`, "swap.used_bytes", model.FieldMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, fields := decodeFixture(t, `{"cpu":0`+tc.raw+`}`)
			if fields[tc.key] != tc.state {
				t.Fatalf("%s = %q, want %q", tc.key, fields[tc.key], tc.state)
			}
		})
	}
	r, _ := decodeFixture(t, `{"b":[0,18446744073709551615],"swap":[0,0],"la":[0,0,0]}`)
	wantValue(t, "zero tx", r.Network.TXBytesPerSecond, 0.0)
	wantValue(t, "uint64 rx", r.Network.RXBytesPerSecond, float64(^uint64(0)))
	wantValue(t, "no swap", r.Swap.TotalBytes, uint64(0))
}
