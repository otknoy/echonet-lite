//go:build linux

package echonet

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"
)

// This only sends to loopback; it never discovers or operates LAN devices.
func TestUDPTransportLoopback(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3610})
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skip("sandbox prohibits UDP sockets")
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		t.Skip("UDP 3610 is already in use")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	}()
	raw, err := server.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var socketErr error
	if err = raw.Control(func(fd uintptr) {
		socketErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_PKTINFO, 1)
	}); err != nil {
		t.Fatal(err)
	}
	if socketErr != nil {
		t.Fatal(socketErr)
	}
	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sender.Close(); err != nil {
			t.Error(err)
		}
	}()
	iface, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	transmit := &udpTransport{conn: sender, index: iface.Index, source: [4]byte{127, 0, 0, 2}}
	receive := &udpTransport{conn: server, index: iface.Index, source: [4]byte{127, 0, 0, 1}}
	if err = server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = transmit.WritePacket([]byte("test"), netip.MustParseAddr("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	data, from, err := receive.ReadPacket()
	if err != nil || string(data) != "test" || from != netip.MustParseAddr("127.0.0.2") {
		t.Fatalf("%q %v %v", data, from, err)
	}
}
