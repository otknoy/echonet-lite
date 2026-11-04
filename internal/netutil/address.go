// Package netutil holds address rules shared by protocol and CLI configuration.
package netutil

import "net/netip"

// IsUnicastIPv4 applies the project's IPv4 endpoint validation policy.
// Loopback and link-local addresses remain valid for local use and testing.
func IsUnicastIPv4(address netip.Addr) bool {
	return address.Is4() && !address.IsMulticast() && !address.IsUnspecified() && address != netip.AddrFrom4([4]byte{255, 255, 255, 255})
}
