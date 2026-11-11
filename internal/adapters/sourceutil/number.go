package sourceutil

import (
	"bytes"
	"encoding/json"
	"math"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// ResourceNumber reads a nonnegative JSON number and preserves a measured zero.
func ResourceNumber(raw json.RawMessage) (*float64, string) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, model.FieldMissing
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) || math.Signbit(value) {
		return nil, model.FieldInvalid
	}
	return &value, model.FieldKnown
}
