package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// SnapshotViews reads both scopes from one configured endpoint and generation.
func (c *Client) SnapshotViews(ctx context.Context, observer string) (model.Snapshot, *model.Snapshot, error) {
	scoped, all, _, err := c.SourcedViews(ctx, observer)
	return scoped, all, err
}

// SourcedViews treats a complete pair as the unit of failover. A scoped read
// alone cannot reselect an endpoint whose global read has already failed.
// Authentication, project and malformed-response errors still fail closed.
func (c *Client) SourcedViews(ctx context.Context, observer string) (model.Snapshot, *model.Snapshot, string, error) {
	if observer == "" {
		snap, endpoint, err := c.Snapshot(ctx, observer)
		return snap, nil, endpoint, err
	}
	var last error
	for i, ep := range c.profile.Servers {
		pairCtx, cancel := endpointContext(ctx, len(c.profile.Servers)-i)
		pinned := &Client{diagnostics: c.diagnostics, topologies: map[string]cachedTopology{ep.URL: c.knownTopology(ep.URL)}, profile: Profile{Project: c.profile.Project, Servers: []Endpoint{ep}}, transports: []*http.Client{c.transports[i]}, Now: c.Now}
		scoped, all, err := pinned.readPair(pairCtx, observer)
		expired := pairCtx.Err() != nil
		cancel()
		if ctx.Err() != nil {
			return model.Snapshot{}, nil, "", ctx.Err()
		}
		if err == nil {
			c.rememberTopology(ep.URL, scoped)
			return scoped, all, ep.URL, nil
		}
		if !errors.Is(err, ErrNoCollector) && !(expired && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))) {
			return model.Snapshot{}, nil, "", err
		}
		last = err
	}
	return model.Snapshot{}, nil, "", fmt.Errorf("%w with consistent observer views: %v", ErrNoCollector, last)
}

func (c *Client) readPair(ctx context.Context, observer string) (model.Snapshot, *model.Snapshot, error) {
	// A topology reload between the two reads earns one same-endpoint retry.
	for range 2 {
		scoped, _, err := c.Snapshot(ctx, observer)
		if err != nil {
			return model.Snapshot{}, nil, err
		}
		all, _, err := c.Snapshot(ctx, "")
		if err != nil {
			return model.Snapshot{}, nil, err
		}
		if all.Revision == scoped.Revision && all.Instance == scoped.Instance {
			return scoped, &all, nil
		}
	}
	return model.Snapshot{}, nil, fmt.Errorf("%w: source changed during snapshot read", ErrNoCollector)
}
