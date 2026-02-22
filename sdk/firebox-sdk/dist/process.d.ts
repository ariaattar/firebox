export interface ExecCommandOptions {
    cwd?: string;
    env?: NodeJS.ProcessEnv;
    timeoutMs?: number;
    input?: string;
}
export interface ExecCommandResult {
    command: string;
    args: string[];
    stdout: string;
    stderr: string;
    exitCode: number;
}
export declare function execCommand(command: string, args: string[], options?: ExecCommandOptions): Promise<ExecCommandResult>;
export declare function assertSuccess(result: ExecCommandResult, context: string): void;
