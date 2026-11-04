package netutil

import (
	"net/netip"
	"testing"
)

func TestIsUnicastIPv4(t *testing.T) {
	for _, tt := range []struct {
		address string
		want    bool
	}{
		{"192.0.2.10", true}, {"127.0.0.1", true}, {"169.254.1.1", true},
		{"0.0.0.0", false}, {"224.0.23.0", false}, {"255.255.255.255", false},
		{"::1", false}, {"::ffff:192.0.2.10", false},
	} {
		if got := IsUnicastIPv4(netip.MustParseAddr(tt.address)); got != tt.want {
			t.Errorf("%s: %v", tt.address, got)
		}
	}
	if IsUnicastIPv4(netip.Addr{}) {
		t.Fatal("invalid address accepted")
	}
}
