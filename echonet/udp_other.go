//go:build !linux

package echonet

import (
	"fmt"
	"net/netip"
)

func openUDP(string) (packetTransport, netip.Addr, error) {
	return nil, netip.Addr{}, fmt.Errorf("ECHONET Lite UDP currently supports Linux only")
}
