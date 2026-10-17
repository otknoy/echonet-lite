package echonet

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Independently specified Get request: TID=1, controller -> node profile, D6.
func TestFrameKnownVector(t *testing.T) {
	wire, _ := hex.DecodeString("1081000105ff010ef0016201d600")
	f, err := parseFrame(wire)
	if err != nil {
		t.Fatal(err)
	}
	if f.tid != 1 || f.seoj != controllerEOJ || f.deoj != nodeEOJ || f.esv != Get || f.properties[0].EPC != 0xd6 {
		t.Fatalf("unexpected frame: %+v", f)
	}
	got, err := f.marshal()
	if err != nil || !bytes.Equal(got, wire) {
		t.Fatalf("marshal %x: %v", got, err)
	}
	for i := 0; i < len(wire); i++ {
		if _, err := parseFrame(wire[:i]); err == nil {
			t.Fatalf("accepted truncation at %d", i)
		}
	}
	for _, invalid := range []string{"1081000105ff010ef0016200", "1081000105ff010ef0016201d60200", "1081000105ff010ef0016201d60000", "1082000105ff010ef0016201d600"} {
		b, _ := hex.DecodeString(invalid)
		if _, err := parseFrame(b); err == nil {
			t.Errorf("accepted %s", invalid)
		}
	}
}
func TestSetGetGroups(t *testing.T) {
	wire, _ := hex.DecodeString("1081000205ff010130016e0180013001bb00")
	f, err := parseFrame(wire)
	if err != nil {
		t.Fatal(err)
	}
	if f.properties[0].EPC != 0x80 || f.readProperties[0].EPC != 0xbb {
		t.Fatal("groups lost")
	}
	b, err := f.marshal()
	if err != nil || !bytes.Equal(b, wire) {
		t.Fatalf("%x %v", b, err)
	}
	if _, err := parseFrame(wire[:15]); err == nil {
		t.Fatal("accepted missing Get group")
	}
}
func TestPropertyMapFormats(t *testing.T) {
	tests := []struct {
		wire []byte
		want int
		bad  bool
	}{
		{[]byte{0}, 0, false}, {[]byte{2, 0x80, 0xb0}, 2, false}, {[]byte{2, 0x80}, 0, true}, {[]byte{2, 0x80, 0x80}, 0, true}, {[]byte{1, 0x01}, 0, true},
	}
	bitmap := make([]byte, 17)
	bitmap[0] = 16
	bitmap[1] = 0xff
	bitmap[2] = 0xff
	tests = append(tests, struct {
		wire []byte
		want int
		bad  bool
	}{bitmap, 16, false})
	broken := append([]byte(nil), bitmap...)
	broken[0] = 17
	tests = append(tests, struct {
		wire []byte
		want int
		bad  bool
	}{broken, 0, true})
	for _, tt := range tests {
		v, err := parsePropertyMap(tt.wire)
		if (err != nil) != tt.bad || (!tt.bad && len(v) != tt.want) {
			t.Errorf("%x -> %v %v", tt.wire, v, err)
		}
	}
	v, _ := parsePropertyMap(bitmap)
	if v[0] != 0x80 || v[1] != 0x90 || v[8] != 0x81 {
		t.Fatalf("wrong bitmap mapping: %x", v)
	}
}
func FuzzParseFrame(f *testing.F) {
	seed, _ := hex.DecodeString("1081000105ff010ef0016201d600")
	f.Add(seed)
	f.Fuzz(func(t *testing.T, b []byte) {
		parsed, err := parseFrame(b)
		if err != nil {
			return
		}
		wire, err := parsed.marshal()
		if err == nil && !bytes.Equal(wire, b) {
			t.Fatalf("roundtrip: %x != %x", wire, b)
		}
	})
}

func TestUnsupportedSetGetZeroCounts(t *testing.T) {
	wire, _ := hex.DecodeString("1081000201300105ff015e0000")
	f, err := parseFrame(wire)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.marshal()
	if err != nil || !bytes.Equal(got, wire) {
		t.Fatalf("%x %v", got, err)
	}
}
