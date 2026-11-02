package cli

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
	"github.com/spf13/cobra"
)

func newDiscoverCommand(open opener, interfaces interfaceLister) *cobra.Command {
	o := &airconOptions{}
	cmd := &cobra.Command{
		Use: "discover", Short: "Air conditioner discover",
		Args: cobra.NoArgs, ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDiscover(cmd, open, o)
		},
	}
	registerAirconFlags(cmd, o, interfaces)
	return cmd
}

func runDiscover(cmd *cobra.Command, open opener, o *airconOptions) error {
	cfg, err := loadAirconConfig(cmd, o)
	if err != nil {
		return err
	}
	iface := o.iface
	if !cmd.Flags().Changed("interface") {
		iface = cfg.Interface
	}
	client, err := open(echonet.Options{Interface: iface, Timeout: o.timeout})
	if err != nil {
		return executionFailure(err)
	}
	defer func() { _ = client.Close() }()
	nodes, requestErr := client.Discover(cmd.Context())
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
	out := cmd.OutOrStdout()
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
	return executionFailure(requestErr)
}
