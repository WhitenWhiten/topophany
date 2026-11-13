//go:build !linux && !darwin

package management

import (
	"errors"
	"os"
)

func checkOwner(os.FileInfo, bool, bool) error {
	return errors.New("management_platform_not_supported")
}
