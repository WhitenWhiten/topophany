package komari

import (
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestOptionalMetricsAvailabilityAndWireTypes(t *testing.T) {
	for _, tc := range []struct{ name, raw, key, state string }{
		{"missing", ``, "network.rx_bytes_per_second", model.FieldMissing},
		{"null", `,"network":{"down":null}`, "network.rx_bytes_per_second", model.FieldMissing},
		{"fraction", `,"network":{"down":1.5}`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"overflow", `,"network":{"down":9223372036854775808}`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"negative zero", `,"network":{"down":-0}`, "network.rx_bytes_per_second", model.FieldInvalid},
		{"negative load", `,"load":{"load1":-1}`, "load.avg_1m", model.FieldInvalid},
		{"swap overrun", `,"swap":{"total":1,"used":2}`, "swap.used_bytes", model.FieldInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := requireResource(t, resourceBody(`"cpu":{}`+tc.raw))
			wantField(t, r.Fields, tc.key, tc.state)
		})
	}
	for _, raw := range []string{`"network":[]`, `"load":42`, `"swap":"bad"`} {
		if r, err := resource(resourceBody(`"cpu":{},`+raw), testObject); err == nil || r != nil {
			t.Fatalf("malformed optional group accepted: %s, %v", raw, err)
		}
	}
	r := requireResource(t, resourceBody(`"network":{"up":0,"down":9223372036854775807},"swap":{"total":0,"used":0},"load":{"load1":0,"load5":0,"load15":0}`))
	wantValue(t, "zero tx", r.Resource.Network.TXBytesPerSecond, 0.0)
	wantValue(t, "int64 rx", r.Resource.Network.RXBytesPerSecond, float64(9223372036854775807))
	wantValue(t, "no swap", r.Resource.Swap.TotalBytes, uint64(0))
}
