package cli

import (
	"fmt"

	"github.com/otknoy/echonet-lite/internal/config"
	"github.com/spf13/cobra"
)

// resolveAirconTargets validates all selected devices before opening a session.
// named retains the output format for explicit selections and current groups.
func resolveAirconTargets(cmd *cobra.Command, o *airconOptions) ([]selectedTarget, bool, error) {
	for _, name := range o.targets {
		if !config.ValidName(name) {
			return nil, false, fmt.Errorf("target name must not be empty")
		}
	}
	cfg, err := loadAirconConfig(cmd, o)
	if err != nil {
		return nil, false, err
	}
	var namedTargets []config.NamedTarget
	explicitHost := cmd.Flags().Changed("host")
	if explicitHost {
		namedTargets = []config.NamedTarget{{Target: config.Target{Host: o.host, EOJ: o.eoj}}}
	} else {
		namedTargets, err = cfg.Resolve(o.targets...)
		if err != nil {
			return nil, false, err
		}
	}
	targets := make([]selectedTarget, 0, len(namedTargets))
	for _, target := range namedTargets {
		address, eoj, err := config.ParseTarget(target.Target)
		if err != nil {
			return nil, false, err
		}
		targets = append(targets, selectedTarget{name: target.Name, address: address, eoj: eoj})
	}
	_, currentIsGroup := cfg.Groups[cfg.CurrentTarget]
	named := (!explicitHost && (len(o.targets) > 0 || currentIsGroup)) || len(targets) > 1
	return targets, named, nil
}
