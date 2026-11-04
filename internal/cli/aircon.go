package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
	"github.com/otknoy/echonet-lite/internal/config"
	"github.com/spf13/cobra"
)

type airconOptions struct {
	configPath, iface, format, host, eoj string
	timeout                              time.Duration
	targets                              []string
}

func newAirconCommand(open opener, interfaces interfaceLister) *cobra.Command {
	air := &cobra.Command{Use: "aircon", Short: "Discover and control air conditioners", RunE: requireSubcommand}
	air.AddCommand(
		newDiscoverCommand(open, interfaces),
		newStatusCommand(open, interfaces),
		newPowerCommand(open, interfaces),
		newModeCommand(open, interfaces),
		newTemperatureCommand(open, interfaces),
	)
	return air
}

func newStatusCommand(open opener, interfaces interfaceLister) *cobra.Command {
	o := &airconOptions{}
	cmd := &cobra.Command{
		Use: "status", Short: "Air conditioner status",
		Args: cobra.NoArgs, ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAirconOperation(cmd, open, o, func(ctx context.Context, ac airconControl) (*aircon.Status, error) {
				status, err := ac.Status(ctx)
				return &status, err
			})
		},
	}
	registerAirconFlags(cmd, o, interfaces)
	registerTargetFlags(cmd, o)
	return cmd
}

func newPowerCommand(open opener, interfaces interfaceLister) *cobra.Command {
	o := &airconOptions{}
	cmd := &cobra.Command{
		Use: "power on|off", Short: "Air conditioner power",
		Args: cobra.ExactArgs(1), ValidArgsFunction: positionalValues("on", "off"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "on" && args[0] != "off" {
				return fmt.Errorf("power must be on or off")
			}
			on := args[0] == "on"
			return runAirconOperation(cmd, open, o, func(ctx context.Context, ac airconControl) (*aircon.Status, error) {
				return nil, ac.SetPower(ctx, on)
			})
		},
	}
	registerAirconFlags(cmd, o, interfaces)
	registerTargetFlags(cmd, o)
	return cmd
}

func newModeCommand(open opener, interfaces interfaceLister) *cobra.Command {
	o := &airconOptions{}
	cmd := &cobra.Command{
		Use: "mode auto|cool|heat|dry|fan", Short: "Air conditioner mode",
		Args: cobra.ExactArgs(1), ValidArgsFunction: positionalValues("auto", "cool", "heat", "dry", "fan"),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := aircon.ParseMode(args[0])
			if err != nil {
				return err
			}
			return runAirconOperation(cmd, open, o, func(ctx context.Context, ac airconControl) (*aircon.Status, error) {
				return nil, ac.SetMode(ctx, mode)
			})
		},
	}
	registerAirconFlags(cmd, o, interfaces)
	registerTargetFlags(cmd, o)
	return cmd
}

func newTemperatureCommand(open opener, interfaces interfaceLister) *cobra.Command {
	o := &airconOptions{}
	cmd := &cobra.Command{
		Use: "temperature VALUE", Short: "Air conditioner temperature",
		Args: cobra.ExactArgs(1), ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			temperature, err := strconv.Atoi(args[0])
			if err != nil || temperature < 0 || temperature > 50 {
				return fmt.Errorf("temperature must be an integer from 0 to 50")
			}
			return runAirconOperation(cmd, open, o, func(ctx context.Context, ac airconControl) (*aircon.Status, error) {
				return nil, ac.SetTemperature(ctx, temperature)
			})
		},
	}
	registerAirconFlags(cmd, o, interfaces)
	registerTargetFlags(cmd, o)
	return cmd
}

func registerAirconFlags(cmd *cobra.Command, o *airconOptions, interfaces interfaceLister) {
	f := cmd.Flags()
	f.StringVar(&o.configPath, "config", "", "configuration file")
	f.StringVar(&o.iface, "interface", "", "force IPv4 interface (default: OS routing; discovery auto-selects)")
	f.DurationVar(&o.timeout, "timeout", 3*time.Second, "request or discovery timeout")
	f.StringVar(&o.format, "format", "text", "text or json")

	mustRegisterCompletion(cmd, "format", cobra.FixedCompletions([]string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp))
	mustRegisterCompletion(cmd, "timeout", cobra.NoFileCompletions)
	mustRegisterCompletion(cmd, "interface", func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		all, err := interfaces()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := make([]string, 0, len(all))
		for _, nic := range all {
			if strings.HasPrefix(nic.name, prefix) {
				names = append(names, nic.name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})
}

func registerTargetFlags(cmd *cobra.Command, o *airconOptions) {
	f := cmd.Flags()
	f.StringVar(&o.host, "host", "", "target IPv4 address")
	f.StringVar(&o.eoj, "eoj", "", "target EOJ (six hexadecimal digits)")
	// StringArray preserves commas and order in configured names.
	f.StringArrayVar(&o.targets, "target", nil, "configured target or group name (repeatable)")
	cmd.MarkFlagsRequiredTogether("host", "eoj")
	cmd.MarkFlagsMutuallyExclusive("target", "host")
	cmd.MarkFlagsMutuallyExclusive("target", "eoj")
	mustRegisterCompletion(cmd, "target", completeTargets)
	for _, flag := range []string{"host", "eoj"} {
		mustRegisterCompletion(cmd, flag, cobra.NoFileCompletions)
	}
}

// loadAirconConfig validates common options before reading configuration.
func loadAirconConfig(cmd *cobra.Command, o *airconOptions) (config.Config, error) {
	if o.timeout <= 0 {
		return config.Config{}, fmt.Errorf("timeout must be positive")
	}
	if o.format != "text" && o.format != "json" {
		return config.Config{}, fmt.Errorf("format must be text or json")
	}
	if cmd.Flags().Changed("config") && o.configPath == "" {
		return config.Config{}, fmt.Errorf("--config requires a path")
	}
	doc, err := loadConfig(o.configPath, cmd.Flags().Changed("config"))
	if err != nil {
		return config.Config{}, err
	}
	if doc == nil {
		return config.Config{}, nil
	}
	return doc.Config, nil
}

func runAirconOperation(cmd *cobra.Command, open opener, o *airconOptions, op operation) error {
	for _, name := range o.targets {
		if !config.ValidName(name) {
			return fmt.Errorf("target name must not be empty")
		}
	}
	cfg, err := loadAirconConfig(cmd, o)
	if err != nil {
		return err
	}
	var namedTargets []config.NamedTarget
	explicitHost := cmd.Flags().Changed("host")
	if explicitHost {
		namedTargets = []config.NamedTarget{{Target: config.Target{Host: o.host, EOJ: o.eoj}}}
	} else {
		namedTargets, err = cfg.Resolve(o.targets...)
		if err != nil {
			return err
		}
	}
	targets := make([]selectedTarget, 0, len(namedTargets))
	for _, target := range namedTargets {
		address, eoj, err := config.ParseTarget(target.Target)
		if err != nil {
			return err
		}
		targets = append(targets, selectedTarget{name: target.Name, address: address, eoj: eoj})
	}
	client, err := open(echonet.Options{Interface: o.iface, Timeout: o.timeout})
	if err != nil {
		return executionFailure(err)
	}
	defer func() { _ = client.Close() }()
	_, currentIsGroup := cfg.Groups[cfg.CurrentTarget]
	named := (!explicitHost && (len(o.targets) > 0 || currentIsGroup)) || len(targets) > 1
	results, requestErr := operateTargets(cmd.Context(), client, targets, op, named)
	outputErr := writeTargetResults(cmd.OutOrStdout(), results, named, o.format)
	return executionFailure(errors.Join(requestErr, outputErr))
}
