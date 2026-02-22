import { FireboxSDK } from "../src/index.js";

export interface ToolBashInput {
  command: string;
  sessionId: string;
  workspaceDir: string;
  timeoutMs?: number;
}

export interface ToolReadInput {
  filePath: string;
  sessionId: string;
  workspaceDir: string;
  timeoutMs?: number;
}

export interface ToolWriteInput extends ToolReadInput {
  content: string;
}

export interface ToolEditInput extends ToolReadInput {
  oldText: string;
  newText: string;
  replaceAll?: boolean;
}

/**
 * Example adapter for tool handlers.
 * This intentionally mirrors a bash/read/write/edit split.
 */
export class ToolFireboxAdapter {
  private readonly sdk: FireboxSDK;

  constructor(sdk: FireboxSDK) {
    this.sdk = sdk;
  }

  async initialize(): Promise<void> {
    await this.sdk.initialize();
  }

  async shouldIgnoreAnthropicRuntime(): Promise<boolean> {
    return this.sdk.shouldIgnoreAnthropicRuntime();
  }

  async bash(input: ToolBashInput): Promise<{ stdout: string; stderr: string; exitCode: number }> {
    return this.sdk.bash(input.command, {
      sessionId: input.sessionId,
      workspaceDir: input.workspaceDir,
      timeoutMs: input.timeoutMs,
    });
  }

  async read(input: ToolReadInput): Promise<{ content: string; path: string }> {
    return this.sdk.read(input.filePath, {
      sessionId: input.sessionId,
      workspaceDir: input.workspaceDir,
      timeoutMs: input.timeoutMs,
    });
  }

  async write(input: ToolWriteInput): Promise<{ path: string; bytesWritten: number }> {
    return this.sdk.write(input.filePath, input.content, {
      sessionId: input.sessionId,
      workspaceDir: input.workspaceDir,
      timeoutMs: input.timeoutMs,
    });
  }

  async edit(input: ToolEditInput): Promise<{ path: string; replacements: number }> {
    return this.sdk.edit(input.filePath, input.oldText, input.newText, {
      sessionId: input.sessionId,
      workspaceDir: input.workspaceDir,
      timeoutMs: input.timeoutMs,
      replaceAll: input.replaceAll,
    });
  }
}
