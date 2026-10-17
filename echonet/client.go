package echonet

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/netip"
	"sync"
	"time"
)

var controllerEOJ = EOJ{0x05, 0xff, 0x01}
var nodeEOJ = EOJ{0x0e, 0xf0, 0x01}
var multicast = netip.MustParseAddr("224.0.23.0")

// Options selects a single IPv4 NIC. Timeout defaults to three seconds.
type Options struct {
	Interface string
	Timeout   time.Duration
}
type packetTransport interface {
	ReadPacket() ([]byte, netip.Addr, error)
	WritePacket([]byte, netip.Addr) error
	Close() error
}
type pending struct {
	address   netip.Addr
	request   frame
	responses chan *Response
	discovery bool
}
type Client struct {
	identity      [17]byte
	transport     packetTransport
	timeout       time.Duration
	localIP       netip.Addr
	mu            sync.Mutex
	pending       map[uint16]*pending
	retired       map[uint16]time.Time
	next          uint16
	closed        chan struct{}
	closeOnce     sync.Once
	workers       sync.WaitGroup
	localRequests chan localRequest
	terminal      error
}
type localRequest struct {
	frame   frame
	address netip.Addr
}

func Open(options Options) (*Client, error) {
	if options.Timeout < 0 {
		return nil, fmt.Errorf("%w: negative timeout", ErrInvalid)
	}
	t, ip, err := openUDP(options.Interface)
	if err != nil {
		return nil, err
	}
	return newClient(t, ip, options.Timeout), nil
}
func newClient(t packetTransport, ip netip.Addr, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	c := &Client{transport: t, timeout: timeout, localIP: ip, pending: make(map[uint16]*pending), retired: make(map[uint16]time.Time), closed: make(chan struct{}), localRequests: make(chan localRequest, 32)}
	c.identity[0] = 0xfe
	c.identity[1], c.identity[2], c.identity[3] = 0xff, 0xff, 0xff
	_, _ = rand.Read(c.identity[4:])
	c.workers.Add(2)
	go c.receive()
	go c.serveLocal()
	return c
}
func (c *Client) Node(address netip.Addr) *Node { return &Node{client: c, address: address.Unmap()} }
func (c *Client) stop(err error) {
	c.closeOnce.Do(func() { c.mu.Lock(); c.terminal = err; c.mu.Unlock(); close(c.closed); c.transport.Close() })
}
func (c *Client) Close() error   { c.stop(ErrClosed); c.workers.Wait(); return nil }
func (c *Client) failure() error { c.mu.Lock(); defer c.mu.Unlock(); return c.terminal }
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
func validProperties(ps []Property, write bool) error {
	if len(ps) == 0 || len(ps) > 255 {
		return fmt.Errorf("%w: property count must be 1..255", ErrInvalid)
	}
	seen := map[EPC]bool{}
	for _, p := range ps {
		if seen[p.EPC] || p.EPC < 0x80 {
			return fmt.Errorf("%w: duplicate or invalid EPC", ErrInvalid)
		}
		seen[p.EPC] = true
		if len(p.EDT) > 255 || (write && len(p.EDT) == 0) {
			return fmt.Errorf("%w: EDT length", ErrInvalid)
		}
	}
	return nil
}
func (o *Object) Get(ctx context.Context, epcs ...EPC) (*Response, error) {
	ps := make([]Property, len(epcs))
	for i, e := range epcs {
		ps[i].EPC = e
	}
	return o.request(ctx, Get, ps)
}
func (o *Object) SetC(ctx context.Context, ps ...Property) (*Response, error) {
	return o.request(ctx, SetC, ps)
}
func (o *Object) request(ctx context.Context, esv ESV, ps []Property) (*Response, error) {
	if !o.node.address.Is4() || o.node.address.IsMulticast() || o.node.address.IsUnspecified() || o.node.address == netip.MustParseAddr("255.255.255.255") || o.eoj[2] == 0 {
		return nil, fmt.Errorf("%w: unicast IP and concrete EOJ required", ErrInvalid)
	}
	if err := validProperties(ps, esv == SetC); err != nil {
		return nil, err
	}
	c := o.node.client
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := &pending{address: o.node.address, request: frame{seoj: controllerEOJ, deoj: o.eoj, esv: esv, properties: ps}, responses: make(chan *Response, 1)}
	id, err := c.register(p)
	if err != nil {
		return nil, err
	}
	defer c.unregister(id)
	b, err := p.request.marshal()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = c.transport.WritePacket(b, p.address); err != nil {
		return nil, err
	}
	unknown := func(err error) (*Response, error) {
		if esv == SetC {
			return nil, &SettingUnknownError{err}
		}
		return nil, err
	}
	select {
	case r := <-p.responses:
		if r.ESV == GetSNA || r.ESV == SetCSNA {
			return r, &ServiceError{r}
		}
		return r, nil
	case <-ctx.Done():
		return unknown(ctx.Err())
	case <-c.closed:
		return unknown(c.failure())
	}
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
