package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// Start without an active target; example addresses must be edited before use.
const initialConfig = `# 探索用。空欄なら候補が一つの場合に自動選択します。
interface: ""

# 対象を登録した後、echonet config use NAME で切り替えます。
current_target: ""

# echonet aircon discover で確認した host・eoj を登録してください。
targets:
  # living:
  #   host: 192.168.1.10
  #   eoj: "013001"
  # bedroom:
  #   host: 192.168.1.11
  #   eoj: "013001"
`

// Init creates a template; force permits atomic replacement of an existing file.
func Init(path string, force bool) error { return create(path, []byte(initialConfig), force) }

// Create saves a completed setup, replacing an existing file only with force.
func Create(path string, cfg Config, force bool) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return create(path, data, force)
}

func create(path string, data []byte, force bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if force {
		mode := os.FileMode(0o600)
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				path, err = filepath.EvalSymlinks(path)
				if err != nil {
					return err
				}
				info, err = os.Stat(path)
				if err != nil {
					return err
				}
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("config %s is not a regular file", path)
			}
			mode = info.Mode().Perm()
		}
		return atomicWrite(path, data, mode, os.Rename)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create config %s: %w", path, err)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}
