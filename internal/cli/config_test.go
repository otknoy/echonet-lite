package cli

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otknoy/echonet-lite/echonet"
)

// Tests must never read or change the developer's real configuration.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "echonet-cli-tests- ")
	if err != nil {
		panic(err)
	}
	if err = os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	if err = os.RemoveAll(dir); err != nil {
		panic(err)
	}
	os.Exit(code)
}

const configSample = `interface: eth0
current_target: living
targets:
  living:
    host: 192.0.2.10
    eoj: "013001"
  bedroom:
    host: 192.0.2.11
    eoj: "013002"
`

func testConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(configSample), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type selectedSession struct {
	fakeSession
	address netip.Addr
	eoj     echonet.EOJ
}

func (s *selectedSession) Aircon(address netip.Addr, eoj echonet.EOJ) (airconControl, error) {
	s.address = address
	s.eoj = eoj
	return &fakeControl{}, nil
}
func TestConfigListAndPersistentUse(t *testing.T) {
	path := testConfig(t)
	noNetwork := func(echonet.Options) (session, error) { t.Fatal("config command opened network"); return nil, nil }
	var out, diag bytes.Buffer
	code := run(context.Background(), []string{"config", "list", "--config", path}, &out, &diag, noNetwork)
	if code != 0 || !strings.Contains(out.String(), "* living 192.0.2.10 013001") {
		t.Fatalf("%d %s %s", code, &out, &diag)
	}
	out.Reset()
	code = run(context.Background(), []string{"config", "use", "bedroom", "--config", path}, &out, &diag, noNetwork)
	if code != 0 || out.String() != "bedroom\n" {
		t.Fatalf("%d %s %s", code, &out, &diag)
	}
	client := &selectedSession{}
	code = run(context.Background(), []string{"aircon", "power", "on", "--config", path}, &out, &diag, func(o echonet.Options) (session, error) {
		if o.Interface != "eth0" {
			t.Fatal(o)
		}
		return client, nil
	})
	if code != 0 || client.address != netip.MustParseAddr("192.0.2.11") || client.eoj != (echonet.EOJ{1, 0x30, 2}) {
		t.Fatalf("%d %+v %s", code, client, &diag)
	}
}
func TestConfigOverrides(t *testing.T) {
	path := testConfig(t)
	for _, test := range []struct {
		args        []string
		iface, host string
		eoj         echonet.EOJ
	}{
		{[]string{"aircon", "status", "--config", path}, "eth0", "192.0.2.10", echonet.EOJ{1, 0x30, 1}},
		{[]string{"aircon", "status", "--config", path, "--interface", "eth1", "--host", "192.0.2.20", "--eoj", "013003"}, "eth1", "192.0.2.20", echonet.EOJ{1, 0x30, 3}},
		{[]string{"aircon", "status", "--config", path, "--interface="}, "", "192.0.2.10", echonet.EOJ{1, 0x30, 1}},
	} {
		client := &selectedSession{}
		var out, diag bytes.Buffer
		code := run(context.Background(), test.args, &out, &diag, func(o echonet.Options) (session, error) {
			if o.Interface != test.iface {
				t.Fatal(o)
			}
			return client, nil
		})
		if code != 0 || client.address.String() != test.host || client.eoj != test.eoj {
			t.Fatalf("%d %+v %s", code, client, &diag)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != configSample {
		t.Fatal("argument overrides modified config", err)
	}
}
func TestDefaultConfigAndDiscover(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	dir := filepath.Join(root, "echonet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("interface: eth2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	client := &fakeSession{}
	code := run(context.Background(), []string{"aircon", "discover"}, &out, &diag, func(o echonet.Options) (session, error) {
		if o.Interface != "eth2" {
			t.Fatal(o)
		}
		return client, nil
	})
	if code != 0 {
		t.Fatalf("%d %s", code, &diag)
	}
	if err := os.WriteFile(path, []byte(configSample), 0o600); err != nil {
		t.Fatal(err)
	}
	selected := &selectedSession{}
	code = run(context.Background(), []string{"aircon", "power", "off"}, &out, &diag, func(echonet.Options) (session, error) { return selected, nil })
	if code != 0 || selected.address.String() != "192.0.2.10" {
		t.Fatalf("%d %+v %s", code, selected, &diag)
	}
}
func TestInvalidConfigBeforeNetwork(t *testing.T) {
	path := testConfig(t)
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	badFile := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(badFile, []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"aircon", "status", "--config", path, "--host", "192.0.2.20"},
		{"aircon", "status", "--config", path, "--eoj", "013002"},
		{"aircon", "status", "--config", missing},
		{"aircon", "discover", "--config", badFile},
		{"aircon", "status", "--config="},
		{"config"}, {"config", "use"}, {"config", "use", "missing", "--config", path},
		{"config", "list", "--config", missing}, {"config", "list", "extra"}, {"config", "list", "--config="},
	} {
		var out, diag bytes.Buffer
		code := run(context.Background(), args, &out, &diag, func(echonet.Options) (session, error) {
			t.Fatal("opened network for invalid config")
			return nil, errors.New("unexpected open")
		})
		if code != 2 || diag.Len() == 0 {
			t.Fatalf("%v: %d %s", args, code, &diag)
		}
	}
}

func TestConfigInit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	for _, explicit := range []bool{false, true} {
		path := filepath.Join(root, "echonet", "config.yaml")
		args := []string{"config", "init", "--empty"}
		if explicit {
			path = filepath.Join(root, "custom", "config.yaml")
			args = append(args, "--config", path)
		}
		var out, diag bytes.Buffer
		noNetwork := func(echonet.Options) (session, error) { t.Fatal("init opened network"); return nil, nil }
		code := run(context.Background(), args, &out, &diag, noNetwork)
		if code != 0 || strings.TrimSpace(out.String()) != path {
			t.Fatalf("%d %s %s", code, &out, &diag)
		}
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out.Reset()
		diag.Reset()
		if code = run(context.Background(), args, &out, &diag, noNetwork); code != 1 {
			t.Fatalf("repeated init: %d", code)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, after) {
			t.Fatal("init overwrote config", err)
		}
	}
}
