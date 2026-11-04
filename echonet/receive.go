package echonet

import "net/netip"

type localRequest struct {
	frame   frame
	address netip.Addr
}

func matches(p *pending, f frame, from netip.Addr) bool {
	if f.deoj != p.request.seoj {
		return false
	}
	if p.discovery {
		return f.esv == GetRes && f.seoj == nodeEOJ && len(f.properties) == 1 && f.properties[0].EPC == 0xd6
	}
	if from != p.address || f.seoj != p.request.deoj {
		return false
	}
	expected, sna := GetRes, GetSNA
	if p.request.esv == SetC {
		expected, sna = SetRes, SetCSNA
	}
	if f.esv != expected && f.esv != sna {
		return false
	}
	if len(f.properties) > len(p.request.properties) || (f.esv != sna && len(f.properties) != len(p.request.properties)) {
		return false
	}
	for i, pv := range f.properties {
		if pv.EPC != p.request.properties[i].EPC {
			return false
		}
		if f.esv == GetRes && len(pv.EDT) == 0 {
			return false
		}
		if f.esv == SetRes && len(pv.EDT) != 0 {
			return false
		}
	}
	return true
}
func (c *Client) receive() {
	defer c.workers.Done()
	for {
		b, from, err := c.transport.ReadPacket()
		if err != nil {
			c.stop(err)
			return
		}
		if !from.Is4() || from == c.localIP {
			continue
		}
		f, err := parseFrame(b)
		if err != nil {
			continue
		}
		if f.esv == Get || f.esv == SetC || f.esv == 0x60 || f.esv == 0x63 || f.esv == INFC || f.esv == 0x6e {
			select {
			case c.localRequests <- localRequest{f, from}:
			default:
			}
			continue
		}
		c.mu.Lock()
		p := c.pending[f.tid]
		if p != nil && matches(p, f, from) {
			r := &Response{from, f.tid, f.seoj, f.deoj, f.esv, f.properties}
			select {
			case p.responses <- r:
			default:
			}
		}
		c.mu.Unlock()
	}
}
