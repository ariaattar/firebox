package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsDefault(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	paths, err := ResolvePathsForDaemonID("")
	if err != nil {
		t.Fatalf("ResolvePathsForDaemonID(\"\") error = %v", err)
	}
	if paths.StateDir != filepath.Join(home, ".firebox", "state") {
		t.Fatalf("StateDir = %q", paths.StateDir)
	}
	if paths.CacheDir != filepath.Join(home, ".firebox", "cache") {
		t.Fatalf("CacheDir = %q", paths.CacheDir)
	}
	if paths.LogsDir != filepath.Join(home, ".firebox", "logs") {
		t.Fatalf("LogsDir = %q", paths.LogsDir)
	}
	if paths.SockPath != filepath.Join(paths.StateDir, "fireboxd.sock") {
		t.Fatalf("SockPath = %q", paths.SockPath)
	}
}

func TestResolvePathsNamespacedDaemon(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	paths, err := ResolvePathsForDaemonID("teamA")
	if err != nil {
		t.Fatalf("ResolvePathsForDaemonID(\"teamA\") error = %v", err)
	}
	base := filepath.Join(home, ".firebox", "daemons", "teamA")
	if paths.StateDir != filepath.Join(base, "state") {
		t.Fatalf("StateDir = %q", paths.StateDir)
	}
	if paths.CacheDir != filepath.Join(base, "cache") {
		t.Fatalf("CacheDir = %q", paths.CacheDir)
	}
	if paths.LogsDir != filepath.Join(base, "logs") {
		t.Fatalf("LogsDir = %q", paths.LogsDir)
	}
	if paths.Runtime != filepath.Join(base, "state", "runtime.json") {
		t.Fatalf("Runtime = %q", paths.Runtime)
	}
}

func TestNormalizeDaemonIDValidation(t *testing.T) {
	valid, err := NormalizeDaemonID("dev_1.alpha-2")
	if err != nil {
		t.Fatalf("NormalizeDaemonID(valid) error = %v", err)
	}
	if valid != "dev_1.alpha-2" {
		t.Fatalf("NormalizeDaemonID(valid) = %q", valid)
	}

	if _, err := NormalizeDaemonID("bad/id"); err == nil {
		t.Fatal("expected error for daemon id containing slash")
	}
	if _, err := NormalizeDaemonID("-bad"); err == nil {
		t.Fatal("expected error for daemon id starting with non-alphanumeric")
	}
}
