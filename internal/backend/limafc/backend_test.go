package limafc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"firebox/internal/model"
)

func TestBuildRunScriptCowModes(t *testing.T) {
	b := &Backend{}
	spec := model.RunSpec{
		Command: []string{"echo", "ok"},
		Cow:     model.CowOn,
		Mounts: []model.MountSpec{
			{
				HostPath:  "/Users/test/project",
				GuestPath: "/workspace",
				Access:    model.AccessRW,
				Cow:       model.CowOff,
			},
			{
				HostPath:  "/Users/test/cache",
				GuestPath: "/cache",
				Access:    model.AccessRW,
				Cow:       model.CowOn,
			},
		},
	}

	script := b.buildRunScript(spec)
	if !strings.Contains(script, "mount source is not writable inside lima host") {
		t.Fatalf("script missing writable check for cow=off mount:\\n%s", script)
	}
	if !strings.Contains(script, "bind_mount_rw") {
		t.Fatalf("script missing bind mount path:\\n%s", script)
	}
	if !strings.Contains(script, "mount -t overlay") {
		t.Fatalf("script missing overlay mount path for cow=on:\\n%s", script)
	}
	if !strings.Contains(script, "export FIREBOX_BACKEND=lima-firecracker") {
		t.Fatalf("script missing backend marker env:\\n%s", script)
	}
	if strings.Contains(script, "cp -a /Users/test/cache") {
		t.Fatalf("script still copies cow-on directories instead of overlay:\\n%s", script)
	}
}

func TestBuildRunScriptPersistentSession(t *testing.T) {
	b := &Backend{}
	spec := model.RunSpec{
		Command:        []string{"echo", "ok"},
		Cow:            model.CowOn,
		PersistSession: true,
		SessionID:      "demo-sandbox",
		Mounts: []model.MountSpec{
			{
				HostPath:  "/Users/test/project",
				GuestPath: "/workspace",
				Access:    model.AccessRW,
				Cow:       model.CowOn,
			},
		},
	}

	script := b.buildRunScript(spec)
	if !strings.Contains(script, "PERSIST_SESSION=1") {
		t.Fatalf("script missing persistent-session marker:\\n%s", script)
	}
	if strings.Contains(script, "rm -rf --one-file-system \"${RUN_ROOT}\" \"${SESSION_ROOT}\"") {
		t.Fatalf("script should not remove persistent session root on cleanup:\\n%s", script)
	}
	if !strings.Contains(script, "if [ \"${PERSIST_SESSION}\" != \"1\" ]; then rm -rf --one-file-system \"${SESSION_ROOT}\"; fi") {
		t.Fatalf("script missing conditional session cleanup:\\n%s", script)
	}
}

func TestBuildRunScriptHostEnvIsolatedByDefault(t *testing.T) {
	b := &Backend{}
	spec := model.RunSpec{
		Command: []string{"echo", "ok"},
		Cow:     model.CowOn,
	}

	script := b.buildRunScript(spec)
	if !strings.Contains(script, "unshare is required for isolated workload mode; rerun with --allow-host-env to bypass") {
		t.Fatalf("script missing unshare requirement message:\\n%s", script)
	}
	if !strings.Contains(script, "HOST_ENV_MASK_DIR=\"${RUN_ROOT}/.firebox-host-env-mask\"") {
		t.Fatalf("script missing host env mask dir:\\n%s", script)
	}
	if !strings.Contains(script, "unshare -m -- /bin/bash -s -- \"$HOST_ENV_HOME\" \"$HOST_ENV_MASK_DIR\" \"$WORKDIR\" \"${WORKLOAD_CMD[@]}\"") {
		t.Fatalf("script missing isolated workload execution:\\n%s", script)
	}
	if !strings.Contains(script, "sudo -n mount --bind \"$mask_dir\" \"$host_home\"") {
		t.Fatalf("script missing host home masking bind mount:\\n%s", script)
	}
}

func TestBuildRunScriptAllowHostEnvBypassesIsolation(t *testing.T) {
	b := &Backend{}
	spec := model.RunSpec{
		Command:      []string{"echo", "ok"},
		Cow:          model.CowOn,
		AllowHostEnv: true,
	}

	script := b.buildRunScript(spec)
	if strings.Contains(script, "unshare -m -- /bin/bash -s --") {
		t.Fatalf("script should not include unshare wrapper when allow_host_env=true:\\n%s", script)
	}
	if !strings.Contains(script, "'echo' 'ok'") {
		t.Fatalf("script missing direct command execution:\\n%s", script)
	}
}

func TestBuildRunScriptNetworkAllowList(t *testing.T) {
	b := &Backend{}
	spec := model.RunSpec{
		Command:      []string{"echo", "ok"},
		Cow:          model.CowOn,
		Network:      model.NetworkNAT,
		NetworkAllow: []string{"10.0.0.0/24", "2001:db8::/64", "github.com"},
		NetworkDeny:  []string{"192.168.1.0/24", "snowflake.com"},
	}

	script := b.buildRunScript(spec)
	if !strings.Contains(script, "setup_network_policy") {
		t.Fatalf("script missing network setup function:\n%s", script)
	}
	if !strings.Contains(script, "FIREBOX_NET_ALLOW4=('10.0.0.0/24')") {
		t.Fatalf("script missing IPv4 allow list:\n%s", script)
	}
	if !strings.Contains(script, "FIREBOX_NET_ALLOW6=('2001:db8::/64')") {
		t.Fatalf("script missing IPv6 allow list:\n%s", script)
	}
	if !strings.Contains(script, "FIREBOX_NET_DENY4=('192.168.1.0/24')") {
		t.Fatalf("script missing IPv4 deny list:\n%s", script)
	}
	if !strings.Contains(script, "FIREBOX_NET_ALLOW_HOST=('github.com')") {
		t.Fatalf("script missing hostname allow list:\n%s", script)
	}
	if !strings.Contains(script, "FIREBOX_NET_DENY_HOST=('snowflake.com')") {
		t.Fatalf("script missing hostname deny list:\n%s", script)
	}
	if !strings.Contains(script, "add_host_rules 4 iptables") {
		t.Fatalf("script missing hostname IPv4 rule builder:\n%s", script)
	}
	if !strings.Contains(script, "add_host_rules 6 ip6tables") {
		t.Fatalf("script missing hostname IPv6 rule builder:\n%s", script)
	}
	if !strings.Contains(script, "getent is required for hostname network policy enforcement") {
		t.Fatalf("script missing getent requirement for hostname rules:\n%s", script)
	}
	if !strings.Contains(script, "sudo -n iptables -A \"${FIREBOX_FW_CHAIN4}\" -j REJECT") {
		t.Fatalf("script missing IPv4 default reject for allow-list mode:\n%s", script)
	}
	if !strings.Contains(script, "sudo -n ip6tables -A \"${FIREBOX_FW_CHAIN6}\" -j REJECT") {
		t.Fatalf("script missing IPv6 default reject for allow-list mode:\n%s", script)
	}
}

func TestBuildRunScriptNetworkNone(t *testing.T) {
	b := &Backend{}
	spec := model.RunSpec{
		Command: []string{"echo", "ok"},
		Cow:     model.CowOn,
		Network: model.NetworkNone,
	}

	script := b.buildRunScript(spec)
	if !strings.Contains(script, "iptables is required for IPv4 network policy enforcement") {
		t.Fatalf("script missing IPv4 tool check:\n%s", script)
	}
	if !strings.Contains(script, "ip6tables is required for IPv6 network policy enforcement") {
		t.Fatalf("script missing IPv6 tool check:\n%s", script)
	}
	if !strings.Contains(script, "FIREBOX_NET_ALLOW4=()") || !strings.Contains(script, "FIREBOX_NET_ALLOW6=()") {
		t.Fatalf("script missing empty allow arrays for network=none:\n%s", script)
	}
	if !strings.Contains(script, "cleanup_network_policy") {
		t.Fatalf("script missing cleanup_network_policy call:\n%s", script)
	}
	if !strings.Contains(script, "sudo -n iptables -A \"${FIREBOX_FW_CHAIN4}\" -j REJECT") {
		t.Fatalf("script missing IPv4 reject-all rule for network=none:\n%s", script)
	}
	if !strings.Contains(script, "sudo -n ip6tables -A \"${FIREBOX_FW_CHAIN6}\" -j REJECT") {
		t.Fatalf("script missing IPv6 reject-all rule for network=none:\n%s", script)
	}
}

func TestSelectCowMounts(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	spec := model.RunSpec{
		Cow: model.CowOn,
		Mounts: []model.MountSpec{
			{
				HostPath:  sub,
				GuestPath: "/workspace",
				Access:    model.AccessRW,
				Cow:       model.CowOn,
			},
			{
				HostPath:  file,
				GuestPath: "/single",
				Access:    model.AccessRW,
				Cow:       model.CowOn,
			},
			{
				HostPath:  sub,
				GuestPath: "/readonly",
				Access:    model.AccessRO,
				Cow:       model.CowOn,
			},
		},
	}

	mounts, err := selectCowMounts(spec, "")
	if err != nil {
		t.Fatalf("selectCowMounts() error = %v", err)
	}
	if len(mounts) != 2 {
		t.Fatalf("len(mounts) = %d, want 2", len(mounts))
	}
	if mounts[0].Kind != "dir" {
		t.Fatalf("mounts[0].Kind = %q, want dir", mounts[0].Kind)
	}
	if mounts[1].Kind != "file" {
		t.Fatalf("mounts[1].Kind = %q, want file", mounts[1].Kind)
	}

	filtered, err := selectCowMounts(spec, "/workspace")
	if err != nil {
		t.Fatalf("selectCowMounts(filtered) error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].GuestPath != "/workspace" {
		t.Fatalf("filtered mounts = %#v, want only /workspace", filtered)
	}
}

func TestSanitizeSockName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "firebox-img-devyaml", want: "firebox-img-devyaml"},
		{in: "img/dev yaml", want: "img_dev_yaml"},
		{in: "....", want: "firebox-host"},
	}
	for _, tt := range tests {
		got := sanitizeSockName(tt.in)
		if got != tt.want {
			t.Fatalf("sanitizeSockName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
