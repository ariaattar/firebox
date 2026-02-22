package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"firebox/internal/config"
)

func TestResolveSetupYAMLPathExplicitFile(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{StateDir: filepath.Join(root, "state")}
	if err := os.MkdirAll(paths.StateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	yamlPath := filepath.Join(root, "custom.yaml")
	yamlData := "images:\n  - location: https://example.com/disk.img\n"
	if err := os.WriteFile(yamlPath, []byte(yamlData), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := resolveSetupYAMLPath(paths, yamlPath)
	if err != nil {
		t.Fatalf("resolveSetupYAMLPath() error = %v", err)
	}
	want, err := filepath.Abs(yamlPath)
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	if got != want {
		t.Fatalf("resolveSetupYAMLPath() = %q, want %q", got, want)
	}
}

func TestResolveSetupYAMLPathFallbackWritesEmbeddedFile(t *testing.T) {
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}

	paths := config.Paths{StateDir: filepath.Join(root, "state")}
	if err := os.MkdirAll(paths.StateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	got, err := resolveSetupYAMLPath(paths, "")
	if err != nil {
		t.Fatalf("resolveSetupYAMLPath() error = %v", err)
	}
	if !strings.HasPrefix(got, paths.StateDir+string(filepath.Separator)) {
		t.Fatalf("resolveSetupYAMLPath() = %q, expected under state dir %q", got, paths.StateDir)
	}

	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "images:") {
		t.Fatalf("embedded setup yaml missing images section: %q", string(data))
	}
}
