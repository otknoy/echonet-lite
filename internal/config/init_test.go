package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInitCreatesLoadableTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "echonet", "config.yaml")
	if err := Init(path, false); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Config.CurrentTarget != "" || len(doc.Config.Targets) != 0 {
		t.Fatal("template must not select example addresses")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions: %v", info.Mode())
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("directory permissions: %v", info.Mode())
	}
}
func TestInitDoesNotOverwrite(t *testing.T) {
	path := writeConfig(t, sample)
	if err := Init(path, false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected existing file error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != sample {
		t.Fatal("existing file changed", err)
	}
	link := filepath.Join(filepath.Dir(path), "dangling.yaml")
	if err = os.Symlink(filepath.Join(filepath.Dir(path), "missing.yaml"), link); err != nil {
		t.Fatal(err)
	}
	if err = Init(link, false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected existing symlink error: %v", err)
	}
}
func TestInitInvalidParent(t *testing.T) {
	path := writeConfig(t, sample)
	if err := Init(filepath.Join(path, "config.yaml"), false); err == nil {
		t.Fatal("accepted file as directory")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != sample {
		t.Fatal("parent file changed", err)
	}
}

func TestForceInitReplacesExistingFile(t *testing.T) {
	path := writeConfig(t, sample)
	if err := Init(path, true); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(path)
	if err != nil || doc.Config.CurrentTarget != "" || len(doc.Config.Targets) != 0 {
		t.Fatalf("%+v %v", doc, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatal(info, err)
	}
}
func TestForceInitCreatesAndRejectsDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new", "config.yaml")
	if err := Init(path, true); err != nil {
		t.Fatal(err)
	}
	if err := Init(dir, true); err == nil {
		t.Fatal("replaced directory")
	}
}
func TestForceInitPreservesSymlink(t *testing.T) {
	path := writeConfig(t, sample)
	link := filepath.Join(filepath.Dir(path), "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := Init(link, true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced", err)
	}
	doc, err := Load(path)
	if err != nil || doc.Config.CurrentTarget != "" {
		t.Fatal(doc, err)
	}
}

func TestConcurrentInitPublishesOneCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	results := make(chan error, 16)
	for range 16 {
		go func() { results <- Init(path, false) }()
	}
	successes := 0
	for range 16 {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful creators: %d", successes)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != initialConfig {
		t.Fatalf("incomplete configuration: %q, %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: %v, %v", entries, err)
	}
}

func TestFailedPublicationLeavesNoConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	injected := errors.New("publication failed")
	err := atomicWrite(path, []byte(initialConfig), 0o600, func(temp, destination string) error {
		data, err := os.ReadFile(temp)
		if err != nil || string(data) != initialConfig {
			t.Fatalf("incomplete temporary file: %v", err)
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("destination published early: %v", err)
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 0 {
		t.Fatalf("files remain after failure: %v, %v", entries, err)
	}
}
