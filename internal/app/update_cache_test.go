package app

import (
	"context"
	"github.com/WhitenWhiten/topophany/internal/update"
	"testing"
	"time"
)

type countedMetadata struct {
	calls, catalogs int
	revoked         bool
}

func (s *countedMetadata) Resolve(_ context.Context, _ update.Root, v, _, _ string, _ uint64, now time.Time) (update.Resolution, error) {
	s.calls++
	return update.Resolution{Manifest: update.Manifest{Version: v}, Artifact: update.Artifact{SHA256: "archive"}, Catalog: update.Catalog{Version: 1}, ResolvedAt: now}, nil
}
func (s *countedMetadata) Catalog(context.Context, update.Root, uint64, time.Time) (update.Catalog, error) {
	s.catalogs++
	c := update.Catalog{Version: 1, Minimum: "v1.0.0"}
	if s.revoked {
		c.RevokedArtifacts = []string{"archive"}
	}
	return c, nil
}
func TestReleaseBatchCacheKeepsRevocationsFresh(t *testing.T) {
	c := &releaseCache{entries: map[string]update.Resolution{}}
	s := &countedMetadata{}
	for range 16 {
		if _, err := c.resolve(context.Background(), s, update.Root{}, "v1.7.0", "linux", "amd64"); err != nil {
			t.Fatal(err)
		}
	}
	if s.calls != 1 || s.catalogs != 15 {
		t.Fatal(s)
	}
	s.revoked = true
	if _, err := c.resolve(context.Background(), s, update.Root{}, "v1.7.0", "linux", "amd64"); err == nil {
		t.Fatal("cached release ignored revocation")
	}
	for k, r := range c.entries {
		r.ResolvedAt = time.Now().Add(-time.Minute)
		c.entries[k] = r
	}
	c.resolve(context.Background(), s, update.Root{}, "v1.7.0", "linux", "amd64")
	if s.calls != 2 {
		t.Fatal("expired identity reused")
	}
}
