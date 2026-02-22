# Firebox TypeScript SDK

TypeScript SDK for integrating Firebox with tool-style handlers (`bash`, `read`, `write`, `edit`) while preserving session and sandbox state.

## What It Does

- Persists Firebox mode across restarts:
  - `off`
  - `on-no-cow`
  - `on-cow`
- Persists session to sandbox mapping (`sessionId -> firebox_sandbox-...`) for resume.
- Auto-bootstraps Firebox runtime when enabled:
  - Ensures a default image exists.
  - Starts daemon when needed.
- Supports custom default image configuration.
- Routing behavior:
  - `off`: local execution and local file IO.
  - `on-no-cow`: `bash` runs through Firebox (`--cow off`), `read/write/edit` remain local.
  - `on-cow`: `bash`, `read`, `write`, `edit` run inside per-session Firebox sandbox.

State file default:

`~/.config/firebox/firebox-sdk.json`

Default generated image YAML path:

`~/.config/firebox/firebox-default.yaml`

## Install / Build

```bash
cd sdk/firebox-sdk
npm install
npm run build
```

## SDK Usage

```ts
import { FireboxSDK } from "@firebox/sdk";

const sdk = new FireboxSDK();
await sdk.initialize();

await sdk.setMode("on-cow");

const sessionId = "abc123";
await sdk.bash("ls -la", { sessionId, workspaceDir: process.cwd() });
await sdk.write("notes.txt", "hello", { sessionId, workspaceDir: process.cwd() });
const read = await sdk.read("notes.txt", { sessionId, workspaceDir: process.cwd() });
console.log(read.content);
```

## CLI

After build:

```bash
node dist/cli.js --firebox-bin /path/to/firebox status
node dist/cli.js --firebox-bin /path/to/firebox set-mode on-cow
node dist/cli.js --firebox-bin /path/to/firebox set-default-image my-runtime --yaml /path/to/runtime.yaml --ensure
node dist/cli.js --firebox-bin /path/to/firebox claude-hook install-bash --scope project-local
```

## Claude Code Hooks (Bash First)

This SDK can install a `PreToolUse` hook for Claude Code's `Bash` tool and transparently rewrite Bash commands through Firebox when Firebox mode is enabled.

- `off`: no rewrite
- `on-no-cow`: rewrite to `firebox run ... --cow off`
- `on-cow`: rewrite to `firebox sandbox exec <session-sandbox> ...`

Install hook into project-local settings (`.claude/settings.local.json`):

```bash
node dist/cli.js claude-hook install-bash --scope project-local
```

Install into user settings (`~/.claude/settings.json`):

```bash
node dist/cli.js claude-hook install-bash --scope user
```

Run hook manually (for testing):

```bash
cat hook-input.json | node dist/cli.js claude-hook pretooluse-bash
```

Hook behavior control:

- `--permission allow` (default): auto-allow rewritten Bash command
- `--permission ask`: require user approval for rewritten Bash command

## Tool Wiring Reference

Use `examples/toolAdapter.ts` as the reference wrapper for an existing tool split:
- `bash` always routes through SDK
- `read/write/edit` route through SDK and become sandbox-backed automatically in `on-cow` mode
- `shouldIgnoreAnthropicRuntime()` is the runtime gate when Firebox is enabled
