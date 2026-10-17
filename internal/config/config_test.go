package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# air conditioners
interface: eth0 # LAN
current_target: living # selected

targets:
  living:
    host: 192.0.2.10
    eoj: "013001" # keep leading zero
  bedroom:
    host: 192.0.2.11
    eoj: "013002"
`

func writeConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(data), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestLoadAndUse(t *testing.T) {
	path := writeConfig(t, sample)
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	target, err := doc.Config.Selected()
	if err != nil || target.Host != "192.0.2.10" || target.EOJ != "013001" {
		t.Fatalf("%+v %v", target, err)
	}
	if err = doc.Use("bedroom"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	target, err = reloaded.Config.Selected()
	if err != nil || target.Host != "192.0.2.11" || target.EOJ != "013002" {
		t.Fatalf("%+v %v", target, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{"# air conditioners", "# LAN", "# selected", "# keep leading zero"} {
		if !strings.Contains(string(data), comment) {
			t.Errorf("lost comment %q", comment)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("permissions: %v", info.Mode())
	}
}
func TestUseAddsSelectionAndKeepsSymlink(t *testing.T) {
	path := writeConfig(t, strings.Replace(sample, "current_target: living # selected\n", "", 1))
	link := filepath.Join(filepath.Dir(path), "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = doc.Config.Selected(); err == nil {
		t.Fatal("selected target should be absent")
	}
	if err = doc.Use("living"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced symlink", err)
	}
	doc, err = Load(path)
	if err != nil || doc.Config.CurrentTarget != "living" {
		t.Fatal(doc, err)
	}
}
func TestInvalidConfigs(t *testing.T) {
	for name, data := range map[string]string{
		"empty": "", "null": "null", "list": "[]", "unknown": "interfase: eth0", "multiple": sample + "---\ninterface: eth1\n",
		"duplicate": sample + "current_target: bedroom\n", "missing target": strings.Replace(sample, "current_target: living", "current_target: absent", 1),
		"bad host": strings.Replace(sample, "192.0.2.10", "example.com", 1), "bad eoj": strings.Replace(sample, "013001", "013000", 1),
		"bad class": strings.Replace(sample, "013001", "001101", 1), "empty name": strings.Replace(sample, "  living:", "  \"\":", 1),
		"incomplete pair": strings.Replace(sample, "    eoj: \"013002\"\n", "", 1), "unknown target field": strings.Replace(sample, "    host: 192.0.2.11", "    hostname: 192.0.2.11", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, data)); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}
func TestInterfaceOnly(t *testing.T) {
	doc, err := Load(writeConfig(t, "interface: eth0\n"))
	if err != nil || doc.Config.Interface != "eth0" {
		t.Fatal(doc, err)
	}
	if _, err = doc.Config.Selected(); err == nil {
		t.Fatal("accepted missing selection")
	}
}
func TestFailedSavePreservesOriginal(t *testing.T) {
	path := writeConfig(t, sample)
	injected := errors.New("rename failed")
	err := atomicWrite(path, []byte("replacement"), 0o640, func(string, string) error { return injected })
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	assertOriginal := func() {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil || string(data) != sample {
			t.Fatalf("original changed: %s %v", data, err)
		}
	}
	assertOriginal()
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatalf("temporary file leaked: %v %v", files, err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = doc.Use("absent"); err == nil {
		t.Fatal("unknown target accepted")
	}
	assertOriginal()
	doc.path = filepath.Join(filepath.Dir(path), "missing", "config.yaml")
	if err = doc.Use("bedroom"); err == nil {
		t.Fatal("save unexpectedly succeeded")
	}
	if doc.Config.CurrentTarget != "living" {
		t.Fatal("in-memory selection changed after failed save")
	}
	assertOriginal()
}
func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := DefaultPath()
	if err != nil || path != filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "echonet", "config.yaml") {
		t.Fatal(path, err)
	}
}
