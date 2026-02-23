#!/usr/bin/env node

import path from "node:path";

import { FireboxSDK } from "./index.js";
import { FireboxMode } from "./types.js";
import { installClaudeBashHook, runClaudePreToolUseBashHook } from "./claudeHooks.js";

function usage(): void {
  console.log(`firebox-sdk commands:
  [--firebox-bin <path>] [--daemon-id <id>] [--state-file <path>]
  status
  set-mode <off|on-no-cow|on-cow>
  set-default-image <name> [--yaml <path>] [--ensure]
  claude-hook pretooluse-bash [--permission <allow|ask>]
  claude-hook install-bash [--scope <project-local|project|user>] [--settings-path <path>] [--permission <allow|ask>]
`);
}

function parseFlag(args: string[], flag: string): string | undefined {
  const idx = args.indexOf(flag);
  if (idx === -1) {
    return undefined;
  }
  const next = args[idx + 1];
  if (!next || next.startsWith("--")) {
    return undefined;
  }
  return next;
}

function hasFlag(args: string[], flag: string): boolean {
  return args.includes(flag);
}

function isMode(value: string): value is FireboxMode {
  return value === "off" || value === "on-no-cow" || value === "on-cow";
}

function isHookPermission(value: string): value is "allow" | "ask" {
  return value === "allow" || value === "ask";
}

function isHookScope(value: string): value is "project-local" | "project" | "user" {
  return value === "project-local" || value === "project" || value === "user";
}

function parseGlobalArgs(argv: string[]): {
  fireboxBin?: string;
  daemonId?: string;
  stateFilePath?: string;
  remaining: string[];
} {
  const remaining: string[] = [];
  let fireboxBin: string | undefined;
  let daemonId: string | undefined;
  let stateFilePath: string | undefined;

  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    if (token === "--firebox-bin") {
      fireboxBin = argv[i + 1];
      i += 1;
      continue;
    }
    if (token === "--daemon-id") {
      daemonId = argv[i + 1];
      i += 1;
      continue;
    }
    if (token === "--state-file") {
      stateFilePath = argv[i + 1];
      i += 1;
      continue;
    }
    remaining.push(token);
  }

  return { fireboxBin, daemonId, stateFilePath, remaining };
}

async function main(): Promise<number> {
  const parsed = parseGlobalArgs(process.argv.slice(2));
  const [command, ...rest] = parsed.remaining;
  const sdk = new FireboxSDK({
    fireboxBin: parsed.fireboxBin,
    daemonId: parsed.daemonId,
    stateFilePath: parsed.stateFilePath,
  });

  if (!command) {
    usage();
    return 2;
  }

  if (command === "status") {
    const state = await sdk.initialize();
    console.log(JSON.stringify(state, null, 2));
    return 0;
  }

  if (command === "set-mode") {
    const mode = rest[0];
    if (!mode || !isMode(mode)) {
      throw new Error("set-mode requires one of: off, on-no-cow, on-cow");
    }
    const state = await sdk.setMode(mode);
    console.log(`mode set to ${state.mode}`);
    return 0;
  }

  if (command === "set-default-image") {
    const name = rest[0];
    if (!name || name.startsWith("--")) {
      throw new Error("set-default-image requires an image name");
    }
    const yaml = parseFlag(rest.slice(1), "--yaml");
    const ensureNow = hasFlag(rest.slice(1), "--ensure");
    const state = await sdk.setDefaultImage(name, yaml, ensureNow);
    console.log(`default image set to ${state.image.name}`);
    if (state.image.yamlPath) {
      console.log(`yaml: ${state.image.yamlPath}`);
    }
    return 0;
  }

  if (command === "claude-hook") {
    const subcommand = rest[0];
    const subArgs = rest.slice(1);

    if (subcommand === "pretooluse-bash") {
      const permission = parseFlag(subArgs, "--permission");
      if (permission && !isHookPermission(permission)) {
        throw new Error("invalid --permission (use allow or ask)");
      }
      const permissionDecision = permission && isHookPermission(permission) ? permission : undefined;
      return runClaudePreToolUseBashHook({
        fireboxBin: parsed.fireboxBin,
        daemonId: parsed.daemonId,
        stateFilePath: parsed.stateFilePath,
        permissionDecision,
      });
    }

    if (subcommand === "install-bash") {
      const scopeRaw = parseFlag(subArgs, "--scope");
      const settingsPath = parseFlag(subArgs, "--settings-path");
      const permission = parseFlag(subArgs, "--permission");
      if (scopeRaw && !isHookScope(scopeRaw)) {
        throw new Error("invalid --scope (use project-local, project, or user)");
      }
      if (permission && !isHookPermission(permission)) {
        throw new Error("invalid --permission (use allow or ask)");
      }
      const scope = scopeRaw && isHookScope(scopeRaw) ? scopeRaw : undefined;
      const permissionDecision = permission && isHookPermission(permission) ? permission : undefined;

      const installResult = await installClaudeBashHook({
        scope,
        settingsPath,
        permissionDecision,
        fireboxBin: parsed.fireboxBin,
        daemonId: parsed.daemonId,
        stateFilePath: parsed.stateFilePath,
        cliScriptPath: path.resolve(process.argv[1] ?? "dist/cli.js"),
      });
      console.log(`installed Bash hook in ${installResult.settingsPath}`);
      console.log(`command: ${installResult.command}`);
      if (!installResult.updated) {
        console.log("hook already present");
      }
      return 0;
    }

    throw new Error(`unknown claude-hook subcommand: ${subcommand ?? "(missing)"}`);
  }

  usage();
  throw new Error(`unknown command: ${command}`);
}

main()
  .then((code) => {
    process.exit(code);
  })
  .catch((error) => {
    console.error((error as Error).message);
    process.exit(1);
  });
