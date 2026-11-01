// Package cli implements the command line without owning protocol logic.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
	"github.com/otknoy/echonet-lite/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type airconControl interface {
	Status(context.Context) (aircon.Status, error)
	SetPower(context.Context, bool) error
	SetMode(context.Context, aircon.Mode) error
	SetTemperature(context.Context, int) error
}
type session interface {
	Aircon(netip.Addr, echonet.EOJ) (airconControl, error)
	Discover(context.Context) ([]echonet.DiscoveredNode, error)
	Close() error
}
type clientSession struct{ *echonet.Client }

func (s clientSession) Aircon(address netip.Addr, eoj echonet.EOJ) (airconControl, error) {
	return aircon.New(s.Node(address).Object(eoj))
}

type opener func(echonet.Options) (session, error)

// Run returns an exit code; help and invalid arguments never open a socket.
func Run(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	return RunWithInput(ctx, args, os.Stdin, out, diagnostics)
}

// RunWithInput accepts an input stream for interactive setup.
func RunWithInput(ctx context.Context, args []string, input io.Reader, out, diagnostics io.Writer) int {
	return runWithInput(ctx, args, input, out, diagnostics, func(o echonet.Options) (session, error) {
		c, err := echonet.Open(o)
		if err != nil {
			return nil, err
		}
		return clientSession{c}, nil
	}, availableInterfaces)
}
func run(ctx context.Context, args []string, out, diagnostics io.Writer, open opener) int {
	return runWithInput(ctx, args, strings.NewReader(""), out, diagnostics, open, availableInterfaces)
}
func runAircon(ctx context.Context, cmd *cobra.Command, args []string, out, diagnostics io.Writer, open opener, o *airconOptions) int {
	report := func(err error) { _, _ = fmt.Fprintln(diagnostics, err) }
	bad := func(err error) int { report(err); return 2 }
	command := cmd.Name()
	value := ""
	if len(args) > 0 {
		value = args[0]
	}
	fs := cmd.Flags()
	configPath, iface, timeout, format := &o.configPath, &o.iface, &o.timeout, &o.format
	host, eojText, targetNames := &o.host, &o.eoj, o.targets
	for _, name := range targetNames {
		if strings.TrimSpace(name) == "" {
			return bad(fmt.Errorf("target name must not be empty"))
		}
	}
	if *timeout <= 0 {
		return bad(fmt.Errorf("timeout must be positive"))
	}
	if *format != "text" && *format != "json" {
		return bad(fmt.Errorf("format must be text or json"))
	}

	specified := make(map[string]bool)
	fs.Visit(func(f *pflag.Flag) { specified[f.Name] = true })
	if specified["config"] && *configPath == "" {
		return bad(fmt.Errorf("--config requires a path"))
	}
	if specified["host"] != specified["eoj"] {
		return bad(fmt.Errorf("--host and --eoj must be specified together"))
	}
	if len(targetNames) > 0 && (specified["host"] || specified["eoj"]) {
		return bad(fmt.Errorf("--target cannot be combined with --host or --eoj"))
	}
	if command == "discover" && (specified["host"] || specified["eoj"] || specified["target"]) {
		return bad(fmt.Errorf("discover does not accept host, eoj or target"))
	}
	doc, err := loadConfig(*configPath, specified["config"])
	if err != nil {
		return bad(err)
	}
	var cfg config.Config
	if doc != nil {
		cfg = doc.Config
	}
	if command == "discover" && !specified["interface"] {
		*iface = cfg.Interface
	}
	var targets []selectedTarget
	var mode aircon.Mode
	var temperature int
	if command != "discover" {
		var named []config.NamedTarget
		if specified["host"] {
			named = []config.NamedTarget{{Target: config.Target{Host: *host, EOJ: *eojText}}}
		} else {
			named, err = cfg.Resolve(targetNames...)
			if err != nil {
				return bad(err)
			}
		}
		targets, err = prepareTargets(named)
		if err != nil {
			return bad(err)
		}
	}
	switch command {
	case "power":
		if value != "on" && value != "off" {
			return bad(fmt.Errorf("power must be on or off"))
		}
	case "mode":
		mode, err = aircon.ParseMode(value)
		if err != nil {
			return bad(err)
		}
	case "temperature":
		temperature, err = strconv.Atoi(value)
		if err != nil || temperature < 0 || temperature > 50 {
			return bad(fmt.Errorf("temperature must be an integer from 0 to 50"))
		}
	}
	client, err := open(echonet.Options{Interface: *iface, Timeout: *timeout})
	if err != nil {
		report(err)
		return 1
	}
	// Client.Close is idempotent and currently always returns nil.
	defer func() { _ = client.Close() }()
	emit := func(v any) error {
		if *format == "json" {
			return json.NewEncoder(out).Encode(v)
		}
		_, err := fmt.Fprintln(out, v)
		return err
	}
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
		if *format == "json" {
			err = emit(result)
		} else {
			for _, r := range result {
				if _, err = fmt.Fprintf(out, "%s %s\n", r.Host, r.EOJ); err != nil {
					break
				}
			}
		}
		if err != nil {
			report(err)
			return 1
		}
		if requestErr != nil {
			report(requestErr)
			return 1
		}
		return 0
	}
	return operateTargets(ctx, client, targets, operation{command: command, power: value == "on", mode: mode, temperature: temperature}, (!specified["host"] && namedSelection(cfg, targetNames)) || len(targets) > 1, *format, out, diagnostics)
}

func fieldText[T any](f aircon.Field[T]) string {
	if f.Value != nil {
		return fmt.Sprint(*f.Value)
	}
	if f.Error != "" {
		return f.State + ": " + f.Error
	}
	return f.State
}
