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

// operation captures validated command input before any configuration or communication.
type operation func(context.Context, airconControl) (*aircon.Status, error)

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
					results[i].Status, errs[i] = op(ctx, controls[i])
				}
				switch {
				case errs[i] != nil:
					results[i].Result = "error"
					results[i].Error = errs[i].Error()
				case results[i].Status != nil:
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
