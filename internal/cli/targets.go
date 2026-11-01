package cli

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
)

type selectedTarget struct {
	name    string
	address netip.Addr
	eoj     echonet.EOJ
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

func operateTargets(ctx context.Context, client session, targets []selectedTarget, op operation, named bool) ([]targetResult, error) {
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
	var failures []error
	for i, err := range errs {
		if err != nil {
			if named {
				err = fmt.Errorf("%s: %w", targets[i].name, err)
			}
			failures = append(failures, err)
		}
	}
	return results, errors.Join(failures...)
}
