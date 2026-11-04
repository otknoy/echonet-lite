package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/otknoy/echonet-lite/internal/config"
)

func setupConfig(ctx context.Context, input io.Reader, out io.Writer, open opener, interfaces interfaceLister, timeout time.Duration) (config.Config, error) {
	cfg := config.Config{Targets: map[string]config.Target{}}
	p := &prompt{ctx: ctx, input: bufio.NewScanner(input), output: out}
	if err := ctx.Err(); err != nil {
		return cfg, err
	}
	candidates, err := interfaces()
	if err != nil {
		return cfg, err
	}
	cfg.Interface, err = p.chooseInterface(candidates)
	if err != nil {
		return cfg, err
	}
	if _, err = fmt.Fprintln(out, "エアコンを探索しています..."); err != nil {
		return cfg, err
	}
	nodes, err := discoverForSetup(ctx, open, cfg.Interface, timeout)
	if err != nil {
		return cfg, err
	}
	registered := []string{}
	for _, device := range setupDevicesFromNodes(nodes) {
		yes, err := p.register(fmt.Sprintf("%s / %s", device.address, device.eoj))
		if err != nil {
			return cfg, err
		}
		if !yes {
			continue
		}
		name, err := p.name(cfg.Targets)
		if err != nil {
			return cfg, err
		}
		cfg.Targets[name] = config.Target{Host: device.address.String(), EOJ: device.eoj.String()}
		registered = append(registered, name)
	}
	if len(registered) > 0 {
		index, err := p.choose("current_target を選択してください。", registered, registered)
		if err != nil {
			return cfg, err
		}
		cfg.CurrentTarget = registered[index]
	} else {
		if _, err = fmt.Fprintln(out, "登録するエアコンがないため、interface のみ保存します。"); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}
