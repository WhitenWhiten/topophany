package probe

import (
	"bytes"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"reflect"
	"strconv"
)

func jsonAssertions(body []byte, assertions map[string]json.RawMessage) string {
	if secure.JSONResponseBudget(body, 128, 4096, 32768) != nil {
		return "invalid_json"
	}
	var root any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&root) != nil {
		return "invalid_json"
	}
	for pointer, raw := range assertions {
		parts, err := config.JSONPointer(pointer)
		if err != nil {
			return "invalid_json_assertion"
		}
		got := root
		for _, part := range parts {
			switch value := got.(type) {
			case map[string]any:
				var exists bool
				got, exists = value[part]
				if !exists {
					return "json_assertion_mismatch"
				}
			case []any:
				index, err := strconv.Atoi(part)
				if err != nil || index < 0 || index >= len(value) || strconv.Itoa(index) != part {
					return "json_assertion_mismatch"
				}
				got = value[index]
			default:
				return "json_assertion_mismatch"
			}
		}
		var want any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&want) != nil {
			return "invalid_json_assertion"
		}
		// Preserve number spelling: no float rounding and no unbounded exponent expansion.
		if !reflect.DeepEqual(got, want) {
			return "json_assertion_mismatch"
		}
	}
	return ""
}
