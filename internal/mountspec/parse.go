package mountspec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"firebox/internal/model"
)

func ParseMany(raw []string, globalCow model.CowMode) ([]model.MountSpec, error) {
	mounts := make([]model.MountSpec, 0, len(raw))
	for _, entry := range raw {
		m, err := ParseOne(entry, globalCow)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, m)
	}
	return mounts, nil
}

func ParseOne(raw string, globalCow model.CowMode) (model.MountSpec, error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 4 {
		return model.MountSpec{}, fmt.Errorf("invalid mount %q, expected /host:/guest[:rw|ro][:cow=on|off]", raw)
	}

	hostPath, err := expandPath(parts[0])
	if err != nil {
		return model.MountSpec{}, err
	}
	guestPath := filepath.Clean(parts[1])

	m := model.MountSpec{
		HostPath:  hostPath,
		GuestPath: guestPath,
		Access:    model.AccessRW,
		Cow:       globalCow,
	}

	for _, opt := range parts[2:] {
		opt = strings.TrimSpace(opt)
		switch opt {
		case "rw":
			m.Access = model.AccessRW
		case "ro":
			m.Access = model.AccessRO
		case "cow=on":
			m.Cow = model.CowOn
		case "cow=off":
			m.Cow = model.CowOff
		default:
			return model.MountSpec{}, fmt.Errorf("invalid mount option %q in %q", opt, raw)
		}
	}

	if err := Validate(m); err != nil {
		return model.MountSpec{}, err
	}

	return m, nil
}

func Validate(m model.MountSpec) error {
	if !filepath.IsAbs(m.HostPath) {
		return fmt.Errorf("host path must be absolute: %q", m.HostPath)
	}
	if !filepath.IsAbs(m.GuestPath) {
		return fmt.Errorf("guest path must be absolute: %q", m.GuestPath)
	}
	if _, err := os.Stat(m.HostPath); err != nil {
		return fmt.Errorf("host path %q: %w", m.HostPath, err)
	}
	if m.Access != model.AccessRW && m.Access != model.AccessRO {
		return fmt.Errorf("invalid access mode %q", m.Access)
	}
	if m.Cow != model.CowOn && m.Cow != model.CowOff {
		m.Cow = model.CowOn
	}
	return nil
}

func NeedsHostWriteAck(mounts []model.MountSpec, global model.CowMode) bool {
	for _, m := range mounts {
		if m.DirectHostWrite(global) {
			return true
		}
	}
	return false
}

func expandPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
	}
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("resolve absolute path %q: %w", p, err)
		}
		p = abs
	}
	return filepath.Clean(p), nil
}
