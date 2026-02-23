import { ExecCommandResult } from "./process.js";
export interface FireboxClientOptions {
    fireboxBin: string;
    daemonId?: string;
}
export interface SandboxInspect {
    id: string;
    status: string;
    spec?: {
        workdir?: string;
        mounts?: Array<{
            host_path?: string;
            guest_path?: string;
            access?: string;
            cow?: string;
        }>;
    };
}
export declare class FireboxClient {
    private readonly fireboxBin;
    private readonly daemonId?;
    constructor(options: FireboxClientOptions);
    ensureAvailable(): Promise<void>;
    daemonRunning(timeoutMs?: number): Promise<boolean>;
    ensureDaemonRunning(timeoutMs?: number): Promise<void>;
    listImages(timeoutMs?: number): Promise<string[]>;
    imageExists(name: string): Promise<boolean>;
    setupImage(name: string, yamlPath?: string, timeoutMs?: number): Promise<void>;
    useImage(name: string, timeoutMs?: number): Promise<void>;
    runNoCow(command: string, workspaceDir: string, timeoutMs?: number): Promise<ExecCommandResult>;
    sandboxInspect(sandboxId: string, timeoutMs?: number): Promise<SandboxInspect | null>;
    sandboxCreate(sandboxId: string, workspaceDir: string, guestMountPath?: string, timeoutMs?: number): Promise<void>;
    sandboxStart(sandboxId: string, timeoutMs?: number): Promise<void>;
    sandboxStop(sandboxId: string, timeoutMs?: number): Promise<void>;
    sandboxRemove(sandboxId: string, timeoutMs?: number): Promise<void>;
    sandboxExec(sandboxId: string, command: string, timeoutMs?: number): Promise<ExecCommandResult>;
    sandboxDiff(sandboxId: string, guestPath?: string, timeoutMs?: number): Promise<string>;
    sandboxApply(sandboxId: string, guestPath?: string, timeoutMs?: number): Promise<string>;
    rawRun(args: string[], timeoutMs?: number): Promise<ExecCommandResult>;
    private run;
}
