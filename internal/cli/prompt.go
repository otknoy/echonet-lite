package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/otknoy/echonet-lite/internal/config"
)

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
		if config.ValidName(name) && !strings.HasPrefix(name, "-") && !duplicate {
			return name, nil
		}
		if _, err := fmt.Fprintln(p.output, "空欄・重複・先頭が '-' の名前は使えません。"); err != nil {
			return "", err
		}
	}
}
func (p *prompt) chooseInterface(candidates []networkInterface) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("no usable IPv4 multicast interface")
	}
	labels, names := []string{}, []string{}
	for _, iface := range candidates {
		labels = append(labels, fmt.Sprintf("%s (%s)", iface.name, iface.address))
		names = append(names, iface.name)
	}
	index := 0
	var err error
	if len(candidates) == 1 {
		if _, err = fmt.Fprintf(p.output, "探索用インターフェース: %s\n", labels[0]); err != nil {
			return "", err
		}
	} else {
		index, err = p.choose("ネットワークインターフェースを選択してください。", labels, names)
		if err != nil {
			return "", err
		}
	}
	return candidates[index].name, nil
}
