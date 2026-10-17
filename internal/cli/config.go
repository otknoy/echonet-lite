package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/otknoy/echonet-lite/internal/config"
)

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
func runConfig(ctx context.Context, args []string, input io.Reader, out, diagnostics io.Writer, open opener, interfaces interfaceLister) int {
	report := func(err error) { _, _ = fmt.Fprintln(diagnostics, err) }
	if len(args) == 0 {
		report(fmt.Errorf("expected 'config init', 'config list' or 'config use NAME'"))
		return 2
	}
	command := args[0]
	rest := args[1:]
	name := ""
	switch command {
	case "init", "list":
	case "use":
		if len(rest) == 0 || rest[0] == "" || rest[0][0] == '-' {
			report(fmt.Errorf("config use requires a target name"))
			return 2
		}
		name = rest[0]
		rest = rest[1:]
	default:
		report(fmt.Errorf("unknown config command %q", command))
		return 2
	}
	fs := flag.NewFlagSet("config "+command, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	path := fs.String("config", "", "configuration file")
	var empty, force bool
	timeout := 3 * time.Second
	if command == "init" {
		fs.BoolVar(&force, "force", false, "replace an existing configuration after setup completes")
		fs.BoolVar(&empty, "empty", false, "create an empty template without interactive setup")
		fs.DurationVar(&timeout, "timeout", timeout, "discovery timeout")
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		report(fmt.Errorf("unexpected config arguments: %v", fs.Args()))
		return 2
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			explicit = true
		}
	})
	if explicit && *path == "" {
		report(fmt.Errorf("--config requires a path"))
		return 2
	}
	if command == "init" {
		if !explicit {
			defaultPath, err := config.DefaultPath()
			if err != nil {
				report(err)
				return 1
			}
			*path = defaultPath
		}
		if timeout <= 0 {
			report(fmt.Errorf("timeout must be positive"))
			return 2
		}
		// Refuse existing files before enumerating interfaces or opening a socket.
		if _, err := os.Lstat(*path); err == nil && !force {
			report(fmt.Errorf("config %s already exists; use --force to replace it", *path))
			return 1
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			report(err)
			return 1
		}
		var err error
		if empty {
			err = config.Init(*path, force)
		} else {
			var cfg config.Config
			cfg, err = setupConfig(ctx, input, out, open, interfaces, timeout)
			if err == nil {
				err = ctx.Err()
			}
			if err == nil {
				err = config.Create(*path, cfg, force)
			}
		}
		if err != nil {
			report(err)
			return 1
		}
		if _, err := fmt.Fprintln(out, *path); err != nil {
			report(err)
			return 1
		}
		return 0
	}
	doc, err := loadConfig(*path, explicit)
	if err != nil {
		report(err)
		return 2
	}
	if doc == nil {
		report(fmt.Errorf("configuration file not found; run 'echonet config init' first"))
		return 2
	}
	if command == "use" {
		if _, ok := doc.Config.Targets[name]; !ok {
			report(fmt.Errorf("target %q is not registered", name))
			return 2
		}
		if err = doc.Use(name); err != nil {
			report(err)
			return 1
		}
		if _, err = fmt.Fprintln(out, name); err != nil {
			report(err)
			return 1
		}
		return 0
	}
	names := make([]string, 0, len(doc.Config.Targets))
	for name := range doc.Config.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		marker := " "
		if name == doc.Config.CurrentTarget {
			marker = "*"
		}
		target := doc.Config.Targets[name]
		if _, err = fmt.Fprintf(out, "%s %s %s %s\n", marker, name, target.Host, target.EOJ); err != nil {
			report(err)
			return 1
		}
	}
	return 0
}
