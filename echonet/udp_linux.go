//go:build linux

package echonet

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"unsafe"
)

// udpTransport uses one socket for unicast and multicast. No port reuse is enabled.
type udpTransport struct {
	conn   *net.UDPConn
	index  int
	source [4]byte
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
func openUDP(name string) (*udpTransport, netip.Addr, error) {
	iface, ip, err := selectInterface(name)
	if err != nil {
		return nil, ip, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 3610})
	if err != nil {
		return nil, ip, err
	}
	fail := func(e error) (*udpTransport, netip.Addr, error) { conn.Close(); return nil, ip, e }
	raw, err := conn.SyscallConn()
	if err != nil {
		return fail(err)
	}
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		addr := ip.As4()
		membership := &syscall.IPMreqn{Multiaddr: [4]byte{224, 0, 23, 0}, Address: addr, Ifindex: int32(iface.Index)}
		if sockErr = syscall.SetsockoptIPMreqn(int(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, membership); sockErr != nil {
			return
		}
		if sockErr = syscall.SetsockoptIPMreqn(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, &syscall.IPMreqn{Address: addr, Ifindex: int32(iface.Index)}); sockErr != nil {
			return
		}
		if sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 1); sockErr != nil {
			return
		}
		sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_PKTINFO, 1)
	})
	if err != nil {
		return fail(err)
	}
	if sockErr != nil {
		return fail(sockErr)
	}
	return &udpTransport{conn: conn, index: iface.Index, source: ip.As4()}, ip, nil
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
			if cm.Header.Level == syscall.IPPROTO_IP && cm.Header.Type == syscall.IP_PKTINFO && len(cm.Data) >= 12 && int(binary.NativeEndian.Uint32(cm.Data[:4])) == u.index {
				return b[:n], from.Addr().Unmap(), nil
			}
		}
	}
}
func (u *udpTransport) WritePacket(b []byte, to netip.Addr) error {
	oob := make([]byte, syscall.CmsgSpace(12))
	header := (*syscall.Cmsghdr)(unsafe.Pointer(&oob[0]))
	header.Level = syscall.IPPROTO_IP
	header.Type = syscall.IP_PKTINFO
	header.SetLen(syscall.CmsgLen(12))
	data := oob[syscall.CmsgLen(0):]
	binary.NativeEndian.PutUint32(data[:4], uint32(u.index))
	copy(data[4:8], u.source[:])
	_, _, err := u.conn.WriteMsgUDPAddrPort(b, oob, netip.AddrPortFrom(to, 3610))
	return err
}
func (u *udpTransport) Close() error { return u.conn.Close() }
