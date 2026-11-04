package cli

import (
	"net"
	"net/netip"
	"sort"
)

type networkInterface struct {
	name    string
	address netip.Addr
}
type interfaceLister func() ([]networkInterface, error)

func availableInterfaces() ([]networkInterface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := []networkInterface{}
	for _, iface := range all {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, a := range addresses {
			p, err := netip.ParsePrefix(a.String())
			if err == nil && p.Addr().Is4() && !p.Addr().IsLoopback() {
				result = append(result, networkInterface{iface.Name, p.Addr()})
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}
