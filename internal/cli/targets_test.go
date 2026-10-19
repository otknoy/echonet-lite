package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
)

type decodedTargetResult struct {
	Target string `json:"target"`
	Result string `json:"result"`
	Error  string `json:"error"`
	Status *struct {
		Temperature aircon.Field[int] `json:"temperature"`
	} `json:"status"`
}

type batchControl struct {
	fakeControl
	name    string
	started chan<- string
	release <-chan struct{}
	calls   atomic.Int32
}

func (b *batchControl) wait(ctx context.Context) error {
	b.calls.Add(1)
	if b.started != nil {
		b.started <- b.name
	}
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.err
}
func (b *batchControl) SetPower(ctx context.Context, on bool) error {
	if err := b.wait(ctx); err != nil {
		return err
	}
	return b.fakeControl.SetPower(ctx, on)
}
func (b *batchControl) SetMode(ctx context.Context, m aircon.Mode) error {
	if err := b.wait(ctx); err != nil {
		return err
	}
	return b.fakeControl.SetMode(ctx, m)
}
func (b *batchControl) SetTemperature(ctx context.Context, n int) error {
	if err := b.wait(ctx); err != nil {
		return err
	}
	return b.fakeControl.SetTemperature(ctx, n)
}
func (b *batchControl) Status(ctx context.Context) (aircon.Status, error) {
	err := b.wait(ctx)
	s, _ := b.fakeControl.Status(ctx)
	return s, err
}

type batchSession struct {
	fakeSession
	controls map[string]*batchControl
}

func (b *batchSession) Aircon(address netip.Addr, _ echonet.EOJ) (airconControl, error) {
	if ac, ok := b.controls[address.String()]; ok {
		return ac, nil
	}
	return nil, errors.New("unknown test device")
}
func TestMultipleTargetsRunConcurrently(t *testing.T) {
	path := testConfig(t)
	started := make(chan string, 2)
	release := make(chan struct{})
	living := &batchControl{name: "living", started: started, release: release}
	bedroom := &batchControl{name: "bedroom", started: started, release: release, fakeControl: fakeControl{err: &echonet.SettingUnknownError{Cause: context.DeadlineExceeded}}}
	client := &batchSession{controls: map[string]*batchControl{"192.0.2.10": living, "192.0.2.11": bedroom}}
	var out, diag bytes.Buffer
	opens := 0
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"aircon", "power", "on", "--target", "living", "--target", "bedroom", "--config", path, "--format", "json"}, &out, &diag, func(o echonet.Options) (session, error) {
			opens++
			if o.Interface != "" {
				return nil, errors.New("expected OS routing")
			}
			return client, nil
		})
	}()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("targets were not started in parallel")
		}
	}
	close(release)
	code := <-done
	var results []decodedTargetResult
	if code != 1 || opens != 1 || !client.closed || json.Unmarshal(out.Bytes(), &results) != nil || len(results) != 2 {
		t.Fatalf("%d opens=%d %s %s", code, opens, &out, &diag)
	}
	if results[0].Target != "living" || results[0].Result != "accepted" || results[1].Target != "bedroom" || results[1].Result != "error" || !strings.Contains(results[1].Error, "setting result unknown") {
		t.Fatalf("%+v", results)
	}
	if living.calls.Load() != 1 || bedroom.calls.Load() != 1 || !living.power {
		t.Fatal("not all devices operated once")
	}
}
func TestNamedOperationsAndDeduplication(t *testing.T) {
	path := testConfig(t)
	for _, command := range [][]string{{"status"}, {"power", "off"}, {"mode", "heat"}, {"temperature", "26"}} {
		client := &batchSession{controls: map[string]*batchControl{"192.0.2.10": {}, "192.0.2.11": {}}}
		args := append([]string{"aircon"}, command...)
		args = append(args, "--target", "bedroom", "--target", "living", "--target", "bedroom", "--config", path, "--format", "json")
		var out, diag bytes.Buffer
		code := run(context.Background(), args, &out, &diag, func(echonet.Options) (session, error) { return client, nil })
		var results []decodedTargetResult
		if code != 0 || json.Unmarshal(out.Bytes(), &results) != nil || len(results) != 2 || results[0].Target != "bedroom" || results[1].Target != "living" {
			t.Fatalf("%v: %d %s %s", command, code, &out, &diag)
		}
		for _, c := range client.controls {
			if c.calls.Load() != 1 {
				t.Fatal("duplicate operation")
			}
		}
		if command[0] == "status" && (results[0].Status == nil || results[1].Status == nil) {
			t.Fatal("lost status values")
		}
	}
}
func TestInvalidTargetSelectionDoesNotOpenNetwork(t *testing.T) {
	path := testConfig(t)
	for _, args := range [][]string{
		{"aircon", "power", "on", "--target", "living", "--target", "unknown"},
		{"aircon", "power", "on", "--target", "living", "--host", "192.0.2.10", "--eoj", "013001"},
		{"aircon", "status", "--target", ""},
		{"aircon", "discover", "--target", "living"},
		{"aircon", "temperature", "51", "--target", "living", "--target", "bedroom"},
	} {
		args = append(args, "--config", path)
		var out, diag bytes.Buffer
		code := run(context.Background(), args, &out, &diag, func(echonet.Options) (session, error) {
			t.Fatal("opened network for invalid selection")
			return nil, nil
		})
		if code != 2 {
			t.Fatalf("%v: %d %s", args, code, &diag)
		}
	}
}
func TestMultipleTargetsPartialStatusAndText(t *testing.T) {
	path := testConfig(t)
	for _, format := range []string{"json", "text"} {
		client := &batchSession{controls: map[string]*batchControl{"192.0.2.10": {}, "192.0.2.11": {fakeControl: fakeControl{err: context.DeadlineExceeded}}}}
		var out, diag bytes.Buffer
		code := run(context.Background(), []string{"aircon", "status", "--target", "living", "--target", "bedroom", "--config", path, "--format", format}, &out, &diag, func(echonet.Options) (session, error) { return client, nil })
		if code != 1 || !strings.Contains(diag.String(), "bedroom") {
			t.Fatalf("%d %s %s", code, &out, &diag)
		}
		if format == "json" {
			var results []decodedTargetResult
			if json.Unmarshal(out.Bytes(), &results) != nil || len(results) != 2 || results[1].Status == nil || results[1].Status.Temperature.Value == nil || results[1].Error == "" {
				t.Fatalf("%s", &out)
			}
		} else if !strings.Contains(out.String(), "living\tpower:") || !strings.Contains(out.String(), "bedroom\ttemperature:") {
			t.Fatal(&out)
		}
	}
}

func testGroupConfig(t *testing.T) string {
	t.Helper()
	path := testConfig(t)
	data := strings.Replace(configSample, "current_target: living", "current_target: upstairs", 1) + "\ngroups:\n  upstairs: [living, bedroom]\n  overlap: [bedroom, living]\n  single: [living]\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestGroupSelectionAndSwitching(t *testing.T) {
	path := testGroupConfig(t)
	for _, selection := range [][]string{nil, {"--target", "upstairs"}, {"--target", "living", "--target", "overlap", "--target", "bedroom"}} {
		client := &batchSession{controls: map[string]*batchControl{"192.0.2.10": {}, "192.0.2.11": {}}}
		args := append([]string{"aircon", "power", "on", "--config", path, "--format", "json"}, selection...)
		var out, diag bytes.Buffer
		code := run(context.Background(), args, &out, &diag, func(echonet.Options) (session, error) { return client, nil })
		var results []decodedTargetResult
		if code != 0 || json.Unmarshal(out.Bytes(), &results) != nil || len(results) != 2 {
			t.Fatalf("%d %s %s", code, &out, &diag)
		}
		for _, control := range client.controls {
			if control.calls.Load() != 1 {
				t.Fatal("overlapping group operated twice")
			}
		}
	}
	var out, diag bytes.Buffer
	noNetwork := func(echonet.Options) (session, error) { t.Fatal("config command opened network"); return nil, nil }
	if code := run(context.Background(), []string{"config", "list", "--config", path}, &out, &diag, noNetwork); code != 0 || !strings.Contains(out.String(), "* upstairs [group] living, bedroom") {
		t.Fatalf("%d %s %s", code, &out, &diag)
	}
	if code := run(context.Background(), []string{"config", "use", "single", "--config", path}, &out, &diag, noNetwork); code != 0 {
		t.Fatalf("%d %s", code, &diag)
	}
	out.Reset()
	diag.Reset()
	client := &batchSession{controls: map[string]*batchControl{"192.0.2.10": {}}}
	code := run(context.Background(), []string{"aircon", "power", "on", "--config", path, "--format", "json"}, &out, &diag, func(echonet.Options) (session, error) { return client, nil })
	var results []decodedTargetResult
	if code != 0 || json.Unmarshal(out.Bytes(), &results) != nil || len(results) != 1 || results[0].Target != "living" {
		t.Fatalf("%d %s %s", code, &out, &diag)
	}
}
func TestDirectAddressOverridesCurrentGroup(t *testing.T) {
	path := testGroupConfig(t)
	var out, diag bytes.Buffer
	client := &batchSession{controls: map[string]*batchControl{"192.0.2.11": {}}}
	code := run(context.Background(), []string{"aircon", "power", "on", "--host", "192.0.2.11", "--eoj", "013002", "--config", path, "--format", "json"}, &out, &diag, func(echonet.Options) (session, error) { return client, nil })
	if code != 0 || out.String() != "{\"status\":\"accepted\"}\n" {
		t.Fatalf("%d %s %s", code, &out, &diag)
	}
}
func TestInvalidGroupNeverOpensNetwork(t *testing.T) {
	path := testConfig(t)
	if err := os.WriteFile(path, []byte(configSample+"groups:\n  upstairs: [living, missing]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	code := run(context.Background(), []string{"aircon", "power", "on", "--target", "upstairs", "--config", path}, &out, &diag, func(echonet.Options) (session, error) { t.Fatal("opened network for invalid group"); return nil, nil })
	if code != 2 {
		t.Fatalf("%d %s", code, &diag)
	}
}
func TestMultipleTargetsCancellation(t *testing.T) {
	path := testConfig(t)
	release := make(chan struct{})
	started := make(chan string, 2)
	client := &batchSession{controls: map[string]*batchControl{"192.0.2.10": {name: "living", release: release, started: started}, "192.0.2.11": {name: "bedroom", release: release, started: started}}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out, diag bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"aircon", "power", "on", "--target", "living", "--target", "bedroom", "--config", path, "--format", "json"}, &out, &diag, func(echonet.Options) (session, error) { return client, nil })
	}()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("requests not started")
		}
	}
	cancel()
	select {
	case code := <-done:
		if code != 1 || !client.closed {
			t.Fatalf("%d %s %s", code, &out, &diag)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not complete")
	}
	var results []decodedTargetResult
	if json.Unmarshal(out.Bytes(), &results) != nil || len(results) != 2 || results[0].Result != "error" || results[1].Result != "error" {
		t.Fatal(&out)
	}
}
