package echonet

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
)

// Discover collects nodes for Options.Timeout, bounded by ctx. No responses is success.
func (c *Client) Discover(ctx context.Context) ([]DiscoveredNode, error) {
	return c.discover(ctx, multicast)
}
func (c *Client) discover(ctx context.Context, target netip.Addr) ([]DiscoveredNode, error) {
	p := &pending{address: target, discovery: true, request: frame{seoj: controllerEOJ, deoj: nodeEOJ, esv: Get, properties: []Property{{EPC: 0xd6}}}, responses: make(chan *Response, 256)}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := c.register(p)
	if err != nil {
		return nil, err
	}
	defer c.unregister(id)
	b, err := p.request.marshal()
	if err != nil {
		return nil, err
	}
	if err = c.transport.WritePacket(b, target); err != nil {
		return nil, err
	}
	window, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
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

// PropertyMaps reports advertised access capabilities. Unknown maps are nil, not empty.
type PropertyMaps struct {
	Get          []EPC
	Set          []EPC
	Announcement []EPC
}

func (o *Object) PropertyMaps(ctx context.Context) (PropertyMaps, error) {
	r, requestErr := o.Get(ctx, GetPropertyMap, SetPropertyMap, AnnouncementPropertyMap)
	var maps PropertyMaps
	if r == nil {
		return maps, requestErr
	}
	for _, entry := range []struct {
		epc EPC
		dst *[]EPC
	}{{GetPropertyMap, &maps.Get}, {SetPropertyMap, &maps.Set}, {AnnouncementPropertyMap, &maps.Announcement}} {
		b, ok := r.Property(entry.epc)
		if !ok || len(b) == 0 {
			continue
		}
		v, err := parsePropertyMap(b)
		if err != nil {
			return maps, fmt.Errorf("EPC %02x: %w", entry.epc, err)
		}
		*entry.dst = v
	}
	return maps, requestErr
}
func parsePropertyMap(b []byte) ([]EPC, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("missing property map")
	}
	n := int(b[0])
	out := make([]EPC, 0, n)
	if n < 16 {
		if len(b) != n+1 {
			return nil, fmt.Errorf("invalid list map length")
		}
		seen := map[EPC]bool{}
		for _, v := range b[1:] {
			e := EPC(v)
			if v < 0x80 || seen[e] {
				return nil, fmt.Errorf("invalid property map entry")
			}
			seen[e] = true
			out = append(out, e)
		}
	} else {
		if len(b) != 17 {
			return nil, fmt.Errorf("invalid bitmap length")
		}
		for low, v := range b[1:] {
			for high := 0; high < 8; high++ {
				if v&(1<<high) != 0 {
					out = append(out, EPC((high+8)*16+low))
				}
			}
		}
		if len(out) != n {
			return nil, fmt.Errorf("bitmap count mismatch")
		}
	}
	return out, nil
}
