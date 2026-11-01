// Package cli implements the command line without owning protocol logic.
package cli

import (
	"context"
	"io"
	"net/netip"
	"os"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
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
	return execute(ctx, args, os.Stdin, out, diagnostics, func(o echonet.Options) (session, error) {
		c, err := echonet.Open(o)
		if err != nil {
			return nil, err
		}
		return clientSession{c}, nil
	}, availableInterfaces)
}
