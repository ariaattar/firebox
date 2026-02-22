import { FireboxSDKState } from "./types.js";
export declare function defaultStateFilePath(): string;
export declare function defaultGeneratedYamlPath(): string;
export declare function createDefaultState(defaultImageName: string, defaultYamlPath?: string): FireboxSDKState;
export declare class FireboxStateStore {
    private readonly statePath;
    private readonly defaultImageName;
    private readonly defaultImageYamlPath?;
    constructor(statePath: string, defaultImageName: string, defaultImageYamlPath?: string);
    load(): Promise<FireboxSDKState>;
    save(state: FireboxSDKState): Promise<void>;
}
