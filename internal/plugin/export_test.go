package plugin

import "net/netip"

// IsPrivateAddr exposes isPrivate to the external test package.
func IsPrivateAddr(a netip.Addr) bool { return isPrivate(a) }
