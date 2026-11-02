package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"strings"
	"testing"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
)

type fakeSession struct {
	closed bool
	nodes  []echonet.DiscoveredNode
	err    error
}

func (f *fakeSession) Aircon(netip.Addr, echonet.EOJ) (airconControl, error) {
	return &fakeControl{}, nil
}
func (f *fakeSession) Discover(context.Context) ([]echonet.DiscoveredNode, error) {
	return f.nodes, f.err
}
func (f *fakeSession) Close() error { f.closed = true; return nil }
func TestArgumentsBeforeNetwork(t *testing.T) {
	for _, args := range []string{
		"aircon status --eoj 013001",
		"aircon status --host 192.0.2.1 --eoj 013001 --target living",
		"aircon status --host 192.0.2.1 --eoj 013001 --target=",
		"aircon invalid", "aircon power", "aircon power yes --host 192.0.2.1 --eoj 013001",
		"aircon status --host example.com --eoj 013001", "aircon status --host 192.0.2.1 --eoj 013000",
		"aircon temperature 51 --host 192.0.2.1 --eoj 013001", "aircon temperature 26.5 --host 192.0.2.1 --eoj 013001",
		"aircon mode other --host 192.0.2.1 --eoj 013001", "aircon discover --format yaml", "aircon discover --timeout -1s", "aircon discover --host 192.0.2.1", "aircon discover extra",
	} {
		t.Run(args, func(t *testing.T) {
			var out, diag bytes.Buffer
			opened := false
			code := run(context.Background(), strings.Fields(args), &out, &diag, func(echonet.Options) (session, error) { opened = true; return nil, errors.New("unexpected open") })
			if code != 2 || opened || diag.Len() == 0 {
				t.Fatalf("code=%d opened=%v diag=%s", code, opened, &diag)
			}
		})
	}
}
func TestDiscoveryJSONAndFiltering(t *testing.T) {
	fake := &fakeSession{nodes: []echonet.DiscoveredNode{{Address: netip.MustParseAddr("192.0.2.10"), Objects: []echonet.EOJ{{1, 0x30, 1}, {0, 0x11, 1}, {1, 0x30, 2}}}}}
	var out, diag bytes.Buffer
	code := run(context.Background(), strings.Fields("aircon discover --format json --interface eth0 --timeout 1s"), &out, &diag, func(o echonet.Options) (session, error) {
		if o.Interface != "eth0" || o.Timeout.Seconds() != 1 {
			t.Fatal(o)
		}
		return fake, nil
	})
	var result []map[string]any
	if code != 0 || !fake.closed || json.Unmarshal(out.Bytes(), &result) != nil || len(result) != 2 || result[0]["eoj"] != "013001" {
		t.Fatalf("code=%d %s %s", code, &out, &diag)
	}
}
func TestEmptyDiscoveryAndFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		fake := &fakeSession{}
		if failed {
			fake.err = context.Canceled
		}
		var out, diag bytes.Buffer
		code := run(context.Background(), strings.Fields("aircon discover --format json"), &out, &diag, func(echonet.Options) (session, error) { return fake, nil })
		if out.String() != "[]\n" || !fake.closed || (code == 0) == failed {
			t.Fatalf("%d %q %q", code, &out, &diag)
		}
	}
}
func TestHelpAndOpenFailure(t *testing.T) {
	var out, diag bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, &out, &diag); code != 0 || !strings.Contains(out.String(), "Usage:") {
		t.Fatal(code, &out)
	}
	out.Reset()
	code := run(context.Background(), strings.Fields("aircon status --host 192.0.2.10 --eoj 013001"), &out, &diag, func(echonet.Options) (session, error) { return nil, errors.New("port in use") })
	if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "port in use") {
		t.Fatalf("%d %s %s", code, &out, &diag)
	}
}

type fakeControl struct {
	err         error
	power       bool
	mode        aircon.Mode
	temperature int
}

func (f *fakeControl) Status(context.Context) (aircon.Status, error) {
	temp := 26
	mode := aircon.Cool
	return aircon.Status{Temperature: aircon.Field[int]{Value: &temp, State: "ok"}, Mode: aircon.Field[aircon.Mode]{Value: &mode, State: "ok"}, RoomTemperature: aircon.Field[int]{State: "error", Error: "missing"}}, f.err
}
func (f *fakeControl) SetPower(_ context.Context, on bool) error      { f.power = on; return f.err }
func (f *fakeControl) SetMode(_ context.Context, m aircon.Mode) error { f.mode = m; return f.err }
func (f *fakeControl) SetTemperature(_ context.Context, n int) error  { f.temperature = n; return f.err }

type controlSession struct {
	fakeSession
	control *fakeControl
}

func (f *controlSession) Aircon(netip.Addr, echonet.EOJ) (airconControl, error) {
	return f.control, nil
}
func TestStatusPreservesPartialJSON(t *testing.T) {
	fake := &controlSession{control: &fakeControl{err: context.DeadlineExceeded}}
	var out, diag bytes.Buffer
	code := run(context.Background(), strings.Fields("aircon status --host 192.0.2.10 --eoj 013001 --format json"), &out, &diag, func(echonet.Options) (session, error) { return fake, nil })
	var result map[string]map[string]any
	if code != 1 || !fake.closed || json.Unmarshal(out.Bytes(), &result) != nil || result["temperature"]["value"] != float64(26) || result["mode"]["value"] != "cool" || result["room_temperature"]["value"] != nil {
		t.Fatalf("%d %s %s", code, &out, &diag)
	}
}
func TestCLIControls(t *testing.T) {
	for _, command := range []string{"power on", "mode heat", "temperature 26"} {
		fake := &controlSession{control: &fakeControl{}}
		var out, diag bytes.Buffer
		args := strings.Fields("aircon " + command + " --host 192.0.2.10 --eoj 013001 --format json")
		code := run(context.Background(), args, &out, &diag, func(echonet.Options) (session, error) { return fake, nil })
		if code != 0 || !fake.closed || out.String() != "{\"status\":\"accepted\"}\n" {
			t.Fatalf("%d %s %s", code, &out, &diag)
		}
		if command == "power on" && !fake.control.power || command == "mode heat" && fake.control.mode != aircon.Heat || command == "temperature 26" && fake.control.temperature != 26 {
			t.Fatal("incorrect control value")
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestHelpOutputFailure(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}} {
		var diagnostics bytes.Buffer
		code := Run(context.Background(), args, failingWriter{}, &diagnostics)
		if code != 1 || strings.Count(diagnostics.String(), "output unavailable") != 1 {
			t.Fatalf("args=%v code=%d diagnostics=%s", args, code, &diagnostics)
		}
	}
}

// run supplies non-interactive input for command tests.
func run(ctx context.Context, args []string, out, diagnostics io.Writer, open opener) int {
	return execute(ctx, args, strings.NewReader(""), out, diagnostics, open, availableInterfaces)
}

func TestCommandsUseConfiguredOutput(t *testing.T) {
	path := testConfig(t)
	for _, args := range [][]string{
		{"aircon", "power", "on", "--host", "192.0.2.10", "--eoj", "013001", "--config", path},
		{"config", "list", "--config", path},
		{"completion", "bash"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			root := newRootCommand(strings.NewReader(""), io.Discard, io.Discard,
				func(echonet.Options) (session, error) { return &fakeSession{}, nil }, availableInterfaces)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if out.Len() == 0 {
				t.Fatal("command did not use the configured output")
			}
		})
	}
}

// Command values must be validated before configuration loading or target resolution.
func TestOperationInputBeforeConfiguration(t *testing.T) {
	for _, selection := range [][]string{
		nil,
		{"--config", "does-not-exist.yaml"},
	} {
		for _, tc := range []struct {
			args []string
			want string
		}{
			{[]string{"power", "yes"}, "power must be on or off"},
			{[]string{"mode", "other"}, "mode must be"},
			{[]string{"temperature", "51"}, "temperature must be"},
			{[]string{"temperature", "26.5"}, "temperature must be"},
		} {
			args := append([]string{"aircon"}, tc.args...)
			args = append(args, selection...)
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				var out, diag bytes.Buffer
				code := run(context.Background(), args, &out, &diag, func(echonet.Options) (session, error) {
					t.Fatal("invalid input must not open a session")
					return nil, nil
				})
				if code != 2 || out.Len() != 0 || !strings.Contains(diag.String(), tc.want) {
					t.Fatalf("code=%d stdout=%q stderr=%q", code, &out, &diag)
				}
			})
		}
	}
}
