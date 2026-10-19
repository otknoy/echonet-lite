package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"sync"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
	"github.com/otknoy/echonet-lite/internal/config"
)

type selectedTarget struct {
	name    string
	address netip.Addr
	eoj     echonet.EOJ
}

func prepareTargets(named []config.NamedTarget) ([]selectedTarget, error) {
	result := make([]selectedTarget, 0, len(named))
	for _, n := range named {
		address, eoj, err := config.ParseTarget(n.Target)
		if err != nil {
			return nil, err
		}
		result = append(result, selectedTarget{n.Name, address, eoj})
	}
	return result, nil
}

type operation struct {
	command     string
	power       bool
	mode        aircon.Mode
	temperature int
}

func (op operation) execute(ctx context.Context, ac airconControl) (*aircon.Status, error) {
	switch op.command {
	case "status":
		status, err := ac.Status(ctx)
		return &status, err
	case "power":
		return nil, ac.SetPower(ctx, op.power)
	case "mode":
		return nil, ac.SetMode(ctx, op.mode)
	case "temperature":
		return nil, ac.SetTemperature(ctx, op.temperature)
	default:
		return nil, fmt.Errorf("unknown operation %q", op.command)
	}
}

type targetResult struct {
	Target string         `json:"target"`
	Host   netip.Addr     `json:"host"`
	EOJ    echonet.EOJ    `json:"eoj"`
	Result string         `json:"result"`
	Status *aircon.Status `json:"status,omitempty"`
	Error  string         `json:"error,omitempty"`
}

func operateTargets(ctx context.Context, client session, targets []selectedTarget, op operation, named bool, format string, out, diagnostics io.Writer) int {
	results := make([]targetResult, len(targets))
	controls := make([]airconControl, len(targets))
	errs := make([]error, len(targets))
	// Construct handles before sending requests; the shared client owns one socket.
	for i, t := range targets {
		results[i] = targetResult{Target: t.name, Host: t.address, EOJ: t.eoj}
		controls[i], errs[i] = client.Aircon(t.address, t.eoj)
	}
	jobs := make(chan int, len(targets))
	for i := range targets {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(8, len(targets)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				if errs[i] == nil {
					errs[i] = ctx.Err()
				}
				if errs[i] == nil {
					results[i].Status, errs[i] = op.execute(ctx, controls[i])
				}
				switch {
				case errs[i] != nil:
					results[i].Result = "error"
					results[i].Error = errs[i].Error()
				case op.command == "status":
					results[i].Result = "ok"
				default:
					results[i].Result = "accepted"
				}
			}
		}()
	}
	workers.Wait()
	code := 0
	for i, err := range errs {
		if err != nil {
			code = 1
			if named {
				_, _ = fmt.Fprintf(diagnostics, "%s: %v\n", targets[i].name, err)
			} else {
				_, _ = fmt.Fprintln(diagnostics, err)
			}
		}
	}
	var err error
	switch {
	case named && format == "json":
		err = json.NewEncoder(out).Encode(results)
	case named:
		for _, r := range results {
			if r.Status != nil {
				err = writeStatus(out, r.Target+"\t", *r.Status)
			} else if r.Error != "" {
				_, err = fmt.Fprintf(out, "%s\terror: %s\n", r.Target, r.Error)
			} else {
				_, err = fmt.Fprintf(out, "%s\t%s\n", r.Target, r.Result)
			}
			if err != nil {
				break
			}
		}
	case len(results) == 1:
		r := results[0]
		if r.Status != nil {
			if format == "json" {
				err = json.NewEncoder(out).Encode(r.Status)
			} else {
				err = writeStatus(out, "", *r.Status)
			}
		} else if errs[0] == nil {
			if format == "json" {
				err = json.NewEncoder(out).Encode(struct {
					Status string `json:"status"`
				}{"accepted"})
			} else {
				_, err = fmt.Fprintln(out, "accepted")
			}
		}
	}
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 1
	}
	return code
}
func writeStatus(out io.Writer, prefix string, status aircon.Status) error {
	_, err := fmt.Fprintf(out, "%spower: %s\n%smode: %s\n%stemperature: %s\n%sroom_temperature: %s\n", prefix, fieldText(status.Power), prefix, fieldText(status.Mode), prefix, fieldText(status.Temperature), prefix, fieldText(status.RoomTemperature))
	return err
}

func namedSelection(cfg config.Config, names []string) bool {
	if len(names) > 0 {
		return true
	}
	_, group := cfg.Groups[cfg.CurrentTarget]
	return group
}
