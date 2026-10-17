//go:build linux

package echonet

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"unsafe"
)

// udpTransport uses one socket for unicast and multicast. No port reuse is enabled.
type udpTransport struct {
	conn            *net.UDPConn
	index           int
	source          [4]byte
	discoveryOnce   sync.Once
	discoveryErr    error
	discoveryIndex  int
	discoverySource [4]byte
}

func selectInterface(name string) (*net.Interface, netip.Addr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, netip.Addr{}, err
	}
	var chosen *net.Interface
	var ip netip.Addr
	for _, i := range interfaces {
		if name != "" && i.Name != name {
			continue
		}
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagMulticast == 0 {
			continue
		}
		addresses, err := i.Addrs()
		if err != nil {
			return nil, ip, err
		}
		for _, a := range addresses {
			p, e := netip.ParsePrefix(a.String())
			if e == nil && p.Addr().Is4() && !p.Addr().IsLoopback() {
				if chosen != nil && chosen.Index != i.Index {
					return nil, ip, fmt.Errorf("%w: multiple interfaces; specify Interface", ErrInvalid)
				}
				c := i
				chosen = &c
				ip = p.Addr()
				break
			}
		}
	}
	if chosen == nil {
		return nil, ip, fmt.Errorf("%w: no usable IPv4 multicast interface %q", ErrInvalid, name)
	}
	return chosen, ip, nil
}

// With no interface, unicast packets use the OS routing table. Multicast
// membership is configured lazily, only when discovery is requested.
func openUDP(name string) (*udpTransport, netip.Addr, error) {
	var iface *net.Interface
	var ip netip.Addr
	var err error
	if name != "" {
		iface, ip, err = selectInterface(name)
		if err != nil {
			return nil, ip, err
		}
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 3610})
	if err != nil {
		return nil, ip, err
	}
	u := &udpTransport{conn: conn}
	if iface != nil {
		u.index = iface.Index
		u.source = ip.As4()
	}
	err = u.control(func(fd int) error {
		if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 1); err != nil {
			return err
		}
		return syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_PKTINFO, 1)
	})
	if err == nil && iface != nil {
		err = u.prepareDiscovery()
	}
	if err != nil {
		_ = conn.Close()
		return nil, ip, err
	}
	return u, ip, nil
}
func (u *udpTransport) control(apply func(int) error) error {
	raw, err := u.conn.SyscallConn()
	if err != nil {
		return err
	}
	var socketErr error
	if err = raw.Control(func(fd uintptr) { socketErr = apply(int(fd)) }); err != nil {
		return err
	}
	return socketErr
}
func (u *udpTransport) prepareDiscovery() error {
	u.discoveryOnce.Do(func() {
		index, source := u.index, u.source
		if index == 0 {
			iface, ip, err := selectInterface("")
			if err != nil {
				u.discoveryErr = err
				return
			}
			index, source = iface.Index, ip.As4()
		}
		u.discoveryErr = u.control(func(fd int) error {
			membership := &syscall.IPMreqn{Multiaddr: [4]byte{224, 0, 23, 0}, Address: source, Ifindex: int32(index)}
			if err := syscall.SetsockoptIPMreqn(fd, syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, membership); err != nil {
				return err
			}
			return syscall.SetsockoptIPMreqn(fd, syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, &syscall.IPMreqn{Address: source, Ifindex: int32(index)})
		})
		if u.discoveryErr == nil {
			u.discoveryIndex = index
			u.discoverySource = source
		}
	})
	return u.discoveryErr
}
func (u *udpTransport) ReadPacket() ([]byte, netip.Addr, error) {
	b := make([]byte, 65536)
	oob := make([]byte, syscall.CmsgSpace(12))
	for {
		n, nn, flags, from, err := u.conn.ReadMsgUDPAddrPort(b, oob)
		if err != nil {
			return nil, netip.Addr{}, err
		}
		if flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 {
			continue
		}
		controls, err := syscall.ParseSocketControlMessage(oob[:nn])
		if err != nil {
			continue
		}
		for _, cm := range controls {
			if cm.Header.Level == syscall.IPPROTO_IP && cm.Header.Type == syscall.IP_PKTINFO && u.acceptPacketInfo(cm.Data, from.Addr().Unmap()) {
				return b[:n], from.Addr().Unmap(), nil
			}
		}
	}
}
func (u *udpTransport) acceptPacketInfo(data []byte, from netip.Addr) bool {
	if len(data) < 12 || (u.index != 0 && int(binary.NativeEndian.Uint32(data[:4])) != u.index) {
		return false
	}
	// Ignore our own multicast requests even in OS-routed mode.
	var local [4]byte
	copy(local[:], data[4:8])
	return from != netip.AddrFrom4(local)
}

func (u *udpTransport) outgoingControl(to netip.Addr) ([]byte, error) {
	index, source := u.index, u.source
	if to.IsMulticast() {
		if err := u.prepareDiscovery(); err != nil {
			return nil, err
		}
		index, source = u.discoveryIndex, u.discoverySource
	}
	if index == 0 {
		return nil, nil
	}
	oob := make([]byte, syscall.CmsgSpace(12))
	header := (*syscall.Cmsghdr)(unsafe.Pointer(&oob[0]))
	header.Level = syscall.IPPROTO_IP
	header.Type = syscall.IP_PKTINFO
	header.SetLen(syscall.CmsgLen(12))
	data := oob[syscall.CmsgLen(0):]
	binary.NativeEndian.PutUint32(data[:4], uint32(index))
	copy(data[4:8], source[:])
	return oob, nil
}
func (u *udpTransport) WritePacket(b []byte, to netip.Addr) error {
	oob, err := u.outgoingControl(to)
	if err != nil {
		return err
	}
	_, _, err = u.conn.WriteMsgUDPAddrPort(b, oob, netip.AddrPortFrom(to, 3610))
	return err
}
func (u *udpTransport) Close() error { return u.conn.Close() }
