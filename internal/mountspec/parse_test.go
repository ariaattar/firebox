package mountspec

import (
	"os"
	"path/filepath"
	"testing"

	"firebox/internal/model"
)

func TestParseOneWithCowOverride(t *testing.T) {
	dir := t.TempDir()
	raw := dir + ":/workspace:rw:cow=off"

	got, err := ParseOne(raw, model.CowOn)
	if err != nil {
		t.Fatalf("ParseOne() error = %v", err)
	}
	if got.HostPath != dir {
		t.Fatalf("HostPath = %q, want %q", got.HostPath, dir)
	}
	if got.GuestPath != "/workspace" {
		t.Fatalf("GuestPath = %q, want /workspace", got.GuestPath)
	}
	if got.Access != model.AccessRW {
		t.Fatalf("Access = %q, want rw", got.Access)
	}
	if got.Cow != model.CowOff {
		t.Fatalf("Cow = %q, want off", got.Cow)
	}
}

func TestParseOneRejectsRelativeGuestPath(t *testing.T) {
	dir := t.TempDir()
	_, err := ParseOne(dir+":workspace:rw", model.CowOn)
	if err == nil {
		t.Fatal("expected error for relative guest path")
	}
}

func TestNeedsHostWriteAck(t *testing.T) {
	dir := t.TempDir()
	mounts, err := ParseMany([]string{
		dir + ":/workspace:rw:cow=off",
		dir + ":/readonly:ro:cow=off",
	}, model.CowOn)
	if err != nil {
		t.Fatalf("ParseMany() error = %v", err)
	}
	if !NeedsHostWriteAck(mounts, model.CowOn) {
		t.Fatal("NeedsHostWriteAck() = false, want true")
	}

	mounts, err = ParseMany([]string{
		dir + ":/workspace:rw:cow=on",
	}, model.CowOn)
	if err != nil {
		t.Fatalf("ParseMany() error = %v", err)
	}
	if NeedsHostWriteAck(mounts, model.CowOn) {
		t.Fatal("NeedsHostWriteAck() = true, want false")
	}
}

func TestExpandPathWithHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	testDir := filepath.Join(home, "tmp-firebox-test-home-expand")
	_ = os.MkdirAll(testDir, 0o755)
	defer os.RemoveAll(testDir)

	m, err := ParseOne("~/tmp-firebox-test-home-expand:/workspace", model.CowOn)
	if err != nil {
		t.Fatalf("ParseOne() error = %v", err)
	}
	if m.HostPath != testDir {
		t.Fatalf("HostPath = %q, want %q", m.HostPath, testDir)
	}
}

func TestParseOneRelativeHostPathGetsAbs(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	got, err := ParseOne("./sub:/workspace", model.CowOn)
	if err != nil {
		t.Fatalf("ParseOne() error = %v", err)
	}
	gotPath, err := filepath.EvalSymlinks(got.HostPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(got.HostPath) error = %v", err)
	}
	wantPath, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatalf("EvalSymlinks(sub) error = %v", err)
	}
	if gotPath != wantPath {
		t.Fatalf("HostPath = %q, want %q", gotPath, wantPath)
	}
}
