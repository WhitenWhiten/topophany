package cloudflare

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Every parser of an untrusted response body has a bounded fuzz target. The
// parser may keep nothing from arbitrary input, but it must never panic,
// exceed the fact budget or keep control characters.
func checkParsedFacts(t *testing.T, o *model.Observation) {
	t.Helper()
	if len(o.Facts) > model.MaxObservationFacts {
		t.Fatalf("facts=%d", len(o.Facts))
	}
	for key, value := range o.Facts {
		if len(key) > 128 || len(value) > 2048 || secure.Text(key) != key || secure.Text(value) != value {
			t.Fatalf("unsafe fact %q=%q", key, value)
		}
	}
}

func FuzzParseTunnel(f *testing.F) {
	f.Add([]byte(`{"success":true,"result":{"status":"healthy","connections":[]}}`))
	if body, err := os.ReadFile(filepath.Join(fixtureDir, "tunnel-healthy.json")); err == nil {
		f.Add(body)
	}
	if body, err := os.ReadFile(filepath.Join(liveFixtureDir, "tunnel.json")); err == nil {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		o := observation()
		_ = collectTunnel(data, tunnelOptions(), o)
		checkParsedFacts(t, o)
	})
}

func FuzzParseDNSRecord(f *testing.F) {
	f.Add([]byte(`{"success":true,"result":[]}`))
	if body, err := os.ReadFile(filepath.Join(fixtureDir, "dns-records.json")); err == nil {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		o := observation()
		_ = collectDNSRecord(data, dnsOptions(), o)
		checkParsedFacts(t, o)
	})
}

func FuzzParsePoolHealth(f *testing.F) {
	f.Add([]byte(`{"success":true,"result":{"pop_health":{"healthy":true,"origins":[]}}}`))
	if body, err := os.ReadFile(filepath.Join(fixtureDir, "pool-health.json")); err == nil {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		o := observation()
		_ = collectPool(data, poolOptions(), o)
		checkParsedFacts(t, o)
	})
}
