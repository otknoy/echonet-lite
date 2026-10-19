package config

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/otknoy/echonet-lite/echonet"
)

// NamedTarget is an individual device after resolving the selected names.
type NamedTarget struct {
	Name   string
	Target Target
}

// Resolve uses CurrentTarget when names is empty, validates every selection,
// and returns each IP/EOJ once in first-selected order.
func (c Config) Resolve(names ...string) ([]NamedTarget, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		if c.CurrentTarget == "" {
			return nil, fmt.Errorf("no current target; use 'echonet config use NAME', --target NAME or specify both --host and --eoj")
		}
		names = []string{c.CurrentTarget}
	}
	type key struct {
		address netip.Addr
		eoj     echonet.EOJ
	}
	seen := map[key]bool{}
	result := make([]NamedTarget, 0, len(names))
	var expanded []string
	for _, name := range names {
		if members, ok := c.Groups[name]; ok {
			expanded = append(expanded, members...)
		} else {
			expanded = append(expanded, name)
		}
	}
	for _, name := range expanded {
		target, ok := c.Targets[name]
		if !ok {
			return nil, fmt.Errorf("target %q is not registered", name)
		}
		address, eoj, err := ParseTarget(target)
		if err != nil {
			return nil, fmt.Errorf("target %q: %w", name, err)
		}
		id := key{address, eoj}
		if seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, NamedTarget{Name: name, Target: target})
	}
	return result, nil
}

// Has includes both individual targets and groups, which share one namespace.
func (c Config) Has(name string) bool {
	_, target := c.Targets[name]
	_, group := c.Groups[name]
	return target || group
}
func (c Config) Validate() error {
	for name, target := range c.Targets {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("config: target name must not be empty")
		}
		if _, _, err := ParseTarget(target); err != nil {
			return fmt.Errorf("config target %q: %w", name, err)
		}
	}
	for name, members := range c.Groups {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("config: group name must not be empty")
		}
		if _, exists := c.Targets[name]; exists {
			return fmt.Errorf("config: target and group share the name %q", name)
		}
		if len(members) == 0 {
			return fmt.Errorf("config: group %q must contain at least one target", name)
		}
		for _, member := range members {
			if _, exists := c.Targets[member]; !exists {
				return fmt.Errorf("config: group %q member %q must name an individual target (no nested groups)", name, member)
			}
		}
	}
	if c.CurrentTarget != "" && !c.Has(c.CurrentTarget) {
		return fmt.Errorf("config: current target %q is not registered", c.CurrentTarget)
	}
	return nil
}
