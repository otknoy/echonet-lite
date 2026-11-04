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

// Options configures IPv4 UDP communication. An empty Interface uses OS routing
// for unicast; discovery selects a multicast interface when needed.
// Timeout defaults to three seconds.
type Options struct {
	Interface string
	Timeout   time.Duration
}
type packetTransport interface {
	ReadPacket() ([]byte, netip.Addr, error)
	WritePacket(context.Context, []byte, netip.Addr) error
	Close() error
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
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.terminal = err
		c.mu.Unlock()
		close(c.closed)
		// Preserve the original shutdown cause; socket cleanup cannot recover it.
		_ = c.transport.Close()
	})
}
func (c *Client) Close() error   { c.stop(ErrClosed); c.workers.Wait(); return nil }
func (c *Client) failure() error { c.mu.Lock(); defer c.mu.Unlock(); return c.terminal }
