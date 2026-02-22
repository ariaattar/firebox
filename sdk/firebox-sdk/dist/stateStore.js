import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
const STATE_VERSION = 1;
export function defaultStateFilePath() {
    return path.join(os.homedir(), ".config", "firebox", "firebox-sdk.json");
}
export function defaultGeneratedYamlPath() {
    return path.join(os.homedir(), ".config", "firebox", "firebox-default.yaml");
}
export function createDefaultState(defaultImageName, defaultYamlPath) {
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
    statePath;
    defaultImageName;
    defaultImageYamlPath;
    constructor(statePath, defaultImageName, defaultImageYamlPath) {
        this.statePath = statePath;
        this.defaultImageName = defaultImageName;
        this.defaultImageYamlPath = defaultImageYamlPath;
    }
    async load() {
        const fallback = createDefaultState(this.defaultImageName, this.defaultImageYamlPath);
        try {
            const raw = await fs.readFile(this.statePath, "utf8");
            const parsed = JSON.parse(raw);
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
        }
        catch (error) {
            if (error.code === "ENOENT") {
                return fallback;
            }
            throw error;
        }
    }
    async save(state) {
        const nextState = {
            ...state,
            version: STATE_VERSION,
            updatedAt: new Date().toISOString(),
        };
        await fs.mkdir(path.dirname(this.statePath), { recursive: true });
        await fs.writeFile(this.statePath, `${JSON.stringify(nextState, null, 2)}\n`, "utf8");
    }
}
