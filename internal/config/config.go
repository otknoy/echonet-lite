// Package config manages CLI configuration. It does not open network connections.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
	"github.com/otknoy/echonet-lite/internal/netutil"
	"go.yaml.in/yaml/v4"
)

type Target struct {
	Host string `yaml:"host"`
	EOJ  string `yaml:"eoj"`
}
type Config struct {
	Interface     string              `yaml:"interface"`
	CurrentTarget string              `yaml:"current_target"`
	Targets       map[string]Target   `yaml:"targets"`
	Groups        map[string][]string `yaml:"groups,omitempty"`
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
	if err != nil || !netutil.IsUnicastIPv4(address) {
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
	// Load requires exactly one document and retains its comments and styles.
	if err = yaml.Load(data, &doc.tree); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if len(doc.tree.Content) != 1 || doc.tree.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config %s: expected a YAML mapping", path)
	}
	// Node.Load has independent options; enforce known fields when converting.
	if err = doc.tree.Load(&doc.Config, yaml.WithKnownFields()); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if err = doc.Config.Validate(); err != nil {
		return nil, err
	}
	return doc, nil
}

func (d *Document) Use(name string) error {
	if _, err := d.Config.Resolve(name); err != nil {
		return err
	}
	// Clone the tree through YAML to leave the loaded document unchanged on failure.
	data, err := yaml.Dump(&d.tree)
	if err != nil {
		return err
	}
	var tree yaml.Node
	if err = yaml.Load(data, &tree); err != nil {
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
	data, err = yaml.Dump(&tree)
	if err != nil {
		return err
	}
	if err = atomicWrite(d.path, data, d.mode, os.Rename); err != nil {
		return err
	}
	d.tree = tree
	d.Config.CurrentTarget = name
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode, publish func(string, string) error) error {
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
	return publish(file.Name(), path)
}
