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
	if x.TLSMinDays > 0 {
		if x.Kind == "tls" {
			return nil
		}
		u, err := url.Parse(Target(c, x))
		if x.Kind != "http" || err != nil || u.Scheme != "https" {
			return errors.New("tls_min_days requires a TLS or HTTPS application check")
		}
	}
	return nil
}
