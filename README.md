# Firebox

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Firebox provides a low-latency sandbox CLI and daemon on macOS using a Lima-hosted runtime path, with strict latency budgeting (`<200ms`) for warm operations.

## What is implemented

- `firebox` CLI + `fireboxd` daemon over Unix socket.
- Warm-path budget enforcement:
  - command run (`run` / `sandbox exec`)
  - sandbox lifecycle (`start` / `stop`)
- CoW toggle semantics:
  - `--cow on|off`
  - per mount `:cow=on|off`
  - CoW-on uses `overlayfs` (diffable upper/work dirs), not directory copy
- Local mount support:
  - `--mount /host:/guest[:rw|ro][:cow=on|off]`
  - `-v, --volume host:guest[:ro]` (igloo-style alias)
  - `--sandbox dst|src:dst` (igloo-style alias, always CoW on)
- Host-write safety guard:
  - direct host writes require `--allow-host-write` in non-interactive calls.
- Policy controls:
  - network allow/deny lists (`--network-allow`, `--network-deny`) with `nat` and `none` modes
  - allow/deny values may be IP, CIDR, hostname, or domain (for example `10.0.0.0/24`, `github.com`)
  - mount source path allow/deny lists (`--file-allow-path`, `--file-deny-path`)
  - mounted file extension allow/deny lists (`--file-allow-ext`, `--file-deny-ext`)
- Metrics endpoint and CLI:
  - p50/p95/p99/max per operation.

## Current architecture status

- Runtime uses a dedicated Lima instance (default: `firebox-host`) and executes workload commands over SSH control multiplexing for low latency.
- You can build/select persistent runtime images from Lima YAML files (`firebox image build/use`).
- The full nested Firecracker agent flow (`fbx-agent` + microVM lifecycle) is not yet implemented in this scaffold.

## Build

```bash
go test ./...
go build -o firebox ./cmd/firebox
```

## One-command setup

`setup` bootstraps the runtime end-to-end:
- installs Lima with Homebrew if `limactl` is missing
- builds (or reuses) a persistent runtime image from YAML
- sets it as active runtime
- restarts and warms `fireboxd`

```bash
./firebox setup
```

Optional flags:

```bash
./firebox setup --name devyaml --file ./examples/firebox-dev.yaml --rebuild
./firebox setup --install-lima=false --restart-daemon=false --warm=false
```

## Basic usage

```bash
./firebox daemon start
./firebox run echo hi
./firebox metrics
./firebox daemon stop
```

## Sandbox usage

```bash
./firebox sandbox create --id demo -v /Users/$USER:/workspace:rw -w /workspace
./firebox sandbox start demo
./firebox sandbox exec demo -- bash -lc 'pwd'
./firebox sandbox diff demo
./firebox sandbox apply demo
./firebox sandbox inspect demo
./firebox sandbox stop demo
./firebox sandbox rm demo
```

For named sandboxes, CoW session data now persists across `sandbox exec` calls so `sandbox diff` and `sandbox apply` can inspect/apply just the changed set.

## Persistent runtime images (YAML)

Prebuilt example: `examples/firebox-dev.yaml` (Python, Git, Bash, `uv`, `jq`, `rsync`).

Create a Lima YAML file (example):

```yaml
vmType: vz
nestedVirtualization: true
mountType: virtiofs
mounts:
  - location: "~"
    writable: true
provision:
  - mode: system
    script: |
      set -euo pipefail
      apt-get update
      apt-get install -y python3-pip
      pip3 install --break-system-packages uv
```

Build and select it:

```bash
./firebox image build --name dev --file ./examples/firebox-dev.yaml --use
./firebox daemon stop
./firebox daemon start
./firebox image list
```

This image is persisted as a Lima instance (e.g. `firebox-img-dev`), so restart does not reinstall dependencies.

## CoW examples

CoW on (default):

```bash
./firebox run -v /Users/$USER/project:/workspace:rw bash -lc 'echo test > file.txt'
```

CoW off (direct host writes):

```bash
./firebox run --allow-host-write -v /Users/$USER/project:/workspace:rw:cow=off bash -lc 'echo test > file.txt'
```

## Policy examples

Block all network:

```bash
./firebox run --network none -- bash -lc 'curl -I https://example.com'
```

Allow domain and CIDR, deny domain:

```bash
./firebox run \
  --network nat \
  --network-allow github.com \
  --network-allow 10.0.0.0/24 \
  --network-deny snowflake.com \
  -- bash -lc 'python -c "print(1)"'
```

Restrict mount sources by path and extension:

```bash
./firebox run \
  --file-allow-path /Users/$USER/Documents/Code \
  --file-deny-path /Users/$USER/Documents/Code/secrets \
  --file-allow-ext .go \
  --mount /Users/$USER/Documents/Code/firebox/main.go:/workspace/main.go:ro \
  -- cat /workspace/main.go
```

## Notes

- If direct host writes fail with "mount source is not writable inside lima host", recreate `firebox-host` so mounts are writable and run again.
- Cold provisioning paths (first Lima bring-up) can exceed 200ms; budget is enforced on warm execution paths.
- CoW runtime state is placed in `~/.firebox-host` inside the Lima VM (persistent disk), not `/tmp`.
- `sandbox apply` syncs CoW upperdir changes from Lima back to macOS over SSH/rsync, so apply still works even when host mounts are read-only inside the VM.
- `--network-allow` / `--network-deny` accept IPs, CIDR blocks, hostnames, and domains (wildcards are not supported).
- file extension policies are enforced for file mounts; directory mounts are rejected when extension policies are set.

## Runtime settings JSON

Global defaults can be set in `~/.firebox/state/runtime.json` and are applied to `run`, `sandbox create`, and `sandbox exec` when a request does not provide its own policy values.

Example:

```json
{
  "instance_name": "firebox-host",
  "image_name": "devyaml",
  "policy": {
    "network_allow": ["github.com"],
    "network_deny": ["snowflake.com"],
    "file_allow_paths": ["/Users/you/Documents/Code"],
    "file_deny_paths": ["/Users/you/Documents/Code/secrets"],
    "file_allow_exts": [".go", ".md"],
    "file_deny_exts": [".pem"]
  }
}
```

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.

## Benchmarking large repos

```bash
python3 benchmark.py \
  --binary ./firebox \
  --iterations 50 \
  --warmup 10 \
  --budget-ms 500 \
  --scenarios run_mount,run_mount_write,sandbox_diff_apply \
  --mount-source /Users/$USER/Documents/Code/cortex \
  --mount-dest /workspace
```

## TypeScript Firebox SDK

A standalone TypeScript SDK is available at `sdk/firebox-sdk` for tool-style integration.

It includes:
- persistent mode state (`off`, `on-no-cow`, `on-cow`)
- persistent session-to-sandbox mapping for resume
- wrappers for `bash`, `read`, `write`, and `edit`
- default-image bootstrap + custom image selection command
- Claude Code hook support for transparent `Bash` wrapping via `updatedInput`

Build:

```bash
cd sdk/firebox-sdk
npm install
npm run build
node dist/cli.js claude-hook install-bash --scope project-local
```
