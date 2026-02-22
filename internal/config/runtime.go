package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type RuntimeConfig struct {
	InstanceName string              `json:"instance_name,omitempty"`
	ImageName    string              `json:"image_name,omitempty"`
	Policy       RuntimePolicyConfig `json:"policy,omitempty"`
}

type RuntimePolicyConfig struct {
	NetworkAllow   []string `json:"network_allow,omitempty"`
	NetworkDeny    []string `json:"network_deny,omitempty"`
	FileAllowPaths []string `json:"file_allow_paths,omitempty"`
	FileDenyPaths  []string `json:"file_deny_paths,omitempty"`
	FileAllowExts  []string `json:"file_allow_exts,omitempty"`
	FileDenyExts   []string `json:"file_deny_exts,omitempty"`
}

func LoadRuntimeConfig(path string) (RuntimeConfig, error) {
	var cfg RuntimeConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read runtime config: %w", err)
	}
	if len(data) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("decode runtime config: %w", err)
	}
	return cfg, nil
}

func SaveRuntimeConfig(path string, cfg RuntimeConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write runtime config: %w", err)
	}
	return nil
}

func (c RuntimeConfig) EffectiveInstanceName() string {
	if v := strings.TrimSpace(c.InstanceName); v != "" {
		return v
	}
	return DefaultInstanceName
}
