package config

import (
	"os"
	"strings"
	"testing"
)

func TestResolveTargets(t *testing.T) {
	cfg := Config{CurrentTarget: "living", Targets: map[string]Target{
		"living":  {Host: "192.0.2.10", EOJ: "013001"},
		"bedroom": {Host: "192.0.2.11", EOJ: "013002"},
		"alias":   {Host: "192.0.2.10", EOJ: "013001"},
	}}
	found, err := cfg.Resolve()
	if err != nil || len(found) != 1 || found[0].Name != "living" {
		t.Fatal(found, err)
	}
	found, err = cfg.Resolve("bedroom", "living", "alias", "bedroom")
	if err != nil || len(found) != 2 || found[0].Name != "bedroom" || found[1].Name != "living" {
		t.Fatal(found, err)
	}
	if _, err = cfg.Resolve("living", "missing"); err == nil {
		t.Fatal("accepted unknown name")
	}
	cfg.Targets["bad"] = Target{Host: "bad", EOJ: "013001"}
	if _, err = cfg.Resolve("living", "bad"); err == nil {
		t.Fatal("accepted invalid target")
	}
	cfg.CurrentTarget = ""
	if _, err = cfg.Resolve(); err == nil {
		t.Fatal("accepted missing selection")
	}
}

func TestResolveGroups(t *testing.T) {
	cfg := Config{CurrentTarget: "upstairs", Targets: map[string]Target{
		"living": {Host: "192.0.2.10", EOJ: "01300a"}, "alias": {Host: "192.0.2.10", EOJ: "01300A"}, "bedroom": {Host: "192.0.2.10", EOJ: "013002"},
	}, Groups: map[string][]string{"upstairs": {"living", "bedroom", "living"}, "overlap": {"alias", "bedroom"}}}
	targets, err := cfg.Resolve()
	if err != nil || len(targets) != 2 || targets[0].Name != "living" || targets[1].Name != "bedroom" {
		t.Fatal(targets, err)
	}
	targets, err = cfg.Resolve("overlap", "upstairs", "bedroom")
	if err != nil || len(targets) != 2 || targets[0].Name != "alias" || targets[1].Name != "bedroom" {
		t.Fatal(targets, err)
	}
}
func TestInvalidGroups(t *testing.T) {
	for name, extra := range map[string]string{
		"empty":          "groups:\n  upstairs: []\n",
		"missing member": "groups:\n  upstairs: [living, missing]\n",
		"nested":         "groups:\n  upstairs: [living]\n  all: [upstairs]\n",
		"self":           "groups:\n  all: [all]\n",
		"name collision": "groups:\n  living: [bedroom]\n",
		"blank name":     "groups:\n  \"\": [living]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, sample+extra)); err == nil {
				t.Fatal("invalid group accepted")
			}
		})
	}
}
func TestUseGroupPreservesConfiguration(t *testing.T) {
	path := writeConfig(t, sample+"\n# shared controls\ngroups:\n  upstairs: [living, bedroom] # members\n")
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = doc.Use("upstairs"); err != nil {
		t.Fatal(err)
	}
	doc, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	found, err := doc.Config.Resolve()
	if err != nil || len(found) != 2 || doc.Config.CurrentTarget != "upstairs" {
		t.Fatal(found, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "# members") || !strings.Contains(string(data), "# shared controls") {
		t.Fatal("lost group comments", err)
	}
}
