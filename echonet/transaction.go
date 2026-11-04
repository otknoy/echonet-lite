package echonet

import (
	"context"
	"net/netip"
	"time"
)

type pending struct {
	address   netip.Addr
	request   frame
	responses chan *Response
	discovery bool
}

func (c *Client) register(p *pending) (uint16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return 0, c.terminal
	default:
	}
	if len(c.pending) >= 32 {
		return 0, ErrBusy
	}
	now := time.Now()
	for id, until := range c.retired {
		if !until.After(now) {
			delete(c.retired, id)
		}
	}
	for range 65536 {
		c.next++
		_, busy := c.pending[c.next]
		_, retired := c.retired[c.next]
		if !busy && !retired {
			p.request.tid = c.next
			c.pending[c.next] = p
			return c.next, nil
		}
	}
	return 0, ErrBusy
}

func (c *Client) unregister(id uint16) {
	c.mu.Lock()
	delete(c.pending, id)
	c.retired[id] = time.Now().Add(2 * c.timeout)
	c.mu.Unlock()
}

// sendPending registers before transmission so an immediate reply cannot be lost.
// The caller owns release only after successful transmission.
func (c *Client) sendPending(ctx context.Context, p *pending) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := c.register(p)
	if err != nil {
		return nil, err
	}
	cleanup := func() { c.unregister(id) }
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	b, err := p.request.marshal()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = c.transport.WritePacket(ctx, b, p.address); err != nil {
		return nil, err
	}
	return cleanup, nil
}
