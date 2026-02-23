import { FireboxSDKState } from "./types.js";
export declare function defaultStateFilePath(daemonId?: string): string;
export declare function legacyStateFilePath(): string;
export declare function defaultGeneratedYamlPath(daemonId?: string): string;
export declare function createDefaultState(defaultImageName: string, defaultYamlPath?: string): FireboxSDKState;
export declare class FireboxStateStore {
    private readonly statePath;
    private readonly legacyStatePath?;
    private readonly defaultImageName;
    private readonly defaultImageYamlPath?;
    constructor(statePath: string, defaultImageName: string, defaultImageYamlPath?: string, legacyStatePath?: string);
    load(): Promise<FireboxSDKState>;
    save(state: FireboxSDKState): Promise<void>;
    private readStateFileWithFallback;
}
