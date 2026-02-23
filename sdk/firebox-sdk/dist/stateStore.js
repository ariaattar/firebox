import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
const STATE_VERSION = 1;
const LEGACY_CONFIG_DIR = path.join(os.homedir(), ".config", "firebox");
const DEFAULT_STATE_DIR = path.join(os.homedir(), ".firebox", "state");
const DEFAULT_DAEMON_ROOT_DIR = path.join(os.homedir(), ".firebox", "daemons");
const DAEMON_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;
function normalizeDaemonID(raw) {
    const value = raw?.trim();
    if (!value) {
        return undefined;
    }
    if (!DAEMON_ID_PATTERN.test(value)) {
        throw new Error(`invalid daemonId "${raw}": expected 1-64 chars [A-Za-z0-9._-] starting with alphanumeric`);
    }
    return value;
}
export function defaultStateFilePath(daemonId) {
    const normalizedDaemonID = normalizeDaemonID(daemonId);
    if (!normalizedDaemonID) {
        return path.join(DEFAULT_STATE_DIR, "firebox-sdk.json");
    }
    return path.join(DEFAULT_DAEMON_ROOT_DIR, normalizedDaemonID, "state", "firebox-sdk.json");
}
export function legacyStateFilePath() {
    return path.join(LEGACY_CONFIG_DIR, "firebox-sdk.json");
}
export function defaultGeneratedYamlPath(daemonId) {
    const normalizedDaemonID = normalizeDaemonID(daemonId);
    if (!normalizedDaemonID) {
        return path.join(LEGACY_CONFIG_DIR, "firebox-default.yaml");
    }
    return path.join(DEFAULT_DAEMON_ROOT_DIR, normalizedDaemonID, "state", "firebox-default.yaml");
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
    legacyStatePath;
    defaultImageName;
    defaultImageYamlPath;
    constructor(statePath, defaultImageName, defaultImageYamlPath, legacyStatePath) {
        this.statePath = statePath;
        this.legacyStatePath = legacyStatePath;
        this.defaultImageName = defaultImageName;
        this.defaultImageYamlPath = defaultImageYamlPath;
    }
    async load() {
        const fallback = createDefaultState(this.defaultImageName, this.defaultImageYamlPath);
        try {
            const raw = await this.readStateFileWithFallback();
            if (!raw) {
                return fallback;
            }
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
    async readStateFileWithFallback() {
        try {
            return await fs.readFile(this.statePath, "utf8");
        }
        catch (error) {
            if (error.code !== "ENOENT") {
                throw error;
            }
        }
        if (!this.legacyStatePath || this.legacyStatePath === this.statePath) {
            return null;
        }
        try {
            return await fs.readFile(this.legacyStatePath, "utf8");
        }
        catch (error) {
            if (error.code === "ENOENT") {
                return null;
            }
            throw error;
        }
    }
}
