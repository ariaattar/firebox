package policy

import (
	"os"
	"path/filepath"
	"testing"

	"firebox/internal/model"
)

func TestNormalizeAndValidateSpec(t *testing.T) {
	spec := model.RunSpec{
		Network:        "",
		NetworkAllow:   []string{" 10.0.0.0/24 ", "10.0.0.0/24"},
		FileAllowPaths: []string{" /tmp/work "},
		FileAllowExts:  []string{"GO", ".md", "go"},
	}

	if err := NormalizeAndValidateSpec(&spec); err != nil {
		t.Fatalf("NormalizeAndValidateSpec() error = %v", err)
	}
	if spec.Network != model.NetworkNAT {
		t.Fatalf("Network = %q, want %q", spec.Network, model.NetworkNAT)
	}
	if len(spec.NetworkAllow) != 1 || spec.NetworkAllow[0] != "10.0.0.0/24" {
		t.Fatalf("NetworkAllow = %#v, want [10.0.0.0/24]", spec.NetworkAllow)
	}
	if len(spec.FileAllowPaths) != 1 || spec.FileAllowPaths[0] != "/tmp/work" {
		t.Fatalf("FileAllowPaths = %#v, want [/tmp/work]", spec.FileAllowPaths)
	}
	if len(spec.FileAllowExts) != 2 || spec.FileAllowExts[0] != ".go" || spec.FileAllowExts[1] != ".md" {
		t.Fatalf("FileAllowExts = %#v, want [.go .md]", spec.FileAllowExts)
	}
}

func TestNormalizeAndValidateSpecRejectsInvalidNetworkDestination(t *testing.T) {
	spec := model.RunSpec{
		Network:      model.NetworkNAT,
		NetworkAllow: []string{"*.example.com"},
	}
	if err := NormalizeAndValidateSpec(&spec); err == nil {
		t.Fatal("expected error for invalid network destination")
	}
}

func TestNormalizeAndValidateSpecAcceptsDomainAndHostname(t *testing.T) {
	spec := model.RunSpec{
		Network:      model.NetworkNAT,
		NetworkAllow: []string{"github.com", "LOCALHOST"},
		NetworkDeny:  []string{"SnowFlake.com."},
	}
	if err := NormalizeAndValidateSpec(&spec); err != nil {
		t.Fatalf("NormalizeAndValidateSpec() error = %v", err)
	}
	if len(spec.NetworkAllow) != 2 || spec.NetworkAllow[0] != "github.com" || spec.NetworkAllow[1] != "localhost" {
		t.Fatalf("NetworkAllow = %#v, want [github.com localhost]", spec.NetworkAllow)
	}
	if len(spec.NetworkDeny) != 1 || spec.NetworkDeny[0] != "snowflake.com" {
		t.Fatalf("NetworkDeny = %#v, want [snowflake.com]", spec.NetworkDeny)
	}
}

func TestNormalizeAndValidateSpecRejectsNetworkAllowWithNone(t *testing.T) {
	spec := model.RunSpec{
		Network:      model.NetworkNone,
		NetworkAllow: []string{"127.0.0.1"},
	}
	if err := NormalizeAndValidateSpec(&spec); err == nil {
		t.Fatal("expected error for network=none with allow list")
	}
}

func TestValidateMountsPathPolicies(t *testing.T) {
	root := t.TempDir()
	allowedDir := filepath.Join(root, "allowed")
	blockedDir := filepath.Join(root, "blocked")
	if err := os.MkdirAll(allowedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(allowedDir) error = %v", err)
	}
	if err := os.MkdirAll(blockedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(blockedDir) error = %v", err)
	}

	spec := model.RunSpec{
		Mounts: []model.MountSpec{
			{HostPath: allowedDir, GuestPath: "/workspace", Access: model.AccessRW},
		},
		FileAllowPaths: []string{filepath.ToSlash(root)},
	}
	if err := ValidateMounts(spec); err != nil {
		t.Fatalf("ValidateMounts(allow) error = %v", err)
	}

	spec.FileDenyPaths = []string{filepath.ToSlash(blockedDir)}
	spec.Mounts[0].HostPath = blockedDir
	if err := ValidateMounts(spec); err == nil {
		t.Fatal("expected deny path validation error")
	}
}

func TestValidateMountsExtensionPolicies(t *testing.T) {
	root := t.TempDir()
	allowedFile := filepath.Join(root, "allowed.go")
	deniedFile := filepath.Join(root, "denied.py")
	if err := os.WriteFile(allowedFile, []byte("package main"), 0o644); err != nil {
		t.Fatalf("WriteFile(allowedFile) error = %v", err)
	}
	if err := os.WriteFile(deniedFile, []byte("print('x')"), 0o644); err != nil {
		t.Fatalf("WriteFile(deniedFile) error = %v", err)
	}

	spec := model.RunSpec{
		Mounts: []model.MountSpec{
			{HostPath: allowedFile, GuestPath: "/workspace/main.go", Access: model.AccessRW},
		},
		FileAllowExts: []string{".go"},
	}
	if err := ValidateMounts(spec); err != nil {
		t.Fatalf("ValidateMounts(allow ext) error = %v", err)
	}

	spec.Mounts[0].HostPath = deniedFile
	if err := ValidateMounts(spec); err == nil {
		t.Fatal("expected extension allow-list validation error")
	}
}

func TestValidateMountsRejectsDirectoryWhenExtensionPolicySet(t *testing.T) {
	root := t.TempDir()
	spec := model.RunSpec{
		Mounts: []model.MountSpec{
			{HostPath: root, GuestPath: "/workspace", Access: model.AccessRW},
		},
		FileAllowExts: []string{".go"},
	}
	if err := ValidateMounts(spec); err == nil {
		t.Fatal("expected directory rejection when extension policy is set")
	}
}

func TestSplitNetworkByFamily(t *testing.T) {
	v4, v6 := SplitNetworkByFamily([]string{
		"10.0.0.0/24",
		"127.0.0.1",
		"::1",
		"2001:db8::/64",
		"github.com",
	})
	if len(v4) != 2 {
		t.Fatalf("len(v4) = %d, want 2", len(v4))
	}
	if len(v6) != 2 {
		t.Fatalf("len(v6) = %d, want 2", len(v6))
	}
}

func TestSplitNetworkDestinations(t *testing.T) {
	v4, v6, hosts := SplitNetworkDestinations([]string{
		"10.0.0.0/24",
		"2001:db8::/64",
		"github.com",
		"localhost",
	})
	if len(v4) != 1 {
		t.Fatalf("len(v4) = %d, want 1", len(v4))
	}
	if len(v6) != 1 {
		t.Fatalf("len(v6) = %d, want 1", len(v6))
	}
	if len(hosts) != 2 {
		t.Fatalf("len(hosts) = %d, want 2", len(hosts))
	}
}
