package plugin

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// DialContext opens a TCP connection to an allowed public host. The socket
// address is checked after DNS resolution to prevent DNS rebinding.
func DialContext(ctx context.Context, allowHosts []string, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("%w: only TCP is allowed", ErrBlocked)
	}
	hostname, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse network address: %w", err)
	}
	allowed := false
	for _, h := range allowHosts {
		if h == "*" || strings.EqualFold(h, hostname) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("%w: host is outside network capability", ErrBlocked)
	}
	d := net.Dialer{Timeout: 10 * time.Second, Control: func(_, addr string, _ syscall.RawConn) error {
		h, _, err := net.SplitHostPort(addr)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(h)
		if err != nil || isPrivate(ip) || !ip.Unmap().IsGlobalUnicast() {
			return fmt.Errorf("%w: destination is not public", ErrBlocked)
		}
		return nil
	}}
	return d.DialContext(ctx, network, address)
}
