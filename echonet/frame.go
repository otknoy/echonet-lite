package echonet

import (
	"encoding/binary"
	"fmt"
)

// frame is Format 1. SetGet has an additional, separate read group.
type frame struct {
	tid                        uint16
	seoj, deoj                 EOJ
	esv                        ESV
	properties, readProperties []Property
}

func setGet(esv ESV) bool { return esv == 0x6e || esv == 0x7e || esv == 0x5e }
func (f frame) marshal() ([]byte, error) {
	b := []byte{0x10, 0x81, byte(f.tid >> 8), byte(f.tid)}
	b = append(b, f.seoj[:]...)
	b = append(b, f.deoj[:]...)
	b = append(b, byte(f.esv))
	appendGroup := func(ps []Property) error {
		if (len(ps) < 1 && f.esv != 0x5e) || len(ps) > 255 {
			return fmt.Errorf("%w: property count", ErrInvalid)
		}
		b = append(b, byte(len(ps)))
		for _, p := range ps {
			if len(p.EDT) > 255 {
				return fmt.Errorf("%w: EDT too long", ErrInvalid)
			}
			b = append(b, byte(p.EPC), byte(len(p.EDT)))
			b = append(b, p.EDT...)
		}
		return nil
	}
	if err := appendGroup(f.properties); err != nil {
		return nil, err
	}
	if setGet(f.esv) {
		if err := appendGroup(f.readProperties); err != nil {
			return nil, err
		}
	}
	if len(b) > 1472 {
		return nil, fmt.Errorf("%w: frame exceeds 1472 bytes", ErrInvalid)
	}
	return b, nil
}
func parseFrame(b []byte) (frame, error) {
	var f frame
	if len(b) < 12 || b[0] != 0x10 || b[1] != 0x81 {
		return f, fmt.Errorf("invalid Format 1 header")
	}
	f.tid = binary.BigEndian.Uint16(b[2:4])
	copy(f.seoj[:], b[4:7])
	copy(f.deoj[:], b[7:10])
	f.esv = ESV(b[10])
	pos := 11
	group := func() ([]Property, error) {
		if pos >= len(b) {
			return nil, fmt.Errorf("missing OPC")
		}
		n := int(b[pos])
		pos++
		if n == 0 && f.esv != 0x5e {
			return nil, fmt.Errorf("zero OPC")
		}
		ps := make([]Property, 0, n)
		for range n {
			if len(b)-pos < 2 {
				return nil, fmt.Errorf("truncated property")
			}
			epc, size := EPC(b[pos]), int(b[pos+1])
			pos += 2
			if len(b)-pos < size {
				return nil, fmt.Errorf("truncated EDT")
			}
			ps = append(ps, Property{epc, append([]byte(nil), b[pos:pos+size]...)})
			pos += size
		}
		return ps, nil
	}
	var err error
	f.properties, err = group()
	if err != nil {
		return f, err
	}
	if setGet(f.esv) {
		f.readProperties, err = group()
		if err != nil {
			return f, err
		}
	}
	if pos != len(b) {
		return f, fmt.Errorf("trailing bytes")
	}
	return f, nil
}
