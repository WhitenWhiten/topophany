package config_test

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"testing"
)

func TestCertificatePolicyContractAndKind(t *testing.T) {
	c := testkit.Config()
	x := c.Checks[0]
	x.Kind = "http"
	x.Target = "https://example.invalid/health"
	before := config.Contract(c, x)
	x.TLSMinDays = 14
	if config.ValidateProbePolicies(c, x) != nil || config.Contract(c, x) == before {
		t.Fatal("expiry policy missing from contract")
	}
	x.Target = "http://example.invalid/health"
	if config.ValidateProbePolicies(c, x) == nil {
		t.Fatal("expiry policy on plaintext")
	}
	x.TLSMinDays = -1
	if config.ValidateProbePolicies(c, x) == nil {
		t.Fatal("negative policy")
	}
}
