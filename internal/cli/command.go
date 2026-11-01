package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type airconOptions struct {
	configPath, iface, format, host, eoj string
	timeout                              time.Duration
	targets                              []string
}
type configOptions struct {
	path         string
	empty, force bool
	timeout      time.Duration
}

// commandExit carries an exit code after an operation has reported its error.
type commandExit int

func (e commandExit) Error() string { return fmt.Sprintf("exit status %d", e) }
func operationResult(code int) error {
	if code != 0 {
		return commandExit(code)
	}
	return nil
}

// Cobra's help/completion writers may discard write errors. Retain the first
// failure so the public runner still reports output failures as exit code 1.
type checkedWriter struct {
	io.Writer
	err error
}

func (w *checkedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if w.err == nil {
		w.err = err
	}
	return n, err
}

func runWithInput(ctx context.Context, args []string, input io.Reader, out, diagnostics io.Writer, open opener, interfaces interfaceLister) int {
	output := &checkedWriter{Writer: out}
	root := newRootCommand(input, output, diagnostics, open, interfaces)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	var code commandExit
	if errors.As(err, &code) {
		return int(code)
	}
	if output.err != nil {
		_, _ = fmt.Fprintln(diagnostics, output.err)
		return 1
	}
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(diagnostics, err)
	return 2
}

func newRootCommand(input io.Reader, out, diagnostics io.Writer, open opener, interfaces interfaceLister) *cobra.Command {
	root := &cobra.Command{
		Use: "echonet", Short: "Control ECHONET Lite air conditioners",
		SilenceErrors: true, SilenceUsage: true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("unknown command %q", args[0])
			}
			return cmd.Help()
		},
	}
	root.SetIn(input)
	root.SetOut(out)
	root.SetErr(diagnostics)
	missingSubcommand := func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf("%s requires a subcommand", cmd.CommandPath())
	}
	air := &cobra.Command{Use: "aircon", Short: "Discover and control air conditioners", RunE: missingSubcommand}
	cfg := &cobra.Command{Use: "config", Short: "Manage configured targets", RunE: missingSubcommand}
	root.AddCommand(air, cfg)
	for _, name := range []string{"discover", "status", "power", "mode", "temperature"} {
		o := &airconOptions{}
		cmd := &cobra.Command{Use: name, Short: "Air conditioner " + name, Args: cobra.NoArgs, ValidArgsFunction: cobra.NoFileCompletions}
		switch name {
		case "power":
			cmd.Use += " on|off"
			cmd.Args = cobra.ExactArgs(1)
			cmd.ValidArgsFunction = positionalValues("on", "off")
		case "mode":
			cmd.Use += " auto|cool|heat|dry|fan"
			cmd.Args = cobra.ExactArgs(1)
			cmd.ValidArgsFunction = positionalValues("auto", "cool", "heat", "dry", "fan")
		case "temperature":
			cmd.Use += " VALUE"
			cmd.Args = cobra.ExactArgs(1)
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return operationResult(runAircon(cmd.Context(), cmd, args, out, diagnostics, open, o))
		}
		f := cmd.Flags()
		f.StringVar(&o.configPath, "config", "", "configuration file")
		f.StringVar(&o.iface, "interface", "", "force IPv4 interface (default: OS routing; discovery auto-selects)")
		f.DurationVar(&o.timeout, "timeout", 3*time.Second, "request or discovery timeout")
		f.StringVar(&o.format, "format", "text", "text or json")
		if name != "discover" {
			f.StringVar(&o.host, "host", "", "target IPv4 address")
			f.StringVar(&o.eoj, "eoj", "", "target EOJ (six hexadecimal digits)")
			// StringArray preserves commas and order in configured names.
			f.StringArrayVar(&o.targets, "target", nil, "configured target or group name (repeatable)")
			mustRegisterCompletion(cmd, "target", completeTargets)
			for _, flag := range []string{"host", "eoj"} {
				mustRegisterCompletion(cmd, flag, cobra.NoFileCompletions)
			}
		}
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
		air.AddCommand(cmd)
	}
	for _, name := range []string{"init", "list", "use"} {
		o := &configOptions{timeout: 3 * time.Second}
		cmd := &cobra.Command{Use: name, Short: "Configuration " + name, Args: cobra.NoArgs, ValidArgsFunction: cobra.NoFileCompletions}
		if name == "use" {
			cmd.Use += " NAME"
			cmd.Args = cobra.ExactArgs(1)
			cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
				if len(args) != 0 {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				return completeTargets(cmd, args, prefix)
			}
		}
		cmd.Flags().StringVar(&o.path, "config", "", "configuration file")
		if name == "init" {
			cmd.Flags().BoolVar(&o.empty, "empty", false, "create an empty template without interactive setup")
			cmd.Flags().BoolVar(&o.force, "force", false, "replace an existing configuration after setup completes")
			cmd.Flags().DurationVar(&o.timeout, "timeout", 3*time.Second, "discovery timeout")
			mustRegisterCompletion(cmd, "timeout", cobra.NoFileCompletions)
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return operationResult(runConfig(cmd.Context(), cmd, args, input, out, diagnostics, open, interfaces, o))
		}
		cfg.AddCommand(cmd)
	}
	root.AddCommand(&cobra.Command{
		Use: "completion bash|zsh", Short: "Generate a shell completion script", Args: cobra.ExactArgs(1),
		ValidArgsFunction: positionalValues("bash", "zsh"),
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			switch args[0] {
			case "bash":
				err = root.GenBashCompletionV2(out, true)
			case "zsh":
				err = root.GenZshCompletion(out)
			default:
				return fmt.Errorf("unsupported shell %q: expected bash or zsh", args[0])
			}
			if err != nil {
				_, _ = fmt.Fprintln(diagnostics, err)
				return commandExit(1)
			}
			return nil
		},
	})
	return root
}

func mustRegisterCompletion(cmd *cobra.Command, name string, fn cobra.CompletionFunc) {
	// Flag names are defined immediately above; failure indicates a programming error.
	if err := cmd.RegisterFlagCompletionFunc(name, fn); err != nil {
		panic(err)
	}
}
