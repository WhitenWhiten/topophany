//go:build darwin

package probe

import (
	"net"
	"os"
	"syscall"
)

// datagramICMP opens macOS's unprivileged ICMP socket, the one ping uses
// without root: a datagram socket that carries ICMP echo and nothing else.
// The kernel keeps the echo's identifier and sequence as sent, and hands the
// reply back behind its IPv4 header, which the probe already steps over.
func datagramICMP(bind string) (net.PacketConn, error) {
	fd, e := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP)
	if e != nil {
		return nil, e
	}
	syscall.CloseOnExec(fd)
	if ip := net.ParseIP(bind).To4(); ip != nil && !ip.IsUnspecified() {
		var a syscall.SockaddrInet4
		copy(a.Addr[:], ip)
		if e = syscall.Bind(fd, &a); e != nil {
			syscall.Close(fd)
			return nil, e
		}
	}
	f := os.NewFile(uintptr(fd), "icmp")
	defer f.Close()
	c, e := net.FilePacketConn(f)
	if e != nil {
		return nil, e
	}
	return datagramConn{c}, nil
}

// datagramConn speaks the raw socket's addresses over a datagram socket, so
// one probe reads either.
type datagramConn struct{ net.PacketConn }

func (c datagramConn) WriteTo(b []byte, a net.Addr) (int, error) {
	if ip, ok := a.(*net.IPAddr); ok {
		a = &net.UDPAddr{IP: ip.IP}
	}
	return c.PacketConn.WriteTo(b, a)
}

func (c datagramConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, a, e := c.PacketConn.ReadFrom(b)
	if u, ok := a.(*net.UDPAddr); ok {
		a = &net.IPAddr{IP: u.IP}
	}
	return n, a, e
}
