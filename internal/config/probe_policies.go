package config

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/model"
	"net/url"
)

func ValidateProbePolicies(c model.Config, x model.Check) error {
	if x.TLSMinDays < 0 || x.TLSMinDays > 3650 {
		return errors.New("tls_min_days must be 0..3650")
	}
	if x.TLSMinDays > 0 && x.Kind != "tls" {
		u, err := url.Parse(Target(c, x))
		if x.Kind != "http" || err != nil || u.Scheme != "https" {
			return errors.New("tls_min_days requires a TLS or HTTPS application check")
		}
	}
	if x.MaxDurationMS < 0 || x.MaxDurationMS > 300000 {
		return errors.New("max_duration_ms must be 0..300000")
	}
	if x.MaxDurationMS > 0 {
		switch x.Kind {
		case "http", "tcp", "tls", "ssh-banner", "dns", "udp-echo", "icmp":
		default:
			return errors.New("max_duration_ms requires a measured network check")
		}
	}
	return ValidateJSONAssertions(x)
}
