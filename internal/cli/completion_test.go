package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/otknoy/echonet-lite/echonet"
)

func completionRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, diag bytes.Buffer
	code := runWithInput(context.Background(), args, strings.NewReader(""), &out, &diag,
		func(echonet.Options) (session, error) { t.Fatal("completion opened network"); return nil, nil },
		func() ([]networkInterface, error) { return []networkInterface{{name: "eth0"}, {name: "wlan0"}}, nil })
	return code, out.String(), diag.String()
}

func TestCompletionCandidates(t *testing.T) {
	for _, test := range []struct {
		name        string
		words, want []string
		directive   string
	}{
		{"commands", []string{""}, []string{"aircon", "completion", "config", "help"}, ":4"},
		{"power", []string{"aircon", "power", "o"}, []string{"on", "off"}, ":4"},
		{"power consumed", []string{"aircon", "power", "on", ""}, nil, ":4"},
		{"mode", []string{"aircon", "mode", "c"}, []string{"cool"}, ":4"},
		{"format", []string{"aircon", "status", "--format", "j"}, []string{"text", "json"}, ":4"},
		{"equals", []string{"aircon", "status", "--format=j"}, []string{"text", "json"}, ":4"},
		{"interface", []string{"aircon", "discover", "--interface", "w"}, []string{"wlan0"}, ":4"},
		{"temperature", []string{"aircon", "temperature", "2"}, nil, ":4"},
		{"timeout", []string{"aircon", "status", "--timeout", ""}, nil, ":4"},
		{"config path", []string{"aircon", "status", "--config", ""}, nil, ":0"},
		{"shell", []string{"completion", "z"}, []string{"zsh"}, ":4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, output, diag := completionRun(t, append([]string{"__completeNoDesc"}, test.words...)...)
			if code != 0 {
				t.Fatalf("code=%d out=%s diag=%s", code, output, diag)
			}
			lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
			if lines[len(lines)-1] != test.directive {
				t.Fatalf("output=%q", output)
			}
			got := lines[:len(lines)-1]
			if len(got) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got=%q want=%q", got, test.want)
			}
		})
	}
}

func TestTargetCompletionDoesNotSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom config.yaml")
	data := configSample + "groups:\n  all rooms: [living, bedroom]\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, words := range [][]string{
		{"aircon", "status", "--config", path, "--target", ""},
		{"aircon", "status", "--config=" + path, "--target", ""},
		{"aircon", "power", "on", "--config", path, "--target", "living", "--target", ""},
		{"config", "use", "--config", path, ""},
	} {
		code, output, diag := completionRun(t, append([]string{"__completeNoDesc"}, words...)...)
		if code != 0 || output != "all rooms\nbedroom\nliving\n:4\n" {
			t.Fatalf("words=%q code=%d out=%q diag=%q", words, code, output, diag)
		}
	}
	code, output, diag := completionRun(t, "__completeNoDesc", "config", "use", "--config", path, "all ")
	if code != 0 || output != "all rooms\n:4\n" {
		t.Fatalf("code=%d out=%q diag=%q", code, output, diag)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != data {
		t.Fatalf("configuration changed: %s, %v", after, err)
	}
}

func TestUnavailableTargetCompletion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, configArgs := range [][]string{nil, {"--config", bad}, {"--config", bad + ".missing"}} {
		words := append([]string{"__completeNoDesc", "aircon", "status"}, configArgs...)
		words = append(words, "--target", "")
		code, output, diag := completionRun(t, words...)
		if code != 0 || output != ":4\n" {
			t.Fatalf("code=%d out=%q diag=%q", code, output, diag)
		}
	}
	code, output, _ := completionRun(t, "__completeNoDesc", "aircon", "power", "--config", bad, "o")
	if code != 0 || output != "on\noff\n:4\n" {
		t.Fatalf("code=%d out=%q", code, output)
	}
}

func TestShellCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			code, output, diag := completionRun(t, "completion", shell)
			if code != 0 || !strings.Contains(output, "__complete") {
				t.Fatalf("code=%d out=%q diag=%q", code, output, diag)
			}
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is unavailable; script execution not checked", shell)
			}
			cmd := exec.Command(binary, "-n")
			cmd.Stdin = strings.NewReader(output)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("syntax: %s: %v", out, err)
			}
		})
	}
	for _, args := range [][]string{{"completion"}, {"completion", "fish"}, {"completion", "bash", "extra"}, {"aircon"}, {"config"}, {"unknown"}} {
		code, _, diag := completionRun(t, args...)
		if code != 2 || diag == "" {
			t.Fatalf("args=%q code=%d diag=%q", args, code, diag)
		}
	}
}

func TestCompletionOutputFailure(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		var diag bytes.Buffer
		code := Run(context.Background(), []string{"completion", shell}, failingWriter{}, &diag)
		if code != 1 || !strings.Contains(diag.String(), "output unavailable") {
			t.Fatalf("code=%d diag=%q", code, &diag)
		}
	}
}
