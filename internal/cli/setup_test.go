package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otknoy/echonet-lite/echonet"
	"github.com/otknoy/echonet-lite/internal/config"
)

func setupInterfaces() ([]networkInterface, error) {
	return []networkInterface{{"eth0", netip.MustParseAddr("192.0.2.1")}, {"eth1", netip.MustParseAddr("192.0.2.2")}}, nil
}
func setupDevices() []echonet.DiscoveredNode {
	return []echonet.DiscoveredNode{
		{Address: netip.MustParseAddr("192.0.2.11"), Objects: []echonet.EOJ{{1, 0x30, 1}}},
		{Address: netip.MustParseAddr("192.0.2.10"), Objects: []echonet.EOJ{{1, 0x30, 2}, {0, 0x11, 1}, {1, 0x30, 1}, {1, 0x30, 1}}},
	}
}
func TestInteractiveInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "config.yaml")
	client := &fakeSession{nodes: setupDevices()}
	var out, diag bytes.Buffer
	// Retry invalid NIC, blank and duplicate names; skip one AC; select the second registration.
	input := strings.NewReader("9\neth1\ny\n\nliving\ny\nliving\n寝室\nn\n2\n")
	code := runWithInput(context.Background(), []string{"config", "init", "--config", path, "--timeout", "2s"}, input, &out, &diag, func(o echonet.Options) (session, error) {
		if o.Interface != "eth1" || o.Timeout != 2*time.Second {
			t.Fatal(o)
		}
		return client, nil
	}, setupInterfaces)
	if code != 0 || !client.closed {
		t.Fatalf("%d closed=%v %s %s", code, client.closed, &out, &diag)
	}
	doc, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Config.Interface != "eth1" || doc.Config.CurrentTarget != "寝室" || len(doc.Config.Targets) != 2 {
		t.Fatalf("%+v", doc.Config)
	}
	if doc.Config.Targets["living"] != (config.Target{Host: "192.0.2.10", EOJ: "013001"}) || doc.Config.Targets["寝室"].EOJ != "013002" {
		t.Fatalf("%+v", doc.Config.Targets)
	}
	if !strings.Contains(out.String(), "current_target") {
		t.Fatal("missing target prompt")
	}
}
func TestInitNoRegistrations(t *testing.T) {
	for _, found := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		client := &fakeSession{}
		input := "1\n"
		if found {
			client.nodes = setupDevices()
			input += "n\nn\nn\n"
		}
		var out, diag bytes.Buffer
		code := runWithInput(context.Background(), []string{"config", "init", "--config", path}, strings.NewReader(input), &out, &diag, func(echonet.Options) (session, error) { return client, nil }, setupInterfaces)
		if code != 0 || !client.closed {
			t.Fatalf("%d %s %s", code, &out, &diag)
		}
		doc, err := config.Load(path)
		if err != nil || doc.Config.Interface != "eth0" || doc.Config.CurrentTarget != "" || len(doc.Config.Targets) != 0 {
			t.Fatalf("%+v %v", doc, err)
		}
	}
}
func TestInitInterruptedOrDiscoveryFailed(t *testing.T) {
	for _, test := range []struct {
		name, input  string
		discoveryErr error
	}{
		{"no input", "", nil}, {"no name", "1\ny\n", nil}, {"no selection", "1\ny\nliving\nn\nn\n", nil},
		{"discovery error", "1\n", errors.New("discovery failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			client := &fakeSession{nodes: setupDevices(), err: test.discoveryErr}
			var out, diag bytes.Buffer
			code := runWithInput(context.Background(), []string{"config", "init", "--config", path}, strings.NewReader(test.input), &out, &diag, func(echonet.Options) (session, error) { return client, nil }, setupInterfaces)
			if code != 1 {
				t.Fatalf("%d %s %s", code, &out, &diag)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("saved incomplete config", err)
			}
			if test.input != "" && !client.closed {
				t.Fatal("discovery client not closed")
			}
		})
	}
}
func TestInitExistingFileDoesNotDiscover(t *testing.T) {
	path := testConfig(t)
	var out, diag bytes.Buffer
	code := runWithInput(context.Background(), []string{"config", "init", "--config", path}, strings.NewReader(""), &out, &diag, func(echonet.Options) (session, error) { t.Fatal("opened network"); return nil, nil }, func() ([]networkInterface, error) { t.Fatal("enumerated interfaces"); return nil, nil })
	if code != 1 {
		t.Fatal(code)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != configSample {
		t.Fatal("overwrote config", err)
	}
}
func TestInitNoUsableInterface(t *testing.T) {
	for _, enumerationErr := range []error{nil, errors.New("interface enumeration failed")} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		var out, diag bytes.Buffer
		code := runWithInput(context.Background(), []string{"config", "init", "--config", path}, strings.NewReader(""), &out, &diag, func(echonet.Options) (session, error) { t.Fatal("opened network"); return nil, nil }, func() ([]networkInterface, error) { return nil, enumerationErr })
		if code != 1 || diag.Len() == 0 {
			t.Fatal(code, &diag)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("saved config without interface", err)
		}
	}
}

// Signals once input is being read, and can be released to avoid leaking the read goroutine.
type waitingInput struct{ started, release chan struct{} }

func (w *waitingInput) Read([]byte) (int, error) { close(w.started); <-w.release; return 0, io.EOF }
func TestInitCancelWhileWaitingForInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	input := &waitingInput{make(chan struct{}), make(chan struct{})}
	defer close(input.release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		var out, diag bytes.Buffer
		done <- runWithInput(ctx, []string{"config", "init", "--config", path}, input, &out, &diag, func(echonet.Options) (session, error) { return nil, errors.New("unexpected network") }, setupInterfaces)
	}()
	select {
	case <-input.started:
	case <-time.After(time.Second):
		t.Fatal("input not requested")
	}
	cancel()
	select {
	case code := <-done:
		if code != 1 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked on input")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("saved cancelled setup", err)
	}
}

func TestForceInitSavesOnlyAfterSetupCompletes(t *testing.T) {
	for _, test := range []struct {
		name, input  string
		discoveryErr error
		success      bool
	}{
		{"completed", "1\ny\nnew-name\nn\nn\n1\n", nil, true},
		{"interrupted", "1\ny\n", nil, false},
		{"discovery failed", "1\n", errors.New("discovery failed"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := testConfig(t)
			client := &fakeSession{nodes: setupDevices(), err: test.discoveryErr}
			var out, diag bytes.Buffer
			code := runWithInput(context.Background(), []string{"config", "init", "--force", "--config", path}, strings.NewReader(test.input), &out, &diag, func(echonet.Options) (session, error) {
				original, err := os.ReadFile(path)
				if err != nil || string(original) != configSample {
					t.Fatal("config replaced before discovery", err)
				}
				return client, nil
			}, setupInterfaces)
			if (code == 0) != test.success {
				t.Fatalf("%d %s %s", code, &out, &diag)
			}
			if test.success {
				doc, err := config.Load(path)
				if err != nil || doc.Config.CurrentTarget != "new-name" {
					t.Fatal(doc, err)
				}
			} else {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != configSample {
					t.Fatal("config changed on failure", err)
				}
			}
		})
	}
}
