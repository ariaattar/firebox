package limafc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"firebox/internal/config"
	"firebox/internal/model"
	"firebox/internal/policy"
)

const (
	runtimeEnsurePeriod = 60 * time.Second
)

type Backend struct {
	paths        config.Paths
	instanceName string
	controlSock  string

	mu               sync.Mutex
	sshConfig        string
	sshHost          string
	runtimeCheckedAt time.Time
}

func New(paths config.Paths) *Backend {
	instanceName := config.DefaultInstanceNameForDaemon(paths.DaemonID)
	if cfg, err := config.LoadRuntimeConfig(paths.Runtime); err == nil {
		instanceName = cfg.EffectiveInstanceNameForDaemon(paths.DaemonID)
	}

	return &Backend{
		paths:        paths,
		instanceName: instanceName,
		controlSock:  filepath.Join(paths.StateDir, "ssh-control-"+sanitizeSockName(instanceName)+".sock"),
	}
}

func sanitizeSockName(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "firebox-host"
	}
	var sb strings.Builder
	sb.Grow(len(v))
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			sb.WriteRune(r)
		case r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '-', r == '_', r == '.':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	out := strings.Trim(sb.String(), "._-")
	if out == "" {
		return "firebox-host"
	}
	return out
}

func (b *Backend) EnsureHost(ctx context.Context) error {
	if b.fastHostReady(ctx) {
		return b.maybeEnsureHostRuntime(ctx)
	}

	if _, err := exec.LookPath("limactl"); err != nil {
		return fmt.Errorf("limactl not found: %w", err)
	}

	inst, found, err := b.findInstance(ctx)
	if err != nil {
		return err
	}
	if !found {
		if err := b.createInstance(ctx); err != nil {
			return err
		}
	} else if !strings.EqualFold(inst.Status, "running") {
		if err := b.startInstance(ctx); err != nil {
			return err
		}
	}

	inst, found, err = b.findInstance(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("instance %q not found after start", b.instanceName)
	}
	if !strings.EqualFold(inst.Status, "running") {
		if err := b.waitForRunning(ctx, 60*time.Second); err != nil {
			return err
		}
		inst, found, err = b.findInstance(ctx)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("instance %q disappeared", b.instanceName)
		}
	}

	if inst.SSHConfigFile == "" {
		return fmt.Errorf("instance %q missing ssh config file", b.instanceName)
	}

	b.mu.Lock()
	b.sshConfig = inst.SSHConfigFile
	b.mu.Unlock()

	if err := b.ensureSSHControl(ctx); err != nil {
		return err
	}
	return b.maybeEnsureHostRuntime(ctx)
}

func (b *Backend) Warm(ctx context.Context, _ int) error {
	return b.EnsureHost(ctx)
}

func (b *Backend) Run(ctx context.Context, spec model.RunSpec) (model.ExecResult, error) {
	if len(spec.Command) == 0 {
		spec.Command = []string{"/bin/bash"}
	}
	if spec.Cow == model.CowAuto {
		spec.Cow = model.CowOn
	}
	if spec.TimeoutMs <= 0 {
		spec.TimeoutMs = 5000
	}
	if spec.PersistSession && strings.TrimSpace(spec.SessionID) == "" {
		return model.ExecResult{}, errors.New("persist_session requires session_id")
	}

	if err := b.EnsureHost(ctx); err != nil {
		return model.ExecResult{}, err
	}

	timeout := time.Duration(spec.TimeoutMs) * time.Millisecond
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	script := b.buildRunScript(spec)
	start := time.Now()
	stdout, stderr, err := b.runSSHScript(runCtx, script)
	duration := time.Since(start)

	res := model.ExecResult{
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   0,
		DurationMs: duration.Milliseconds(),
	}
	if err == nil {
		return res, nil
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.ExitCode = 124
		if res.Stderr == "" {
			res.Stderr = "command timed out"
		}
		return res, nil
	}

	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}

	res.ExitCode = 1
	return res, fmt.Errorf("remote run failed: %w", err)
}

type cowMountPlan struct {
	ID        string `json:"id"`
	HostPath  string `json:"host_path"`
	GuestPath string `json:"guest_path"`
	Kind      string `json:"kind"`
}

type remoteDiffPayload struct {
	Mounts []struct {
		GuestPath string `json:"guest_path"`
		HostPath  string `json:"host_path"`
		Added     int    `json:"added"`
		Modified  int    `json:"modified"`
		Deleted   int    `json:"deleted"`
		Truncated bool   `json:"truncated"`
		Changes   []struct {
			Op   string `json:"op"`
			Path string `json:"path"`
		} `json:"changes"`
	} `json:"mounts"`
}

type remoteApplyPayload struct {
	Mounts []struct {
		ID        string `json:"id"`
		GuestPath string `json:"guest_path"`
		HostPath  string `json:"host_path"`
		Kind      string `json:"kind"`
		UpperPath string `json:"upper_path"`
		Applied   int    `json:"applied"`
		Deleted   int    `json:"deleted"`
		Whiteouts []struct {
			Path   string `json:"path"`
			Opaque bool   `json:"opaque"`
		} `json:"whiteouts"`
	} `json:"mounts"`
}

func (b *Backend) SandboxDiff(ctx context.Context, sandboxID string, spec model.RunSpec, guestPath string, limit int) (model.SandboxDiffResult, error) {
	start := time.Now()
	result := model.SandboxDiffResult{
		SandboxID: sandboxID,
		Path:      strings.TrimSpace(guestPath),
	}
	if limit <= 0 {
		limit = 200
	}

	if err := b.EnsureHost(ctx); err != nil {
		return result, err
	}

	mounts, err := selectCowMounts(spec, guestPath)
	if err != nil {
		return result, err
	}
	if len(mounts) == 0 {
		result.DurationMs = time.Since(start).Milliseconds()
		return result, nil
	}

	mountJSON, err := json.Marshal(mounts)
	if err != nil {
		return result, fmt.Errorf("encode mount plan: %w", err)
	}
	script := buildDiffScript(sandboxSessionKey(sandboxID), string(mountJSON), limit)
	stdout, stderr, err := b.runSSHScript(ctx, script)
	if err != nil {
		return result, remoteScriptErr("sandbox diff", stdout, stderr, err)
	}

	var payload remoteDiffPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		return result, fmt.Errorf("decode sandbox diff output: %w", err)
	}

	result.Mounts = make([]model.SandboxMountDiff, 0, len(payload.Mounts))
	for _, m := range payload.Mounts {
		out := model.SandboxMountDiff{
			GuestPath: m.GuestPath,
			HostPath:  m.HostPath,
			Added:     m.Added,
			Modified:  m.Modified,
			Deleted:   m.Deleted,
			Truncated: m.Truncated,
		}
		for _, ch := range m.Changes {
			out.Changes = append(out.Changes, model.SandboxDiffChange{
				Op:   model.DiffOp(ch.Op),
				Path: ch.Path,
			})
		}
		result.Mounts = append(result.Mounts, out)
		result.Added += m.Added
		result.Modified += m.Modified
		result.Deleted += m.Deleted
	}
	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

func (b *Backend) SandboxApply(ctx context.Context, sandboxID string, spec model.RunSpec, guestPath string) (model.SandboxApplyResult, error) {
	start := time.Now()
	result := model.SandboxApplyResult{
		SandboxID: sandboxID,
		Path:      strings.TrimSpace(guestPath),
	}

	if err := b.EnsureHost(ctx); err != nil {
		return result, err
	}

	mounts, err := selectCowMounts(spec, guestPath)
	if err != nil {
		return result, err
	}
	if len(mounts) == 0 {
		result.DurationMs = time.Since(start).Milliseconds()
		return result, nil
	}

	mountJSON, err := json.Marshal(mounts)
	if err != nil {
		return result, fmt.Errorf("encode mount plan: %w", err)
	}
	script := buildApplyScript(sandboxSessionKey(sandboxID), string(mountJSON))
	stdout, stderr, err := b.runSSHScript(ctx, script)
	if err != nil {
		return result, remoteScriptErr("sandbox apply", stdout, stderr, err)
	}

	var payload remoteApplyPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		return result, fmt.Errorf("decode sandbox apply output: %w", err)
	}

	result.Mounts = make([]model.SandboxMountApply, 0, len(payload.Mounts))
	resetIDs := make([]string, 0, len(payload.Mounts))
	for _, m := range payload.Mounts {
		whiteouts := make([]whiteoutEntry, 0, len(m.Whiteouts))
		for _, w := range m.Whiteouts {
			whiteouts = append(whiteouts, whiteoutEntry{
				Path:   w.Path,
				Opaque: w.Opaque,
			})
		}
		if err := applyWhiteoutsLocal(m.HostPath, whiteouts); err != nil {
			return result, fmt.Errorf("apply whiteouts for %s: %w", m.GuestPath, err)
		}

		if m.UpperPath != "" {
			if err := b.rsyncUpperToHost(ctx, m.Kind, m.UpperPath, m.HostPath); err != nil {
				return result, fmt.Errorf("sync %s to host: %w", m.GuestPath, err)
			}
		}
		if m.Kind == "dir" && (m.UpperPath != "" || m.Deleted > 0) {
			resetIDs = append(resetIDs, m.ID)
		}

		out := model.SandboxMountApply{
			GuestPath: m.GuestPath,
			HostPath:  m.HostPath,
			Applied:   m.Applied,
			Deleted:   m.Deleted,
		}
		result.Mounts = append(result.Mounts, out)
		result.Applied += m.Applied
		result.Deleted += m.Deleted
	}
	if err := b.clearSessionMounts(ctx, sandboxSessionKey(sandboxID), resetIDs); err != nil {
		return result, err
	}
	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

func (b *Backend) CleanupSandbox(ctx context.Context, sandboxID string) error {
	if strings.TrimSpace(sandboxID) == "" {
		return nil
	}
	if err := b.EnsureHost(ctx); err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("set -euo pipefail\n")
	sb.WriteString("FB_BASE=\"${HOME}/.firebox-host\"\n")
	sb.WriteString("SESSION_ROOT=\"${FB_BASE}/sessions/")
	sb.WriteString(shDQuoteEscape(sandboxSessionKey(sandboxID)))
	sb.WriteString("\"\n")
	sb.WriteString("rm -rf --one-file-system \"${SESSION_ROOT}\"\n")
	stdout, stderr, err := b.runSSHScript(ctx, sb.String())
	if err != nil {
		return remoteScriptErr("cleanup sandbox session", stdout, stderr, err)
	}
	return nil
}

func selectCowMounts(spec model.RunSpec, guestPath string) ([]cowMountPlan, error) {
	filter := strings.TrimSpace(guestPath)
	if filter != "" {
		if !path.IsAbs(filter) {
			return nil, fmt.Errorf("path must be absolute: %q", guestPath)
		}
		filter = path.Clean(filter)
	}

	out := make([]cowMountPlan, 0, len(spec.Mounts))
	for i, m := range spec.Mounts {
		if m.Access != model.AccessRW {
			continue
		}
		if m.EffectiveCow(spec.Cow) != model.CowOn {
			continue
		}
		guest := path.Clean(m.GuestPath)
		if filter != "" && guest != filter {
			continue
		}
		kind := "dir"
		info, err := os.Stat(m.HostPath)
		if err != nil {
			return nil, fmt.Errorf("stat mount source %q: %w", m.HostPath, err)
		}
		if !info.IsDir() {
			kind = "file"
		}
		out = append(out, cowMountPlan{
			ID:        fmt.Sprintf("m%d", i),
			HostPath:  path.Clean(m.HostPath),
			GuestPath: guest,
			Kind:      kind,
		})
	}

	if filter != "" && len(out) == 0 {
		return nil, fmt.Errorf("sandbox has no writable cow mount at %s", filter)
	}
	return out, nil
}

func sandboxSessionKey(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "sandbox"
	}
	var sb strings.Builder
	sb.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			sb.WriteRune(r)
		case r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '-', r == '_', r == '.':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	out := strings.Trim(sb.String(), "._-")
	if out == "" {
		return "sandbox"
	}
	return out
}

func remoteScriptErr(action, stdout, stderr string, err error) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = strings.TrimSpace(stdout)
	}
	if msg != "" {
		return fmt.Errorf("%s: %s", action, msg)
	}
	return fmt.Errorf("%s: %w", action, err)
}

type whiteoutEntry struct {
	Path   string
	Opaque bool
}

func applyWhiteoutsLocal(base string, whiteouts []whiteoutEntry) error {
	if len(whiteouts) == 0 {
		return nil
	}
	base = filepath.Clean(base)

	sort.Slice(whiteouts, func(i, j int) bool {
		iDepth := strings.Count(whiteouts[i].Path, "/")
		jDepth := strings.Count(whiteouts[j].Path, "/")
		if iDepth != jDepth {
			return iDepth > jDepth
		}
		if whiteouts[i].Opaque != whiteouts[j].Opaque {
			return whiteouts[i].Opaque
		}
		return whiteouts[i].Path > whiteouts[j].Path
	})

	for _, w := range whiteouts {
		target, err := safeJoin(base, w.Path)
		if err != nil {
			return err
		}
		if w.Opaque {
			if err := clearDirContents(target); err != nil {
				return err
			}
			continue
		}
		if err := removePath(target); err != nil {
			return err
		}
	}
	return nil
}

func safeJoin(base, rel string) (string, error) {
	base = filepath.Clean(base)
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		return base, nil
	}
	rel = filepath.Clean(filepath.FromSlash(rel))
	if rel == "." {
		return base, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid relative path %q", rel)
	}
	target := filepath.Join(base, rel)
	target = filepath.Clean(target)
	if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes mount root: %q", rel)
	}
	return target, nil
}

func clearDirContents(dir string) error {
	info, err := os.Lstat(dir)
	switch {
	case os.IsNotExist(err):
		return os.MkdirAll(dir, 0o755)
	case err != nil:
		return err
	}
	if !info.IsDir() || (info.Mode()&os.ModeSymlink) != 0 {
		if err := removePath(dir); err != nil {
			return err
		}
		return os.MkdirAll(dir, 0o755)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := removePath(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func removePath(target string) error {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if (info.Mode() & os.ModeSymlink) != 0 {
		return os.Remove(target)
	}
	if info.IsDir() {
		return os.RemoveAll(target)
	}
	return os.Remove(target)
}

func (b *Backend) rsyncUpperToHost(ctx context.Context, kind, remotePath, hostPath string) error {
	b.mu.Lock()
	cfg := b.sshConfig
	host := b.sshHost
	b.mu.Unlock()
	if cfg == "" || host == "" {
		return errors.New("ssh control info not initialized")
	}
	sshCmd := fmt.Sprintf("ssh -S %s -F %s", b.controlSock, cfg)
	remoteSpec := host + ":" + remotePath

	args := []string{"-a", "--no-devices", "--no-specials", "-e", sshCmd}
	switch kind {
	case "file":
		if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
			return err
		}
		args = append(args, remoteSpec, hostPath)
	default:
		if err := os.MkdirAll(hostPath, 0o755); err != nil {
			return err
		}
		args = append(args, "--exclude=.wh.*", remoteSpec+"/", hostPath+"/")
	}

	cmd := exec.CommandContext(ctx, "rsync", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rsync failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (b *Backend) clearSessionMounts(ctx context.Context, sessionKey string, mountIDs []string) error {
	if len(mountIDs) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("set -euo pipefail\n")
	sb.WriteString("if ! sudo -n true 2>/dev/null; then echo \"passwordless sudo is required for cow reset\" 1>&2; exit 24; fi\n")
	sb.WriteString("FB_BASE=\"${HOME}/.firebox-host\"\n")
	sb.WriteString("SESSION_ROOT=\"${FB_BASE}/sessions/")
	sb.WriteString(shDQuoteEscape(sessionKey))
	sb.WriteString("\"\n")
	sb.WriteString("if [ ! -d \"${SESSION_ROOT}\" ]; then exit 0; fi\n")
	for _, id := range mountIDs {
		sb.WriteString("MOUNT_ROOT=\"${SESSION_ROOT}/mounts/")
		sb.WriteString(shDQuoteEscape(id))
		sb.WriteString("\"\n")
		sb.WriteString("sudo -n rm -rf --one-file-system \"${MOUNT_ROOT}/upper\" \"${MOUNT_ROOT}/work\"\n")
		sb.WriteString("mkdir -p \"${MOUNT_ROOT}/upper\" \"${MOUNT_ROOT}/work\" \"${MOUNT_ROOT}/merged\"\n")
	}
	stdout, stderr, err := b.runSSHScript(ctx, sb.String())
	if err != nil {
		return remoteScriptErr("reset sandbox cow state", stdout, stderr, err)
	}
	return nil
}

func buildDiffScript(sessionKey, mountsJSON string, limit int) string {
	var sb strings.Builder
	sb.WriteString("set -euo pipefail\n")
	sb.WriteString("FB_BASE=\"${HOME}/.firebox-host\"\n")
	sb.WriteString("SESSION_ROOT=\"${FB_BASE}/sessions/")
	sb.WriteString(shDQuoteEscape(sessionKey))
	sb.WriteString("\"\n")
	sb.WriteString("if [ ! -d \"${SESSION_ROOT}\" ]; then echo '{\"mounts\":[]}'; exit 0; fi\n")
	sb.WriteString("export FIREBOX_SESSION_ROOT=\"${SESSION_ROOT}\"\n")
	sb.WriteString("export FIREBOX_MOUNTS_JSON=")
	sb.WriteString(shQuote(mountsJSON))
	sb.WriteString("\n")
	sb.WriteString("export FIREBOX_DIFF_LIMIT=")
	sb.WriteString(shQuote(fmt.Sprintf("%d", limit)))
	sb.WriteString("\n")
	sb.WriteString("python3 - <<'PY'\n")
	sb.WriteString(`import json
import os
import stat

session_root = os.environ["FIREBOX_SESSION_ROOT"]
mounts = json.loads(os.environ["FIREBOX_MOUNTS_JSON"])
limit = int(os.environ.get("FIREBOX_DIFF_LIMIT", "200"))


def guest_join(base: str, rel: str) -> str:
    if base == "/":
        return "/" + rel if rel else "/"
    return base + ("/" + rel if rel else "")


def files_equal(a: str, b: str) -> bool:
    try:
        sa = os.stat(a)
        sb = os.stat(b)
        if sa.st_size != sb.st_size:
            return False
        if int(sa.st_mtime) == int(sb.st_mtime):
            return True
        with open(a, "rb") as fa, open(b, "rb") as fb:
            return fa.read() == fb.read()
    except OSError:
        return False


result = {"mounts": []}

for mount in mounts:
    mount_root = os.path.join(session_root, "mounts", mount["id"])
    guest = mount["guest_path"] or "/"
    host = mount["host_path"]
    kind = mount.get("kind", "dir")

    added = 0
    modified = 0
    deleted = 0
    changes = []

    def record(op: str, rel_path: str) -> None:
        if len(changes) < limit:
            changes.append({"op": op, "path": guest_join(guest, rel_path)})

    if kind == "file":
        upper_file = os.path.join(mount_root, "file")
        if os.path.exists(upper_file):
            if not os.path.exists(host):
                added += 1
                record("add", "")
            elif not files_equal(upper_file, host):
                modified += 1
                record("modify", "")
    else:
        upper = os.path.join(mount_root, "upper")
        if os.path.isdir(upper):
            for root, _, files in os.walk(upper):
                rel_dir = os.path.relpath(root, upper)
                if rel_dir == ".":
                    rel_dir = ""
                for name in files:
                    full_path = os.path.join(root, name)
                    is_char_whiteout = False
                    try:
                        st = os.lstat(full_path)
                        is_char_whiteout = stat.S_ISCHR(st.st_mode) and os.major(st.st_rdev) == 0 and os.minor(st.st_rdev) == 0
                    except OSError:
                        is_char_whiteout = False

                    if name.startswith(".wh.") or is_char_whiteout:
                        if name == ".wh..wh..opq":
                            if rel_dir:
                                deleted += 1
                                record("delete", rel_dir)
                        else:
                            if name.startswith(".wh."):
                                target = name[4:]
                            else:
                                target = name
                            rel = os.path.join(rel_dir, target) if rel_dir else target
                            deleted += 1
                            record("delete", rel)
                        continue

                    rel = os.path.join(rel_dir, name) if rel_dir else name
                    lower = os.path.join(host, rel)
                    if os.path.lexists(lower):
                        modified += 1
                        record("modify", rel)
                    else:
                        added += 1
                        record("add", rel)

    if added or modified or deleted:
        result["mounts"].append(
            {
                "guest_path": guest,
                "host_path": host,
                "added": added,
                "modified": modified,
                "deleted": deleted,
                "truncated": len(changes) < (added + modified + deleted),
                "changes": changes,
            }
        )

print(json.dumps(result))
`)
	sb.WriteString("\nPY\n")
	return sb.String()
}

func buildApplyScript(sessionKey, mountsJSON string) string {
	var sb strings.Builder
	sb.WriteString("set -euo pipefail\n")
	sb.WriteString("FB_BASE=\"${HOME}/.firebox-host\"\n")
	sb.WriteString("SESSION_ROOT=\"${FB_BASE}/sessions/")
	sb.WriteString(shDQuoteEscape(sessionKey))
	sb.WriteString("\"\n")
	sb.WriteString("if [ ! -d \"${SESSION_ROOT}\" ]; then echo '{\"mounts\":[]}'; exit 0; fi\n")
	sb.WriteString("export FIREBOX_SESSION_ROOT=\"${SESSION_ROOT}\"\n")
	sb.WriteString("export FIREBOX_MOUNTS_JSON=")
	sb.WriteString(shQuote(mountsJSON))
	sb.WriteString("\n")
	sb.WriteString("python3 - <<'PY'\n")
	sb.WriteString(`import json
import os
import stat

session_root = os.environ["FIREBOX_SESSION_ROOT"]
mounts = json.loads(os.environ["FIREBOX_MOUNTS_JSON"])


def files_equal(a: str, b: str) -> bool:
    try:
        sa = os.stat(a)
        sb = os.stat(b)
        if sa.st_size != sb.st_size:
            return False
        if int(sa.st_mtime) == int(sb.st_mtime):
            return True
        with open(a, "rb") as fa, open(b, "rb") as fb:
            return fa.read() == fb.read()
    except OSError:
        return False


result = {"mounts": []}

for mount in mounts:
    mount_id = mount["id"]
    mount_root = os.path.join(session_root, "mounts", mount_id)
    guest = mount["guest_path"] or "/"
    host = mount["host_path"]
    kind = mount.get("kind", "dir")

    applied = 0
    deleted = 0
    whiteouts = []
    upper_path = ""

    if kind == "file":
        upper_file = os.path.join(mount_root, "file")
        if os.path.exists(upper_file):
            upper_path = upper_file
            if not os.path.exists(host) or not files_equal(upper_file, host):
                applied = 1
    else:
        upper = os.path.join(mount_root, "upper")
        if os.path.isdir(upper):
            upper_path = upper
            has_entries = False
            for root, dirs, files in os.walk(upper):
                rel_dir = os.path.relpath(root, upper)
                if rel_dir == ".":
                    rel_dir = ""
                if dirs or files:
                    has_entries = True
                for name in files:
                    full_path = os.path.join(root, name)
                    is_char_whiteout = False
                    try:
                        st = os.lstat(full_path)
                        is_char_whiteout = stat.S_ISCHR(st.st_mode) and os.major(st.st_rdev) == 0 and os.minor(st.st_rdev) == 0
                    except OSError:
                        is_char_whiteout = False

                    if name.startswith(".wh.") or is_char_whiteout:
                        if name == ".wh..wh..opq":
                            whiteouts.append({"path": rel_dir, "opaque": True})
                            deleted += 1
                        else:
                            if name.startswith(".wh."):
                                target = name[4:]
                            else:
                                target = name
                            rel = os.path.join(rel_dir, target) if rel_dir else target
                            whiteouts.append({"path": rel, "opaque": False})
                            deleted += 1
                        continue
                    applied += 1
            if not has_entries and deleted == 0:
                upper_path = ""

    if upper_path or deleted:
        result["mounts"].append(
            {
                "id": mount_id,
                "guest_path": guest,
                "host_path": host,
                "kind": kind,
                "upper_path": upper_path,
                "applied": applied,
                "deleted": deleted,
                "whiteouts": whiteouts,
            }
        )

print(json.dumps(result))
`)
	sb.WriteString("\nPY\n")
	return sb.String()
}

type limaInstance struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	SSHConfigFile string `json:"sshConfigFile"`
	Config        struct {
		Mounts []struct {
			Writable bool `json:"writable"`
		} `json:"mounts"`
	} `json:"config"`
}

func (b *Backend) findInstance(ctx context.Context) (limaInstance, bool, error) {
	instances, err := b.listInstances(ctx)
	if err != nil {
		return limaInstance{}, false, err
	}
	for _, inst := range instances {
		if inst.Name == b.instanceName {
			return inst, true, nil
		}
	}
	return limaInstance{}, false, nil
}

func (b *Backend) listInstances(ctx context.Context) ([]limaInstance, error) {
	cmd := exec.CommandContext(ctx, "limactl", "list", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("limactl list --json: %w", err)
	}

	instances := make([]limaInstance, 0)
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var inst limaInstance
		if err := json.Unmarshal([]byte(line), &inst); err != nil {
			continue
		}
		instances = append(instances, inst)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan limactl output: %w", err)
	}

	return instances, nil
}

func (b *Backend) createInstance(ctx context.Context) error {
	args := []string{
		"start", "-y",
		"--name", b.instanceName,
		"--set", `.vmType="vz"`,
		"--set", `.nestedVirtualization=true`,
		"--set", `.mountType="virtiofs"`,
		"--mount-writable",
		"template://default",
	}
	cmd := exec.CommandContext(ctx, "limactl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create lima instance: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (b *Backend) startInstance(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "limactl", "start", "-y", b.instanceName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start lima instance %q: %w: %s", b.instanceName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (b *Backend) waitForRunning(ctx context.Context, maxWait time.Duration) error {
	deadline := time.Now().Add(maxWait)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %q to become running", b.instanceName)
		}

		inst, found, err := b.findInstance(ctx)
		if err == nil && found && strings.EqualFold(inst.Status, "running") {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
}

func (b *Backend) ensureSSHControl(ctx context.Context) error {
	b.mu.Lock()
	sshConfig := b.sshConfig
	b.mu.Unlock()
	if sshConfig == "" {
		return errors.New("ssh config path missing")
	}

	alias, err := parseSSHAlias(sshConfig)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.sshHost = alias
	b.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(b.controlSock), 0o755); err != nil {
		return fmt.Errorf("create state dir for ssh control: %w", err)
	}

	checkCmd := exec.CommandContext(ctx, "ssh", "-S", b.controlSock, "-O", "check", "-F", sshConfig, alias)
	if err := checkCmd.Run(); err == nil {
		return nil
	}

	_ = os.Remove(b.controlSock)

	startCmd := exec.CommandContext(ctx,
		"ssh",
		"-M",
		"-N",
		"-f",
		"-S", b.controlSock,
		"-o", "ControlMaster=yes",
		"-o", "ControlPersist=600",
		"-F", sshConfig,
		alias,
	)
	out, err := startCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start ssh control master: %w: %s", err, strings.TrimSpace(string(out)))
	}

	checkCmd = exec.CommandContext(ctx, "ssh", "-S", b.controlSock, "-O", "check", "-F", sshConfig, alias)
	if err := checkCmd.Run(); err != nil {
		return fmt.Errorf("ssh control master health check failed: %w", err)
	}

	return nil
}

func parseSSHAlias(cfgPath string) (string, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", fmt.Errorf("read ssh config %q: %w", cfgPath, err)
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Host ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		alias := fields[1]
		if alias == "*" {
			continue
		}
		return alias, nil
	}
	return "", fmt.Errorf("no host alias found in %q", cfgPath)
}

func (b *Backend) runSSHScript(ctx context.Context, script string) (string, string, error) {
	b.mu.Lock()
	cfg := b.sshConfig
	host := b.sshHost
	b.mu.Unlock()

	if cfg == "" || host == "" {
		return "", "", errors.New("ssh control info not initialized")
	}

	cmd := exec.CommandContext(ctx,
		"ssh",
		"-S", b.controlSock,
		"-F", cfg,
		host,
		"/bin/bash", "-s", "--",
	)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (b *Backend) ensureHostRuntime(ctx context.Context) error {
	script := strings.Join([]string{
		"set -euo pipefail",
		"if ! sudo -n true 2>/dev/null; then",
		"  echo \"passwordless sudo is required inside firebox-host\" 1>&2",
		"  exit 24",
		"fi",
		"FB_BASE=\"${HOME}/.firebox-host\"",
		"mkdir -p \"${FB_BASE}/runs\" \"${FB_BASE}/sessions\"",
		"find \"${FB_BASE}/runs\" \"${FB_BASE}/sessions\" -mindepth 1 -maxdepth 1 -type d -mmin +240 -exec rm -rf {} + >/dev/null 2>&1 || true",
	}, "\n")

	stdout, stderr, err := b.runSSHScript(ctx, script)
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg != "" {
			return fmt.Errorf("prepare firebox host runtime: %s", msg)
		}
	}
	return fmt.Errorf("prepare firebox host runtime: %w", err)
}

func (b *Backend) maybeEnsureHostRuntime(ctx context.Context) error {
	b.mu.Lock()
	due := b.runtimeCheckedAt.IsZero() || time.Since(b.runtimeCheckedAt) > runtimeEnsurePeriod
	b.mu.Unlock()
	if !due {
		return nil
	}
	if err := b.ensureHostRuntime(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	b.runtimeCheckedAt = time.Now()
	b.mu.Unlock()
	return nil
}

func (b *Backend) fastHostReady(ctx context.Context) bool {
	b.mu.Lock()
	cfg := b.sshConfig
	host := b.sshHost
	b.mu.Unlock()
	if cfg == "" || host == "" {
		return false
	}

	fastCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	checkCmd := exec.CommandContext(fastCtx, "ssh", "-S", b.controlSock, "-O", "check", "-F", cfg, host)
	if err := checkCmd.Run(); err == nil {
		return true
	}

	if err := b.ensureSSHControl(fastCtx); err == nil {
		return true
	}
	return false
}

func (b *Backend) buildRunScript(spec model.RunSpec) string {
	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	sessionKey := ""
	if spec.PersistSession {
		sessionKey = sandboxSessionKey(spec.SessionID)
	}

	var sb strings.Builder
	sb.WriteString("set -euo pipefail\n")
	sb.WriteString("if ! sudo -n true 2>/dev/null; then echo \"passwordless sudo is required for mount operations\" 1>&2; exit 24; fi\n")
	sb.WriteString("RUN_ID=")
	sb.WriteString(shQuote(runID))
	sb.WriteString("\n")
	sb.WriteString("FB_BASE=\"${HOME}/.firebox-host\"\n")
	sb.WriteString("RUN_ROOT=\"${FB_BASE}/runs/${RUN_ID}/root\"\n")
	if spec.PersistSession {
		sb.WriteString("PERSIST_SESSION=1\n")
		sb.WriteString("SESSION_ROOT=\"${FB_BASE}/sessions/")
		sb.WriteString(shDQuoteEscape(sessionKey))
		sb.WriteString("\"\n")
	} else {
		sb.WriteString("PERSIST_SESSION=0\n")
		sb.WriteString("SESSION_ROOT=\"${FB_BASE}/sessions/${RUN_ID}\"\n")
	}
	sb.WriteString("mkdir -p \"${FB_BASE}/runs\" \"${FB_BASE}/sessions\"\n")
	sb.WriteString("FREE_KB=$(df -Pk \"${FB_BASE}\" | awk 'NR==2 {print $4}')\n")
	sb.WriteString("if [ \"${FREE_KB:-0}\" -lt 1048576 ]; then echo \"insufficient free space in firebox host storage\" 1>&2; exit 28; fi\n")
	sb.WriteString("rm -rf \"${RUN_ROOT}\"\n")
	sb.WriteString("if [ \"${PERSIST_SESSION}\" != \"1\" ]; then rm -rf \"${SESSION_ROOT}\"; fi\n")
	sb.WriteString("mkdir -p \"${RUN_ROOT}\" \"${SESSION_ROOT}\" \"${SESSION_ROOT}/mounts\"\n")
	sb.WriteString("declare -a BIND_TARGETS=()\n")
	sb.WriteString("declare -a OVERLAY_MERGED=()\n")
	networkPolicyEnabled := appendNetworkPolicyScript(&sb, spec)
	sb.WriteString("\n")
	sb.WriteString("prepare_target() {\n")
	sb.WriteString("  local src=\"$1\"\n")
	sb.WriteString("  local dst=\"$2\"\n")
	sb.WriteString("  if [ -d \"$src\" ]; then\n")
	sb.WriteString("    mkdir -p \"$dst\"\n")
	sb.WriteString("  else\n")
	sb.WriteString("    mkdir -p \"$(dirname \"$dst\")\"\n")
	sb.WriteString("    : > \"$dst\"\n")
	sb.WriteString("  fi\n")
	sb.WriteString("}\n")
	sb.WriteString("\n")
	sb.WriteString("bind_mount_rw() {\n")
	sb.WriteString("  local src=\"$1\"\n")
	sb.WriteString("  local dst=\"$2\"\n")
	sb.WriteString("  prepare_target \"$src\" \"$dst\"\n")
	sb.WriteString("  sudo -n mount --bind \"$src\" \"$dst\"\n")
	sb.WriteString("  BIND_TARGETS+=(\"$dst\")\n")
	sb.WriteString("}\n")
	sb.WriteString("\n")
	sb.WriteString("bind_mount_ro() {\n")
	sb.WriteString("  local src=\"$1\"\n")
	sb.WriteString("  local dst=\"$2\"\n")
	sb.WriteString("  bind_mount_rw \"$src\" \"$dst\"\n")
	sb.WriteString("  sudo -n mount -o remount,ro,bind \"$dst\"\n")
	sb.WriteString("}\n")
	sb.WriteString("\n")
	sb.WriteString("mount_overlay_rw() {\n")
	sb.WriteString("  local lower=\"$1\"\n")
	sb.WriteString("  local dst=\"$2\"\n")
	sb.WriteString("  local mount_id=\"$3\"\n")
	sb.WriteString("  local mount_root=\"${SESSION_ROOT}/mounts/${mount_id}\"\n")
	sb.WriteString("  local upper=\"${mount_root}/upper\"\n")
	sb.WriteString("  local work=\"${mount_root}/work\"\n")
	sb.WriteString("  local merged=\"${mount_root}/merged\"\n")
	sb.WriteString("  if [ -f \"$lower\" ]; then\n")
	sb.WriteString("    mkdir -p \"$mount_root\"\n")
	sb.WriteString("    local upper_file=\"${mount_root}/file\"\n")
	sb.WriteString("    if [ ! -e \"$upper_file\" ]; then cp -a \"$lower\" \"$upper_file\"; fi\n")
	sb.WriteString("    bind_mount_rw \"$upper_file\" \"$dst\"\n")
	sb.WriteString("    return\n")
	sb.WriteString("  fi\n")
	sb.WriteString("  if [ ! -d \"$lower\" ]; then\n")
	sb.WriteString("    echo \"overlay lowerdir must be a directory: $lower\" 1>&2\n")
	sb.WriteString("    exit 27\n")
	sb.WriteString("  fi\n")
	sb.WriteString("  mkdir -p \"$upper\" \"$work\" \"$merged\"\n")
	sb.WriteString("  local lower_esc=\"${lower//,/\\\\,}\"\n")
	sb.WriteString("  local upper_esc=\"${upper//,/\\\\,}\"\n")
	sb.WriteString("  local work_esc=\"${work//,/\\\\,}\"\n")
	sb.WriteString("  sudo -n mount -t overlay overlay -o \"lowerdir=${lower_esc},upperdir=${upper_esc},workdir=${work_esc}\" \"$merged\"\n")
	sb.WriteString("  OVERLAY_MERGED+=(\"$merged\")\n")
	sb.WriteString("  bind_mount_rw \"$merged\" \"$dst\"\n")
	sb.WriteString("}\n")
	sb.WriteString("\n")
	sb.WriteString("cleanup() {\n")
	sb.WriteString("  set +e\n")
	sb.WriteString("  cd /\n")
	if networkPolicyEnabled {
		sb.WriteString("  cleanup_network_policy\n")
	}
	sb.WriteString("  for ((i=${#BIND_TARGETS[@]}-1; i>=0; i--)); do\n")
	sb.WriteString("    t=\"${BIND_TARGETS[$i]}\"\n")
	sb.WriteString("    if mountpoint -q \"$t\"; then sudo -n umount \"$t\" || sudo umount \"$t\" || true; fi\n")
	sb.WriteString("  done\n")
	sb.WriteString("  for ((i=${#OVERLAY_MERGED[@]}-1; i>=0; i--)); do\n")
	sb.WriteString("    t=\"${OVERLAY_MERGED[$i]}\"\n")
	sb.WriteString("    if mountpoint -q \"$t\"; then sudo -n umount \"$t\" || sudo umount \"$t\" || true; fi\n")
	sb.WriteString("  done\n")
	sb.WriteString("  rm -rf --one-file-system \"${RUN_ROOT}\"\n")
	sb.WriteString("  if [ \"${PERSIST_SESSION}\" != \"1\" ]; then rm -rf --one-file-system \"${SESSION_ROOT}\"; fi\n")
	sb.WriteString("}\n")
	sb.WriteString("trap cleanup EXIT INT TERM\n")
	if networkPolicyEnabled {
		sb.WriteString("setup_network_policy\n")
	}

	for i, m := range spec.Mounts {
		host := path.Clean(m.HostPath)
		targetSuffix := strings.TrimPrefix(path.Clean(m.GuestPath), "/")
		targetExpr := "${RUN_ROOT}"
		if targetSuffix != "." && targetSuffix != "" {
			targetExpr = "${RUN_ROOT}/" + shDQuoteEscape(targetSuffix)
		}
		effectiveCow := m.EffectiveCow(spec.Cow)

		sb.WriteString("mount_src=")
		sb.WriteString(shQuote(host))
		sb.WriteString("\n")
		sb.WriteString("mount_target=\"")
		sb.WriteString(targetExpr)
		sb.WriteString("\"\n")
		sb.WriteString("if [ ! -e \"$mount_src\" ]; then echo \"mount source missing: ")
		sb.WriteString(strings.ReplaceAll(host, "\"", "\\\""))
		sb.WriteString("\" 1>&2; exit 22; fi\n")

		switch {
		case m.Access == model.AccessRO:
			sb.WriteString("bind_mount_ro \"$mount_src\" \"$mount_target\"\n")
		case effectiveCow == model.CowOn:
			sb.WriteString("mount_overlay_rw \"$mount_src\" \"$mount_target\" ")
			sb.WriteString(shQuote(fmt.Sprintf("m%d", i)))
			sb.WriteString("\n")
		default:
			sb.WriteString("if [ ! -w \"$mount_src\" ]; then echo \"mount source is not writable inside lima host: ")
			sb.WriteString(strings.ReplaceAll(host, "\"", "\\\""))
			sb.WriteString(" (recreate firebox-host with writable mounts)\" 1>&2; exit 23; fi\n")
			sb.WriteString("bind_mount_rw \"$mount_src\" \"$mount_target\"\n")
		}
	}

	workdirExpr := "${RUN_ROOT}"
	if spec.Workdir != "" {
		wd := path.Clean(spec.Workdir)
		if path.IsAbs(wd) {
			wd = strings.TrimPrefix(wd, "/")
		}
		if wd != "." && wd != "" {
			workdirExpr = "${RUN_ROOT}/" + shDQuoteEscape(wd)
		}
	} else if len(spec.Mounts) > 0 {
		wd := strings.TrimPrefix(path.Clean(spec.Mounts[0].GuestPath), "/")
		if wd != "." && wd != "" {
			workdirExpr = "${RUN_ROOT}/" + shDQuoteEscape(wd)
		}
	}

	sb.WriteString("WORKDIR=\"")
	sb.WriteString(workdirExpr)
	sb.WriteString("\"\n")
	sb.WriteString("mkdir -p \"$WORKDIR\"\n")
	sb.WriteString("cd \"$WORKDIR\"\n")
	sb.WriteString("export FIREBOX_GUEST_ROOT=\"$RUN_ROOT\"\n")
	sb.WriteString("export FIREBOX_BACKEND=lima-firecracker\n")
	sb.WriteString("export FIREBOX_SESSION_ROOT=\"$SESSION_ROOT\"\n")
	for _, env := range spec.Env {
		parts := strings.SplitN(env, "=", 2)
		key := parts[0]
		val := ""
		if len(parts) == 2 {
			val = parts[1]
		}
		if key == "" {
			continue
		}
		sb.WriteString("export ")
		sb.WriteString(key)
		sb.WriteString("=")
		sb.WriteString(shQuote(val))
		sb.WriteString("\n")
	}

	if spec.AllowHostEnv {
		sb.WriteString(joinCommand(spec.Command))
		sb.WriteString("\n")
		return sb.String()
	}

	hostHomePath := ""
	if home, err := os.UserHomeDir(); err == nil {
		home = strings.TrimSpace(home)
		if home != "" {
			hostHomePath = path.Clean(home)
		}
	}
	if hostHomePath == "." || hostHomePath == "/" {
		hostHomePath = ""
	}

	sb.WriteString("if ! command -v unshare >/dev/null 2>&1; then echo \"unshare is required for isolated workload mode; rerun with --allow-host-env to bypass\" 1>&2; exit 29; fi\n")
	sb.WriteString("HOST_ENV_HOME=")
	sb.WriteString(shQuote(hostHomePath))
	sb.WriteString("\n")
	sb.WriteString("HOST_ENV_MASK_DIR=\"${RUN_ROOT}/.firebox-host-env-mask\"\n")
	sb.WriteString("mkdir -p \"$HOST_ENV_MASK_DIR\"\n")
	appendShellArray(&sb, "WORKLOAD_CMD", spec.Command)
	sb.WriteString("unshare -m -- /bin/bash -s -- \"$HOST_ENV_HOME\" \"$HOST_ENV_MASK_DIR\" \"$WORKDIR\" \"${WORKLOAD_CMD[@]}\" <<'FIREBOX_ISOLATED_RUN'\n")
	sb.WriteString("set -euo pipefail\n")
	sb.WriteString("host_home=\"$1\"\n")
	sb.WriteString("mask_dir=\"$2\"\n")
	sb.WriteString("workdir=\"$3\"\n")
	sb.WriteString("shift 3\n")
	sb.WriteString("sudo -n mount --make-rprivate /\n")
	sb.WriteString("if [ -n \"$host_home\" ] && [ -d \"$host_home\" ]; then\n")
	sb.WriteString("  sudo -n mount --bind \"$mask_dir\" \"$host_home\"\n")
	sb.WriteString("fi\n")
	sb.WriteString("cd \"$workdir\"\n")
	sb.WriteString("\"$@\"\n")
	sb.WriteString("FIREBOX_ISOLATED_RUN\n")

	return sb.String()
}

func appendNetworkPolicyScript(sb *strings.Builder, spec model.RunSpec) bool {
	networkPolicyEnabled := spec.Network == model.NetworkNone || len(spec.NetworkAllow) > 0 || len(spec.NetworkDeny) > 0
	if !networkPolicyEnabled {
		return false
	}

	allowMode := spec.Network == model.NetworkNone || len(spec.NetworkAllow) > 0
	allow4, allow6, allowHosts := policy.SplitNetworkDestinations(spec.NetworkAllow)
	deny4, deny6, denyHosts := policy.SplitNetworkDestinations(spec.NetworkDeny)
	hasHostPolicy := len(allowHosts) > 0 || len(denyHosts) > 0

	needIPv4 := spec.Network == model.NetworkNone || len(spec.NetworkAllow) > 0 || len(allow4) > 0 || len(deny4) > 0 || hasHostPolicy
	needIPv6 := spec.Network == model.NetworkNone || len(spec.NetworkAllow) > 0 || len(allow6) > 0 || len(deny6) > 0 || hasHostPolicy

	sb.WriteString("FIREBOX_FW_CHAIN4=\"\"\n")
	sb.WriteString("FIREBOX_FW_CHAIN6=\"\"\n")
	appendShellArray(sb, "FIREBOX_NET_ALLOW4", allow4)
	appendShellArray(sb, "FIREBOX_NET_ALLOW6", allow6)
	appendShellArray(sb, "FIREBOX_NET_DENY4", deny4)
	appendShellArray(sb, "FIREBOX_NET_DENY6", deny6)
	appendShellArray(sb, "FIREBOX_NET_ALLOW_HOST", allowHosts)
	appendShellArray(sb, "FIREBOX_NET_DENY_HOST", denyHosts)
	sb.WriteString("\n")
	sb.WriteString("cleanup_network_policy() {\n")
	sb.WriteString("  if command -v iptables >/dev/null 2>&1 && [ -n \"${FIREBOX_FW_CHAIN4}\" ]; then\n")
	sb.WriteString("    sudo -n iptables -D OUTPUT -j \"${FIREBOX_FW_CHAIN4}\" >/dev/null 2>&1 || true\n")
	sb.WriteString("    sudo -n iptables -F \"${FIREBOX_FW_CHAIN4}\" >/dev/null 2>&1 || true\n")
	sb.WriteString("    sudo -n iptables -X \"${FIREBOX_FW_CHAIN4}\" >/dev/null 2>&1 || true\n")
	sb.WriteString("  fi\n")
	sb.WriteString("  if command -v ip6tables >/dev/null 2>&1 && [ -n \"${FIREBOX_FW_CHAIN6}\" ]; then\n")
	sb.WriteString("    sudo -n ip6tables -D OUTPUT -j \"${FIREBOX_FW_CHAIN6}\" >/dev/null 2>&1 || true\n")
	sb.WriteString("    sudo -n ip6tables -F \"${FIREBOX_FW_CHAIN6}\" >/dev/null 2>&1 || true\n")
	sb.WriteString("    sudo -n ip6tables -X \"${FIREBOX_FW_CHAIN6}\" >/dev/null 2>&1 || true\n")
	sb.WriteString("  fi\n")
	sb.WriteString("}\n")
	sb.WriteString("\n")
	sb.WriteString("add_host_rules() {\n")
	sb.WriteString("  local family=\"$1\"\n")
	sb.WriteString("  local tool=\"$2\"\n")
	sb.WriteString("  local chain=\"$3\"\n")
	sb.WriteString("  local action=\"$4\"\n")
	sb.WriteString("  local host=\"$5\"\n")
	sb.WriteString("  local db=\"ahostsv4\"\n")
	sb.WriteString("  if [ \"$family\" = \"6\" ]; then db=\"ahostsv6\"; fi\n")
	sb.WriteString("  local resolved=\"\"\n")
	sb.WriteString("  resolved=\"$(getent \"$db\" \"$host\" 2>/dev/null | awk '{print $1}' | sort -u || true)\"\n")
	sb.WriteString("  if [ -z \"$resolved\" ]; then return 0; fi\n")
	sb.WriteString("  while IFS= read -r ip; do\n")
	sb.WriteString("    [ -n \"$ip\" ] || continue\n")
	sb.WriteString("    sudo -n \"$tool\" -A \"$chain\" -d \"$ip\" -j \"$action\"\n")
	sb.WriteString("  done <<< \"$resolved\"\n")
	sb.WriteString("}\n")
	sb.WriteString("\n")
	sb.WriteString("host_has_any_record() {\n")
	sb.WriteString("  local host=\"$1\"\n")
	sb.WriteString("  if getent ahostsv4 \"$host\" >/dev/null 2>&1; then return 0; fi\n")
	sb.WriteString("  if getent ahostsv6 \"$host\" >/dev/null 2>&1; then return 0; fi\n")
	sb.WriteString("  return 1\n")
	sb.WriteString("}\n")
	sb.WriteString("\n")
	sb.WriteString("setup_network_policy() {\n")
	if needIPv4 {
		sb.WriteString("  if ! command -v iptables >/dev/null 2>&1; then echo \"iptables is required for IPv4 network policy enforcement\" 1>&2; exit 25; fi\n")
	}
	if needIPv6 {
		sb.WriteString("  if ! command -v ip6tables >/dev/null 2>&1; then echo \"ip6tables is required for IPv6 network policy enforcement\" 1>&2; exit 25; fi\n")
	}
	if hasHostPolicy {
		sb.WriteString("  if ! command -v getent >/dev/null 2>&1; then echo \"getent is required for hostname network policy enforcement\" 1>&2; exit 25; fi\n")
		sb.WriteString("  for host in \"${FIREBOX_NET_DENY_HOST[@]}\"; do\n")
		sb.WriteString("    if ! host_has_any_record \"$host\"; then echo \"failed to resolve hostname for network policy: ${host}\" 1>&2; exit 25; fi\n")
		sb.WriteString("  done\n")
		sb.WriteString("  for host in \"${FIREBOX_NET_ALLOW_HOST[@]}\"; do\n")
		sb.WriteString("    if ! host_has_any_record \"$host\"; then echo \"failed to resolve hostname for network policy: ${host}\" 1>&2; exit 25; fi\n")
		sb.WriteString("  done\n")
	}
	sb.WriteString("  local chain_base=\"FBX${RUN_ID}\"\n")
	if needIPv4 {
		sb.WriteString("  FIREBOX_FW_CHAIN4=\"${chain_base}4\"\n")
		sb.WriteString("  sudo -n iptables -N \"${FIREBOX_FW_CHAIN4}\"\n")
		sb.WriteString("  sudo -n iptables -I OUTPUT 1 -j \"${FIREBOX_FW_CHAIN4}\"\n")
		sb.WriteString("  sudo -n iptables -A \"${FIREBOX_FW_CHAIN4}\" -o lo -j ACCEPT\n")
		sb.WriteString("  sudo -n iptables -A \"${FIREBOX_FW_CHAIN4}\" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT\n")
		sb.WriteString("  for entry in \"${FIREBOX_NET_DENY4[@]}\"; do sudo -n iptables -A \"${FIREBOX_FW_CHAIN4}\" -d \"$entry\" -j REJECT; done\n")
		sb.WriteString("  for host in \"${FIREBOX_NET_DENY_HOST[@]}\"; do add_host_rules 4 iptables \"${FIREBOX_FW_CHAIN4}\" REJECT \"$host\"; done\n")
		sb.WriteString("  for entry in \"${FIREBOX_NET_ALLOW4[@]}\"; do sudo -n iptables -A \"${FIREBOX_FW_CHAIN4}\" -d \"$entry\" -j ACCEPT; done\n")
		sb.WriteString("  for host in \"${FIREBOX_NET_ALLOW_HOST[@]}\"; do add_host_rules 4 iptables \"${FIREBOX_FW_CHAIN4}\" ACCEPT \"$host\"; done\n")
		if allowMode {
			sb.WriteString("  sudo -n iptables -A \"${FIREBOX_FW_CHAIN4}\" -j REJECT\n")
		} else {
			sb.WriteString("  sudo -n iptables -A \"${FIREBOX_FW_CHAIN4}\" -j ACCEPT\n")
		}
	}
	if needIPv6 {
		sb.WriteString("  FIREBOX_FW_CHAIN6=\"${chain_base}6\"\n")
		sb.WriteString("  sudo -n ip6tables -N \"${FIREBOX_FW_CHAIN6}\"\n")
		sb.WriteString("  sudo -n ip6tables -I OUTPUT 1 -j \"${FIREBOX_FW_CHAIN6}\"\n")
		sb.WriteString("  sudo -n ip6tables -A \"${FIREBOX_FW_CHAIN6}\" -o lo -j ACCEPT\n")
		sb.WriteString("  sudo -n ip6tables -A \"${FIREBOX_FW_CHAIN6}\" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT\n")
		sb.WriteString("  for entry in \"${FIREBOX_NET_DENY6[@]}\"; do sudo -n ip6tables -A \"${FIREBOX_FW_CHAIN6}\" -d \"$entry\" -j REJECT; done\n")
		sb.WriteString("  for host in \"${FIREBOX_NET_DENY_HOST[@]}\"; do add_host_rules 6 ip6tables \"${FIREBOX_FW_CHAIN6}\" REJECT \"$host\"; done\n")
		sb.WriteString("  for entry in \"${FIREBOX_NET_ALLOW6[@]}\"; do sudo -n ip6tables -A \"${FIREBOX_FW_CHAIN6}\" -d \"$entry\" -j ACCEPT; done\n")
		sb.WriteString("  for host in \"${FIREBOX_NET_ALLOW_HOST[@]}\"; do add_host_rules 6 ip6tables \"${FIREBOX_FW_CHAIN6}\" ACCEPT \"$host\"; done\n")
		if allowMode {
			sb.WriteString("  sudo -n ip6tables -A \"${FIREBOX_FW_CHAIN6}\" -j REJECT\n")
		} else {
			sb.WriteString("  sudo -n ip6tables -A \"${FIREBOX_FW_CHAIN6}\" -j ACCEPT\n")
		}
	}
	sb.WriteString("}\n")
	return true
}

func appendShellArray(sb *strings.Builder, name string, values []string) {
	sb.WriteString(name)
	sb.WriteString("=(")
	for i, value := range values {
		if i > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(shQuote(value))
	}
	sb.WriteString(")\n")
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func shDQuoteEscape(s string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"$", "\\$",
		"`", "\\`",
	)
	return replacer.Replace(s)
}

func joinCommand(args []string) string {
	if len(args) == 0 {
		return "/bin/bash"
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, shQuote(a))
	}
	return strings.Join(parts, " ")
}
