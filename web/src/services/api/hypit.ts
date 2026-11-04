import { http } from "@/services/api/request";

/**
 * Hypit 视频复刻任务。
 *
 * 执行侧是独立的 Hypit Worker（node22 + ffmpeg + headless chromium + agent CLI），
 * 画布只负责任务的创建、排队、展示与产物回链，不在后端进程内跑渲染。
 */
export type HypitTaskStatus = "queued" | "running" | "succeeded" | "failed" | "canceled";

/** 复刻类型：与 hypit 的 workflow 改写方式一一对应。 */
export type HypitReplicaMode = "full-clone" | "swap-host" | "swap-script" | "swap-broll" | "localize" | "combo";

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
    durationSec?: number;
    language?: string;
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
    durationSec?: number;
    language?: string;
};

export type HypitTaskPage = {
    items: HypitTask[];
    total: number;
    page: number;
    limit: number;
    /** 执行节点心跳：false 时任务只排队保存，页面需要明确提示。 */
    workerOnline?: boolean;
};

export const HYPIT_REPLICA_MODES: Array<{ value: HypitReplicaMode; label: string; caption: string; hint: string }> = [
    { value: "full-clone", label: "整片复刻", caption: "保留结构与节奏", hint: "把参考片的镜头、字幕、B-roll 与节奏整体复刻，只替换内容素材" },
    { value: "swap-host", label: "换主角", caption: "人物 / 商品替换", hint: "保留分镜与字幕节奏，替换出镜人物或主推商品" },
    { value: "swap-script", label: "换文案", caption: "口播与字幕改写", hint: "保留画面结构，改写口播与字幕文案，时长自动重排" },
    { value: "swap-broll", label: "换 B-roll", caption: "空镜与素材替换", hint: "保留 A-roll 与节奏，替换中间的 B-roll 空镜素材" },
    { value: "localize", label: "换语言", caption: "多语言本地化", hint: "同一支片输出多语言版本，字幕与配音跟随改写" },
    { value: "combo", label: "组合替换", caption: "多项一起换", hint: "人物、文案、B-roll 同时替换，适合做同结构不同内容的变体" },
];

export const HYPIT_ASPECT_RATIOS = [
    { value: "9:16", label: "竖屏 9:16" },
    { value: "16:9", label: "横屏 16:9" },
    { value: "1:1", label: "方形 1:1" },
    { value: "3:4", label: "竖版 3:4" },
];

export const HYPIT_DURATIONS = [
    { value: "0", label: "跟随原片" },
    { value: "15", label: "15 秒" },
    { value: "30", label: "30 秒" },
    { value: "60", label: "60 秒" },
];

export const HYPIT_LANGUAGES = [
    { value: "source", label: "跟随原片" },
    { value: "zh", label: "中文" },
    { value: "en", label: "英文" },
    { value: "ja", label: "日文" },
];

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
