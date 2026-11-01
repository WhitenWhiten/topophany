package config

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"strings"
)

// JSONPointer returns decoded RFC 6901 tokens. Empty selects the whole value.
func JSONPointer(pointer string) ([]string, error) {
	if len(pointer) > 256 || (pointer != "" && pointer[0] != '/') {
		return nil, errors.New("invalid JSON assertion pointer")
	}
	if pointer == "" {
		return nil, nil
	}
	parts := strings.Split(pointer[1:], "/")
	if len(parts) > 16 {
		return nil, errors.New("JSON assertion pointer too deep")
	}
	for i, p := range parts {
		for j := 0; j < len(p); j++ {
			if p[j] == '~' {
				if j+1 == len(p) || (p[j+1] != '0' && p[j+1] != '1') {
					return nil, errors.New("invalid JSON assertion escape")
				}
				j++
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func ValidateJSONAssertions(x model.Check) error {
	if len(x.HTTP.JSONEquals) == 0 {
		return nil
	}
	if x.Kind != "http" || len(x.HTTP.JSONEquals) > 32 {
		return errors.New("json_equals requires HTTP and at most 32 assertions")
	}
	total := 0
	for pointer, want := range x.HTTP.JSONEquals {
		if _, err := JSONPointer(pointer); err != nil {
			return err
		}
		total += len(want)
		if total > 16384 || secure.JSONResponseBudget(want, 128, 256, 512) != nil {
			return errors.New("invalid or oversized JSON assertion value")
		}
	}
	return nil
}
