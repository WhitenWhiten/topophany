package config_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func traceConfig() model.Config {
	c := testkit.Config()
	c.Checks[0].HTTP.ExitFormat = "trace"
	return c
}

// A trace expectation is bounded to Cloudflare's trace vocabularies: the WARP
// and Gateway words, and at most sixteen distinct three-letter colo codes. It
// requires exit_format trace, because only that format parses a trace body.
func TestExpectedTraceValidation(t *testing.T) {
	colos := func(n int) []string {
		all := []string{"HKG", "NRT", "LAX", "SFO", "SEA", "ORD", "IAD", "JFK", "LHR", "FRA", "AMS", "CDG", "SIN", "SYD", "GRU", "JNB", "HND"}
		return append([]string{}, all[:n]...)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*model.Config)
		valid  bool
	}{
		{"unset", func(*model.Config) {}, true},
		{"empty", func(c *model.Config) { c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{} }, false},
		{"warp off", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "off"}
		}, true},
		{"warp on", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "on"}
		}, true},
		{"warp plus", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "plus"}
		}, true},
		{"unknown warp", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "maybe"}
		}, false},
		{"uppercase warp", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "ON"}
		}, false},
		{"gateway on", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Gateway: "on"}
		}, true},
		{"gateway off", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Gateway: "off"}
		}, true},
		{"gateway plus", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Gateway: "plus"}
		}, false},
		{"sixteen colos", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Colos: colos(16)}
		}, true},
		{"seventeen colos", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Colos: colos(17)}
		}, false},
		{"lowercase colo", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Colos: []string{"hkg"}}
		}, false},
		{"two-letter colo", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Colos: []string{"HK"}}
		}, false},
		{"four-letter colo", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Colos: []string{"HKGG"}}
		}, false},
		{"colo with a space", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Colos: []string{"H KG"}}
		}, false},
		{"duplicate colos", func(c *model.Config) {
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Colos: []string{"HKG", "HKG"}}
		}, false},
		{"requires trace format", func(c *model.Config) {
			c.Checks[0].HTTP.ExitFormat = ""
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "on"}
		}, false},
		{"plain format refused", func(c *model.Config) {
			c.Checks[0].HTTP.ExitFormat = "plain"
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "on"}
		}, false},
		{"json format refused", func(c *model.Config) {
			c.Checks[0].HTTP.ExitFormat = "json"
			c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "on"}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := traceConfig()
			tc.mutate(&c)
			if err := config.Validate(c); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
	c := traceConfig()
	c.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "must declare warp, gateway or colos") {
		t.Fatalf("empty expectation error is %v", err)
	}
}

// expected_trace is inside HTTPOptions, so it is part of the measurement
// contract: unset it writes nothing and leaves a default configuration's
// contracts unchanged, and set it gives that check a new contract. It is not a
// secret, so redaction keeps it while it does not weaken the private fields
// around it.
func TestExpectedTraceContractAndDefaultJSON(t *testing.T) {
	c := testkit.Config()
	before := config.Contracts(c)
	config.Defaults(&c)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"expected_trace"`) {
		t.Fatal("default config carries expected_trace")
	}
	if !maps.Equal(config.Contracts(c), before) {
		t.Fatal("unset expected_trace changed a contract")
	}

	traced := testkit.Config()
	traced.Checks[0].HTTP.ExitFormat = "trace"
	base := config.Contracts(traced)[traced.Checks[0].ID]
	if base == before[traced.Checks[0].ID] {
		t.Fatal("exit_format is not in the measurement contract")
	}
	traced.Checks[0].HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "on", Gateway: "off", Colos: []string{"HKG", "NRT"}}
	if config.Contracts(traced)[traced.Checks[0].ID] == base {
		t.Fatal("expected_trace did not change the check's contract")
	}
	if config.Validate(traced) != nil {
		t.Fatal("valid expected_trace refused")
	}
	public := config.Public(traced).Checks[0].HTTP.ExpectedTrace
	if public == nil || public.Warp != "on" || len(public.Colos) != 2 {
		t.Fatalf("public topology lost expected_trace: %#v", public)
	}
	clone := config.Clone(traced)
	clone.Checks[0].HTTP.ExpectedTrace.Colos[0] = "LAX"
	if traced.Checks[0].HTTP.ExpectedTrace.Colos[0] != "HKG" {
		t.Fatal("clone shares the expected trace colos")
	}
}

// optedInExample names the reviewed worked examples that opt into fields added
// for Cloudflare: examples/cloudflare.json exercises expected_pops,
// expected_trace, request_header_envs, adapter.cloudflared and the documented
// adapter.cloudflare reads, and examples/products.json sends Supabase's anon
// key as a header from the worker's environment. Every older example predates
// them and must stay measured as it was.
func optedInExample(path string) bool {
	switch filepath.Base(path) {
	case "cloudflare.json", "cloudflare-diagnostics.json", "mihomo-diagnostics.json", "products.json":
		return true
	default:
		return false
	}
}

// Every topology shipped in examples/ except the Cloudflare worked example
// was written before expected_trace and request_header_envs, so none of those
// may carry either: their default contracts stay what they were, and the
// upgrade order in the reference is the only way to opt in.
func exampleTopologies(t *testing.T) map[string]model.Config {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("examples not found: %v", err)
	}
	out := map[string]model.Config{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]json.RawMessage
		if json.Unmarshal(raw, &keys) != nil {
			continue // credentials and monitor examples are not topologies
		}
		if _, ok := keys["checks"]; !ok {
			continue
		}
		c, err := config.Load(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out[path] = c
	}
	if len(out) == 0 {
		t.Fatal("no example topologies found")
	}
	return out
}

func TestExampleTopologiesCarryNoExpectedTrace(t *testing.T) {
	for path, c := range exampleTopologies(t) {
		if optedInExample(path) {
			continue
		}
		out, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), `"expected_trace"`) {
			t.Fatalf("%s carries expected_trace", path)
		}
	}
}

func TestExampleTopologiesCarryNoRequestHeaderEnvs(t *testing.T) {
	for path, c := range exampleTopologies(t) {
		if optedInExample(path) {
			continue
		}
		out, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), `"request_header_envs"`) {
			t.Fatalf("%s carries request_header_envs", path)
		}
	}
}
