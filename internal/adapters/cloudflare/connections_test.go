package cloudflare

import (
	"net/url"
	"testing"
)

func TestDedicatedConnections(t *testing.T) {
	options := tunnelOptions()
	options.Read = ReadTunnelConnections
	want := 3
	options.ExpectedConnections = &want
	if err := Validate(options); err != nil {
		t.Fatal(err)
	}
	request, _ := readFor(options)
	if !request.allowed() || request.path != "/accounts/"+testAccountID+"/cfd_tunnel/"+testTunnelID+"/connections" {
		t.Fatal(request)
	}
	request.query = url.Values{"page": {"2"}}
	if request.allowed() {
		t.Fatal("unexpected query allowed")
	}
	for _, tc := range []struct{ body, outcome, count, code string }{
		{`{"success":true,"result":[{"conns":[{"colo_name":"HKG"},{"colo_name":"SIN"}]},{"conns":[{"colo_name":"LAX"}]}]}`, "ok", "3", ""},
		{`{"success":true,"result":[]}`, "policy", "0", ""},
		{`{"success":true,"result":[{"conns":[]}]}`, "policy", "0", ""},
		{`{"success":true,"result":[{}]}`, "", "", "unsupported_response_shape"},
		{`{"success":true,"result":null}`, "", "", "unsupported_response_shape"},
		{`{"success":true,"result":[],"result_info":{"total_count":2}}`, "", "", "unsupported_response_shape"},
	} {
		o := observation()
		err := collectTunnelConnections([]byte(tc.body), options, o)
		if failureCode(err) != tc.code {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if tc.code != "" {
			continue
		}
		if o.Outcome != tc.outcome || o.Facts["tunnel_connections"] != tc.count || o.Facts["tunnel_status"] != "" {
			t.Fatalf("%s: %+v", tc.body, o)
		}
	}
}
