package probe

import (
	"context"
	"net"
)

type DialPolicy func(context.Context, *net.Dialer, string, string) (net.Conn, error)
type dialPolicyKey struct{}

// WithDialPolicy keeps local management authorization at the actual connection
// boundary, including DNS probes and external source HTTP transports.
func WithDialPolicy(ctx context.Context, policy DialPolicy) context.Context {
	return context.WithValue(ctx, dialPolicyKey{}, policy)
}
func dialContext(ctx context.Context, d *net.Dialer, network, address string) (net.Conn, error) {
	if policy, ok := ctx.Value(dialPolicyKey{}).(DialPolicy); ok && policy != nil {
		return policy(ctx, d, network, address)
	}
	return d.DialContext(ctx, network, address)
}
func NetworkPolicyDenied() error { return local("setup", "management_network_denied") }
