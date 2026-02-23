type HookPermissionDecision = "allow" | "ask";
export interface ClaudeHookRunOptions {
    fireboxBin?: string;
    daemonId?: string;
    stateFilePath?: string;
    permissionDecision?: HookPermissionDecision;
}
export interface InstallClaudeBashHookOptions extends ClaudeHookRunOptions {
    scope?: "project-local" | "project" | "user";
    settingsPath?: string;
    cliScriptPath: string;
    cwd?: string;
}
export interface InstallClaudeBashHookResult {
    settingsPath: string;
    command: string;
    updated: boolean;
}
export declare function runClaudePreToolUseBashHook(options?: ClaudeHookRunOptions): Promise<number>;
export declare function installClaudeBashHook(options: InstallClaudeBashHookOptions): Promise<InstallClaudeBashHookResult>;
export {};
