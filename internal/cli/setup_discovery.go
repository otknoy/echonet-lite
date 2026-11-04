package cli

import (
	"context"
	"net/netip"
	"sort"
	"time"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
)

type setupDevice struct {
	address netip.Addr
	eoj     echonet.EOJ
}

func setupDevicesFromNodes(nodes []echonet.DiscoveredNode) []setupDevice {
	// Deduplicate and sort candidates so selection is independent of response order.
	seen := map[setupDevice]bool{}
	devices := []setupDevice{}
	for _, node := range nodes {
		for _, eoj := range node.Objects {
			device := setupDevice{node.Address, eoj}
			if aircon.IsAircon(eoj) && !seen[device] {
				seen[device] = true
				devices = append(devices, device)
			}
		}
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].address != devices[j].address {
			return devices[i].address.Less(devices[j].address)
		}
		return devices[i].eoj.String() < devices[j].eoj.String()
	})
	return devices
}

func discoverForSetup(ctx context.Context, open opener, iface string, timeout time.Duration) ([]echonet.DiscoveredNode, error) {
	client, err := open(echonet.Options{Interface: iface, Timeout: timeout})
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	return client.Discover(ctx)
}
