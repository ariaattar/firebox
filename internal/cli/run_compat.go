package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"firebox/internal/model"
	"firebox/internal/mountspec"
)

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func parseRunCommand(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("missing command: expected <command...> or -- <command...>")
	}
	return args, nil
}

func mergeMountInputs(rawMounts, rawVolumes, rawSandbox []string, globalCow model.CowMode) ([]model.MountSpec, error) {
	combined := make([]string, 0, len(rawMounts)+len(rawVolumes)+len(rawSandbox))
	combined = append(combined, rawMounts...)
	combined = append(combined, rawVolumes...)

	for _, s := range rawSandbox {
		source, guest, err := parseSandboxMount(s)
		if err != nil {
			return nil, err
		}
		combined = append(combined, fmt.Sprintf("%s:%s:rw:cow=on", source, guest))
	}

	return mountspec.ParseMany(combined, globalCow)
}

func parseSandboxMount(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("empty --sandbox value")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("resolve cwd: %w", err)
	}

	var source, guest string
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) == 1 {
		source = cwd
		guest = parts[0]
	} else {
		source = parts[0]
		guest = parts[1]
		if source == "" {
			source = cwd
		}
	}

	if !filepath.IsAbs(source) {
		abs, err := filepath.Abs(source)
		if err != nil {
			return "", "", fmt.Errorf("resolve sandbox source %q: %w", source, err)
		}
		source = abs
	}
	if !strings.HasPrefix(guest, "/") {
		return "", "", fmt.Errorf("sandbox destination must be absolute: %q", guest)
	}
	return source, guest, nil
}

func normalizeEnvVars(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	for _, env := range raw {
		key := env
		value := ""
		if strings.Contains(env, "=") {
			parts := strings.SplitN(env, "=", 2)
			key = parts[0]
			value = parts[1]
		} else {
			value = os.Getenv(env)
		}
		if !envKeyRe.MatchString(key) {
			return nil, fmt.Errorf("invalid environment variable key %q", key)
		}
		out = append(out, key+"="+value)
	}
	return out, nil
}
