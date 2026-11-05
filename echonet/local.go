package echonet

import (
	"context"
	"net/netip"
	"sort"
)

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
		_ = c.writeLocal(b, multicast)
	}
}

// localTargets resolves a class-wide EOJ to the concrete local objects.
func localTargets(destination EOJ) []EOJ {
	if destination[2] != 0 {
		return []EOJ{destination}
	}
	var targets []EOJ
	for _, eoj := range []EOJ{nodeEOJ, controllerEOJ} {
		if eoj[0] == destination[0] && eoj[1] == destination[1] {
			targets = append(targets, eoj)
		}
	}
	return targets
}

// validLocalRequest checks service-specific EDT lengths. parseFrame validates
// the frame structure before a request reaches the local service.
func validLocalRequest(request frame) bool {
	switch request.esv {
	case Get, INFReq:
		for _, p := range request.properties {
			if len(p.EDT) != 0 {
				return false
			}
		}
	case SetI, SetC:
		for _, p := range request.properties {
			if len(p.EDT) == 0 {
				return false
			}
		}
	case INFC, SetGet:
		// SetGet is rejected without executing either property group.
	default:
		return false
	}
	return true
}

// localResponse builds a response without I/O or modifying the request or
// properties. A false result means the local service must not send a response.
func localResponse(request frame, target EOJ, properties map[EPC][]byte) (frame, bool) {
	if properties == nil || !validLocalRequest(request) {
		return frame{}, false
	}
	response := frame{tid: request.tid, seoj: target, deoj: request.seoj}
	switch request.esv {
	case Get, INFReq:
		unavailable := false
		for _, p := range request.properties {
			value, supported := properties[p.EPC]
			if request.esv == INFReq && target == nodeEOJ && p.EPC == 0xd5 {
				value, supported = []byte{1, 0x05, 0xff, 1}, true
			}
			if !supported {
				unavailable = true
			}
			response.properties = append(response.properties, Property{p.EPC, value})
		}
		if request.esv == Get {
			response.esv = GetRes
			if unavailable {
				response.esv = GetSNA
			}
		} else {
			response.esv = INF
			if unavailable {
				response.esv = INFSNA
			}
		}
	case SetI, SetC:
		// All exposed properties are read-only in this prototype.
		response.properties = append([]Property(nil), request.properties...)
		response.esv = SetCSNA
		if request.esv == SetI {
			response.esv = SetISNA
		}
	case SetGet:
		response.esv = SetGetSNA
	case INFC:
		response.esv = INFCRes
		for _, p := range request.properties {
			response.properties = append(response.properties, Property{EPC: p.EPC})
		}
	}
	return response, true
}

func (c *Client) replyLocal(req localRequest) {
	for _, target := range localTargets(req.frame.deoj) {
		response, ok := localResponse(req.frame, target, c.localProperties(target))
		if !ok {
			continue
		}
		b, err := response.marshal()
		if err == nil {
			_ = c.writeLocal(b, req.address)
		}
	}
}

func (c *Client) writeLocal(b []byte, to netip.Addr) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	return c.transport.WritePacket(ctx, b, to)
}
