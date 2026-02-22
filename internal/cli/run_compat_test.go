package cli

import (
	"os"
	"path/filepath"
	"testing"

	"firebox/internal/model"
)

func TestParseRunCommand(t *testing.T) {
	cmd, err := parseRunCommand([]string{"echo", "ok"})
	if err != nil {
		t.Fatalf("parseRunCommand() error = %v", err)
	}
	if len(cmd) != 2 || cmd[0] != "echo" || cmd[1] != "ok" {
		t.Fatalf("command = %#v, want [echo ok]", cmd)
	}
}

func TestParseRunCommandRejectsEmpty(t *testing.T) {
	_, err := parseRunCommand(nil)
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestParseRunCommandAllowsRelativePath(t *testing.T) {
	cmd, err := parseRunCommand([]string{"./script.sh", "arg1"})
	if err != nil {
		t.Fatalf("parseRunCommand() error = %v", err)
	}
	if len(cmd) != 2 || cmd[0] != "./script.sh" || cmd[1] != "arg1" {
		t.Fatalf("command = %#v, want [./script.sh arg1]", cmd)
	}
}

func TestNormalizeEnvVars(t *testing.T) {
	t.Setenv("FROM_HOST", "abc")

	got, err := normalizeEnvVars([]string{"FOO=bar", "FROM_HOST"})
	if err != nil {
		t.Fatalf("normalizeEnvVars() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0] != "FOO=bar" || got[1] != "FROM_HOST=abc" {
		t.Fatalf("got = %#v, want [FOO=bar FROM_HOST=abc]", got)
	}
}

func TestMergeMountInputsWithSandbox(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}

	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	mounts, err := mergeMountInputs(nil, []string{src + ":/bind:rw"}, []string{src + ":/sandbox"}, model.CowOff)
	if err != nil {
		t.Fatalf("mergeMountInputs() error = %v", err)
	}
	if len(mounts) != 2 {
		t.Fatalf("len(mounts) = %d, want 2", len(mounts))
	}
	// --sandbox always enforces CoW on even if global cow is off.
	if mounts[1].Cow != model.CowOn {
		t.Fatalf("sandbox mount Cow = %q, want on", mounts[1].Cow)
	}
}

func TestParseNetwork(t *testing.T) {
	got, err := parseNetwork("nat")
	if err != nil {
		t.Fatalf("parseNetwork(nat) error = %v", err)
	}
	if got != model.NetworkNAT {
		t.Fatalf("parseNetwork(nat) = %q, want nat", got)
	}

	got, err = parseNetwork("none")
	if err != nil {
		t.Fatalf("parseNetwork(none) error = %v", err)
	}
	if got != model.NetworkNone {
		t.Fatalf("parseNetwork(none) = %q, want none", got)
	}

	if _, err := parseNetwork("host"); err == nil {
		t.Fatal("expected error for invalid network mode")
	}
}
