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

func FuzzParseFrame(f *testing.F) {
	for _, wire := range []string{
		"1081000105ff010ef0016201d600", "1081000205ff010130016e0180013001bb00",
		"1081000201300105ff015e0000", "1081000105ff010ef0016200",
		"1081000105ff010ef0016201d60200", "1081000105ff010ef0016201d60000",
	} {
		seed, _ := hex.DecodeString(wire)
		f.Add(seed)
	}
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

func FuzzParseInstances(f *testing.F) {
	for _, seed := range [][]byte{nil, {0}, {1, 1, 0x30, 1}, {2, 1, 0x30, 1}, {1, 1, 0x30, 0}, {2, 1, 0x30, 1, 0}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		objects, err := parseInstances(b)
		if err != nil {
			return
		}
		if len(b) != 1+3*len(objects) || len(objects) > int(b[0]) || len(objects) > 84 {
			t.Fatalf("invalid accepted list: %x", b)
		}
		for i, e := range objects {
			if e[2] == 0 || !bytes.Equal(e[:], b[1+3*i:4+3*i]) {
				t.Fatalf("invalid decoded instance: %x", b)
			}
		}
	})
}
