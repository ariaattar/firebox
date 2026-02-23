import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { FireboxSDK } from "./index.js";
const DEFAULT_PERMISSION_DECISION = "allow";
export async function runClaudePreToolUseBashHook(options = {}) {
    try {
        const rawInput = await readStdin();
        if (!rawInput.trim()) {
            return 0;
        }
        const hookInput = JSON.parse(rawInput);
        if (hookInput.hook_event_name !== "PreToolUse" || hookInput.tool_name !== "Bash") {
            return 0;
        }
        if (!isRecord(hookInput.tool_input)) {
            return 0;
        }
        const toolInput = hookInput.tool_input;
        if (typeof toolInput.command !== "string" || !toolInput.command.trim()) {
            return 0;
        }
        if (isAlreadyFireboxWrapped(toolInput.command)) {
            return 0;
        }
        const sdk = new FireboxSDK({
            fireboxBin: options.fireboxBin,
            daemonId: options.daemonId,
            stateFilePath: options.stateFilePath,
            autoSetup: true,
            autoStartDaemon: true,
        });
        await sdk.initialize();
        const prepared = await sdk.prepareBashCommand(toolInput.command, {
            workspaceDir: typeof hookInput.cwd === "string" ? hookInput.cwd : process.cwd(),
            sessionId: typeof hookInput.session_id === "string" ? hookInput.session_id : undefined,
        });
        if (!prepared) {
            return 0;
        }
        const decision = normalizePermissionDecision(options.permissionDecision);
        const output = {
            hookSpecificOutput: {
                hookEventName: "PreToolUse",
                permissionDecision: decision,
                permissionDecisionReason: `Wrapped Bash through Firebox (${prepared.mode})`,
                updatedInput: {
                    ...toolInput,
                    command: prepared.wrappedCommand,
                },
            },
        };
        process.stdout.write(`${JSON.stringify(output)}\n`);
        return 0;
    }
    catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        const output = {
            hookSpecificOutput: {
                hookEventName: "PreToolUse",
                permissionDecision: "deny",
                permissionDecisionReason: `Firebox Bash hook failed: ${message}`,
            },
        };
        process.stdout.write(`${JSON.stringify(output)}\n`);
        return 0;
    }
}
export async function installClaudeBashHook(options) {
    const scope = options.scope ?? "project-local";
    const settingsPath = options.settingsPath
        ? path.resolve(options.settingsPath)
        : defaultClaudeSettingsPath(scope, options.cwd ?? process.cwd());
    const command = buildHookCommand({
        cliScriptPath: options.cliScriptPath,
        fireboxBin: options.fireboxBin,
        daemonId: options.daemonId,
        stateFilePath: options.stateFilePath,
        permissionDecision: normalizePermissionDecision(options.permissionDecision),
    });
    const config = await readJsonObject(settingsPath);
    const hooksNode = ensureRecord(config, "hooks");
    const preToolUseNode = ensureArray(hooksNode, "PreToolUse");
    const bashMatcher = upsertBashMatcher(preToolUseNode);
    const hooks = ensureObjectArray(bashMatcher, "hooks");
    const exists = hooks.some((entry) => {
        if (!isRecord(entry)) {
            return false;
        }
        return entry.type === "command" && entry.command === command;
    });
    if (!exists) {
        hooks.push({
            type: "command",
            command,
        });
    }
    await fs.mkdir(path.dirname(settingsPath), { recursive: true });
    await fs.writeFile(settingsPath, `${JSON.stringify(config, null, 2)}\n`, "utf8");
    return {
        settingsPath,
        command,
        updated: !exists,
    };
}
function normalizePermissionDecision(value) {
    if (value === "ask") {
        return "ask";
    }
    return DEFAULT_PERMISSION_DECISION;
}
function defaultClaudeSettingsPath(scope, cwd) {
    if (scope === "user") {
        return path.join(os.homedir(), ".claude", "settings.json");
    }
    if (scope === "project") {
        return path.join(cwd, ".claude", "settings.json");
    }
    return path.join(cwd, ".claude", "settings.local.json");
}
function isAlreadyFireboxWrapped(command) {
    const normalized = command.trim();
    return /(^|\s)(?:\S*\/)?firebox(?:\s+--daemon-id\s+\S+)?\s+(run|sandbox\s+exec)\b/.test(normalized);
}
async function readStdin() {
    return new Promise((resolve, reject) => {
        let data = "";
        process.stdin.setEncoding("utf8");
        process.stdin.on("data", (chunk) => {
            data += chunk;
        });
        process.stdin.on("error", (error) => {
            reject(error);
        });
        process.stdin.on("end", () => {
            resolve(data);
        });
    });
}
function shellQuote(value) {
    return `'${value.replace(/'/g, `'\"'\"'`)}'`;
}
function buildHookCommand(options) {
    const parts = ["node", options.cliScriptPath];
    if (options.fireboxBin) {
        parts.push("--firebox-bin", options.fireboxBin);
    }
    if (options.daemonId) {
        parts.push("--daemon-id", options.daemonId);
    }
    if (options.stateFilePath) {
        parts.push("--state-file", options.stateFilePath);
    }
    parts.push("claude-hook", "pretooluse-bash");
    if (options.permissionDecision !== DEFAULT_PERMISSION_DECISION) {
        parts.push("--permission", options.permissionDecision);
    }
    return parts.map((part) => shellQuote(part)).join(" ");
}
async function readJsonObject(filePath) {
    try {
        const raw = await fs.readFile(filePath, "utf8");
        if (!raw.trim()) {
            return {};
        }
        const parsed = JSON.parse(raw);
        if (!isRecord(parsed)) {
            return {};
        }
        return parsed;
    }
    catch (error) {
        if (error.code === "ENOENT") {
            return {};
        }
        throw error;
    }
}
function isRecord(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
function ensureRecord(parent, key) {
    const value = parent[key];
    if (isRecord(value)) {
        return value;
    }
    const next = {};
    parent[key] = next;
    return next;
}
function ensureArray(parent, key) {
    const value = parent[key];
    if (Array.isArray(value)) {
        return value;
    }
    const next = [];
    parent[key] = next;
    return next;
}
function ensureObjectArray(parent, key) {
    const value = parent[key];
    if (Array.isArray(value)) {
        if (value.every((item) => isRecord(item))) {
            return value;
        }
        const next = value.filter((item) => isRecord(item));
        parent[key] = next;
        return next;
    }
    const next = [];
    parent[key] = next;
    return next;
}
function upsertBashMatcher(entries) {
    for (const entry of entries) {
        if (!isRecord(entry)) {
            continue;
        }
        if (entry.matcher === "Bash") {
            return entry;
        }
    }
    const matcher = {
        matcher: "Bash",
        hooks: [],
    };
    entries.push(matcher);
    return matcher;
}
