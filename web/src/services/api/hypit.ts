import { http } from "@/services/api/request";

/**
 * Hypit 复刻视频任务。
 *
 * 执行侧是独立的 Hypit Worker（node22 + ffmpeg + headless chromium + agent CLI），
 * 画布只负责任务的创建、排队、展示与产物回链，不在后端进程内跑渲染。
 */
export type HypitTaskStatus = "queued" | "running" | "succeeded" | "failed" | "canceled";

/**
 * 替换类型。
 *
 * 前 8 个是工作台当前展示的槽位类型（模特 / 服饰 / 人脸 / 背景 / 商品 / 精准商品 / 精准人物 / 组合），
 * 后 6 个是早期版本的类型，保留只是为了历史任务能正常回显标题。
 */
export type HypitReplicaMode =
    | "model-swap"
    | "outfit-swap"
    | "face-swap"
    | "background-swap"
    | "product-swap"
    | "product-precise"
    | "person-precise"
    | "combo-swap"
    | "full-clone"
    | "swap-host"
    | "swap-script"
    | "swap-broll"
    | "localize"
    | "combo";

/** 声音处理方式：替换音频 / 保留原声 / 静音。 */
export type HypitVoiceMode = "original" | "replace" | "mute";

/** 一个素材槽位提交的资源列表，slot 为业务槽位名（person / outfit / product …）。 */
export type HypitMaterialSlot = {
    slot: string;
    resourceIds: string[];
};

export type HypitTask = {
    id: string;
    title: string;
    mode: HypitReplicaMode;
    referenceResourceId?: string;
    referenceUrl?: string;
    referenceName?: string;
    instructions: string;
    model?: string;
    aspectRatio?: string;
    resolution?: string;
    durationSec?: number;
    language?: string;
    /** 素材槽位快照（JSON 字符串，形如 [{"slot":"person","resourceIds":["..."]}]）。 */
    materialsJson?: string;
    voiceMode?: HypitVoiceMode;
    keepMusic?: boolean;
    talkingHead?: boolean;
    subject?: string;
    status: HypitTaskStatus;
    progress: number;
    stage?: string;
    outputUrl?: string;
    outputResourceId?: string;
    error?: string;
    createdAt: string;
    startedAt?: string;
    finishedAt?: string;
};

export type HypitTaskCreateInput = {
    mode: HypitReplicaMode;
    referenceResourceId?: string;
    referenceUrl?: string;
    referenceName?: string;
    instructions?: string;
    model?: string;
    aspectRatio?: string;
    resolution?: string;
    durationSec?: number;
    language?: string;
    materials?: HypitMaterialSlot[];
    voiceMode?: HypitVoiceMode;
    keepMusic?: boolean;
    talkingHead?: boolean;
    subject?: string;
};

export type HypitTaskPage = {
    items: HypitTask[];
    total: number;
    page: number;
    limit: number;
    /** 执行节点心跳：false 时任务只排队保存，页面需要明确提示。 */
    workerOnline?: boolean;
};

export const HYPIT_MODE_LABELS: Record<string, string> = {
    "model-swap": "模特替换",
    "outfit-swap": "服饰替换",
    "face-swap": "人脸替换",
    "background-swap": "换背景",
    "product-swap": "商品替换",
    "product-precise": "精准商品",
    "person-precise": "精准人物",
    "combo-swap": "组合替换",
    "full-clone": "整片复刻",
    "swap-host": "换主角",
    "swap-script": "换文案",
    "swap-broll": "换 B-roll",
    localize: "换语言",
    combo: "组合替换",
};

export const HYPIT_VOICE_LABELS: Record<HypitVoiceMode, string> = {
    replace: "替换音频",
    original: "保留原声",
    mute: "静音",
};

export const HYPIT_STATUS_LABELS: Record<HypitTaskStatus, string> = {
    queued: "排队中",
    running: "复刻中",
    succeeded: "已完成",
    failed: "失败",
    canceled: "已取消",
};

export async function createHypitTask(input: HypitTaskCreateInput): Promise<HypitTask> {
    const data = await http.post<{ task: HypitTask }>("/hypit/tasks", input);
    return data.task;
}

export async function listHypitTasks(params: { page?: number; limit?: number } = {}): Promise<HypitTaskPage> {
    return http.get<HypitTaskPage>("/hypit/tasks", { params });
}

export async function getHypitTask(id: string): Promise<HypitTask> {
    const data = await http.get<{ task: HypitTask }>(`/hypit/tasks/${encodeURIComponent(id)}`);
    return data.task;
}

export async function cancelHypitTask(id: string): Promise<HypitTask> {
    const data = await http.post<{ task: HypitTask }>(`/hypit/tasks/${encodeURIComponent(id)}/cancel`);
    return data.task;
}

/** 结果产物统一走资源解析，和生成工作台保持同一套 URL 回退逻辑。 */
export function hypitTaskOutputURL(task: HypitTask): string | undefined {
    return task.outputUrl || undefined;
}

export function hypitTaskIsActive(task: HypitTask): boolean {
    return task.status === "queued" || task.status === "running";
}
