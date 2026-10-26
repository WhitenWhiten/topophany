package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// CollectorID is a persistent random data-store identity, independent of its
// configured display name, address, DNS records and TLS certificate renewal.
func (s *Store) CollectorID() string { return s.collectorID }
func (s *Store) loadCollectorID() error {
	path := filepath.Join(s.dir, "collector-id")
	b, err := secure.PrivateRead(path)
	if errors.Is(err, os.ErrNotExist) {
		id, err := secure.Token()
		if err != nil {
			return err
		}
		if err = secure.AtomicWrite(path, []byte(id+"\n"), 0600); err != nil {
			return err
		}
		s.collectorID = id
		return nil
	}
	if err != nil {
		return err
	}
	id := strings.TrimSpace(string(b))
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return errors.New("invalid collector-id; restore its original private file")
	}
	s.collectorID = id
	return nil
}
