package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
)

func NewCatalog(root Root, version uint64, minimum string, revokedArtifacts, revokedKeys []string, now time.Time) (Catalog, error) {
	c := Catalog{Schema: 1, RepositoryID: root.RepositoryID, RootEpoch: root.Trust.Epoch, Version: version, Issued: now.UTC(), Expires: now.UTC().Add(24 * time.Hour), Minimum: minimum, RevokedArtifacts: revokedArtifacts, RevokedKeys: revokedKeys}
	if e := root.Validate(now); e != nil {
		return c, e
	}
	if _, e := Version(minimum); e != nil {
		return c, e
	}
	if version == 0 || version > 1<<53-1 {
		return c, errors.New("invalid_catalog_version")
	}
	for _, h := range revokedArtifacts {
		if !management.HashOK(h) {
			return c, errors.New("invalid_revocation_digest")
		}
	}
	return c, nil
}

// RefreshCatalog authenticates historical policy without treating expired policy
// as permission to install. Revocations and the minimum version are preserved.
func RefreshCatalog(root Root, old management.Envelope, key management.SigningKey, now time.Time) (management.Envelope, error) {
	raw, e := management.Verify(CatalogDomain, "catalog", old, root.Trust, now, 256<<10)
	if e != nil {
		return management.Envelope{}, e
	}
	var previous Catalog
	if e = management.Decode(raw, &previous, 256<<10); e != nil {
		return management.Envelope{}, e
	}
	if previous.Schema != 1 || previous.RepositoryID != root.RepositoryID || previous.RootEpoch != root.Trust.Epoch || previous.Version == 0 || previous.Version >= 1<<53-1 || previous.Issued.After(now.Add(30*time.Second)) {
		return management.Envelope{}, errors.New("invalid_previous_catalog")
	}
	next, e := NewCatalog(root, previous.Version+1, previous.Minimum, previous.RevokedArtifacts, previous.RevokedKeys, now)
	if e != nil {
		return management.Envelope{}, e
	}
	private, e := base64.RawURLEncoding.DecodeString(key.Private)
	if e != nil || len(private) != ed25519.PrivateKeySize {
		return management.Envelope{}, errors.New("invalid_catalog_signing_key")
	}
	env, e := management.Sign(CatalogDomain, management.Marshal(next), key.ID, private)
	if e != nil {
		return env, e
	}
	_, e = VerifyCatalog(env, root, previous.Version+1, now)
	return env, e
}
