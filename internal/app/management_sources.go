package app

import (
	"context"
	"errors"
	"net"

	"github.com/WhitenWhiten/topophany/internal/management"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/runner"
	"github.com/WhitenWhiten/topophany/internal/source"
)

func managedNetwork(ctx context.Context, a *management.Agent) context.Context {
	if a == nil {
		return ctx
	}
	return probe.WithDialPolicy(ctx, func(ctx context.Context, d *net.Dialer, network, address string) (net.Conn, error) {
		conn, e := a.Dial(ctx, d, network, address)
		if errors.Is(e, management.ErrNetworkPolicy) {
			return nil, probe.NetworkPolicyDenied()
		}
		return conn, e
	})
}

type managedSources struct {
	runtime runner.SourceRuntime
	agent   *management.Agent
}

func (s managedSources) Validate(c model.Config) error { return s.runtime.Validate(c) }
func (s managedSources) Collect(ctx context.Context, c model.Config, src model.Source, requests []source.Request) ([]source.Reading, error) {
	for _, x := range c.Checks {
		if x.Observer == src.Observer && x.External != nil && x.External.Source == src.ID {
			if e := s.agent.Guard(c, x); e != nil {
				return nil, source.NewError("management_policy_denied", "local management policy refused collection")
			}
		}
	}
	return s.runtime.Collect(managedNetwork(ctx, s.agent), c, src, requests)
}
