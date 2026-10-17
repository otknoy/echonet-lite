// Package echonet provides ECHONET Lite object and property operations over IPv4 UDP.
package echonet

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
)

type EOJ [3]byte

func (e EOJ) String() string               { return hex.EncodeToString(e[:]) }
func (e EOJ) MarshalText() ([]byte, error) { return []byte(e.String()), nil }
func ParseEOJ(s string) (EOJ, error) {
	var e EOJ
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 3 {
		return e, fmt.Errorf("EOJ must be six hexadecimal digits")
	}
	copy(e[:], b)
	return e, nil
}

type EPC byte
type ESV byte

const (
	SetC    ESV = 0x61
	Get     ESV = 0x62
	SetRes  ESV = 0x71
	GetRes  ESV = 0x72
	SetCSNA ESV = 0x51
	GetSNA  ESV = 0x52
	INF     ESV = 0x73
	INFC    ESV = 0x74
	INFCRes ESV = 0x7a
)
const (
	AnnouncementPropertyMap EPC = 0x9d
	SetPropertyMap          EPC = 0x9e
	GetPropertyMap          EPC = 0x9f
)

var (
	ErrClosed      = errors.New("echonet: client closed")
	ErrInvalid     = errors.New("echonet: invalid input")
	ErrUnsupported = errors.New("echonet: unsupported property")
	ErrBusy        = errors.New("echonet: too many pending requests")
)

// Property holds wire EDT bytes without device-specific conversion.
type Property struct {
	EPC EPC    `json:"epc"`
	EDT []byte `json:"edt"`
}

// Response preserves both successful and unavailable properties in an SNA response.
type Response struct {
	Address    netip.Addr `json:"address"`
	TID        uint16     `json:"tid"`
	SEOJ       EOJ        `json:"seoj"`
	DEOJ       EOJ        `json:"deoj"`
	ESV        ESV        `json:"esv"`
	Properties []Property `json:"properties"`
}

func (r *Response) Property(epc EPC) ([]byte, bool) {
	if r != nil {
		for _, p := range r.Properties {
			if p.EPC == epc {
				return p.EDT, true
			}
		}
	}
	return nil, false
}

type ServiceError struct{ Response *Response }

func (e *ServiceError) Error() string {
	return fmt.Sprintf("echonet: service rejected (ESV=%02x)", byte(e.Response.ESV))
}

// SettingUnknownError means a write may have taken effect; callers must not retry blindly.
type SettingUnknownError struct{ Cause error }

func (e *SettingUnknownError) Error() string {
	return "echonet: setting result unknown: " + e.Cause.Error()
}
func (e *SettingUnknownError) Unwrap() error { return e.Cause }

type Node struct {
	client  *Client
	address netip.Addr
}

func (n *Node) Address() netip.Addr    { return n.address }
func (n *Node) Object(eoj EOJ) *Object { return &Object{node: n, eoj: eoj} }

type Object struct {
	node *Node
	eoj  EOJ
}

func (o *Object) EOJ() EOJ            { return o.eoj }
func (o *Object) Address() netip.Addr { return o.node.address }

// DiscoveredNode is a snapshot; addresses are not persistent device identities.
type DiscoveredNode struct {
	Address netip.Addr `json:"address"`
	Objects []EOJ      `json:"objects"`
}
