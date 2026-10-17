package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/otknoy/echonet-lite/aircon"
	"github.com/otknoy/echonet-lite/echonet"
	"github.com/otknoy/echonet-lite/internal/config"
)

type networkInterface struct {
	name    string
	address netip.Addr
}
type interfaceLister func() ([]networkInterface, error)

func availableInterfaces() ([]networkInterface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := []networkInterface{}
	for _, iface := range all {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, a := range addresses {
			p, err := netip.ParsePrefix(a.String())
			if err == nil && p.Addr().Is4() && !p.Addr().IsLoopback() {
				result = append(result, networkInterface{iface.Name, p.Addr()})
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

type prompt struct {
	ctx    context.Context
	input  *bufio.Scanner
	output io.Writer
}

func (p *prompt) line(question string) (string, error) {
	if err := p.ctx.Err(); err != nil {
		return "", err
	}
	if _, err := fmt.Fprint(p.output, question); err != nil {
		return "", err
	}
	// Reading terminal input must not prevent cancellation of the CLI.
	// The pending reader ends when input is closed (or the CLI process exits).
	type result struct {
		text string
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		if !p.input.Scan() {
			err := p.input.Err()
			if err == nil {
				err = fmt.Errorf("setup interrupted: input ended before completion")
			}
			ready <- result{err: err}
			return
		}
		ready <- result{text: strings.TrimSpace(p.input.Text())}
	}()
	select {
	case <-p.ctx.Done():
		return "", p.ctx.Err()
	case answer := <-ready:
		if err := p.ctx.Err(); err != nil {
			return "", err
		}
		return answer.text, answer.err
	}
}
func (p *prompt) choose(title string, labels, names []string) (int, error) {
	if _, err := fmt.Fprintln(p.output, title); err != nil {
		return 0, err
	}
	for i, label := range labels {
		if _, err := fmt.Fprintf(p.output, "  %d) %s\n", i+1, label); err != nil {
			return 0, err
		}
	}
	for {
		answer, err := p.line("番号または名前を入力: ")
		if err != nil {
			return 0, err
		}
		for i, name := range names {
			if answer == name {
				return i, nil
			}
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(labels) {
			return n - 1, nil
		}
		if _, err := fmt.Fprintln(p.output, "一覧の番号または名前を指定してください。"); err != nil {
			return 0, err
		}
	}
}
func (p *prompt) register(label string) (bool, error) {
	for {
		answer, err := p.line(label + " を登録しますか? [y/N]: ")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "y", "yes":
			return true, nil
		case "", "n", "no":
			return false, nil
		}
		if _, err := fmt.Fprintln(p.output, "y または n を入力してください。"); err != nil {
			return false, err
		}
	}
}
func (p *prompt) name(targets map[string]config.Target) (string, error) {
	for {
		name, err := p.line("登録名: ")
		if err != nil {
			return "", err
		}
		_, duplicate := targets[name]
		if name != "" && !strings.HasPrefix(name, "-") && !duplicate {
			return name, nil
		}
		if _, err := fmt.Fprintln(p.output, "空欄・重複・先頭が '-' の名前は使えません。"); err != nil {
			return "", err
		}
	}
}
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
	if len(candidates) == 0 {
		return cfg, fmt.Errorf("no usable IPv4 multicast interface")
	}
	labels, names := []string{}, []string{}
	for _, iface := range candidates {
		labels = append(labels, fmt.Sprintf("%s (%s)", iface.name, iface.address))
		names = append(names, iface.name)
	}
	index, err := p.choose("ネットワークインターフェースを選択してください。", labels, names)
	if err != nil {
		return cfg, err
	}
	cfg.Interface = candidates[index].name
	if _, err = fmt.Fprintln(out, "エアコンを探索しています..."); err != nil {
		return cfg, err
	}
	nodes, err := discoverForSetup(ctx, open, cfg.Interface, timeout)
	if err != nil {
		return cfg, err
	}
	// Deduplicate and sort candidates so selection is independent of response order.
	type found struct {
		address netip.Addr
		eoj     echonet.EOJ
	}
	seen := map[found]bool{}
	devices := []found{}
	for _, node := range nodes {
		for _, eoj := range node.Objects {
			device := found{node.Address, eoj}
			if aircon.IsAircon(eoj) && !seen[device] {
				seen[device] = true
				devices = append(devices, device)
			}
		}
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].address != devices[j].address {
			return devices[i].address.Less(devices[j].address)
		}
		return devices[i].eoj.String() < devices[j].eoj.String()
	})
	registered := []string{}
	for _, device := range devices {
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
		index, err = p.choose("current_target を選択してください。", registered, registered)
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
func discoverForSetup(ctx context.Context, open opener, iface string, timeout time.Duration) ([]echonet.DiscoveredNode, error) {
	client, err := open(echonet.Options{Interface: iface, Timeout: timeout})
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	return client.Discover(ctx)
}
