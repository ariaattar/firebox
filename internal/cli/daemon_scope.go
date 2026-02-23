package cli

import (
	"os"
	"strings"

	"firebox/internal/config"
)

var cliDaemonID string

func resolveDaemonID(override string) (string, error) {
	candidate := strings.TrimSpace(override)
	if candidate == "" {
		candidate = strings.TrimSpace(cliDaemonID)
	}
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv(config.DaemonIDEnvVar))
	}
	return config.NormalizeDaemonID(candidate)
}

func applyDaemonIDEnv(override string) error {
	id, err := resolveDaemonID(override)
	if err != nil {
		return err
	}
	if id == "" {
		return os.Unsetenv(config.DaemonIDEnvVar)
	}
	return os.Setenv(config.DaemonIDEnvVar, id)
}
