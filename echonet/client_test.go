package echonet

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type packet struct {
	data []byte
	from netip.Addr
}
type fakeTransport struct {
	incoming chan packet
	outgoing chan packet
	done     chan struct{}
	once     sync.Once
	hook     func(frame, netip.Addr)
}

func newFake() *fakeTransport {
	return &fakeTransport{incoming: make(chan packet, 256), outgoing: make(chan packet, 256), done: make(chan struct{})}
}
func (f *fakeTransport) ReadPacket() ([]byte, netip.Addr, error) {
	select {
	case p := <-f.incoming:
		return p.data, p.from, nil
	case <-f.done:
		return nil, netip.Addr{}, ErrClosed
	}
}
func (f *fakeTransport) WritePacket(ctx context.Context, b []byte, to netip.Addr) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return ErrClosed
	default:
	}
	parsed, err := parseFrame(b)
	if err != nil {
		return err
	}
	if f.hook != nil {
		f.hook(parsed, to)
	}
	select {
	case f.outgoing <- packet{append([]byte(nil), b...), to}:
	default:
	}
	return nil
}
func (f *fakeTransport) Close() error { f.once.Do(func() { close(f.done) }); return nil }
func (f *fakeTransport) inject(fr frame, from netip.Addr) {
	b, _ := fr.marshal()
	f.incoming <- packet{b, from}
}

var peer = netip.MustParseAddr("192.0.2.10")
var local = netip.MustParseAddr("192.0.2.1")
var acEOJ = EOJ{1, 0x30, 1}

func reply(req frame, ps []Property, esv ESV) frame {
	return frame{tid: req.tid, seoj: req.deoj, deoj: req.seoj, esv: esv, properties: ps}
}
func testClient(t *testing.T, hook func(frame, netip.Addr)) (*Client, *fakeTransport) {
	t.Helper()
	f := newFake()
	f.hook = hook
	c := newClient(f, local, 100*time.Millisecond)
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c, f
}
func TestRequestRejectsUnrelatedResponses(t *testing.T) {
	var transport *fakeTransport
	c, f := testClient(t, func(req frame, to netip.Addr) {
		if req.esv != Get {
			return
		}
		good := reply(req, []Property{{0x80, []byte{0x30}}}, GetRes)
		wrong := good
		wrong.tid++
		transport.inject(wrong, to)
		wrong = good
		wrong.seoj = EOJ{1, 0x30, 2}
		transport.inject(wrong, to)
		wrong = good
		wrong.deoj = nodeEOJ
		transport.inject(wrong, to)
		transport.inject(good, netip.MustParseAddr("192.0.2.11"))
		wrong = good
		wrong.properties = []Property{{0xb0, []byte{0x42}}}
		transport.inject(wrong, to)
		wrong = good
		wrong.esv = SetRes
		transport.inject(wrong, to)
		// Malformed traffic must not stop reception.
		transport.incoming <- packet{[]byte{0x10}, to}
		transport.inject(good, to)
		transport.inject(good, to)
	})
	transport = f
	r, err := c.Node(peer).Object(acEOJ).Get(context.Background(), 0x80)
	if err != nil || r.ESV != GetRes {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestPartialSNAAndSettingUnknown(t *testing.T) {
	var transport *fakeTransport
	c, f := testClient(t, func(req frame, to netip.Addr) {
		if req.esv == Get {
			transport.inject(reply(req, []Property{{0x80, []byte{0x30}}, {0xbb, nil}}, GetSNA), to)
		}
	})
	transport = f
	o := c.Node(peer).Object(acEOJ)
	r, err := o.Get(context.Background(), 0x80, 0xbb)
	var sna *ServiceError
	if !errors.As(err, &sna) || r == nil {
		t.Fatalf("%v %+v", err, r)
	}
	if v, _ := r.Property(0x80); len(v) != 1 || v[0] != 0x30 {
		t.Fatal("lost partial value")
	}
	_, err = o.SetC(context.Background(), Property{0x80, []byte{0x30}})
	var unknown *SettingUnknownError
	if !errors.As(err, &unknown) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected unknown timeout, got %v", err)
	}
	count := 0
	for {
		select {
		case p := <-f.outgoing:
			fr, _ := parseFrame(p.data)
			if fr.esv == SetC {
				count++
			}
		default:
			if count != 1 {
				t.Fatalf("write attempts: %d", count)
			}
			return
		}
	}
}
func TestCancelCloseAndTID(t *testing.T) {
	c, _ := testClient(t, nil)
	o := c.Node(peer).Object(acEOJ)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := o.Get(ctx, 0x80); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := o.Get(context.Background(), 0x80); finished <- err }()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err == nil {
		t.Fatal("Close did not cancel request")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Get(context.Background(), 0x80); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	other, _ := testClient(t, nil)
	other.next = 65535
	id, err := other.register(&pending{})
	if err != nil || id != 0 {
		t.Fatalf("wrap %d %v", id, err)
	}
	other.unregister(id)
	other.next = 65535
	next, err := other.register(&pending{})
	if err != nil || next == id {
		t.Fatalf("reused retired TID: %d %v", next, err)
	}
}
func TestConcurrentRequests(t *testing.T) {
	var transport *fakeTransport
	c, f := testClient(t, func(req frame, to netip.Addr) {
		if req.esv == Get {
			transport.inject(reply(req, []Property{{0x80, []byte{0x30}}}, GetRes), to)
		}
	})
	transport = f
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Node(peer).Object(acEOJ).Get(context.Background(), 0x80); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != 0 {
		t.Fatal("pending requests leaked")
	}
}
func TestDiscoveryDeduplication(t *testing.T) {
	var transport *fakeTransport
	c, f := testClient(t, func(req frame, to netip.Addr) {
		if req.esv != Get {
			return
		}
		// The discovery response's source is the node profile, not the AC.
		good := reply(req, []Property{{0xd6, []byte{2, 1, 0x30, 1, 1, 0x30, 2}}}, GetRes)
		transport.inject(good, peer)
		transport.inject(good, peer)
		bad := good
		bad.properties = []Property{{0xd6, []byte{2, 1, 0x30, 1, 0}}}
		transport.inject(bad, netip.MustParseAddr("192.0.2.11"))
	})
	transport = f
	nodes, err := c.Discover(context.Background())
	if err != nil || len(nodes) != 1 || len(nodes[0].Objects) != 2 {
		t.Fatalf("%+v %v", nodes, err)
	}
	empty, _ := testClient(t, nil)
	nodes, err = empty.Discover(context.Background())
	if err != nil || len(nodes) != 0 {
		t.Fatalf("%+v %v", nodes, err)
	}
}
func TestGetSetReadback(t *testing.T) {
	value := byte(24)
	var transport *fakeTransport
	c, f := testClient(t, func(req frame, to netip.Addr) {
		switch req.esv {
		case Get:
			transport.inject(reply(req, []Property{{0xb3, []byte{value}}}, GetRes), to)
		case SetC:
			value = req.properties[0].EDT[0]
			transport.inject(reply(req, []Property{{0xb3, nil}}, SetRes), to)
		}
	})
	transport = f
	o := c.Node(peer).Object(acEOJ)
	if _, err := o.SetC(context.Background(), Property{0xb3, []byte{26}}); err != nil {
		t.Fatal(err)
	}
	r, err := o.Get(context.Background(), 0xb3)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := r.Property(0xb3)
	if b[0] != 26 {
		t.Fatal("setting not retained")
	}
}
func TestInvalidRequests(t *testing.T) {
	c, _ := testClient(t, nil)
	for _, epcs := range [][]EPC{nil, {0x80, 0x80}, {0x01}} {
		if _, err := c.Node(peer).Object(acEOJ).Get(context.Background(), epcs...); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, ip := range []string{"224.0.23.0", "0.0.0.0", "255.255.255.255", "::1"} {
		if _, err := c.Node(netip.MustParseAddr(ip)).Object(acEOJ).Get(context.Background(), 0x80); !errors.Is(err, ErrInvalid) {
			t.Fatal(ip, err)
		}
	}
}
func TestLocalNode(t *testing.T) {
	c, f := testClient(t, nil)
	req := frame{tid: 42, seoj: acEOJ, deoj: nodeEOJ, esv: Get, properties: []Property{{EPC: 0xd6}, {EPC: 0x83}}}
	f.inject(req, peer)
	timeout := time.After(time.Second)
	for {
		select {
		case p := <-f.outgoing:
			fr, _ := parseFrame(p.data)
			if fr.tid != 42 {
				continue
			}
			if p.from != peer || fr.esv != GetRes || len(fr.properties[1].EDT) != 17 {
				t.Fatalf("%+v", fr)
			}
			instances, err := parseInstances(fr.properties[0].EDT)
			if err != nil || fmt.Sprint(instances) != fmt.Sprint([]EOJ{controllerEOJ}) {
				t.Fatalf("%v %v", instances, err)
			}
			maps := c.localProperties(nodeEOJ)
			if _, err := parsePropertyMap(maps[0x9f]); err != nil {
				t.Fatal(err)
			}
			return
		case <-timeout:
			t.Fatal("missing local reply")
		}
	}
}

func TestPendingLimitAndReadFailure(t *testing.T) {
	c, f := testClient(t, nil)
	for range 32 {
		if _, err := c.register(&pending{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.register(&pending{}); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	// A receive failure terminates the client and future requests.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.closed:
	case <-time.After(time.Second):
		t.Fatal("client did not stop")
	}
	if _, err := c.Node(peer).Object(acEOJ).Get(context.Background(), 0x80); err == nil {
		t.Fatal("request accepted after receive failure")
	}
}

func TestLocalRejectionAndINFC(t *testing.T) {
	c, f := testClient(t, nil)
	tests := []struct {
		esv   ESV
		props []Property
		want  ESV
	}{
		{Get, []Property{{EPC: 0x80}, {EPC: 0xfe}}, GetSNA},
		{SetC, []Property{{0x80, []byte{0x31}}}, SetCSNA},
		{INFC, []Property{{0x80, []byte{0x30}}}, INFCRes},
	}
	for i, tt := range tests {
		tid := uint16(100 + i)
		f.inject(frame{tid: tid, seoj: acEOJ, deoj: controllerEOJ, esv: tt.esv, properties: tt.props}, peer)
		timer := time.NewTimer(time.Second)
	loop:
		for {
			select {
			case p := <-f.outgoing:
				fr, _ := parseFrame(p.data)
				if fr.tid != tid {
					continue
				}
				if fr.esv != tt.want || fr.seoj != controllerEOJ || fr.deoj != acEOJ {
					t.Fatalf("unexpected reply: %+v", fr)
				}
				break loop
			case <-timer.C:
				t.Fatal("missing reply")
			}
		}
		timer.Stop()
	}
	nodeID := c.localProperties(nodeEOJ)[0x83]
	controllerID := c.localProperties(controllerEOJ)[0x83]
	if string(nodeID) == string(controllerID) {
		t.Fatal("object identities must differ")
	}
}

func TestTruncatedSNAIsReturned(t *testing.T) {
	var transport *fakeTransport
	c, f := testClient(t, func(req frame, to netip.Addr) {
		if req.esv == Get {
			transport.inject(reply(req, []Property{{0x80, []byte{0x30}}}, GetSNA), to)
		}
	})
	transport = f
	r, err := c.Node(peer).Object(acEOJ).Get(context.Background(), 0x80, 0xb0, 0xb3)
	var service *ServiceError
	if !errors.As(err, &service) || r == nil || len(r.Properties) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

type blockedTransport struct {
	*fakeTransport
	started chan struct{}
}

func (f *blockedTransport) WritePacket(ctx context.Context, _ []byte, _ netip.Addr) error {
	select {
	case f.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return ErrClosed
	}
}

func TestBlockedSend(t *testing.T) {
	for _, operation := range []string{"get", "set", "discover"} {
		t.Run(operation, func(t *testing.T) {
			f := &blockedTransport{fakeTransport: newFake(), started: make(chan struct{}, 1)}
			c := newClient(f, netip.Addr{}, 20*time.Millisecond)
			defer func() { _ = c.Close() }()
			var err error
			switch operation {
			case "get":
				_, err = c.Node(peer).Object(acEOJ).Get(context.Background(), 0x80)
			case "set":
				_, err = c.Node(peer).Object(acEOJ).SetC(context.Background(), Property{0x80, []byte{0x30}})
			case "discover":
				_, err = c.Discover(context.Background())
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("blocked send: %v", err)
			}
			var unknown *SettingUnknownError
			if errors.As(err, &unknown) {
				t.Fatal("failed send reported as acknowledged transmission")
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if len(c.pending) != 0 {
				t.Fatal("pending request leaked")
			}
		})
	}
}

func TestCancelBlockedSend(t *testing.T) {
	f := &blockedTransport{fakeTransport: newFake(), started: make(chan struct{}, 1)}
	c := newClient(f, netip.Addr{}, time.Second)
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.Node(peer).Object(acEOJ).Get(ctx, 0x80); result <- err }()
	<-f.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("send ignored cancellation")
	}
}

func TestCloseInterruptsBlockedSend(t *testing.T) {
	f := &blockedTransport{fakeTransport: newFake(), started: make(chan struct{}, 1)}
	c := newClient(f, netip.Addr{}, time.Minute)
	t.Cleanup(func() { _ = c.Close() })
	result := make(chan error, 1)
	go func() { _, err := c.Node(peer).Object(acEOJ).Get(context.Background(), 0x80); result <- err }()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("send did not start")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt send")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != 0 {
		t.Fatal("pending send leaked after Close")
	}
}

func TestSetCancellationAfterSendIsUnknownWithoutRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, f := testClient(t, func(req frame, _ netip.Addr) {
		if req.esv == SetC {
			cancel()
		}
	})
	_, err := c.Node(peer).Object(acEOJ).SetC(ctx, Property{0x80, []byte{0x30}})
	var unknown *SettingUnknownError
	if !errors.As(err, &unknown) || !errors.Is(err, context.Canceled) {
		t.Fatalf("sent setting must be unknown after cancellation: %v", err)
	}
	attempts := 0
	for {
		select {
		case p := <-f.outgoing:
			req, err := parseFrame(p.data)
			if err != nil {
				t.Fatal(err)
			}
			if req.esv == SetC {
				attempts++
			}
		default:
			if attempts != 1 {
				t.Fatalf("setting attempts: %d", attempts)
			}
			return
		}
	}
}

func TestDiscoveryCancellationReleasesPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, f := testClient(t, nil)
	finished := make(chan struct{})
	var nodes []DiscoveredNode
	var err error
	go func() { nodes, err = c.Discover(ctx); close(finished) }()
	var request frame
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
waitForRequest:
	for {
		select {
		case p := <-f.outgoing:
			request, _ = parseFrame(p.data)
			if request.esv == Get {
				break waitForRequest
			}
		case <-timer.C:
			t.Fatal("discovery request did not start")
		}
	}
	f.inject(reply(request, []Property{{0xd6, []byte{1, 1, 0x30, 1}}}, GetRes), peer)
	// Cancellation may race collection, so only assert the cancellation contract;
	// the normal timeout/deduplication test covers the collected snapshot.
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("discovery ignored cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v %v", nodes, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != 0 {
		t.Fatal("pending discovery leaked")
	}
}

func TestMarshalFailureReleasesAndRetiresTID(t *testing.T) {
	c, _ := testClient(t, nil)
	props := make([]Property, 6)
	for i := range props {
		props[i] = Property{EPC: EPC(0x80 + i), EDT: make([]byte, 255)}
	}
	_, err := c.Node(peer).Object(acEOJ).SetC(context.Background(), props...)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized frame: %v", err)
	}
	var unknown *SettingUnknownError
	if errors.As(err, &unknown) {
		t.Fatal("unsent request must not be setting unknown")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != 0 || len(c.retired) != 1 {
		t.Fatalf("pending=%d retired=%d", len(c.pending), len(c.retired))
	}
	for _, until := range c.retired {
		if !until.After(time.Now()) {
			t.Fatal("released TID not quarantined")
		}
	}
}
