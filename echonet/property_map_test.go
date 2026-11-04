package echonet

import "testing"

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

func FuzzParsePropertyMap(f *testing.F) {
	for _, seed := range [][]byte{nil, {0}, {2, 0x80, 0xb0}, {2, 0x80}, {2, 0x80, 0x80}, {1, 1}, {16, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		epcs, err := parsePropertyMap(b)
		if err != nil {
			return
		}
		if len(epcs) != int(b[0]) {
			t.Fatalf("count mismatch: %x", b)
		}
		seen := map[EPC]bool{}
		for _, epc := range epcs {
			if epc < 0x80 || seen[epc] {
				t.Fatalf("invalid/duplicate EPC: %x", b)
			}
			seen[epc] = true
		}
		if b[0] >= 16 {
			for epc := 0x80; epc <= 0xff; epc++ {
				want := b[1+epc%16]&(1<<(epc/16-8)) != 0
				if seen[EPC(epc)] != want {
					t.Fatalf("bitmap mismatch: %x", b)
				}
			}
		}
	})
}
