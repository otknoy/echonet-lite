//go:build linux

package echonet

import (
	"bytes"
	"encoding/binary"
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

func TestUnicastUsesOSRoutingUnlessExplicit(t *testing.T) {
	unicast := netip.MustParseAddr("192.0.2.10")
	// A previous discovery selection must not pin future unicast traffic.
	automatic := &udpTransport{discoveryIndex: 7, discoverySource: [4]byte{192, 0, 2, 1}}
	automatic.discoveryOnce.Do(func() {})
	oob, err := automatic.outgoingControl(unicast)
	if err != nil || len(oob) != 0 {
		t.Fatalf("automatic unicast forced interface: %x %v", oob, err)
	}
	oob, err = automatic.outgoingControl(multicast)
	if err != nil {
		t.Fatal(err)
	}
	checkControl := func(oob []byte, index int, source [4]byte) {
		t.Helper()
		controls, err := syscall.ParseSocketControlMessage(oob)
		if err != nil || len(controls) != 1 {
			t.Fatalf("invalid packet info: %v %v", controls, err)
		}
		data := controls[0].Data
		if int(binary.NativeEndian.Uint32(data[:4])) != index || !bytes.Equal(data[4:8], source[:]) {
			t.Fatalf("wrong interface/source: %x", data)
		}
	}
	checkControl(oob, 7, [4]byte{192, 0, 2, 1})
	explicit := &udpTransport{index: 8, source: [4]byte{192, 0, 2, 2}}
	oob, err = explicit.outgoingControl(unicast)
	if err != nil {
		t.Fatal(err)
	}
	checkControl(oob, 8, [4]byte{192, 0, 2, 2})
}
func TestReceiveWithoutPinnedInterface(t *testing.T) {
	packetInfo := make([]byte, 12)
	binary.NativeEndian.PutUint32(packetInfo[:4], 9)
	copy(packetInfo[4:8], []byte{192, 0, 2, 1})
	automatic := &udpTransport{}
	if !automatic.acceptPacketInfo(packetInfo, netip.MustParseAddr("192.0.2.10")) {
		t.Fatal("automatic mode rejected peer interface")
	}
	if automatic.acceptPacketInfo(packetInfo, netip.MustParseAddr("192.0.2.1")) {
		t.Fatal("accepted own multicast request")
	}
	if automatic.acceptPacketInfo(packetInfo[:4], netip.MustParseAddr("192.0.2.10")) {
		t.Fatal("accepted truncated packet info")
	}
	explicit := &udpTransport{index: 8}
	if explicit.acceptPacketInfo(packetInfo, netip.MustParseAddr("192.0.2.10")) {
		t.Fatal("explicit mode accepted foreign interface")
	}
	binary.NativeEndian.PutUint32(packetInfo[:4], 8)
	if !explicit.acceptPacketInfo(packetInfo, netip.MustParseAddr("192.0.2.10")) {
		t.Fatal("explicit mode rejected matching interface")
	}
}
