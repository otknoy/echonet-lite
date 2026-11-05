package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

// Preserve the v3 configuration format when updating the YAML implementation:
// quoted string names/EOJs, mapping indentation and non-compact sequences.
func TestCreateYAMLCompatibility(t *testing.T) {
	cfg := Config{
		Interface: "eth0", CurrentTarget: "on",
		Targets: map[string]Target{
			"on":   {Host: "192.0.2.10", EOJ: "013001"},
			"true": {Host: "192.0.2.11", EOJ: "013002"},
		},
		Groups: map[string][]string{"all": {"on", "true"}},
	}
	const want = `interface: eth0
current_target: "on"
targets:
    "on":
        host: 192.0.2.10
        eoj: "013001"
    "true":
        host: 192.0.2.11
        eoj: "013002"
groups:
    all:
        - "on"
        - "true"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Create(path, cfg, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("configuration formatting changed:\n%s\nwant:\n%s", data, want)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Config.CurrentTarget != "on" || doc.Config.Targets["true"].EOJ != "013002" {
		t.Fatalf("string values changed: %+v", doc.Config)
	}
}

func TestUseYAMLAliasCompatibility(t *testing.T) {
	path := writeConfig(t, `interface: eth0
current_target: living # selected
targets:
  living: &shared
    host: 192.0.2.10
    eoj: "013001"
  alias: *shared
  bedroom:
    host: 192.0.2.11
    eoj: "013002"
groups:
  all: [living, alias, bedroom] # members
`)
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = doc.Use("all"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{"&shared", "*shared", "# selected", "# members", "[living, alias, bedroom]"} {
		if !strings.Contains(string(data), preserved) {
			t.Errorf("lost %q:\n%s", preserved, data)
		}
	}
	doc, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := doc.Config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Name != "living" || targets[1].Name != "bedroom" {
		t.Fatalf("aliased targets changed: %+v", targets)
	}
}

func TestCreateKeepsLongScalarOnOneLine(t *testing.T) {
	name := strings.Repeat("living room ", 8) + "device"
	cfg := Config{CurrentTarget: name, Targets: map[string]Target{
		name: {Host: "192.0.2.10", EOJ: "013001"},
	}}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Create(path, cfg, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "current_target: "+name+"\n") {
		t.Fatalf("long selection was wrapped:\n%s", data)
	}
}

func TestLoadRejectsTrailingYAMLDocuments(t *testing.T) {
	for name, trailing := range map[string]string{
		"empty document":        "---\n",
		"another configuration": "---\ninterface: eth1\n",
		"malformed document":    "---\ntargets: [\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, sample+trailing)); err == nil {
				t.Fatal("accepted trailing YAML document")
			}
		})
	}
}

func TestLoadPreservesStructuredYAMLErrors(t *testing.T) {
	for name, data := range map[string]string{
		"unknown field":    sample + "unexpected: true\n",
		"wrong field type": strings.Replace(sample, "interface: eth0", "interface: [eth0]", 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, data)
			_, err := Load(path)
			var details *yaml.LoadErrors
			if !errors.As(err, &details) || len(details.Errors) == 0 {
				t.Fatalf("structured YAML error was lost: %v", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("config path was lost: %v", err)
			}
		})
	}
}
