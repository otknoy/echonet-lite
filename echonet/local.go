package echonet

import "sort"

// The prototype exposes only properties it can answer. It has no assigned manufacturer code.
func (c *Client) localProperties(eoj EOJ) map[EPC][]byte {
	identity := c.identity
	for i, value := range eoj {
		identity[14+i] ^= value
	}
	common := map[EPC][]byte{0x80: {0x30}, 0x81: {0}, 0x88: {0x42}, 0x8a: {0xff, 0xff, 0xff}, 0x83: append([]byte(nil), identity[:]...)}
	switch eoj {
	case nodeEOJ:
		common[0x82] = []byte{1, 14, 1, 0}
		common[0xd3] = []byte{0, 0, 1}
		common[0xd4] = []byte{0, 2}
		common[0xd6] = []byte{1, 0x05, 0xff, 1}
		common[0xd7] = []byte{1, 0x05, 0xff}
	case controllerEOJ:
		common[0x82] = []byte{0, 0, 'R', 0}
	default:
		return nil
	}
	common[0x9d] = []byte{1, 0x80}
	if eoj == nodeEOJ {
		common[0x9d] = []byte{2, 0x80, 0xd5}
	}
	common[0x9e] = []byte{0}
	epcs := []int{0x9f}
	for epc := range common {
		epcs = append(epcs, int(epc))
	}
	sort.Ints(epcs)
	getMap := []byte{byte(len(epcs))}
	for _, e := range epcs {
		getMap = append(getMap, byte(e))
	}
	common[0x9f] = getMap
	return common
}
func (c *Client) serveLocal() {
	defer c.workers.Done()
	// Announce the controller instance after the receiver has started.
	if c.localIP.IsValid() {
		c.announce()
	}
	for {
		select {
		case req := <-c.localRequests:
			c.replyLocal(req)
		case <-c.closed:
			return
		}
	}
}
func (c *Client) announce() {
	f := frame{seoj: nodeEOJ, deoj: nodeEOJ, esv: INF, properties: []Property{{0xd5, []byte{1, 0x05, 0xff, 1}}}}
	b, err := f.marshal()
	if err == nil {
		_ = c.transport.WritePacket(b, multicast)
	}
}
func (c *Client) replyLocal(req localRequest) {
	f := req.frame
	targets := []EOJ{f.deoj}
	if f.deoj[2] == 0 {
		targets = nil
		for _, e := range []EOJ{nodeEOJ, controllerEOJ} {
			if e[0] == f.deoj[0] && e[1] == f.deoj[1] {
				targets = append(targets, e)
			}
		}
	}
	for _, target := range targets {
		props := c.localProperties(target)
		if props == nil {
			continue
		}
		out := frame{tid: f.tid, seoj: target, deoj: f.seoj, esv: GetRes}
		if f.esv == 0x6e {
			out.esv = 0x5e
			b, err := out.marshal()
			if err == nil {
				_ = c.transport.WritePacket(b, req.address)
			}
			continue
		}
		if f.esv == 0x63 && target == nodeEOJ {
			props[0xd5] = []byte{1, 0x05, 0xff, 1}
		}
		failed := false
		for _, p := range f.properties {
			switch f.esv {
			case Get, 0x63:
				if len(p.EDT) != 0 {
					return
				}
				v, ok := props[p.EPC]
				if !ok {
					failed = true
				}
				out.properties = append(out.properties, Property{p.EPC, v})
			case SetC, 0x60:
				// All exposed properties are read-only in this prototype.
				if len(p.EDT) == 0 {
					return
				}
				failed = true
				out.properties = append(out.properties, p)
			case INFC:
				out.properties = append(out.properties, Property{EPC: p.EPC})
			}
		}
		switch f.esv {
		case Get:
			if failed {
				out.esv = GetSNA
			}
		case 0x63:
			if failed {
				out.esv = 0x53
			} else {
				out.esv = INF
			}
		case SetC:
			out.esv = SetCSNA
		case 0x60:
			out.esv = 0x50
		case INFC:
			out.esv = INFCRes
		default:
			continue
		}
		b, err := out.marshal()
		if err == nil {
			_ = c.transport.WritePacket(b, req.address)
		}
	}
}
