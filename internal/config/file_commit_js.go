//go:build js

package config

import (
	"errors"
	"os"
)

// Browser viewers never acquire filesystem locks or write topology.
func CommitFile(_ string, expected FileVersion, _ []byte, _ os.FileMode) (FileVersion, error) {
	return expected, errors.New("browser viewers cannot write topology files")
}
