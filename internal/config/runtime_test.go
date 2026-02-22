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
}

func TestRuntimeConfigEffectiveInstanceName(t *testing.T) {
	cfg := RuntimeConfig{}
	if got := cfg.EffectiveInstanceName(); got != DefaultInstanceName {
		t.Fatalf("EffectiveInstanceName() = %q, want %q", got, DefaultInstanceName)
	}
	cfg.InstanceName = "firebox-img-dev"
	if got := cfg.EffectiveInstanceName(); got != "firebox-img-dev" {
		t.Fatalf("EffectiveInstanceName() = %q, want %q", got, "firebox-img-dev")
	}
}
