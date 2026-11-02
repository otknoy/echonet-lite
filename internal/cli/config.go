package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/otknoy/echonet-lite/internal/config"
	"github.com/spf13/cobra"
)

type configOptions struct {
	path         string
	empty, force bool
	timeout      time.Duration
}

func newConfigCommand(open opener, interfaces interfaceLister) *cobra.Command {
	cfg := &cobra.Command{Use: "config", Short: "Manage configured targets", RunE: requireSubcommand}
	cfg.AddCommand(newConfigInitCommand(open, interfaces), newConfigListCommand(), newConfigUseCommand())
	return cfg
}

func newConfigInitCommand(open opener, interfaces interfaceLister) *cobra.Command {
	o := &configOptions{}
	cmd := &cobra.Command{
		Use: "init", Short: "Configuration init",
		Args: cobra.NoArgs, ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigInit(cmd, open, interfaces, o)
		},
	}
	cmd.Flags().StringVar(&o.path, "config", "", "configuration file")
	cmd.Flags().BoolVar(&o.empty, "empty", false, "create an empty template without interactive setup")
	cmd.Flags().BoolVar(&o.force, "force", false, "replace an existing configuration after setup completes")
	cmd.Flags().DurationVar(&o.timeout, "timeout", 3*time.Second, "discovery timeout")
	mustRegisterCompletion(cmd, "timeout", cobra.NoFileCompletions)
	return cmd
}

func newConfigListCommand() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use: "list", Short: "Configuration list",
		Args: cobra.NoArgs, ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigList(cmd, path)
		},
	}
	cmd.Flags().StringVar(&path, "config", "", "configuration file")
	return cmd
}

func newConfigUseCommand() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use: "use NAME", Short: "Configuration use", Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeTargets(cmd, args, prefix)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigUse(cmd, args[0], path)
		},
	}
	cmd.Flags().StringVar(&path, "config", "", "configuration file")
	return cmd
}

func loadConfig(path string, explicit bool) (*config.Document, error) {
	if !explicit {
		var err error
		path, err = config.DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	doc, err := config.Load(path)
	if !explicit && errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return doc, err
}

func runConfigInit(cmd *cobra.Command, open opener, interfaces interfaceLister, o *configOptions) error {
	ctx, input, out := cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout()
	path := o.path
	explicit := cmd.Flags().Changed("config")
	if explicit && path == "" {
		return fmt.Errorf("--config requires a path")
	}
	if !explicit {
		defaultPath, err := config.DefaultPath()
		if err != nil {
			return executionFailure(err)
		}
		path = defaultPath
	}
	if o.timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	// Refuse existing files before enumerating interfaces or opening a socket.
	if _, err := os.Lstat(path); err == nil && !o.force {
		return executionFailure(fmt.Errorf("config %s already exists; use --force to replace it", path))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return executionFailure(err)
	}
	var err error
	if o.empty {
		err = config.Init(path, o.force)
	} else {
		var cfg config.Config
		cfg, err = setupConfig(ctx, input, out, open, interfaces, o.timeout)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = config.Create(path, cfg, o.force)
		}
	}
	if err != nil {
		return executionFailure(err)
	}
	if _, err := fmt.Fprintln(out, path); err != nil {
		return executionFailure(err)
	}
	return nil
}

func loadRequiredConfig(cmd *cobra.Command, path string) (*config.Document, error) {
	explicit := cmd.Flags().Changed("config")
	if explicit && path == "" {
		return nil, fmt.Errorf("--config requires a path")
	}
	doc, err := loadConfig(path, explicit)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("configuration file not found; run 'echonet config init' first")
	}
	return doc, nil
}

func runConfigUse(cmd *cobra.Command, name, path string) error {
	doc, err := loadRequiredConfig(cmd, path)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !doc.Config.Has(name) {
		return fmt.Errorf("target %q is not registered", name)
	}
	if err = doc.Use(name); err != nil {
		return executionFailure(err)
	}
	if _, err = fmt.Fprintln(out, name); err != nil {
		return executionFailure(err)
	}
	return nil
}

func runConfigList(cmd *cobra.Command, path string) error {
	doc, err := loadRequiredConfig(cmd, path)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	names := make([]string, 0, len(doc.Config.Targets)+len(doc.Config.Groups))
	for name := range doc.Config.Targets {
		names = append(names, name)
	}
	for name := range doc.Config.Groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		marker := " "
		if name == doc.Config.CurrentTarget {
			marker = "*"
		}
		if members, group := doc.Config.Groups[name]; group {
			if _, err = fmt.Fprintf(out, "%s %s [group] %s\n", marker, name, strings.Join(members, ", ")); err != nil {
				return executionFailure(err)
			}
			continue
		}
		target := doc.Config.Targets[name]
		if _, err = fmt.Fprintf(out, "%s %s %s %s\n", marker, name, target.Host, target.EOJ); err != nil {
			return executionFailure(err)
		}
	}
	return nil
}
