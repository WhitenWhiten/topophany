package config_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func cloudflareConfig(options *model.CloudflareOptions) model.Config {
	c := testkit.Config()
	x := &c.Checks[0]
	x.Kind, x.Target, x.Endpoint, x.Path, x.Service = "cloudflare", "", "", "", ""
	x.HTTP = model.HTTPOptions{}
	x.Adapter.TokenEnv = "CF_API_TOKEN"
	x.Adapter.Cloudflare = options
	return c
}

func cloudflareTunnelOptions() *model.CloudflareOptions {
	return &model.CloudflareOptions{
		Read:      "tunnel",
		AccountID: "11111111111111111111111111111111",
		TunnelID:  "22222222222222222222222222222222",
	}
}

func TestCloudflareValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.Config, *model.Check)
		valid  bool
	}{
		{"default target", func(*model.Config, *model.Check) {}, true},
		{"loopback target", func(_ *model.Config, x *model.Check) { x.Target = "http://127.0.0.1:8080/client/v4" }, true},
		{"remote http", func(_ *model.Config, x *model.Check) { x.Target = "http://192.0.2.1/client/v4" }, false},
		{"local https", func(_ *model.Config, x *model.Check) { x.Target = "https://api.cloudflare.com/client/v4" }, true},
		{"endpoint", func(_ *model.Config, x *model.Check) { x.Endpoint = "dmit-http" }, false},
		{"interval below minimum", func(_ *model.Config, x *model.Check) { x.IntervalSeconds = 59 }, false},
		{"interval at minimum", func(_ *model.Config, x *model.Check) { x.IntervalSeconds = 60 }, true},
		{"missing token env", func(_ *model.Config, x *model.Check) { x.Adapter.TokenEnv = "" }, false},
		{"invalid token env", func(_ *model.Config, x *model.Check) { x.Adapter.TokenEnv = "CF TOKEN" }, false},
		{"bind ip", func(_ *model.Config, x *model.Check) { x.BindIP = "127.0.0.1" }, true},
		{"resolve ip", func(_ *model.Config, x *model.Check) { x.ResolveIP = "127.0.0.1" }, true},
		{"path without proxies", func(_ *model.Config, x *model.Check) { x.Path = "direct" }, true},
		{"path with proxies", func(c *model.Config, x *model.Check) {
			c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:1080"}}
			x.Path = "direct"
		}, true},
		{"loopback http through a proxy", func(c *model.Config, x *model.Check) {
			c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:1080"}}
			x.Path = "direct"
			x.Target = "http://127.0.0.1:8080/client/v4"
		}, false},
		{"https through a proxy", func(c *model.Config, x *model.Check) {
			c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:1080"}}
			x.Path = "direct"
		}, true},
		{"loopback http with a remote resolve override", func(_ *model.Config, x *model.Check) {
			x.Target = "http://127.0.0.1:8080/client/v4"
			x.ResolveIP = "192.0.2.1"
		}, false},
		{"invalid options", func(_ *model.Config, x *model.Check) {
			x.Adapter.Cloudflare = &model.CloudflareOptions{Read: "tunnel", AccountID: "short", TunnelID: "22222222222222222222222222222222"}
		}, false},
		{"invalid dns record", func(_ *model.Config, x *model.Check) {
			x.Adapter.Cloudflare = &model.CloudflareOptions{Read: "dns_record", ZoneID: "33333333333333333333333333333333", RecordName: "www.example.com", RecordType: "MX"}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cloudflareConfig(cloudflareTunnelOptions())
			tc.mutate(&c, &c.Checks[0])
			if err := config.Validate(c); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
	// The default target is the documented v4 base, and it is what the
	// contract measures.
	c := cloudflareConfig(cloudflareTunnelOptions())
	if got := config.Target(c, c.Checks[0]); got != "https://api.cloudflare.com/client/v4" {
		t.Fatalf("default target %q", got)
	}
}

// Validation and JSON decoding must preserve both UUID and legacy ID spelling.
// The frozen probe.TestAdapterCompatibility fixture pins the legacy contract.
func TestCloudflareTunnelIDRoundTripAndContract(t *testing.T) {
	for _, id := range []string{
		"1bedc50d-42b3-473c-b108-ff3d10c0d925",
		"1BEDC50D-42B3-473C-B108-FF3D10C0D925",
		"22222222222222222222222222222222",
	} {
		t.Run(id, func(t *testing.T) {
			o := cloudflareTunnelOptions()
			o.TunnelID = id
			c := cloudflareConfig(o)
			before := config.Contract(c, c.Checks[0])
			if err := config.Validate(c); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			back, err := config.Decode(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			if back.Checks[0].Adapter.Cloudflare.TunnelID != id || config.Contract(back, back.Checks[0]) != before {
				t.Fatal("validation or JSON decoding changed the tunnel ID or contract")
			}
			public := config.Public(back)
			if err := config.ValidatePublic(public); err != nil {
				t.Fatal(err)
			}
			if public.Checks[0].Adapter.Cloudflare.TunnelID != id {
				t.Fatal("public topology changed the tunnel ID")
			}
		})
	}
}

// The nested options are part of the measurement contract: setting or
// changing them changes that check's contract and no other, they are not
// secrets, and clone and the typed view detach every pointer and slice. An
// unset field is covered by the example topologies below: every shipped
// topology predates adapter.cloudflare and its contract hashes stay put.
func TestCloudflareContractCloneAndJSON(t *testing.T) {
	c := testkit.Config()
	before := config.Contracts(c)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"cloudflare":{`) {
		t.Fatal("a configuration without the field carries cloudflare options")
	}
	var back model.Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(before, config.Contracts(back)) {
		t.Fatal("an unset cloudflare field changed a contract")
	}

	plain := cloudflareConfig(cloudflareTunnelOptions())
	if err := config.Validate(plain); err != nil {
		t.Fatal(err)
	}
	base := config.Contracts(plain)[plain.Checks[0].ID]
	declared := cloudflareConfig(&model.CloudflareOptions{
		Read:                "tunnel",
		AccountID:           "11111111111111111111111111111111",
		TunnelID:            "22222222222222222222222222222222",
		ExpectedConnections: intPointer(4),
		ExpectedColos:       []string{"HKG", "NRT"},
	})
	if err := config.Validate(declared); err != nil {
		t.Fatal(err)
	}
	if config.Contracts(declared)[declared.Checks[0].ID] == base {
		t.Fatal("cloudflare options did not change the contract")
	}
	for id, hash := range config.Contracts(plain) {
		if id != declared.Checks[0].ID && config.Contracts(declared)[id] != hash {
			t.Fatalf("%s contract changed", id)
		}
	}
	public := config.Public(declared)
	if public.Checks[0].Adapter.TokenEnv != "" {
		t.Fatal("public topology kept token_env")
	}
	if err := config.ValidatePublic(public); err != nil {
		t.Fatalf("public snapshot of a cloudflare API read was refused: %v", err)
	}
	view := public.Checks[0].Adapter.Cloudflare
	if view == nil || view.ExpectedConnections == nil || *view.ExpectedConnections != 4 || len(view.ExpectedColos) != 2 || view.AccountID != "11111111111111111111111111111111" {
		t.Fatalf("public topology lost cloudflare options: %+v", view)
	}

	copy := config.Clone(declared)
	*copy.Checks[0].Adapter.Cloudflare.ExpectedConnections = 2
	copy.Checks[0].Adapter.Cloudflare.ExpectedColos[0] = "LAX"
	if *declared.Checks[0].Adapter.Cloudflare.ExpectedConnections != 4 || declared.Checks[0].Adapter.Cloudflare.ExpectedColos[0] != "HKG" {
		t.Fatal("clone aliases cloudflare options")
	}
	typed := declared.Checks[0].Adapter.CloudflareOptions()
	*typed.ExpectedConnections, typed.ExpectedColos[0] = 2, "LAX"
	if *declared.Checks[0].Adapter.Cloudflare.ExpectedConnections != 4 || declared.Checks[0].Adapter.Cloudflare.ExpectedColos[0] != "HKG" {
		t.Fatal("typed view aliases the adapter")
	}

	// The DNS read carries a pointer and a content expectation that are part
	// of the contract and are not secret.
	proxied := true
	plainDNS := cloudflareConfig(&model.CloudflareOptions{
		Read: "dns_record", ZoneID: "33333333333333333333333333333333",
		RecordName: "www.example.com", RecordType: "A",
	})
	if err := config.Validate(plainDNS); err != nil {
		t.Fatal(err)
	}
	dns := cloudflareConfig(&model.CloudflareOptions{
		Read: "dns_record", ZoneID: "33333333333333333333333333333333",
		RecordName: "www.example.com", RecordType: "A",
		ExpectedProxied: &proxied, ExpectedContent: "198.51.100.7",
	})
	if err := config.Validate(dns); err != nil {
		t.Fatal(err)
	}
	if config.Contracts(dns)[dns.Checks[0].ID] == config.Contracts(plainDNS)[plainDNS.Checks[0].ID] {
		t.Fatal("dns expectations did not change the contract")
	}
	cloned := config.Clone(dns)
	*cloned.Checks[0].Adapter.Cloudflare.ExpectedProxied = false
	if *dns.Checks[0].Adapter.Cloudflare.ExpectedProxied != true {
		t.Fatal("clone aliases expected_proxied")
	}

	// The pool read's origin slice is cloned and detached from the typed view.
	pool := cloudflareConfig(&model.CloudflareOptions{
		Read: "pool", AccountID: "11111111111111111111111111111111", PoolID: "44444444444444444444444444444444",
		ExpectedOrigins: []string{"192.0.2.10", "2001:db8::10"},
	})
	if err := config.Validate(pool); err != nil {
		t.Fatal(err)
	}
	clonedPool := config.Clone(pool)
	clonedPool.Checks[0].Adapter.Cloudflare.ExpectedOrigins[0] = "192.0.2.99"
	if pool.Checks[0].Adapter.Cloudflare.ExpectedOrigins[0] != "192.0.2.10" {
		t.Fatal("clone aliases expected_origins")
	}
	typedPool := pool.Checks[0].Adapter.CloudflareOptions()
	typedPool.ExpectedOrigins[0] = "192.0.2.99"
	if pool.Checks[0].Adapter.Cloudflare.ExpectedOrigins[0] != "192.0.2.10" {
		t.Fatal("typed view aliases expected_origins")
	}
}

// Every topology shipped in examples/ predates adapter.cloudflare, so none may
// carry it, not even as a null. Unset, the field writes nothing and leaves
// every contract hash unchanged through a JSON round trip. The Cloudflare
// worked example names the three API reads in its notes, which is text, not a
// configured adapter; it still carries no active cloudflare read.
func TestExampleTopologiesCarryNoCloudflare(t *testing.T) {
	for path, c := range exampleTopologies(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if containsKey(document, "cloudflare") {
			t.Fatalf("%s carries adapter.cloudflare", path)
		}
		before := config.Contracts(c)
		out, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var back model.Config
		if err := json.Unmarshal(out, &back); err != nil {
			t.Fatal(err)
		}
		if !mapsEqual(before, config.Contracts(back)) {
			t.Fatalf("%s: unset cloudflare changed a contract", path)
		}
	}
}

func intPointer(v int) *int { return &v }
