package policy

import (
	"fmt"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"firebox/internal/model"
)

var hostnameLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func NormalizeAndValidateSpec(spec *model.RunSpec) error {
	if spec == nil {
		return fmt.Errorf("spec is nil")
	}

	if spec.Cow == model.CowAuto {
		spec.Cow = model.CowOn
	}
	if spec.Network == "" {
		spec.Network = model.NetworkNAT
	}
	switch spec.Network {
	case model.NetworkNAT, model.NetworkNone:
	default:
		return fmt.Errorf("invalid network mode %q, expected nat|none", spec.Network)
	}

	var err error
	spec.NetworkAllow, err = normalizeNetworkEntries(spec.NetworkAllow, "network allow")
	if err != nil {
		return err
	}
	spec.NetworkDeny, err = normalizeNetworkEntries(spec.NetworkDeny, "network deny")
	if err != nil {
		return err
	}
	if spec.Network == model.NetworkNone && len(spec.NetworkAllow) > 0 {
		return fmt.Errorf("network allow list is incompatible with network=none")
	}

	spec.FileAllowPaths, err = normalizePathPatterns(spec.FileAllowPaths, "file allow path")
	if err != nil {
		return err
	}
	spec.FileDenyPaths, err = normalizePathPatterns(spec.FileDenyPaths, "file deny path")
	if err != nil {
		return err
	}
	spec.FileAllowExts, err = normalizeExtensions(spec.FileAllowExts, "file allow extension")
	if err != nil {
		return err
	}
	spec.FileDenyExts, err = normalizeExtensions(spec.FileDenyExts, "file deny extension")
	if err != nil {
		return err
	}

	return nil
}

func ValidateMounts(spec model.RunSpec) error {
	hasPathPolicies := len(spec.FileAllowPaths) > 0 || len(spec.FileDenyPaths) > 0
	hasExtPolicies := len(spec.FileAllowExts) > 0 || len(spec.FileDenyExts) > 0
	if !hasPathPolicies && !hasExtPolicies {
		return nil
	}

	for _, m := range spec.Mounts {
		cleaned := filepath.ToSlash(filepath.Clean(m.HostPath))

		if matchesAnyPath(cleaned, spec.FileDenyPaths) {
			return fmt.Errorf("mount host path %q is blocked by file deny path policy", m.HostPath)
		}
		if len(spec.FileAllowPaths) > 0 && !matchesAnyPath(cleaned, spec.FileAllowPaths) {
			return fmt.Errorf("mount host path %q is not allowed by file allow path policy", m.HostPath)
		}

		if !hasExtPolicies {
			continue
		}

		info, err := os.Stat(m.HostPath)
		if err != nil {
			return fmt.Errorf("stat mount host path %q: %w", m.HostPath, err)
		}
		if info.IsDir() {
			return fmt.Errorf("file extension policies require file mounts, but %q is a directory", m.HostPath)
		}

		ext := strings.ToLower(filepath.Ext(m.HostPath))
		if ext != "" && contains(spec.FileDenyExts, ext) {
			return fmt.Errorf("mount host path %q is blocked by file deny extension policy", m.HostPath)
		}
		if len(spec.FileAllowExts) > 0 && !contains(spec.FileAllowExts, ext) {
			if ext == "" {
				return fmt.Errorf("mount host path %q has no extension, but file allow extension policy is set", m.HostPath)
			}
			return fmt.Errorf("mount host path %q extension %q is not allowed", m.HostPath, ext)
		}
	}

	return nil
}

func SplitNetworkByFamily(entries []string) (ipv4 []string, ipv6 []string) {
	ipv4, ipv6, _ = SplitNetworkDestinations(entries)
	return ipv4, ipv6
}

func SplitNetworkDestinations(entries []string) (ipv4 []string, ipv6 []string, hostnames []string) {
	ipv4 = make([]string, 0, len(entries))
	ipv6 = make([]string, 0, len(entries))
	hostnames = make([]string, 0, len(entries))

	for _, entry := range entries {
		if pfx, err := netip.ParsePrefix(entry); err == nil {
			if pfx.Addr().Is6() {
				ipv6 = append(ipv6, entry)
			} else {
				ipv4 = append(ipv4, entry)
			}
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			if addr.Is6() {
				ipv6 = append(ipv6, entry)
			} else {
				ipv4 = append(ipv4, entry)
			}
			continue
		}
		hostnames = append(hostnames, entry)
	}
	return ipv4, ipv6, hostnames
}

func normalizeNetworkEntries(raw []string, label string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))

	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		normalized, err := normalizeNetworkDestination(entry)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", label, entry, err)
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	return out, nil
}

func normalizeNetworkDestination(v string) (string, error) {
	if pfx, err := netip.ParsePrefix(v); err == nil {
		return pfx.Masked().String(), nil
	}
	if addr, err := netip.ParseAddr(v); err == nil {
		return addr.String(), nil
	}

	host, err := normalizeHostname(v)
	if err != nil {
		return "", fmt.Errorf("must be an IP, CIDR block, hostname, or domain: %w", err)
	}
	return host, nil
}

func normalizeHostname(v string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(v))
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	if strings.ContainsAny(host, "/*?[]") {
		return "", fmt.Errorf("wildcards are not supported")
	}
	if len(host) > 253 {
		return "", fmt.Errorf("host is too long")
	}

	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !hostnameLabelRe.MatchString(label) {
			return "", fmt.Errorf("invalid host label %q", label)
		}
	}
	return host, nil
}

func normalizePathPatterns(raw []string, label string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))

	for _, pattern := range raw {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		pattern = filepath.ToSlash(filepath.Clean(pattern))
		if !strings.HasPrefix(pattern, "/") {
			return nil, fmt.Errorf("%s %q must be absolute", label, pattern)
		}
		if hasGlob(pattern) {
			if _, err := path.Match(pattern, pattern); err != nil {
				return nil, fmt.Errorf("%s %q is not a valid glob: %w", label, pattern, err)
			}
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		out = append(out, pattern)
	}

	return out, nil
}

func normalizeExtensions(raw []string, label string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))

	for _, ext := range raw {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if strings.ContainsAny(ext, `/\`) {
			return nil, fmt.Errorf("%s %q must not contain path separators", label, ext)
		}
		if strings.ContainsAny(ext, "*?[]") {
			return nil, fmt.Errorf("%s %q must be a literal extension, not a glob", label, ext)
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if ext == "." {
			return nil, fmt.Errorf("%s cannot be '.'", label)
		}
		if _, ok := seen[ext]; ok {
			continue
		}
		seen[ext] = struct{}{}
		out = append(out, ext)
	}

	return out, nil
}

func matchesAnyPath(candidate string, patterns []string) bool {
	for _, pattern := range patterns {
		if pathMatches(pattern, candidate) {
			return true
		}
	}
	return false
}

func pathMatches(pattern, candidate string) bool {
	if hasGlob(pattern) {
		ok, err := path.Match(pattern, candidate)
		return err == nil && ok
	}

	if pattern == "/" {
		return true
	}
	return candidate == pattern || strings.HasPrefix(candidate, pattern+"/")
}

func hasGlob(v string) bool {
	return strings.ContainsAny(v, "*?[")
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
