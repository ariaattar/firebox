import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";

import { FireboxSDKState } from "./types.js";

const STATE_VERSION = 1;

export function defaultStateFilePath(): string {
  return path.join(os.homedir(), ".config", "firebox", "firebox-sdk.json");
}

export function defaultGeneratedYamlPath(): string {
  return path.join(os.homedir(), ".config", "firebox", "firebox-default.yaml");
}

export function createDefaultState(defaultImageName: string, defaultYamlPath?: string): FireboxSDKState {
  const now = new Date().toISOString();
  return {
    version: STATE_VERSION,
    mode: "off",
    image: {
      name: defaultImageName,
      yamlPath: defaultYamlPath,
    },
    sessions: {},
    updatedAt: now,
  };
}

export class FireboxStateStore {
  private readonly statePath: string;
  private readonly defaultImageName: string;
  private readonly defaultImageYamlPath?: string;

  constructor(statePath: string, defaultImageName: string, defaultImageYamlPath?: string) {
    this.statePath = statePath;
    this.defaultImageName = defaultImageName;
    this.defaultImageYamlPath = defaultImageYamlPath;
  }

  async load(): Promise<FireboxSDKState> {
    const fallback = createDefaultState(this.defaultImageName, this.defaultImageYamlPath);
    try {
      const raw = await fs.readFile(this.statePath, "utf8");
      const parsed = JSON.parse(raw) as Partial<FireboxSDKState>;
      return {
        version: STATE_VERSION,
        mode: parsed.mode ?? fallback.mode,
        image: {
          name: parsed.image?.name ?? fallback.image.name,
          yamlPath: parsed.image?.yamlPath ?? fallback.image.yamlPath,
        },
        sessions: parsed.sessions ?? {},
        updatedAt: parsed.updatedAt ?? fallback.updatedAt,
      };
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === "ENOENT") {
        return fallback;
      }
      throw error;
    }
  }

  async save(state: FireboxSDKState): Promise<void> {
    const nextState: FireboxSDKState = {
      ...state,
      version: STATE_VERSION,
      updatedAt: new Date().toISOString(),
    };
    await fs.mkdir(path.dirname(this.statePath), { recursive: true });
    await fs.writeFile(this.statePath, `${JSON.stringify(nextState, null, 2)}\n`, "utf8");
  }
}
