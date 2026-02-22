package limafc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"firebox/internal/model"
)

func TestBuildRunScriptCowModes(t *testing.T) {
	b := &Backend{}
	spec := model.RunSpec{
		Command: []string{"echo", "ok"},
		Cow:     model.CowOn,
		Mounts: []model.MountSpec{
			{
				HostPath:  "/Users/test/project",
				GuestPath: "/workspace",
				Access:    model.AccessRW,
				Cow:       model.CowOff,
			},
			{
				HostPath:  "/Users/test/cache",
				GuestPath: "/cache",
				Access:    model.AccessRW,
				Cow:       model.CowOn,
			},
		},
	}

	script := b.buildRunScript(spec)
	if !strings.Contains(script, "mount source is not writable inside lima host") {
		t.Fatalf("script missing writable check for cow=off mount:\\n%s", script)
	}
	if !strings.Contains(script, "bind_mount_rw") {
		t.Fatalf("script missing bind mount path:\\n%s", script)
	}
	if !strings.Contains(script, "mount -t overlay") {
		t.Fatalf("script missing overlay mount path for cow=on:\\n%s", script)
	}
	if !strings.Contains(script, "export FIREBOX_BACKEND=lima-firecracker") {
		t.Fatalf("script missing backend marker env:\\n%s", script)
	}
	if strings.Contains(script, "cp -a /Users/test/cache") {
		t.Fatalf("script still copies cow-on directories instead of overlay:\\n%s", script)
	}
}

func TestBuildRunScriptPersistentSession(t *testing.T) {
	b := &Backend{}
	spec := model.RunSpec{
		Command:        []string{"echo", "ok"},
		Cow:            model.CowOn,
		PersistSession: true,
		SessionID:      "demo-sandbox",
		Mounts: []model.MountSpec{
			{
				HostPath:  "/Users/test/project",
				GuestPath: "/workspace",
				Access:    model.AccessRW,
				Cow:       model.CowOn,
			},
		},
	}

	script := b.buildRunScript(spec)
	if !strings.Contains(script, "PERSIST_SESSION=1") {
		t.Fatalf("script missing persistent-session marker:\\n%s", script)
	}
	if strings.Contains(script, "rm -rf --one-file-system \"${RUN_ROOT}\" \"${SESSION_ROOT}\"") {
		t.Fatalf("script should not remove persistent session root on cleanup:\\n%s", script)
	}
	if !strings.Contains(script, "if [ \"${PERSIST_SESSION}\" != \"1\" ]; then rm -rf --one-file-system \"${SESSION_ROOT}\"; fi") {
		t.Fatalf("script missing conditional session cleanup:\\n%s", script)
	}
}

func TestSelectCowMounts(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	spec := model.RunSpec{
		Cow: model.CowOn,
		Mounts: []model.MountSpec{
			{
				HostPath:  sub,
				GuestPath: "/workspace",
				Access:    model.AccessRW,
				Cow:       model.CowOn,
			},
			{
				HostPath:  file,
				GuestPath: "/single",
				Access:    model.AccessRW,
				Cow:       model.CowOn,
			},
			{
				HostPath:  sub,
				GuestPath: "/readonly",
				Access:    model.AccessRO,
				Cow:       model.CowOn,
			},
		},
	}

	mounts, err := selectCowMounts(spec, "")
	if err != nil {
		t.Fatalf("selectCowMounts() error = %v", err)
	}
	if len(mounts) != 2 {
		t.Fatalf("len(mounts) = %d, want 2", len(mounts))
	}
	if mounts[0].Kind != "dir" {
		t.Fatalf("mounts[0].Kind = %q, want dir", mounts[0].Kind)
	}
	if mounts[1].Kind != "file" {
		t.Fatalf("mounts[1].Kind = %q, want file", mounts[1].Kind)
	}

	filtered, err := selectCowMounts(spec, "/workspace")
	if err != nil {
		t.Fatalf("selectCowMounts(filtered) error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].GuestPath != "/workspace" {
		t.Fatalf("filtered mounts = %#v, want only /workspace", filtered)
	}
}

func TestSanitizeSockName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "firebox-img-devyaml", want: "firebox-img-devyaml"},
		{in: "img/dev yaml", want: "img_dev_yaml"},
		{in: "....", want: "firebox-host"},
	}
	for _, tt := range tests {
		got := sanitizeSockName(tt.in)
		if got != tt.want {
			t.Fatalf("sanitizeSockName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
