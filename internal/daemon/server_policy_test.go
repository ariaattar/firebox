package daemon

import (
	"path/filepath"
	"testing"

	"firebox/internal/config"
	"firebox/internal/model"
)

func TestApplyRuntimePolicyDefaults(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "runtime.json")
	cfg := config.RuntimeConfig{
		Policy: config.RuntimePolicyConfig{
			NetworkAllow:   []string{"github.com"},
			NetworkDeny:    []string{"snowflake.com"},
			FileAllowPaths: []string{"/Users/test/work"},
			FileDenyPaths:  []string{"/Users/test/secrets"},
			FileAllowExts:  []string{".go"},
			FileDenyExts:   []string{".pem"},
		},
	}
	if err := config.SaveRuntimeConfig(runtimePath, cfg); err != nil {
		t.Fatalf("SaveRuntimeConfig() error = %v", err)
	}

	s := &Server{paths: config.Paths{Runtime: runtimePath}}
	spec := model.RunSpec{}
	if err := s.applyRuntimePolicyDefaults(&spec); err != nil {
		t.Fatalf("applyRuntimePolicyDefaults() error = %v", err)
	}
	if len(spec.NetworkAllow) != 1 || spec.NetworkAllow[0] != "github.com" {
		t.Fatalf("NetworkAllow = %#v, want [github.com]", spec.NetworkAllow)
	}
	if len(spec.FileDenyExts) != 1 || spec.FileDenyExts[0] != ".pem" {
		t.Fatalf("FileDenyExts = %#v, want [.pem]", spec.FileDenyExts)
	}
}

func TestApplyRuntimePolicyDefaultsRespectsExplicitSpec(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "runtime.json")
	cfg := config.RuntimeConfig{
		Policy: config.RuntimePolicyConfig{
			NetworkAllow: []string{"github.com"},
		},
	}
	if err := config.SaveRuntimeConfig(runtimePath, cfg); err != nil {
		t.Fatalf("SaveRuntimeConfig() error = %v", err)
	}

	s := &Server{paths: config.Paths{Runtime: runtimePath}}
	spec := model.RunSpec{
		NetworkAllow: []string{"api.github.com"},
	}
	if err := s.applyRuntimePolicyDefaults(&spec); err != nil {
		t.Fatalf("applyRuntimePolicyDefaults() error = %v", err)
	}
	if len(spec.NetworkAllow) != 1 || spec.NetworkAllow[0] != "api.github.com" {
		t.Fatalf("NetworkAllow = %#v, want [api.github.com]", spec.NetworkAllow)
	}
}
