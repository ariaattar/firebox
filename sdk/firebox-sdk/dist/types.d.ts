export type FireboxMode = "off" | "on-no-cow" | "on-cow";
export interface FireboxImageConfig {
    name: string;
    yamlPath?: string;
}
export interface SessionSandboxBinding {
    sessionId: string;
    sandboxId: string;
    workspaceDir: string;
    guestMountPath: string;
    updatedAt: string;
}
export interface FireboxSDKState {
    version: number;
    mode: FireboxMode;
    image: FireboxImageConfig;
    sessions: Record<string, SessionSandboxBinding>;
    updatedAt: string;
}
export interface FireboxSDKOptions {
    fireboxBin?: string;
    daemonId?: string;
    stateFilePath?: string;
    defaultWorkspaceDir?: string;
    defaultImageName?: string;
    defaultImageYamlPath?: string;
    autoSetup?: boolean;
    autoStartDaemon?: boolean;
}
export interface FireboxExecOptions {
    sessionId?: string;
    workspaceDir?: string;
    timeoutMs?: number;
}
export interface FireboxEditOptions extends FireboxExecOptions {
    replaceAll?: boolean;
}
export interface BashResult {
    stdout: string;
    stderr: string;
    exitCode: number;
}
export interface PreparedBashCommand {
    mode: FireboxMode;
    wrappedCommand: string;
    sandboxId?: string;
}
export interface ReadResult {
    content: string;
    path: string;
}
export interface WriteResult {
    path: string;
    bytesWritten: number;
}
export interface EditResult {
    path: string;
    replacements: number;
}
