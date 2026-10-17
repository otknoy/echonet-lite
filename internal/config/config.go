// Package config manages CLI configuration. It does not open network connections.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
	"go.yaml.in/yaml/v3"
)

type Target struct {
	Host string `yaml:"host"`
	EOJ  string `yaml:"eoj"`
}
type Config struct {
	Interface     string            `yaml:"interface"`
	CurrentTarget string            `yaml:"current_target"`
	Targets       map[string]Target `yaml:"targets"`
}

// Document retains the YAML tree so changing the selection preserves comments.
type Document struct {
	Config Config
	path   string
	mode   os.FileMode
	tree   yaml.Node
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "echonet", "config.yaml"), nil
}

// ParseTarget applies the same validation to configured and explicit targets.
func ParseTarget(target Target) (netip.Addr, echonet.EOJ, error) {
	address, err := netip.ParseAddr(target.Host)
	if err != nil || !address.Is4() || address.IsUnspecified() || address.IsMulticast() || address == netip.MustParseAddr("255.255.255.255") {
		return netip.Addr{}, echonet.EOJ{}, fmt.Errorf("host must be a unicast IPv4 address")
	}
	eoj, err := echonet.ParseEOJ(target.EOJ)
	if err != nil || !aircon.IsAircon(eoj) {
		return netip.Addr{}, echonet.EOJ{}, fmt.Errorf("eoj must identify a household air conditioner instance (0130xx)")
	}
	return address, eoj, nil
}
func Load(path string) (*Document, error) {
	// Resolve a symlink before replacement so saving does not replace the link itself.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	doc := &Document{path: resolved, mode: info.Mode().Perm()}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err = decoder.Decode(&doc.Config); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	var extra yaml.Node
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("config %s: expected one YAML document", path)
	}
	if err = yaml.Unmarshal(data, &doc.tree); err != nil {
		return nil, err
	}
	if len(doc.tree.Content) != 1 || doc.tree.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config %s: expected a YAML mapping", path)
	}
	for name, target := range doc.Config.Targets {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("config: target name must not be empty")
		}
		if _, _, err = ParseTarget(target); err != nil {
			return nil, fmt.Errorf("config target %q: %w", name, err)
		}
	}
	if name := doc.Config.CurrentTarget; name != "" {
		if _, ok := doc.Config.Targets[name]; !ok {
			return nil, fmt.Errorf("config: current target %q is not registered", name)
		}
	}
	return doc, nil
}
func (c Config) Selected() (Target, error) {
	if c.CurrentTarget == "" {
		return Target{}, fmt.Errorf("no current target; use 'echonet config use NAME' or specify both --host and --eoj")
	}
	target, ok := c.Targets[c.CurrentTarget]
	if !ok {
		return Target{}, fmt.Errorf("target %q is not registered", c.CurrentTarget)
	}
	return target, nil
}
func (d *Document) Use(name string) error {
	if _, ok := d.Config.Targets[name]; !ok {
		return fmt.Errorf("target %q is not registered", name)
	}
	// Clone the tree through YAML to leave the loaded document unchanged on failure.
	data, err := yaml.Marshal(&d.tree)
	if err != nil {
		return err
	}
	var tree yaml.Node
	if err = yaml.Unmarshal(data, &tree); err != nil {
		return err
	}
	root := tree.Content[0]
	found := false
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "current_target" {
			value := root.Content[i+1]
			value.Kind = yaml.ScalarNode
			value.Tag = "!!str"
			value.Value = name
			value.Alias = nil
			value.Content = nil
			found = true
			break
		}
	}
	if !found {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "current_target"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name})
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err = encoder.Encode(&tree); err != nil {
		return err
	}
	if err = encoder.Close(); err != nil {
		return err
	}
	if err = atomicWrite(d.path, output.Bytes(), d.mode, os.Rename); err != nil {
		return err
	}
	d.tree = tree
	d.Config.CurrentTarget = name
	return nil
}
func atomicWrite(path string, data []byte, mode os.FileMode, rename func(string, string) error) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".echonet-config-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err = file.Chmod(mode); err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return rename(file.Name(), path)
}
