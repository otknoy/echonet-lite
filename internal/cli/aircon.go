package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
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

func newDiscoverCommand(open opener, interfaces interfaceLister) *cobra.Command {
	o := &airconOptions{}
	cmd := &cobra.Command{
		Use: "discover", Short: "Air conditioner discover",
		Args: cobra.NoArgs, ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAircon(cmd, args, open, o)
		},
	}
	registerAirconFlags(cmd, o, interfaces)
	return cmd
}

func newStatusCommand(open opener, interfaces interfaceLister) *cobra.Command {
	o := &airconOptions{}
	cmd := &cobra.Command{
		Use: "status", Short: "Air conditioner status",
		Args: cobra.NoArgs, ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAircon(cmd, args, open, o)
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
			return runAircon(cmd, args, open, o)
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
			return runAircon(cmd, args, open, o)
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
			return runAircon(cmd, args, open, o)
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

func runAircon(cmd *cobra.Command, args []string, open opener, o *airconOptions) error {
	ctx, out := cmd.Context(), cmd.OutOrStdout()
	command := cmd.Name()
	value := ""
	if len(args) > 0 {
		value = args[0]
	}
	fs := cmd.Flags()
	iface := o.iface
	targetNames := o.targets
	for _, name := range targetNames {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("target name must not be empty")
		}
	}
	if o.timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if o.format != "text" && o.format != "json" {
		return fmt.Errorf("format must be text or json")
	}

	if fs.Changed("config") && o.configPath == "" {
		return fmt.Errorf("--config requires a path")
	}
	doc, err := loadConfig(o.configPath, fs.Changed("config"))
	if err != nil {
		return err
	}
	var cfg config.Config
	if doc != nil {
		cfg = doc.Config
	}
	if command == "discover" && !fs.Changed("interface") {
		iface = cfg.Interface
	}
	var targets []selectedTarget
	var mode aircon.Mode
	var temperature int
	if command != "discover" {
		var named []config.NamedTarget
		if fs.Changed("host") {
			named = []config.NamedTarget{{Target: config.Target{Host: o.host, EOJ: o.eoj}}}
		} else {
			named, err = cfg.Resolve(targetNames...)
			if err != nil {
				return err
			}
		}
		for _, target := range named {
			address, eoj, err := config.ParseTarget(target.Target)
			if err != nil {
				return err
			}
			targets = append(targets, selectedTarget{name: target.Name, address: address, eoj: eoj})
		}
	}
	switch command {
	case "power":
		if value != "on" && value != "off" {
			return fmt.Errorf("power must be on or off")
		}
	case "mode":
		mode, err = aircon.ParseMode(value)
		if err != nil {
			return err
		}
	case "temperature":
		temperature, err = strconv.Atoi(value)
		if err != nil || temperature < 0 || temperature > 50 {
			return fmt.Errorf("temperature must be an integer from 0 to 50")
		}
	}
	client, err := open(echonet.Options{Interface: iface, Timeout: o.timeout})
	if err != nil {
		return executionFailure(err)
	}
	// Client.Close is idempotent and currently always returns nil.
	defer func() { _ = client.Close() }()
	if command == "discover" {
		nodes, requestErr := client.Discover(ctx)
		type found struct {
			Host netip.Addr  `json:"host"`
			EOJ  echonet.EOJ `json:"eoj"`
		}
		result := []found{}
		for _, n := range nodes {
			for _, e := range n.Objects {
				if aircon.IsAircon(e) {
					result = append(result, found{n.Address, e})
				}
			}
		}
		if o.format == "json" {
			err = json.NewEncoder(out).Encode(result)
		} else {
			for _, r := range result {
				if _, err = fmt.Fprintf(out, "%s %s\n", r.Host, r.EOJ); err != nil {
					break
				}
			}
		}
		if err != nil {
			return executionFailure(err)
		}
		if requestErr != nil {
			return executionFailure(requestErr)
		}
		return nil
	}
	_, currentIsGroup := cfg.Groups[cfg.CurrentTarget]
	named := !fs.Changed("host") && (len(targetNames) > 0 || currentIsGroup)
	op := operation{command: command, power: value == "on", mode: mode, temperature: temperature}
	named = named || len(targets) > 1
	results, requestErr := operateTargets(ctx, client, targets, op, named)
	outputErr := writeTargetResults(out, results, named, o.format)
	return executionFailure(errors.Join(requestErr, outputErr))
}
