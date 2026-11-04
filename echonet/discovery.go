package echonet

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
)

// Discover collects nodes for Options.Timeout, bounded by ctx. No responses is success.
func (c *Client) Discover(ctx context.Context) ([]DiscoveredNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-c.closed:
		return nil, c.failure()
	default:
	}
	if transport, ok := c.transport.(interface{ prepareDiscovery() error }); ok {
		if err := transport.prepareDiscovery(); err != nil {
			return nil, err
		}
		if !c.localIP.IsValid() {
			c.announce()
		}
	}
	return c.discover(ctx, multicast)
}

func (c *Client) discover(ctx context.Context, target netip.Addr) ([]DiscoveredNode, error) {
	p := &pending{address: target, discovery: true, request: frame{seoj: controllerEOJ, deoj: nodeEOJ, esv: Get, properties: []Property{{EPC: 0xd6}}}, responses: make(chan *Response, 256)}
	window, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	release, err := c.sendPending(window, p)
	if err != nil {
		return nil, err
	}
	defer release()
	return c.collectDiscovery(ctx, window, p)
}

func (c *Client) collectDiscovery(ctx, window context.Context, p *pending) ([]DiscoveredNode, error) {
	nodes := map[netip.Addr]map[EOJ]bool{}
	snapshot := func() []DiscoveredNode {
		out := make([]DiscoveredNode, 0, len(nodes))
		for addr, objects := range nodes {
			n := DiscoveredNode{Address: addr, Objects: []EOJ{}}
			for e := range objects {
				n.Objects = append(n.Objects, e)
			}
			sort.Slice(n.Objects, func(i, j int) bool { return n.Objects[i].String() < n.Objects[j].String() })
			out = append(out, n)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Address.Less(out[j].Address) })
		return out
	}
	for {
		select {
		case r := <-p.responses:
			objects, err := parseInstances(r.Properties[0].EDT)
			if err != nil {
				continue
			}
			if nodes[r.Address] == nil {
				if len(nodes) >= 1024 {
					continue
				}
				nodes[r.Address] = map[EOJ]bool{}
			}
			for _, e := range objects {
				nodes[r.Address][e] = true
			}
		case <-window.Done():
			return snapshot(), ctx.Err()
		case <-c.closed:
			return snapshot(), c.failure()
		}
	}
}

func parseInstances(b []byte) ([]EOJ, error) {
	// D6 may report a total larger than the entries included in this frame.
	if len(b) < 1 || (len(b)-1)%3 != 0 || (len(b)-1)/3 > 84 || (len(b)-1)/3 > int(b[0]) {
		return nil, fmt.Errorf("invalid instance list")
	}
	out := make([]EOJ, 0, int(b[0]))
	for i := 1; i < len(b); i += 3 {
		var e EOJ
		copy(e[:], b[i:i+3])
		if e[2] == 0 {
			return nil, fmt.Errorf("wildcard instance in list")
		}
		out = append(out, e)
	}
	return out, nil
}
