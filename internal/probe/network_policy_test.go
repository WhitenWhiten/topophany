package probe

import (
	"context"
	"net"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestManagedNetworkDenialIsMonitorError(t *testing.T) {
	for _, kind := range []string{"tcp", "http", "dns"} {
		t.Run(kind, func(t *testing.T) {
			called := false
			ctx := WithDialPolicy(context.Background(), func(context.Context, *net.Dialer, string, string) (net.Conn, error) {
				called = true
				return nil, NetworkPolicyDenied()
			})
			x := model.Check{ID: "probe", Observer: "here", Kind: kind, Target: "127.0.0.1:1", TimeoutSeconds: 1}
			if kind == "http" {
				x.Target = "http://127.0.0.1:1/"
			}
			if kind == "dns" {
				x.DNS = model.DNSOptions{Name: "example.invalid", Type: "A"}
			}
			result := (Runner{}).Run(ctx, model.Config{Project: "test"}, x)
			if !called || result.Outcome != "error" || result.Code != "management_network_denied" {
				t.Fatalf("policy denial became target evidence: %+v", result)
			}
		})
	}
}
