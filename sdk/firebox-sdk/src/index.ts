import { promises as fs } from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";

import { FireboxClient } from "./fireboxClient.js";
import { execCommand } from "./process.js";
import {
  createDefaultState,
  defaultGeneratedYamlPath,
  defaultStateFilePath,
  FireboxStateStore,
} from "./stateStore.js";
import {
  BashResult,
  EditResult,
  FireboxEditOptions,
  FireboxExecOptions,
  FireboxMode,
  FireboxSDKOptions,
  FireboxSDKState,
  PreparedBashCommand,
  ReadResult,
  SessionSandboxBinding,
  WriteResult,
} from "./types.js";

const DEFAULT_IMAGE_NAME = "firebox-lite";
const DEFAULT_GUEST_MOUNT = "/workspace";

const LIGHTWEIGHT_IMAGE_YAML = `vmType: vz
nestedVirtualization: true
mountType: virtiofs
cpus: 2
memory: "4GiB"
disk: "40GiB"
images:
  - location: "https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img"
    arch: "x86_64"
  - location: "https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-arm64.img"
    arch: "aarch64"
mounts:
  - location: "~"
    writable: true
provision:
  - mode: system
    script: |
      set -eu
      export DEBIAN_FRONTEND=noninteractive
      apt-get update
      apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        python-is-python3 \
        python3 \
        python3-pip
`;

export * from "./types.js";

export class FireboxSDK {
  private readonly options: Required<
    Pick<FireboxSDKOptions, "fireboxBin" | "defaultWorkspaceDir" | "autoSetup" | "autoStartDaemon">
  > &
    Pick<FireboxSDKOptions, "stateFilePath" | "defaultImageName" | "defaultImageYamlPath">;
  private readonly client: FireboxClient;
  private readonly store: FireboxStateStore;
  private state: FireboxSDKState;
  private loaded = false;

  constructor(options: FireboxSDKOptions = {}) {
    this.options = {
      fireboxBin: options.fireboxBin ?? "firebox",
      stateFilePath: options.stateFilePath,
      defaultWorkspaceDir: options.defaultWorkspaceDir ?? process.cwd(),
      defaultImageName: options.defaultImageName,
      defaultImageYamlPath: options.defaultImageYamlPath,
      autoSetup: options.autoSetup ?? true,
      autoStartDaemon: options.autoStartDaemon ?? true,
    };
    const statePath = this.options.stateFilePath ?? defaultStateFilePath();
    const defaultImageName = this.options.defaultImageName ?? DEFAULT_IMAGE_NAME;
    this.store = new FireboxStateStore(statePath, defaultImageName, this.options.defaultImageYamlPath);
    this.state = createDefaultState(defaultImageName, this.options.defaultImageYamlPath);
    this.client = new FireboxClient({ fireboxBin: this.options.fireboxBin });
  }

  async initialize(): Promise<FireboxSDKState> {
    await this.ensureLoaded();
    if (this.state.mode !== "off") {
      await this.client.ensureAvailable();
      await this.ensureRuntimeReady();
    }
    return this.snapshot();
  }

  async getState(): Promise<FireboxSDKState> {
    await this.ensureLoaded();
    return this.snapshot();
  }

  async getMode(): Promise<FireboxMode> {
    await this.ensureLoaded();
    return this.state.mode;
  }

  async shouldIgnoreAnthropicRuntime(): Promise<boolean> {
    await this.ensureLoaded();
    return this.state.mode !== "off";
  }

  async setMode(mode: FireboxMode): Promise<FireboxSDKState> {
    await this.ensureLoaded();
    this.state.mode = mode;
    await this.saveState();
    if (mode !== "off") {
      await this.ensureRuntimeReady();
    }
    return this.snapshot();
  }

  async setDefaultImage(name: string, yamlPath?: string, ensureNow = false): Promise<FireboxSDKState> {
    await this.ensureLoaded();
    const normalizedName = name.trim();
    if (!normalizedName) {
      throw new Error("default image name cannot be empty");
    }
    this.state.image.name = normalizedName;
    this.state.image.yamlPath = yamlPath ? path.resolve(yamlPath) : undefined;
    await this.saveState();
    if (ensureNow || this.state.mode !== "off") {
      await this.ensureRuntimeReady();
    }
    return this.snapshot();
  }

  async prepareBashCommand(
    command: string,
    options: FireboxExecOptions = {},
  ): Promise<PreparedBashCommand | null> {
    await this.ensureLoaded();
    const workspaceDir = this.resolveWorkspace(options.workspaceDir);

    if (this.state.mode === "off") {
      return null;
    }

    await this.ensureRuntimeReady();

    if (this.state.mode === "on-no-cow") {
      const wrappedCommand = argsToShellCommand(this.options.fireboxBin, [
        "run",
        "--strict-budget=false",
        "--allow-host-write",
        "--cow",
        "off",
        "--mount",
        `${workspaceDir}:/workspace:cow=off`,
        "-w",
        "/workspace",
        "bash",
        "-lc",
        command,
      ]);
      return {
        mode: this.state.mode,
        wrappedCommand,
      };
    }

    const sessionId = options.sessionId?.trim();
    if (!sessionId) {
      throw new Error("sessionId is required for bash in on-cow mode");
    }
    const binding = await this.ensureSessionSandbox(sessionId, workspaceDir);
    const wrappedCommand = argsToShellCommand(this.options.fireboxBin, [
      "sandbox",
      "exec",
      binding.sandboxId,
      "--",
      "bash",
      "-lc",
      command,
    ]);
    return {
      mode: this.state.mode,
      wrappedCommand,
      sandboxId: binding.sandboxId,
    };
  }

  async bash(command: string, options: FireboxExecOptions = {}): Promise<BashResult> {
    await this.ensureLoaded();
    const timeoutMs = options.timeoutMs ?? 60000;
    const workspaceDir = this.resolveWorkspace(options.workspaceDir);

    if (this.state.mode === "off") {
      const result = await execCommand("bash", ["-lc", command], {
        cwd: workspaceDir,
        timeoutMs,
      });
      return {
        stdout: result.stdout,
        stderr: result.stderr,
        exitCode: result.exitCode,
      };
    }

    await this.ensureRuntimeReady();

    if (this.state.mode === "on-no-cow") {
      const result = await this.client.runNoCow(command, workspaceDir, timeoutMs);
      return {
        stdout: result.stdout,
        stderr: result.stderr,
        exitCode: result.exitCode,
      };
    }

    const sessionId = options.sessionId?.trim();
    if (!sessionId) {
      throw new Error("sessionId is required for bash in on-cow mode");
    }
    const binding = await this.ensureSessionSandbox(sessionId, workspaceDir);
    const result = await this.client.sandboxExec(binding.sandboxId, command, timeoutMs);
    return {
      stdout: result.stdout,
      stderr: result.stderr,
      exitCode: result.exitCode,
    };
  }

  async read(filePath: string, options: FireboxExecOptions = {}): Promise<ReadResult> {
    await this.ensureLoaded();
    const workspaceDir = this.resolveWorkspace(options.workspaceDir);

    if (this.state.mode !== "on-cow") {
      const absolute = this.resolveLocalPath(workspaceDir, filePath);
      const content = await fs.readFile(absolute, "utf8");
      return { content, path: absolute };
    }

    await this.ensureRuntimeReady();
    const sessionId = options.sessionId?.trim();
    if (!sessionId) {
      throw new Error("sessionId is required for read in on-cow mode");
    }
    const binding = await this.ensureSessionSandbox(sessionId, workspaceDir);
    const hostPath = this.resolveWorkspaceBoundPath(binding.workspaceDir, filePath);
    const sandboxPath = this.hostToSandboxPath(binding, hostPath);
    const script = [
      "import base64, pathlib, sys",
      "p = pathlib.Path(sys.argv[1])",
      "data = p.read_bytes()",
      "sys.stdout.write(base64.b64encode(data).decode('ascii'))",
    ].join("; ");
    const command = `python3 -c ${shQuote(script)} ${shQuote(sandboxPath)}`;
    const result = await this.client.sandboxExec(binding.sandboxId, command, options.timeoutMs ?? 20000);
    if (result.exitCode !== 0) {
      throw new Error(`sandbox read failed: ${result.stderr || result.stdout}`);
    }
    const raw = result.stdout.trim();
    const content = Buffer.from(raw, "base64").toString("utf8");
    return {
      content,
      path: hostPath,
    };
  }

  async write(filePath: string, content: string, options: FireboxExecOptions = {}): Promise<WriteResult> {
    await this.ensureLoaded();
    const workspaceDir = this.resolveWorkspace(options.workspaceDir);

    if (this.state.mode !== "on-cow") {
      const absolute = this.resolveLocalPath(workspaceDir, filePath);
      await fs.mkdir(path.dirname(absolute), { recursive: true });
      await fs.writeFile(absolute, content, "utf8");
      return { path: absolute, bytesWritten: Buffer.byteLength(content) };
    }

    await this.ensureRuntimeReady();
    const sessionId = options.sessionId?.trim();
    if (!sessionId) {
      throw new Error("sessionId is required for write in on-cow mode");
    }
    const binding = await this.ensureSessionSandbox(sessionId, workspaceDir);
    const hostPath = this.resolveWorkspaceBoundPath(binding.workspaceDir, filePath);
    const sandboxPath = this.hostToSandboxPath(binding, hostPath);
    const encoded = Buffer.from(content, "utf8").toString("base64");
    const script = [
      "import base64, pathlib, sys",
      "p = pathlib.Path(sys.argv[1])",
      "payload = base64.b64decode(sys.argv[2].encode('ascii'))",
      "p.parent.mkdir(parents=True, exist_ok=True)",
      "tmp = p.parent / f'.{p.name}.fireboxsdk.tmp'",
      "tmp.write_bytes(payload)",
      "tmp.replace(p)",
      "print(len(payload))",
    ].join("; ");
    const command = `python3 -c ${shQuote(script)} ${shQuote(sandboxPath)} ${shQuote(encoded)}`;
    const result = await this.client.sandboxExec(binding.sandboxId, command, options.timeoutMs ?? 20000);
    if (result.exitCode !== 0) {
      throw new Error(`sandbox write failed: ${result.stderr || result.stdout}`);
    }
    return {
      path: hostPath,
      bytesWritten: Buffer.byteLength(content),
    };
  }

  async edit(
    filePath: string,
    oldText: string,
    newText: string,
    options: FireboxEditOptions = {},
  ): Promise<EditResult> {
    await this.ensureLoaded();
    const workspaceDir = this.resolveWorkspace(options.workspaceDir);
    const replaceAll = options.replaceAll ?? false;

    if (this.state.mode !== "on-cow") {
      const absolute = this.resolveLocalPath(workspaceDir, filePath);
      const original = await fs.readFile(absolute, "utf8");
      if (!original.includes(oldText)) {
        throw new Error(`oldText not found in ${absolute}`);
      }
      const replacements = replaceAll ? countOccurrences(original, oldText) : 1;
      const updated = replaceAll ? original.split(oldText).join(newText) : original.replace(oldText, newText);
      await fs.writeFile(absolute, updated, "utf8");
      return { path: absolute, replacements };
    }

    await this.ensureRuntimeReady();
    const sessionId = options.sessionId?.trim();
    if (!sessionId) {
      throw new Error("sessionId is required for edit in on-cow mode");
    }
    const binding = await this.ensureSessionSandbox(sessionId, workspaceDir);
    const hostPath = this.resolveWorkspaceBoundPath(binding.workspaceDir, filePath);
    const sandboxPath = this.hostToSandboxPath(binding, hostPath);
    const oldB64 = Buffer.from(oldText, "utf8").toString("base64");
    const newB64 = Buffer.from(newText, "utf8").toString("base64");
    const script = [
      "import base64, pathlib, sys",
      "p = pathlib.Path(sys.argv[1])",
      "old = base64.b64decode(sys.argv[2].encode('ascii')).decode('utf-8')",
      "new = base64.b64decode(sys.argv[3].encode('ascii')).decode('utf-8')",
      "replace_all = sys.argv[4] == '1'",
      "text = p.read_text(encoding='utf-8')",
      "if old not in text: sys.exit(3)",
      "if replace_all:",
      "    count = text.count(old)",
      "    text = text.replace(old, new)",
      "else:",
      "    count = 1",
      "    text = text.replace(old, new, 1)",
      "tmp = p.parent / f'.{p.name}.fireboxsdk.tmp'",
      "tmp.write_text(text, encoding='utf-8')",
      "tmp.replace(p)",
      "print(count)",
    ].join("\n");
    const command = [
      "python3 -c",
      shQuote(script),
      shQuote(sandboxPath),
      shQuote(oldB64),
      shQuote(newB64),
      replaceAll ? "1" : "0",
    ].join(" ");
    const result = await this.client.sandboxExec(binding.sandboxId, command, options.timeoutMs ?? 25000);
    if (result.exitCode === 3) {
      throw new Error(`oldText not found in ${hostPath}`);
    }
    if (result.exitCode !== 0) {
      throw new Error(`sandbox edit failed: ${result.stderr || result.stdout}`);
    }
    const replacements = Number.parseInt(result.stdout.trim(), 10);
    return {
      path: hostPath,
      replacements: Number.isFinite(replacements) ? replacements : 0,
    };
  }

  async diffSession(sessionId: string): Promise<string> {
    await this.ensureLoaded();
    if (this.state.mode !== "on-cow") {
      return "";
    }
    const binding = this.state.sessions[sessionId];
    if (!binding) {
      throw new Error(`no sandbox mapping found for session ${sessionId}`);
    }
    return this.client.sandboxDiff(binding.sandboxId, binding.guestMountPath);
  }

  async applySession(sessionId: string): Promise<string> {
    await this.ensureLoaded();
    if (this.state.mode !== "on-cow") {
      return "";
    }
    const binding = this.state.sessions[sessionId];
    if (!binding) {
      throw new Error(`no sandbox mapping found for session ${sessionId}`);
    }
    return this.client.sandboxApply(binding.sandboxId, binding.guestMountPath);
  }

  async stopSession(sessionId: string): Promise<void> {
    await this.ensureLoaded();
    const binding = this.state.sessions[sessionId];
    if (!binding) {
      return;
    }
    await this.client.sandboxStop(binding.sandboxId);
  }

  async removeSession(sessionId: string): Promise<void> {
    await this.ensureLoaded();
    const binding = this.state.sessions[sessionId];
    if (!binding) {
      return;
    }
    await this.client.sandboxStop(binding.sandboxId);
    await this.client.sandboxRemove(binding.sandboxId);
    delete this.state.sessions[sessionId];
    await this.saveState();
  }

  private async ensureRuntimeReady(): Promise<void> {
    const image = await this.ensureImageConfig();
    const imageExists = await this.client.imageExists(image.name);

    if (!imageExists) {
      if (!this.options.autoSetup) {
        throw new Error(`firebox image "${image.name}" is missing and autoSetup=false`);
      }
      if (!image.yamlPath) {
        throw new Error(
          `firebox image "${image.name}" is missing. Provide yamlPath via setDefaultImage(name, yamlPath).`,
        );
      }
      await this.client.setupImage(image.name, image.yamlPath);
    } else {
      await this.client.useImage(image.name);
    }

    if (this.options.autoStartDaemon) {
      await this.client.ensureDaemonRunning();
    }
  }

  private async ensureImageConfig(): Promise<{ name: string; yamlPath?: string }> {
    const imageName = this.state.image.name || DEFAULT_IMAGE_NAME;
    let yamlPath = this.state.image.yamlPath ? path.resolve(this.state.image.yamlPath) : undefined;
    if (!yamlPath && imageName === DEFAULT_IMAGE_NAME) {
      yamlPath = defaultGeneratedYamlPath();
      await this.writeDefaultImageYaml(yamlPath);
      this.state.image.yamlPath = yamlPath;
      await this.saveState();
    }
    return { name: imageName, yamlPath };
  }

  private async writeDefaultImageYaml(yamlPath: string): Promise<void> {
    try {
      await fs.access(yamlPath);
      return;
    } catch {
      await fs.mkdir(path.dirname(yamlPath), { recursive: true });
      await fs.writeFile(yamlPath, LIGHTWEIGHT_IMAGE_YAML, "utf8");
    }
  }

  private resolveWorkspace(workspaceDir?: string): string {
    return path.resolve(workspaceDir ?? this.options.defaultWorkspaceDir);
  }

  private resolveLocalPath(workspaceDir: string, filePath: string): string {
    if (path.isAbsolute(filePath)) {
      return path.resolve(filePath);
    }
    return path.resolve(workspaceDir, filePath);
  }

  private resolveWorkspaceBoundPath(workspaceDir: string, filePath: string): string {
    const candidate = this.resolveLocalPath(workspaceDir, filePath);
    assertInside(workspaceDir, candidate);
    return candidate;
  }

  private hostToSandboxPath(binding: SessionSandboxBinding, hostPath: string): string {
    const relative = path.relative(binding.workspaceDir, hostPath);
    if (!relative || relative === ".") {
      return ".";
    }
    return relative.split(path.sep).join(path.posix.sep);
  }

  private async ensureSessionSandbox(sessionId: string, workspaceDir: string): Promise<SessionSandboxBinding> {
    const existing = this.state.sessions[sessionId];
    if (existing) {
      const inspect = await this.client.sandboxInspect(existing.sandboxId);
      const workspaceChanged = path.resolve(existing.workspaceDir) !== workspaceDir;
      if (workspaceChanged) {
        await this.client.sandboxStop(existing.sandboxId);
        await this.client.sandboxRemove(existing.sandboxId);
      } else if (inspect) {
        if (inspect.status !== "running") {
          await this.client.sandboxStart(existing.sandboxId);
        }
        existing.updatedAt = new Date().toISOString();
        await this.saveState();
        return existing;
      }
    }

    const sandboxId = existing?.sandboxId ?? sandboxIdForSession(sessionId);
    await this.client.sandboxCreate(sandboxId, workspaceDir, DEFAULT_GUEST_MOUNT);
    await this.client.sandboxStart(sandboxId);

    const binding: SessionSandboxBinding = {
      sessionId,
      sandboxId,
      workspaceDir,
      guestMountPath: DEFAULT_GUEST_MOUNT,
      updatedAt: new Date().toISOString(),
    };
    this.state.sessions[sessionId] = binding;
    await this.saveState();
    return binding;
  }

  private snapshot(): FireboxSDKState {
    return JSON.parse(JSON.stringify(this.state)) as FireboxSDKState;
  }

  private async ensureLoaded(): Promise<void> {
    if (this.loaded) {
      return;
    }
    this.state = await this.store.load();
    this.loaded = true;
  }

  private async saveState(): Promise<void> {
    await this.store.save(this.state);
  }
}

function sandboxIdForSession(sessionId: string): string {
  const normalized = sessionId
    .toLowerCase()
    .replace(/[^a-z0-9._-]/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
  const prefix = normalized.slice(0, 42) || "session";
  const hash = createHash("sha1").update(sessionId).digest("hex").slice(0, 8);
  return `firebox_sandbox-${prefix}-${hash}`;
}

function assertInside(parentDir: string, candidatePath: string): void {
  const relative = path.relative(path.resolve(parentDir), path.resolve(candidatePath));
  if (relative.startsWith("..") || path.isAbsolute(relative)) {
    throw new Error(`path ${candidatePath} is outside mounted workspace ${parentDir}`);
  }
}

function shQuote(value: string): string {
  return `'${value.replace(/'/g, `'\"'\"'`)}'`;
}

function argsToShellCommand(command: string, args: string[]): string {
  return [command, ...args].map((value) => shQuote(value)).join(" ");
}

function countOccurrences(haystack: string, needle: string): number {
  if (!needle) {
    return 0;
  }
  return haystack.split(needle).length - 1;
}
