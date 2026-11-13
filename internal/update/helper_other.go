//go:build !linux

package update

import (
	"context"
	"errors"
	"net"
)

func StartHelper(context.Context, string) error {
	return errors.New("managed_updates_require_linux_systemd")
}

func verifyHelperPeer(net.Conn) error { return errors.New("managed_updates_require_linux_systemd") }
