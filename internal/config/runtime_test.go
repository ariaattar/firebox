package config

import (
	"path/filepath"
	"testing"
)

func TestRuntimeConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	in := RuntimeConfig{
		InstanceName: "firebox-img-dev",
		ImageName:    "dev",
		Policy: RuntimePolicyConfig{
			NetworkAllow:   []string{"github.com", "10.0.0.0/24"},
			NetworkDeny:    []string{"snowflake.com"},
			FileAllowPaths: []string{"/Users/test/work"},
			FileDenyPaths:  []string{"/Users/test/secrets"},
			FileAllowExts:  []string{".go"},
			FileDenyExts:   []string{".pem"},
		},
	}
	if err := SaveRuntimeConfig(path, in); err != nil {
		t.Fatalf("SaveRuntimeConfig() error = %v", err)
	}
	out, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatalf("LoadRuntimeConfig() error = %v", err)
	}
	if out.InstanceName != in.InstanceName {
		t.Fatalf("InstanceName = %q, want %q", out.InstanceName, in.InstanceName)
	}
	if out.ImageName != in.ImageName {
		t.Fatalf("ImageName = %q, want %q", out.ImageName, in.ImageName)
	}
	if len(out.Policy.NetworkAllow) != 2 || out.Policy.NetworkAllow[0] != "github.com" {
		t.Fatalf("Policy.NetworkAllow = %#v, want github.com + cidr", out.Policy.NetworkAllow)
	}
	if len(out.Policy.NetworkDeny) != 1 || out.Policy.NetworkDeny[0] != "snowflake.com" {
		t.Fatalf("Policy.NetworkDeny = %#v, want [snowflake.com]", out.Policy.NetworkDeny)
	}
	if len(out.Policy.FileAllowPaths) != 1 || out.Policy.FileAllowPaths[0] != "/Users/test/work" {
		t.Fatalf("Policy.FileAllowPaths = %#v, want [/Users/test/work]", out.Policy.FileAllowPaths)
	}
	if len(out.Policy.FileDenyExts) != 1 || out.Policy.FileDenyExts[0] != ".pem" {
		t.Fatalf("Policy.FileDenyExts = %#v, want [.pem]", out.Policy.FileDenyExts)
	}
}

func TestRuntimeConfigEffectiveInstanceName(t *testing.T) {
	cfg := RuntimeConfig{}
	if got := cfg.EffectiveInstanceName(); got != DefaultInstanceName {
		t.Fatalf("EffectiveInstanceName() = %q, want %q", got, DefaultInstanceName)
	}
	if got := cfg.EffectiveInstanceNameForDaemon("team-a"); got != "firebox-host-team-a" {
		t.Fatalf("EffectiveInstanceNameForDaemon() = %q, want %q", got, "firebox-host-team-a")
	}
	cfg.InstanceName = "firebox-img-dev"
	if got := cfg.EffectiveInstanceName(); got != "firebox-img-dev" {
		t.Fatalf("EffectiveInstanceName() = %q, want %q", got, "firebox-img-dev")
	}
	if got := cfg.EffectiveInstanceNameForDaemon("team-a"); got != "firebox-img-dev" {
		t.Fatalf("EffectiveInstanceNameForDaemon() = %q, want %q", got, "firebox-img-dev")
	}
}
