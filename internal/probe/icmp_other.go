//go:build !darwin

package probe

import (
	"errors"
	"net"
)

// datagramICMP is macOS's unprivileged ICMP socket. Elsewhere ICMP needs a
// raw socket: Linux's datagram ICMP rewrites the echo's identifier, which
// this probe matches byte for byte.
func datagramICMP(string) (net.PacketConn, error) { return nil, errors.ErrUnsupported }
