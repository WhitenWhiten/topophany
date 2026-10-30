package cloudflare

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

const (
	fixtureDir = "testdata/api-v4/2026-10-19"

	testAccountID  = "11111111111111111111111111111111"
	testZoneID     = "33333333333333333333333333333333"
	testTunnelID   = "1bedc50d-42b3-473c-b108-ff3d10c0d925"
	testPoolID     = "44444444444444444444444444444444"
	legacyTunnelID = "22222222222222222222222222222222"
)

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

func tunnelOptions() model.CloudflareOptions {
	return model.CloudflareOptions{Read: ReadTunnel, AccountID: testAccountID, TunnelID: testTunnelID}
}

func dnsOptions() model.CloudflareOptions {
	return model.CloudflareOptions{Read: ReadDNSRecord, ZoneID: testZoneID, RecordName: "www.example.com", RecordType: "A"}
}

func poolOptions() model.CloudflareOptions {
	return model.CloudflareOptions{Read: ReadPool, AccountID: testAccountID, PoolID: testPoolID}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func observation() *model.Observation {
	return &model.Observation{Outcome: "unknown", Facts: map[string]string{}}
}

func TestValidateOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options model.CloudflareOptions
		valid   bool
	}{
		{"tunnel", tunnelOptions(), true},
		{"unknown read", model.CloudflareOptions{Read: "zones"}, false},
		{"no read", model.CloudflareOptions{}, false},
		{"tunnel missing account", model.CloudflareOptions{Read: ReadTunnel, TunnelID: testTunnelID}, false},
		{"tunnel missing tunnel", model.CloudflareOptions{Read: ReadTunnel, AccountID: testAccountID}, false},
		{"tunnel short id", model.CloudflareOptions{Read: ReadTunnel, AccountID: "abc", TunnelID: testTunnelID}, false},
		{"tunnel UUID account", model.CloudflareOptions{Read: ReadTunnel, AccountID: testTunnelID, TunnelID: testTunnelID}, false},
		{"tunnel non-hex id", model.CloudflareOptions{Read: ReadTunnel, AccountID: testAccountID, TunnelID: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"}, false},
		{"tunnel with zone", func() model.CloudflareOptions {
			o := tunnelOptions()
			o.ZoneID = testZoneID
			return o
		}(), false},
		{"tunnel expected connections", func() model.CloudflareOptions {
			o := tunnelOptions()
			o.ExpectedConnections = intPtr(4)
			o.ExpectedColos = []string{"HKG", "NRT"}
			return o
		}(), true},
		{"tunnel zero connections", func() model.CloudflareOptions {
			o := tunnelOptions()
			o.ExpectedConnections = intPtr(0)
			return o
		}(), false},
		{"tunnel too many connections", func() model.CloudflareOptions {
			o := tunnelOptions()
			o.ExpectedConnections = intPtr(MaxExpectedConnections + 1)
			return o
		}(), false},
		{"tunnel lowercase colo", func() model.CloudflareOptions {
			o := tunnelOptions()
			o.ExpectedColos = []string{"hkg"}
			return o
		}(), false},
		{"tunnel duplicate colo", func() model.CloudflareOptions {
			o := tunnelOptions()
			o.ExpectedColos = []string{"HKG", "HKG"}
			return o
		}(), false},
		{"dns A", dnsOptions(), true},
		{"dns AAAA", func() model.CloudflareOptions { o := dnsOptions(); o.RecordType = "AAAA"; return o }(), true},
		{"dns CNAME", func() model.CloudflareOptions { o := dnsOptions(); o.RecordType = "CNAME"; return o }(), true},
		{"dns MX", func() model.CloudflareOptions { o := dnsOptions(); o.RecordType = "MX"; return o }(), false},
		{"dns missing zone", model.CloudflareOptions{Read: ReadDNSRecord, RecordName: "www.example.com", RecordType: "A"}, false},
		{"dns UUID zone", func() model.CloudflareOptions { o := dnsOptions(); o.ZoneID = testTunnelID; return o }(), false},
		{"dns bad name", func() model.CloudflareOptions { o := dnsOptions(); o.RecordName = "www example.com"; return o }(), false},
		{"dns wildcard name", func() model.CloudflareOptions { o := dnsOptions(); o.RecordName = "*.example.com"; return o }(), true},
		{"dns with account", func() model.CloudflareOptions { o := dnsOptions(); o.AccountID = testAccountID; return o }(), false},
		{"dns expected proxied", func() model.CloudflareOptions { o := dnsOptions(); o.ExpectedProxied = boolPtr(true); return o }(), true},
		{"dns expected content", func() model.CloudflareOptions { o := dnsOptions(); o.ExpectedContent = "198.51.100.7"; return o }(), true},
		{"dns control content", func() model.CloudflareOptions { o := dnsOptions(); o.ExpectedContent = "198.51.100.7\nX"; return o }(), false},
		{"pool", poolOptions(), true},
		{"pool UUID account", func() model.CloudflareOptions { o := poolOptions(); o.AccountID = testTunnelID; return o }(), false},
		{"pool UUID resource", func() model.CloudflareOptions { o := poolOptions(); o.PoolID = testTunnelID; return o }(), false},
		{"pool expected origins", func() model.CloudflareOptions {
			o := poolOptions()
			o.ExpectedOrigins = []string{"192.0.2.10", "2001:db8::10", "origin.example.com"}
			return o
		}(), true},
		{"pool duplicate origin", func() model.CloudflareOptions {
			o := poolOptions()
			o.ExpectedOrigins = []string{"2001:db8::10", "2001:0db8::10"}
			return o
		}(), false},
		{"pool duplicate name case", func() model.CloudflareOptions {
			o := poolOptions()
			o.ExpectedOrigins = []string{"Origin.Example.com", "origin.example.com"}
			return o
		}(), false},
		{"pool too many origins", func() model.CloudflareOptions {
			o := poolOptions()
			for i := 0; i < MaxExpectedOrigins+1; i++ {
				o.ExpectedOrigins = append(o.ExpectedOrigins, fmt.Sprintf("192.0.2.%d", i+1))
			}
			return o
		}(), false},
		{"pool with expectations", func() model.CloudflareOptions {
			o := poolOptions()
			o.ExpectedConnections = intPtr(2)
			return o
		}(), false},
		{"pool bad origin", func() model.CloudflareOptions {
			o := poolOptions()
			o.ExpectedOrigins = []string{"192.0.2.10/x"}
			return o
		}(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.options); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
}

func TestValidateTunnelIDs(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		valid    bool
	}{
		{"UUID lowercase", testTunnelID, true},
		{"UUID uppercase", strings.ToUpper(testTunnelID), true},
		{"legacy compact", legacyTunnelID, true},
		{"legacy compact uppercase", "ABCDEF0123456789ABCDEF0123456789", true},
		{"empty", "", false},
		{"short", testTunnelID[:len(testTunnelID)-1], false},
		{"long", testTunnelID + "0", false},
		{"wrong groups", "1bedc50d4-2b3-473c-b108-ff3d10c0d925", false},
		{"non hex", "g" + testTunnelID[1:], false},
		{"leading space", " " + testTunnelID, false},
		{"trailing space", testTunnelID + " ", false},
		{"newline", testTunnelID + "\n", false},
		{"braces", "{" + testTunnelID + "}", false},
		{"URN", "urn:uuid:" + testTunnelID, false},
		{"encoded hyphens", strings.ReplaceAll(testTunnelID, "-", "%2D"), false},
		{"unicode hyphen", strings.ReplaceAll(testTunnelID, "-", "‐"), false},
		{"path", testTunnelID + "/connections", false},
		{"traversal", "../" + testTunnelID, false},
		{"encoded traversal", "%2e%2e%2f" + testTunnelID, false},
		{"backslash", testTunnelID + "\\connections", false},
		{"query", testTunnelID + "?x=1", false},
		{"fragment", testTunnelID + "#x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := tunnelOptions()
			o.TunnelID = tc.id
			if err := Validate(o); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
}

func TestValidateTarget(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		valid        bool
	}{
		{"default", "https://api.cloudflare.com/client/v4", true},
		{"trailing slash", "https://api.cloudflare.com/client/v4/", true},
		{"default port", "https://api.cloudflare.com:443/client/v4", true},
		{"host only", "https://api.cloudflare.com", false},
		{"wrong host", "https://api.example.com/client/v4", false},
		{"lookalike host", "https://api.cloudflare.com.example/client/v4", false},
		{"wrong path", "https://api.cloudflare.com/client/v5", false},
		{"extra path", "https://api.cloudflare.com/client/v4/extra", false},
		{"port too large", "https://api.cloudflare.com:70000/client/v4", false},
		{"port zero", "https://api.cloudflare.com:0/client/v4", false},
		{"loopback http", "http://127.0.0.1:8080/client/v4", true},
		{"loopback https", "https://127.0.0.1:8080/client/v4", true},
		{"ipv6 loopback http", "http://[::1]:8080/client/v4", true},
		{"localhost http", "http://localhost:8080", false},
		{"remote http", "http://192.0.2.1/client/v4", false},
		{"ftp", "ftp://api.cloudflare.com/client/v4", false},
		{"userinfo", "https://user@api.cloudflare.com/client/v4", false},
		{"query", "https://api.cloudflare.com/client/v4?x=1", false},
		{"fragment", "https://api.cloudflare.com/client/v4#x", false},
		{"traversal", "https://api.cloudflare.com/client/v4/../x", false},
		{"backslash", `https://api.cloudflare.com/client\v4`, false},
		{"relative", "/client/v4", false},
		{"empty", "", false},
		{"control", "https://api.cloudflare.com/client/v4\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTarget(tc.target); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
}

func TestValidDNSName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"www.example.com", true},
		{"example.com.", true},
		{"_dmarc.example.com", true},
		{"*.example.com", true},
		{"a", true},
		{"", false},
		{".", false},
		{"www..example.com", false},
		{"www.example.com..", false},
		{"-bad.example.com", true}, // shape only; Cloudflare decides label rules
		{"www example.com", false},
		{"www.example.com/path", false},
		{"例え.example.com", false},
	} {
		if got := validDNSName(tc.name); got != tc.valid {
			t.Fatalf("%q: valid=%v want %v", tc.name, got, tc.valid)
		}
	}
}
