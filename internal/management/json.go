// Package management implements the separately authorized, opt-in control plane.
// Observation credentials and viewer APIs are never management capabilities.
package management

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// Decode rejects duplicate keys as well as unknown fields and trailing values.
// Signature verification operates on the original bytes, never a reserialization.
func Decode(b []byte, v any, limit int) error {
	if !utf8.Valid(b) {
		return errors.New("management_invalid_utf8")
	}
	if len(b) > limit {
		return errors.New("management_payload_too_large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 32 {
			return errors.New("management_json_depth")
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		if x, ok := t.(json.Delim); ok {
			switch x {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					s, ok := k.(string)
					if !ok || keys[s] {
						return errors.New("management_duplicate_key")
					}
					keys[s] = true
					if e = visit(depth + 1); e != nil {
						return e
					}
				}
				t, e = d.Token()
				if e != nil || t != json.Delim('}') {
					return errors.New("management_invalid_object")
				}
			case '[':
				for d.More() {
					if e = visit(depth + 1); e != nil {
						return e
					}
				}
				t, e = d.Token()
				if e != nil || t != json.Delim(']') {
					return errors.New("management_invalid_array")
				}
			default:
				return errors.New("management_invalid_json")
			}
		}
		return nil
	}
	if e := visit(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errors.New("management_trailing_json")
	}
	if e := exactFields(b, v); e != nil {
		return e
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
