import { execCommand } from "./process.js";
export class FireboxClient {
    fireboxBin;
    daemonId;
    constructor(options) {
        this.fireboxBin = options.fireboxBin;
        this.daemonId = options.daemonId?.trim() || undefined;
    }
    async ensureAvailable() {
        const result = await this.run(["--help"], 5000);
        if (result.exitCode !== 0) {
            throw new Error(`firebox binary not available: ${result.stderr || result.stdout}`);
        }
    }
    async daemonRunning(timeoutMs = 5000) {
        const result = await this.run(["daemon", "status"], timeoutMs);
        return result.exitCode === 0;
    }
    async ensureDaemonRunning(timeoutMs = 20000) {
        if (await this.daemonRunning(timeoutMs)) {
            return;
        }
        const start = await this.run(["daemon", "start"], timeoutMs);
        if (start.exitCode !== 0) {
            throw new Error(`failed to start firebox daemon: ${start.stderr || start.stdout}`);
        }
    }
    async listImages(timeoutMs = 12000) {
        const result = await this.run(["image", "list"], timeoutMs);
        if (result.exitCode !== 0) {
            throw new Error(`firebox image list failed: ${result.stderr || result.stdout}`);
        }
        const names = [];
        for (const line of result.stdout.split(/\r?\n/)) {
            const trimmed = line.trim();
            if (!trimmed || trimmed.startsWith("NAME\t")) {
                continue;
            }
            const firstColumn = trimmed.split(/\s+/)[0];
            if (firstColumn) {
                names.push(firstColumn);
            }
        }
        return names;
    }
    async imageExists(name) {
        const images = await this.listImages();
        return images.includes(name);
    }
    async setupImage(name, yamlPath, timeoutMs = 180000) {
        const args = ["setup", "--name", name];
        if (yamlPath) {
            args.push("--file", yamlPath);
        }
        const result = await this.run(args, timeoutMs);
        if (result.exitCode !== 0) {
            throw new Error(`firebox setup failed: ${result.stderr || result.stdout}`);
        }
    }
    async useImage(name, timeoutMs = 15000) {
        const result = await this.run(["image", "use", name], timeoutMs);
        if (result.exitCode !== 0) {
            throw new Error(`firebox image use failed: ${result.stderr || result.stdout}`);
        }
    }
    async runNoCow(command, workspaceDir, timeoutMs = 60000) {
        return this.run([
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
        ], timeoutMs);
    }
    async sandboxInspect(sandboxId, timeoutMs = 12000) {
        const result = await this.run(["sandbox", "inspect", sandboxId], timeoutMs);
        if (result.exitCode !== 0) {
            const out = `${result.stderr}\n${result.stdout}`.toLowerCase();
            if (out.includes("not found") || out.includes("unknown")) {
                return null;
            }
            throw new Error(`sandbox inspect failed: ${result.stderr || result.stdout}`);
        }
        const raw = result.stdout.trim();
        if (!raw) {
            return null;
        }
        try {
            return JSON.parse(raw);
        }
        catch (error) {
            throw new Error(`invalid sandbox inspect output: ${error.message}`);
        }
    }
    async sandboxCreate(sandboxId, workspaceDir, guestMountPath = "/workspace", timeoutMs = 20000) {
        const result = await this.run([
            "sandbox",
            "create",
            "--id",
            sandboxId,
            "--strict-budget=false",
            "--mount",
            `${workspaceDir}:${guestMountPath}:cow=on`,
            "-w",
            guestMountPath,
        ], timeoutMs);
        if (result.exitCode !== 0) {
            throw new Error(`sandbox create failed: ${result.stderr || result.stdout}`);
        }
    }
    async sandboxStart(sandboxId, timeoutMs = 15000) {
        const result = await this.run(["sandbox", "start", sandboxId], timeoutMs);
        if (result.exitCode !== 0) {
            throw new Error(`sandbox start failed: ${result.stderr || result.stdout}`);
        }
    }
    async sandboxStop(sandboxId, timeoutMs = 15000) {
        const result = await this.run(["sandbox", "stop", sandboxId], timeoutMs);
        if (result.exitCode !== 0) {
            const out = `${result.stderr}\n${result.stdout}`.toLowerCase();
            if (out.includes("not found")) {
                return;
            }
            throw new Error(`sandbox stop failed: ${result.stderr || result.stdout}`);
        }
    }
    async sandboxRemove(sandboxId, timeoutMs = 15000) {
        const result = await this.run(["sandbox", "rm", sandboxId], timeoutMs);
        if (result.exitCode !== 0) {
            const out = `${result.stderr}\n${result.stdout}`.toLowerCase();
            if (out.includes("not found")) {
                return;
            }
            throw new Error(`sandbox rm failed: ${result.stderr || result.stdout}`);
        }
    }
    async sandboxExec(sandboxId, command, timeoutMs = 60000) {
        return this.run(["sandbox", "exec", sandboxId, "--", "bash", "-lc", command], timeoutMs);
    }
    async sandboxDiff(sandboxId, guestPath = "/workspace", timeoutMs = 20000) {
        const result = await this.run(["sandbox", "diff", sandboxId, "--path", guestPath], timeoutMs);
        if (result.exitCode !== 0) {
            throw new Error(`sandbox diff failed: ${result.stderr || result.stdout}`);
        }
        return result.stdout;
    }
    async sandboxApply(sandboxId, guestPath = "/workspace", timeoutMs = 20000) {
        const result = await this.run(["sandbox", "apply", sandboxId, "--path", guestPath], timeoutMs);
        if (result.exitCode !== 0) {
            throw new Error(`sandbox apply failed: ${result.stderr || result.stdout}`);
        }
        return result.stdout;
    }
    async rawRun(args, timeoutMs = 60000) {
        return this.run(args, timeoutMs);
    }
    run(args, timeoutMs) {
        const commandArgs = this.daemonId ? ["--daemon-id", this.daemonId, ...args] : args;
        return execCommand(this.fireboxBin, commandArgs, { timeoutMs });
    }
}
