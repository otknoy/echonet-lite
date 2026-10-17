// Package cli implements the command line without owning protocol logic.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
)

const usage = `Usage:
  echonet aircon discover [--interface eth0] [--timeout 3s] [--format json]
  echonet aircon status --host IP --eoj 013001
  echonet aircon power on|off --host IP --eoj 013001
  echonet aircon mode auto|cool|heat|dry|fan --host IP --eoj 013001
  echonet aircon temperature 26 --host IP --eoj 013001
All commands accept --interface, --timeout, --format text|json.
`

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
	return run(ctx, args, out, diagnostics, func(o echonet.Options) (session, error) {
		c, err := echonet.Open(o)
		if err != nil {
			return nil, err
		}
		return clientSession{c}, nil
	})
}
func run(ctx context.Context, args []string, out, diagnostics io.Writer, open opener) int {
	bad := func(err error) int { fmt.Fprintln(diagnostics, err); return 2 }
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		fmt.Fprint(out, usage)
		return 0
	}
	if len(args) < 2 || args[0] != "aircon" {
		return bad(fmt.Errorf("expected aircon subcommand"))
	}
	command := args[1]
	rest := args[2:]
	value := ""
	switch command {
	case "discover", "status":
	case "power", "mode", "temperature":
		if len(rest) == 0 || strings.HasPrefix(rest[0], "--") {
			return bad(fmt.Errorf("%s requires a value", command))
		}
		value = rest[0]
		rest = rest[1:]
	default:
		return bad(fmt.Errorf("unknown command %q", command))
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	iface := fs.String("interface", "", "IPv4 interface")
	timeout := fs.Duration("timeout", 3*time.Second, "request or discovery timeout")
	format := fs.String("format", "text", "text or json")
	host := fs.String("host", "", "target IPv4 address")
	eojText := fs.String("eoj", "", "target EOJ (six hexadecimal digits)")
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		return bad(fmt.Errorf("unexpected arguments: %v", fs.Args()))
	}
	if *timeout <= 0 {
		return bad(fmt.Errorf("timeout must be positive"))
	}
	if *format != "text" && *format != "json" {
		return bad(fmt.Errorf("format must be text or json"))
	}
	var address netip.Addr
	var eoj echonet.EOJ
	var mode aircon.Mode
	var temperature int
	var err error
	if command == "discover" {
		if *host != "" || *eojText != "" {
			return bad(fmt.Errorf("discover does not accept host or eoj"))
		}
	} else {
		address, err = netip.ParseAddr(*host)
		if err != nil || !address.Is4() || address.IsUnspecified() || address.IsMulticast() || address == netip.MustParseAddr("255.255.255.255") {
			return bad(fmt.Errorf("host must be a unicast IPv4 address"))
		}
		eoj, err = echonet.ParseEOJ(*eojText)
		if err != nil || !aircon.IsAircon(eoj) {
			return bad(fmt.Errorf("eoj must identify a household air conditioner instance (0130xx)"))
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
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	defer client.Close()
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
			fmt.Fprintln(diagnostics, err)
			return 1
		}
		if requestErr != nil {
			fmt.Fprintln(diagnostics, requestErr)
			return 1
		}
		return 0
	}
	ac, err := client.Aircon(address, eoj)
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	if command == "status" {
		status, requestErr := ac.Status(ctx)
		if *format == "json" {
			err = emit(status)
		} else {
			_, err = fmt.Fprintf(out, "power: %s\nmode: %s\ntemperature: %s\nroom_temperature: %s\n", fieldText(status.Power), fieldText(status.Mode), fieldText(status.Temperature), fieldText(status.RoomTemperature))
		}
		if err != nil {
			fmt.Fprintln(diagnostics, err)
			return 1
		}
		if requestErr != nil {
			fmt.Fprintln(diagnostics, requestErr)
			return 1
		}
		return 0
	}
	switch command {
	case "power":
		err = ac.SetPower(ctx, value == "on")
	case "mode":
		err = ac.SetMode(ctx, mode)
	case "temperature":
		err = ac.SetTemperature(ctx, temperature)
	}
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	if *format == "json" {
		err = emit(struct {
			Status string `json:"status"`
		}{"accepted"})
	} else {
		err = emit("accepted")
	}
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	return 0
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
