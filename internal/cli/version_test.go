package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/otknoy/echonet-lite/echonet"
)

func TestModuleVersion(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"", "dev"},
		{"(devel)", "dev"},
		{"v0.0.3", "v0.0.3"},
		{"v0.0.4-0.20261117000000-0123456789ab", "v0.0.4-0.20261117000000-0123456789ab"},
	} {
		if got := moduleVersion(tt.input); got != tt.want {
			t.Errorf("moduleVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestVersionWithoutNetwork(t *testing.T) {
	for _, flag := range []string{"--version", "-v"} {
		t.Run(flag, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			code := run(context.Background(), []string{flag}, &out, &diagnostics, func(echonet.Options) (session, error) {
				t.Fatal("version must not open a socket")
				return nil, nil
			})
			if code != 0 || out.String() != "echonet version dev\n" || diagnostics.Len() != 0 {
				t.Fatalf("code=%d out=%q diagnostics=%q", code, &out, &diagnostics)
			}
		})
	}
}
