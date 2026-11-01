package client

import (
	"context"
	"errors"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// AddressCount and SnapshotAt inspect every configured address independently;
// normal viewer failover intentionally returns only the first usable replica.
// Credentials remain inside Client, and alternate URLs keep their identity pin.
func (c *Client) AddressCount() int { return len(c.profile.Servers) }
func (c *Client) SnapshotAt(ctx context.Context, index int) (model.Snapshot, error) {
	if index < 0 || index >= len(c.profile.Servers) {
		return model.Snapshot{}, errors.New("invalid collector address index")
	}
	one, err := New(Profile{Project: c.profile.Project, Servers: []Endpoint{c.profile.Servers[index]}})
	if err != nil {
		return model.Snapshot{}, err
	}
	defer one.Close()
	one.Now = c.Now
	snap, _, err := one.Snapshot(ctx, "")
	return snap, err
}
