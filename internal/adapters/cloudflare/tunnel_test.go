package cloudflare

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestCollectTunnelFixture(t *testing.T) {
	o := observation()
	if err := collectTunnel(fixture(t, "tunnel-healthy.json"), tunnelOptions(), o); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"tunnel_status":            "healthy",
		"tunnel_connections":       "3",
		"tunnel_pending_reconnect": "1",
		"tunnel_colos":             "HKG,LAX,SIN",
		"oldest_connection_at":     "2026-10-19T07:59:00Z",
	}
	for key, value := range want {
		if o.Facts[key] != value {
			t.Fatalf("%s=%q want %q", key, o.Facts[key], value)
		}
	}
	if o.Outcome != "ok" {
		t.Fatalf("outcome=%s", o.Outcome)
	}
	// Identifiers, the origin address, the synthetic name and the account tag
	// never become facts.
	raw, _ := json.Marshal(o.Facts)
	for _, forbidden := range []string{"192.0.2.10", "aaaaaaaa-aaaa", "synthetic-tunnel", testAccountID, "22222222"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("%q reached the facts: %s", forbidden, raw)
		}
	}
}

func TestCollectTunnelClosedStatus(t *testing.T) {
	for _, status := range []string{"healthy", "degraded", "down", "inactive"} {
		body := []byte(`{"success":true,"result":{"status":"` + status + `","connections":[]}}`)
		o := observation()
		if err := collectTunnel(body, tunnelOptions(), o); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if o.Facts["tunnel_status"] != status || o.Outcome != "ok" {
			t.Fatalf("%s: %+v", status, o)
		}
	}
	for _, body := range []string{
		`{"success":true,"result":{"status":"mystery","connections":[]}}`,
		`{"success":true,"result":{"connections":[]}}`,
		`{"success":true,"result":null}`,
		`{"success":true}`,
	} {
		if err := collectTunnel([]byte(body), tunnelOptions(), observation()); failureCode(err) != "unsupported_response_shape" {
			t.Fatalf("%s: %v", body, err)
		}
	}
}

// The deprecated connections array is documented to start returning empty.
// Beside healthy or degraded, which mean at least one live connection by
// definition, an empty list is the field having moved: nothing is reported
// about connections and neither connection policy can fire. Beside down or
// inactive it is a count of zero, and the declared state still applies.
func TestCollectTunnelEmptyConnectionList(t *testing.T) {
	connectionFacts := []string{"tunnel_connections", "tunnel_pending_reconnect", "tunnel_colos", "connector_versions", "oldest_connection_at"}
	declared := tunnelOptions()
	declared.ExpectedConnections = intPtr(2)
	declared.ExpectedColos = []string{"HKG"}
	for _, tc := range []struct {
		status   string
		reported bool
	}{
		{"healthy", false},
		{"degraded", false},
		{"down", true},
		{"inactive", true},
	} {
		t.Run(tc.status, func(t *testing.T) {
			body := []byte(`{"success":true,"result":{"status":"` + tc.status + `","connections":[]}}`)
			plain := observation()
			if err := collectTunnel(body, tunnelOptions(), plain); err != nil {
				t.Fatal(err)
			}
			policed := observation()
			if err := collectTunnel(body, declared, policed); err != nil {
				t.Fatal(err)
			}
			for _, o := range []*model.Observation{plain, policed} {
				if o.Facts["tunnel_status"] != tc.status {
					t.Fatalf("status=%q", o.Facts["tunnel_status"])
				}
			}
			if tc.reported {
				if plain.Outcome != "ok" || plain.Facts["tunnel_connections"] != "0" || plain.Facts["tunnel_pending_reconnect"] != "0" || plain.Detail != "" {
					t.Fatalf("a listless %s tunnel is not a count of zero: %+v", tc.status, plain)
				}
				if policed.Outcome != "policy" || policed.Code != "connections_below_expected" {
					t.Fatalf("zero connections beside %s did not fall below the declared count: %+v", tc.status, policed)
				}
				return
			}
			for _, o := range []*model.Observation{plain, policed} {
				for _, key := range connectionFacts {
					if value, ok := o.Facts[key]; ok {
						t.Fatalf("%s=%q reported without a connection list", key, value)
					}
				}
			}
			if plain.Outcome != "ok" || plain.Code != "" || plain.Detail != connectionsNotReported {
				t.Fatalf("an unconfigured read should still succeed: %+v", plain)
			}
			if policed.Outcome != "unknown" || policed.Code != "connections_unreported" || policed.Detail != connectionsNotReported {
				t.Fatalf("unreported connections cannot establish a policy match: %+v", policed)
			}
		})
	}
}

// A populated array is counted exactly as before, whatever the status says.
func TestCollectTunnelPopulatedListIsCounted(t *testing.T) {
	for _, status := range []string{"healthy", "degraded", "down", "inactive"} {
		t.Run(status, func(t *testing.T) {
			body := []byte(`{"success":true,"result":{"status":"` + status + `","connections":[{"colo_name":"HKG","client_version":"2026.10.0","opened_at":"2026-10-19T08:00:00Z","is_pending_reconnect":true}]}}`)
			options := tunnelOptions()
			options.ExpectedConnections = intPtr(2)
			o := observation()
			if err := collectTunnel(body, options, o); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{
				"tunnel_connections":       "1",
				"tunnel_pending_reconnect": "1",
				"tunnel_colos":             "HKG",
				"oldest_connection_at":     "2026-10-19T08:00:00Z",
			} {
				if o.Facts[key] != value {
					t.Fatalf("%s=%q want %q", key, o.Facts[key], value)
				}
			}
			if o.Outcome != "policy" || o.Code != "connections_below_expected" || o.Detail == connectionsNotReported {
				t.Fatalf("a populated list was not compared: %+v", o)
			}
		})
	}
}

func TestCollectTunnelPolicies(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*model.CloudflareOptions)
		code   string
	}{
		{"declared connections", func(o *model.CloudflareOptions) { o.ExpectedConnections = intPtr(3) }, ""},
		{"below declared", func(o *model.CloudflareOptions) { o.ExpectedConnections = intPtr(4) }, "connections_below_expected"},
		{"declared colos", func(o *model.CloudflareOptions) { o.ExpectedColos = []string{"HKG", "LAX", "SIN"} }, ""},
		{"outside colo", func(o *model.CloudflareOptions) { o.ExpectedColos = []string{"HKG", "NRT"} }, "colo_outside_expected"},
		{"connections before colos", func(o *model.CloudflareOptions) {
			o.ExpectedConnections = intPtr(8)
			o.ExpectedColos = []string{"HKG"}
		}, "connections_below_expected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := tunnelOptions()
			tc.mutate(&options)
			o := observation()
			if err := collectTunnel(fixture(t, "tunnel-healthy.json"), options, o); err != nil {
				t.Fatal(err)
			}
			if o.Code != tc.code {
				t.Fatalf("code=%q want %q", o.Code, tc.code)
			}
			if tc.code != "" && o.Outcome != "policy" {
				t.Fatalf("outcome=%s", o.Outcome)
			}
			if tc.code == "" && o.Outcome != "ok" {
				t.Fatalf("outcome=%s", o.Outcome)
			}
		})
	}
}

func TestCollectTunnelBoundsAndShapes(t *testing.T) {
	body := `{"success":true,"result":{"status":"healthy","connections":[` +
		`{"colo_name":"HKG","client_version":"2026.10.0","opened_at":"2026-10-19T08:00:00Z"},` +
		`{"colo_name":"SIN","client_version":"2026.9.1"},` +
		`{"colo_name":"LAX","client_version":"2026.8.0"},` +
		`{"colo_name":"NRT","client_version":"2026.7.0"},` +
		`{"colo_name":"SFO","client_version":"2026.6.0"},` +
		`{"colo_name":"SEA","client_version":"2026.5.0"},` +
		`{"colo_name":"ORD","client_version":"2026.4.0"},` +
		`{"colo_name":"IAD","client_version":"2026.3.0"},` +
		`{"colo_name":"JFK","client_version":"2026.2.0"},` +
		`{"colo_name":"AMS","client_version":"2026.1.0"},` +
		`{"colo_name":"lower","client_version":"not a version"},` +
		`{"colo_name":"123","client_version":"` + strings.Repeat("9", 40) + `"}` +
		`]}}`
	o := observation()
	if err := collectTunnel([]byte(body), tunnelOptions(), o); err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(o.Facts["tunnel_colos"], ","); len(got) != MaxTunnelColos {
		t.Fatalf("colos=%v", got)
	}
	if _, ok := o.Facts["connector_versions"]; ok {
		t.Fatal("untrusted connector versions must not be exported")
	}
	if o.Facts["tunnel_connections"] != "12" {
		t.Fatalf("connections=%s", o.Facts["tunnel_connections"])
	}
	for _, fact := range []string{"tunnel_colos"} {
		if strings.Contains(o.Facts[fact], "lower") || strings.Contains(o.Facts[fact], "123") || strings.Contains(o.Facts[fact], "not a version") {
			t.Fatalf("malformed token kept: %s=%s", fact, o.Facts[fact])
		}
	}
}

func TestCollectTunnelMissingColoEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, connections, outcome, code string
	}{
		{"missing", `[{"colo_name":"HKG"},{}]`, "unknown", "connection_colo_unreported"},
		{"invalid", `[{"colo_name":"invalid"}]`, "unknown", "connection_colo_unreported"},
		{"known violation wins", `[{"colo_name":"LAX"},{}]`, "policy", "colo_outside_expected"},
		{"all allowed", `[{"colo_name":"HKG"}]`, "ok", ""},
		{"numbered with missing evidence", `[{"colo_name":"hkg12"},{}]`, "unknown", "connection_colo_unreported"},
		{"numbered violation wins", `[{"colo_name":"lax13"},{}]`, "policy", "colo_outside_expected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := tunnelOptions()
			options.ExpectedColos = []string{"HKG"}
			o := observation()
			if err := collectTunnel([]byte(`{"success":true,"result":{"status":"healthy","connections":`+tc.connections+`}}`), options, o); err != nil {
				t.Fatal(err)
			}
			if o.Outcome != tc.outcome || o.Code != tc.code {
				t.Fatalf("outcome=%s code=%s", o.Outcome, o.Code)
			}
		})
	}
}

// The policy compares every distinct reported colo, including one beyond the
// eight the fact can carry: a ninth colo that sorts last must still be named
// as outside the declared list.
func TestCollectTunnelColoPolicyUsesFullSet(t *testing.T) {
	const connections = `[{"colo_name":"HKG"},{"colo_name":"IAD"},{"colo_name":"JFK"},{"colo_name":"LAX"},{"colo_name":"NRT"},{"colo_name":"ORD"},{"colo_name":"SEA"},{"colo_name":"SFO"},{"colo_name":"SIN"}]`
	body := `{"success":true,"result":{"status":"healthy","connections":` + connections + `}}`
	options := tunnelOptions()
	options.ExpectedColos = []string{"HKG", "IAD", "JFK", "LAX", "NRT", "ORD", "SEA", "SFO"}
	o := observation()
	if err := collectTunnel([]byte(body), options, o); err != nil {
		t.Fatal(err)
	}
	if o.Outcome != "policy" || o.Code != "colo_outside_expected" {
		t.Fatalf("ninth colo was not compared: %+v", o)
	}
	if got := strings.Split(o.Facts["tunnel_colos"], ","); len(got) != MaxTunnelColos || strings.Contains(o.Facts["tunnel_colos"], "SIN") {
		t.Fatalf("fact is not the capped list: %q", o.Facts["tunnel_colos"])
	}
}

func TestTunnelStatusExpectationPrecedesMissingConnections(t *testing.T) {
	for _, status := range []string{"healthy", "degraded", "down", "inactive"} {
		options := tunnelOptions()
		options.ExpectedStatus = "healthy"
		o := observation()
		err := collectTunnel([]byte(`{"success":true,"result":{"status":"`+status+`","connections":[]}}`), options, o)
		if err != nil {
			t.Fatal(err)
		}
		if status == "healthy" {
			if o.Outcome != "ok" {
				t.Fatal(o)
			}
		} else if o.Outcome != "policy" || o.Code != "tunnel_status_mismatch" {
			t.Fatal(o)
		}
	}
	options := tunnelOptions()
	options.ExpectedStatus = "healthy"
	count := 4
	options.ExpectedConnections = &count
	o := observation()
	if err := collectTunnel([]byte(`{"success":true,"result":{"status":"degraded","connections":[]}}`), options, o); err != nil || o.Code != "tunnel_status_mismatch" {
		t.Fatal(err, o)
	}
	for _, read := range []string{ReadTunnelConnections, ReadDNSRecord, ReadPool} {
		options.Read = read
		if Validate(options) == nil {
			t.Fatal("status expectation accepted for", read)
		}
	}
	options.Read = ReadTunnel
	options.ExpectedStatus = "unknown"
	if Validate(options) == nil {
		t.Fatal("unknown status accepted")
	}
}
