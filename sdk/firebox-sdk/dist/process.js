import { spawn } from "node:child_process";
export async function execCommand(command, args, options = {}) {
    return new Promise((resolve, reject) => {
        const child = spawn(command, args, {
            cwd: options.cwd,
            env: options.env ?? process.env,
            stdio: "pipe",
        });
        let stdout = "";
        let stderr = "";
        let timedOut = false;
        const timeoutMs = options.timeoutMs ?? 0;
        const timeout = timeoutMs > 0
            ? setTimeout(() => {
                timedOut = true;
                child.kill("SIGKILL");
            }, timeoutMs)
            : undefined;
        child.stdout.on("data", (chunk) => {
            stdout += chunk.toString();
        });
        child.stderr.on("data", (chunk) => {
            stderr += chunk.toString();
        });
        child.on("error", (error) => {
            if (timeout) {
                clearTimeout(timeout);
            }
            reject(error);
        });
        child.on("close", (code) => {
            if (timeout) {
                clearTimeout(timeout);
            }
            const exitCode = typeof code === "number" ? code : 1;
            if (timedOut) {
                resolve({
                    command,
                    args,
                    stdout,
                    stderr: stderr || `command timed out after ${timeoutMs}ms`,
                    exitCode: 124,
                });
                return;
            }
            resolve({
                command,
                args,
                stdout,
                stderr,
                exitCode,
            });
        });
        if (options.input) {
            child.stdin.write(options.input);
        }
        child.stdin.end();
    });
}
export function assertSuccess(result, context) {
    if (result.exitCode === 0) {
        return;
    }
    const output = (result.stderr || result.stdout).trim();
    const detail = output ? `: ${output}` : "";
    throw new Error(`${context} failed (exit=${result.exitCode})${detail}`);
}
