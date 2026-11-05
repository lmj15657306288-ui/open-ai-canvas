import { App, Button, Input, Progress } from "antd";
import { ArrowUp, Check, CircleAlert, Clapperboard, Download, Film, FolderOpen, ImagePlus, LayoutGrid, Music, RefreshCw, RotateCcw, Upload, X, XCircle } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceState } from "@/components/layout/workspace-state";
import { ModelPicker } from "@/components/model-picker";
import { AntTooltip as Tooltip } from "@/components/ui/base/tooltip/ant-tooltip";
import { cn } from "@/lib/utils";
import { ChoiceChips, ComposerSection, SwitchRow } from "@/pages/workbench/workbench-composer";
import { HYPIT_MODE_LABELS, HYPIT_STATUS_LABELS, HYPIT_VOICE_LABELS, cancelHypitTask, createHypitTask, hypitTaskIsActive, listHypitTasks, type HypitMaterialSlot, type HypitReplicaMode, type HypitTask, type HypitVoiceMode } from "@/services/api/hypit";
import { uploadResourceFile } from "@/services/api/resources";
import { selectableModelsByCapability, useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";

// 复用画布工作台的样式体系，保证与 /toolbox/image、/toolbox/video 完全同源。
import "@/pages/workbench/workbench.css";
import "./hypit-workbench.css";

const POLL_INTERVAL_MS = 4000;
const REFERENCE_ACCEPT = ".mp4,.mov,.webm,video/*";
const IMAGE_ACCEPT = "image/*";
const AUDIO_ACCEPT = "audio/*";
const MAX_PER_SLOT = 3;
const MAX_TOTAL_MATERIALS = 12;

type Reference = { source: "resource" | "url"; id?: string; url?: string; name: string; size?: number };
type MaterialItem = { resourceId: string; name: string; previewUrl?: string; kind: "image" | "audio" };

type SlotSpec = {
    /** 提交给后端的业务槽位名。 */
    slot: string;
    label: string;
    hint: string;
    kind: "image" | "audio";
    max: number;
};

type ReplicaSpec = {
    id: HypitReplicaMode;
    label: string;
    hint: string;
    slots: SlotSpec[];
    tip: { good: string[]; bad: string[] };
    instructionPlaceholder: string;
    instructionMax: number;
    checks: Array<{ key: "keepMusic" | "talkingHead"; label: string; detail: string }>;
};

const CHECK_KEEP_MUSIC = { key: "keepMusic" as const, label: "保留背景音乐", detail: "沿用参考视频的 BGM 与音效" };
const CHECK_TALKING = { key: "talkingHead" as const, label: "生成人物口播", detail: "重新配音，时长可能变长" };

/** 8 种替换类型：槽位、提示、字数上限都跟着类型走，与后端 hypitReplicaModes 一一对应。 */
const REPLICA_TYPES: ReplicaSpec[] = [
    {
        id: "model-swap",
        label: "模特替换",
        hint: "保留原片的动作、镜头与节奏，只把出镜模特换成你上传的人物",
        slots: [
            { slot: "person", label: "人物全身图", hint: "正面全身，单人入镜", kind: "image", max: 3 },
            { slot: "outfit", label: "服装参考", hint: "可选，平铺或上身图", kind: "image", max: 1 },
        ],
        tip: {
            good: ["单人、正面站立、全身完整入镜", "背景干净、光线均匀，脸部无遮挡"],
            bad: ["多人合影，或只拍到半身、大头照", "逆光、强阴影，戴帽子墨镜遮住五官"],
        },
        instructionPlaceholder: "例如：换成 25 岁女性，白色针织衫，保留原片的怼脸推近运镜",
        instructionMax: 500,
        checks: [CHECK_KEEP_MUSIC, CHECK_TALKING],
    },
    {
        id: "outfit-swap",
        label: "服饰替换",
        hint: "人物不变，把身上的衣服换成你上传的款式",
        slots: [
            { slot: "outfit", label: "服装图", hint: "平铺或上身图", kind: "image", max: 3 },
            { slot: "person", label: "人物图", hint: "可选，指定模特时上传", kind: "image", max: 1 },
        ],
        tip: {
            good: ["服装正面平铺图，图案与颜色完整", "需要指定模特时附带一张正面全身图"],
            bad: ["衣服被手遮住，或只拍到局部", "多套衣服混在一张图里"],
        },
        instructionPlaceholder: "例如：换成图中的米色风衣，保留原片的转身动作",
        instructionMax: 500,
        checks: [CHECK_KEEP_MUSIC],
    },
    {
        id: "face-swap",
        label: "人脸替换",
        hint: "只换脸，保留原片的服装、发型与身体动作",
        slots: [{ slot: "face", label: "人脸图", hint: "正面清晰，光线均匀", kind: "image", max: 3 }],
        tip: {
            good: ["正面免冠，五官清晰，分辨率足够", "光线均匀，不带夸张滤镜"],
            bad: ["侧脸、仰头，或脸部被头发遮挡", "多人同框、卡通头像"],
        },
        instructionPlaceholder: "例如：换成图中的脸，保留原片的妆容浓淡",
        instructionMax: 500,
        checks: [CHECK_KEEP_MUSIC, CHECK_TALKING],
    },
    {
        id: "background-swap",
        label: "换背景",
        hint: "人物与动作不变，把场景背景换成你上传的图",
        slots: [{ slot: "background", label: "背景图", hint: "场景完整、无人物，横图更佳", kind: "image", max: 3 }],
        tip: {
            good: ["场景完整、没有其他人，光线方向明确", "与人物景别匹配（室内 / 街景 / 纯色）"],
            bad: ["背景里已有其他人或文字", "纯白无细节的空白图"],
        },
        instructionPlaceholder: "例如：换成图中的咖啡馆内景，保持原片的窗边自然光",
        instructionMax: 500,
        checks: [CHECK_KEEP_MUSIC],
    },
    {
        id: "product-swap",
        label: "商品替换",
        hint: "把片中主推的商品换成你的商品，人物与场景保留",
        slots: [
            { slot: "product", label: "商品图", hint: "白底图或多角度", kind: "image", max: 3 },
            { slot: "scene", label: "场景参考", hint: "可选，使用场景图", kind: "image", max: 1 },
        ],
        tip: {
            good: ["商品白底图 + 一张使用场景图", "包装文字清晰，颜色还原真实"],
            bad: ["商品被手挡住，或反光过曝", "多件商品挤在一张图里"],
        },
        instructionPlaceholder: "例如：换成图中的保温杯，保留原片的举杯特写",
        instructionMax: 500,
        checks: [CHECK_KEEP_MUSIC, CHECK_TALKING],
    },
    {
        id: "product-precise",
        label: "精准商品",
        hint: "强约束商品外观，包装文字与 logo 尽量不走样",
        slots: [
            { slot: "product", label: "商品图", hint: "正面 + 细节各一张", kind: "image", max: 3 },
            { slot: "label", label: "包装文字", hint: "可选，文字特写", kind: "image", max: 1 },
        ],
        tip: {
            good: ["同一商品的正反面各一张", "包装上的文字单独拍一张特写"],
            bad: ["压缩过的电商截图，文字已经糊了"],
        },
        instructionPlaceholder: "包装文字原样保留",
        instructionMax: 10,
        checks: [CHECK_KEEP_MUSIC],
    },
    {
        id: "person-precise",
        label: "精准人物",
        hint: "强约束五官与身材比例，适合固定出镜人设",
        slots: [
            { slot: "person", label: "人物全身图", hint: "正面全身", kind: "image", max: 3 },
            { slot: "face", label: "人脸特写", hint: "可选，正脸特写", kind: "image", max: 1 },
        ],
        tip: {
            good: ["同一人的全身图 + 一张正脸特写", "服装简单，避免大面积花纹"],
            bad: ["不同人的照片混着传", "重度美颜或换脸滤镜"],
        },
        instructionPlaceholder: "保持五官一致",
        instructionMax: 10,
        checks: [CHECK_KEEP_MUSIC, CHECK_TALKING],
    },
    {
        id: "combo-swap",
        label: "组合替换",
        hint: "人物、商品、背景一起换，一次产出同结构不同内容的变体",
        slots: [
            { slot: "person", label: "人物图", hint: "正面全身", kind: "image", max: 3 },
            { slot: "product", label: "商品图", hint: "可选", kind: "image", max: 2 },
            { slot: "background", label: "背景图", hint: "可选", kind: "image", max: 2 },
        ],
        tip: {
            good: ["每类素材只放对应的图，不要混传", "人物与商品图风格尽量统一"],
            bad: ["同一张图同时当人物和背景", "素材总量超过 12 张"],
        },
        instructionPlaceholder: "例如：人物换成图中女生，商品换成保温杯，背景换成秋天的街道",
        instructionMax: 500,
        checks: [CHECK_KEEP_MUSIC, CHECK_TALKING],
    },
];

const VOICE_OPTIONS: Array<{ value: HypitVoiceMode; label: string; caption: string }> = [
    { value: "replace", label: HYPIT_VOICE_LABELS.replace, caption: "上传音频替换人声" },
    { value: "original", label: HYPIT_VOICE_LABELS.original, caption: "保留原片人声" },
    { value: "mute", label: HYPIT_VOICE_LABELS.mute, caption: "只留环境音" },
];

const RATIO_OPTIONS = [
    { value: "9:16", label: "9:16", caption: "竖屏" },
    { value: "16:9", label: "16:9", caption: "横屏" },
    { value: "1:1", label: "1:1", caption: "方形" },
    { value: "3:4", label: "3:4", caption: "竖版" },
];

const RESOLUTION_OPTIONS = [
    { value: "480p", label: "480P", caption: "草稿" },
    { value: "720p", label: "720P", caption: "常规" },
    { value: "1080p", label: "1080P", caption: "高清" },
];

function formatSize(bytes?: number): string {
    if (!bytes || bytes <= 0) return "";
    const mb = bytes / (1024 * 1024);
    return mb >= 1 ? `${mb.toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`;
}

function isHttpURL(value: string): boolean {
    try {
        const parsed = new URL(value.trim());
        return parsed.protocol === "http:" || parsed.protocol === "https:";
    } catch {
        return false;
    }
}

function taskChips(task: HypitTask): string[] {
    const chips = [HYPIT_MODE_LABELS[task.mode] ?? task.mode];
    if (task.aspectRatio) chips.push(`${task.aspectRatio}${task.resolution ? ` · ${task.resolution}` : ""}`);
    if (task.voiceMode) chips.push(HYPIT_VOICE_LABELS[task.voiceMode] ?? task.voiceMode);
    if (task.keepMusic) chips.push("保留音乐");
    if (task.talkingHead) chips.push("人物口播");
    return chips;
}

/** 单个素材槽位：复用画布的 wb-slot / wb-ref 结构，空态是 wb-drop，有图后是缩略图 + 追加按钮。 */
function MaterialSlot({
    slot,
    items,
    required,
    uploading,
    percent,
    onPick,
    onLibrary,
    onRemove,
}: {
    slot: SlotSpec;
    items: MaterialItem[];
    required: boolean;
    uploading: boolean;
    percent: number;
    onPick: () => void;
    onLibrary: () => void;
    onRemove: (resourceId: string) => void;
}) {
    const full = items.length >= slot.max;
    const missing = required && items.length === 0;
    return (
        <div className={cn("wb-slot", missing && "is-missing")}>
            <div className="wb-slot-head">
                <strong>{slot.label}</strong>
                <span className={cn("wb-badge", required && "is-required")}>{required ? "必填" : "可选"}</span>
                <small>
                    {items.length} / {slot.max}
                </small>
            </div>
            <div className="wb-ref-row">
                {items.map((item) => (
                    <div key={item.resourceId} className="wb-ref">
                        <span className="wb-ref-media">
                            {item.previewUrl ? (
                                <img src={item.previewUrl} alt={item.name} />
                            ) : (
                                <span className="wb-ref-audio">
                                    <Music aria-hidden />
                                </span>
                            )}
                        </span>
                        <button type="button" className="wb-ref-remove" aria-label={`移除${item.name}`} onClick={() => onRemove(item.resourceId)}>
                            <X aria-hidden />
                        </button>
                    </div>
                ))}
                {uploading ? (
                    <div className="wb-ref">
                        <span className="wb-ref-media">
                            <span className="wb-ref-progress">{percent}%</span>
                        </span>
                    </div>
                ) : null}
                {full ? null : items.length ? (
                    <Tooltip title={`继续上传${slot.label}`}>
                        <button type="button" className="wb-ref-add" data-icon-only aria-label={`继续上传${slot.label}`} onClick={onPick}>
                            <ImagePlus aria-hidden />
                        </button>
                    </Tooltip>
                ) : (
                    <button type="button" className="wb-drop" onClick={onPick}>
                        <span className="wb-drop-icon">
                            <ImagePlus aria-hidden />
                        </span>
                        <span className="wb-drop-copy">
                            <strong>上传{slot.label}</strong>
                            <small>{slot.hint}</small>
                        </span>
                    </button>
                )}
                <button type="button" className="wb-ref-library" aria-label={`从素材库选择${slot.label}`} title={full ? "已达到数量上限" : "一次选多张批量导入"} disabled={full} onClick={onLibrary}>
                    <FolderOpen aria-hidden />
                    <small>素材库</small>
                </button>
            </div>
        </div>
    );
}

export default function HypitWorkbenchPage() {
    const { message } = App.useApp();
    const navigate = useNavigate();
    const features = useUserStore((state) => state.features);
    const config = useEffectiveConfig();
    const updateConfig = useConfigStore((state) => state.updateConfig);
    const refFileRef = useRef<HTMLInputElement | null>(null);
    const slotFileRef = useRef<HTMLInputElement | null>(null);
    const libraryFileRef = useRef<HTMLInputElement | null>(null);
    /** 记录待释放的本地预览地址，避免 objectURL 泄漏。 */
    const previewsRef = useRef<string[]>([]);

    const [typeID, setTypeID] = useState<HypitReplicaMode>("model-swap");
    const [materials, setMaterials] = useState<Record<string, MaterialItem[]>>({});
    const [reference, setReference] = useState<Reference | null>(null);
    const [urlDraft, setUrlDraft] = useState("");
    const [voiceMode, setVoiceMode] = useState<HypitVoiceMode>("original");
    const [keepMusic, setKeepMusic] = useState(true);
    const [talkingHead, setTalkingHead] = useState(false);
    const [model, setModel] = useState("");
    const [aspectRatio, setAspectRatio] = useState("9:16");
    const [resolution, setResolution] = useState("720p");
    const [instructions, setInstructions] = useState("");

    const [uploadingTarget, setUploadingTarget] = useState<string | null>(null);
    const [uploadPercent, setUploadPercent] = useState(0);
    const [submitting, setSubmitting] = useState(false);

    const [tasks, setTasks] = useState<HypitTask[]>([]);
    const [loadingTasks, setLoadingTasks] = useState(true);
    const [nodeOnline, setNodeOnline] = useState(true);
    const [loadError, setLoadError] = useState<string | null>(null);

    const spec = useMemo(() => REPLICA_TYPES.find((item) => item.id === typeID) ?? REPLICA_TYPES[0], [typeID]);
    const activeTask = useMemo(() => tasks.find(hypitTaskIsActive) ?? null, [tasks]);
    const totalMaterials = useMemo(() => Object.values(materials).reduce((sum, items) => sum + items.length, 0), [materials]);

    // 模型目录直接取画布当前生效的渠道配置，和创作页同一份可选列表。
    const models = useMemo(() => selectableModelsByCapability(config, "video"), [config]);
    const configuredModel = config.videoModel || config.model || "";
    const selectedModel = useMemo(() => {
        if (model && models.includes(model)) return model;
        if (configuredModel && models.includes(configuredModel)) return configuredModel;
        return models[0] || "";
    }, [configuredModel, model, models]);

    useEffect(() => {
        return () => {
            previewsRef.current.forEach((url) => URL.revokeObjectURL(url));
            previewsRef.current = [];
        };
    }, []);

    const refreshTasks = useCallback(async () => {
        try {
            const page = await listHypitTasks({ page: 1, limit: 20 });
            setTasks(page.items);
            setNodeOnline(page.workerOnline ?? true);
            setLoadError(null);
        } catch (error) {
            setLoadError(error instanceof Error ? error.message : "任务列表加载失败");
        } finally {
            setLoadingTasks(false);
        }
    }, []);

    useEffect(() => {
        void refreshTasks();
    }, [refreshTasks]);

    // 仅在存在进行中任务时轮询，避免空转请求。
    useEffect(() => {
        if (!activeTask) return;
        const timer = window.setInterval(() => void refreshTasks(), POLL_INTERVAL_MS);
        return () => window.clearInterval(timer);
    }, [activeTask, refreshTasks]);

    /** 声音选「替换音频」时，在素材区多出一个音频槽位。 */
    const voiceSlots: SlotSpec[] = useMemo(() => (voiceMode === "replace" ? [{ slot: "voice", label: "替换音频", hint: "mp3 / wav，选填时长接近原片", kind: "audio", max: 1 }] : []), [voiceMode]);
    const activeSlots = useMemo(() => [...spec.slots, ...voiceSlots], [spec, voiceSlots]);

    const slotLimit = useCallback((slot: string): number => activeSlots.find((item) => item.slot === slot)?.max ?? MAX_PER_SLOT, [activeSlots]);

    const appendMaterials = useCallback(
        (slot: string, items: MaterialItem[]) => {
            setMaterials((current) => {
                const existing = current[slot] ?? [];
                const limit = Math.min(slotLimit(slot), MAX_TOTAL_MATERIALS - (totalMaterials - existing.length));
                const next = [...existing, ...items].slice(0, Math.max(limit, existing.length));
                return { ...current, [slot]: next };
            });
        },
        [slotLimit, totalMaterials],
    );

    const uploadToSlot = useCallback(
        async (slot: string, files: File[]) => {
            const kind = activeSlots.find((item) => item.slot === slot)?.kind ?? "image";
            const accepted = files.filter((file) => (kind === "audio" ? file.type.startsWith("audio/") : file.type.startsWith("image/")));
            if (accepted.length === 0) {
                message.error(kind === "audio" ? "请上传音频文件" : "素材需要是图片");
                return;
            }
            const existing = materials[slot]?.length ?? 0;
            const room = Math.min(slotLimit(slot) - existing, MAX_TOTAL_MATERIALS - totalMaterials);
            if (room <= 0) {
                message.warning(totalMaterials >= MAX_TOTAL_MATERIALS ? "素材总数已达 12 张上限" : "这个槽位已经满了");
                return;
            }
            const queue = accepted.slice(0, room);
            if (queue.length < accepted.length) message.warning("超出数量的素材已自动忽略");

            setUploadingTarget(slot);
            setUploadPercent(0);
            const uploaded: MaterialItem[] = [];
            try {
                for (let index = 0; index < queue.length; index += 1) {
                    const file = queue[index];
                    const resource = await uploadResourceFile(file, kind, { fileName: file.name }, (sent, total) => {
                        const per = total > 0 ? sent / total : 0;
                        setUploadPercent(Math.round(((index + per) / queue.length) * 100));
                    });
                    const previewUrl = kind === "image" ? URL.createObjectURL(file) : undefined;
                    if (previewUrl) previewsRef.current.push(previewUrl);
                    uploaded.push({ resourceId: resource.id, name: file.name, previewUrl, kind });
                }
                appendMaterials(slot, uploaded);
            } catch (error) {
                message.error(error instanceof Error ? error.message : "素材上传失败");
            } finally {
                setUploadingTarget(null);
                setUploadPercent(0);
            }
        },
        [activeSlots, appendMaterials, materials, message, slotLimit, totalMaterials],
    );

    /** 同一个隐藏 input 服务所有槽位：点击时按槽位切换 accept 与目标槽位。 */
    const openSlotPicker = useCallback((slot: string, kind: "image" | "audio") => {
        const input = slotFileRef.current;
        if (!input) return;
        input.accept = kind === "audio" ? AUDIO_ACCEPT : IMAGE_ACCEPT;
        input.dataset.slot = slot;
        input.click();
    }, []);

    const removeMaterial = useCallback((slot: string, resourceId: string) => {
        setMaterials((current) => {
            const items = current[slot] ?? [];
            const target = items.find((item) => item.resourceId === resourceId);
            if (target?.previewUrl) URL.revokeObjectURL(target.previewUrl);
            return { ...current, [slot]: items.filter((item) => item.resourceId !== resourceId) };
        });
    }, []);

    const clearMaterials = useCallback(() => {
        setMaterials((current) => {
            Object.values(current).forEach((items) => items.forEach((item) => item.previewUrl && URL.revokeObjectURL(item.previewUrl)));
            return {};
        });
    }, []);

    const pickReferenceFile = useCallback(
        async (file: File) => {
            if (!file.type.startsWith("video/")) {
                message.error("参考视频需要是 mp4 / mov / webm");
                return;
            }
            setUploadingTarget("reference");
            setUploadPercent(0);
            try {
                const resource = await uploadResourceFile(file, "video", { fileName: file.name }, (sent, total) => {
                    setUploadPercent(total > 0 ? Math.round((sent / total) * 100) : 0);
                });
                setReference({ source: "resource", id: resource.id, name: file.name, size: file.size });
                setUrlDraft("");
            } catch (error) {
                message.error(error instanceof Error ? error.message : "参考视频上传失败");
            } finally {
                setUploadingTarget(null);
                setUploadPercent(0);
            }
        },
        [message],
    );

    const pasteFromClipboard = useCallback(async () => {
        try {
            const text = await navigator.clipboard.readText();
            if (!text.trim()) {
                message.warning("剪贴板里没有链接");
                return;
            }
            setUrlDraft(text.trim());
        } catch {
            message.error("浏览器拒绝了剪贴板读取，请手动粘贴");
        }
    }, [message]);

    const applyUrlReference = useCallback(() => {
        const value = urlDraft.trim();
        if (!isHttpURL(value)) {
            message.error("请填入可访问的视频链接（http/https）");
            return;
        }
        const name = value.split("/").pop()?.split("?")[0] || value;
        setReference({ source: "url", url: value, name });
    }, [urlDraft, message]);

    const resetComposer = useCallback(() => {
        clearMaterials();
        setReference(null);
        setUrlDraft("");
        setInstructions("");
        setVoiceMode("original");
        setKeepMusic(true);
        setTalkingHead(false);
        setAspectRatio("9:16");
        setResolution("720p");
    }, [clearMaterials]);

    const primarySlot = spec.slots[0];
    const disabledReason = !reference
        ? "先选一个参考视频"
        : primarySlot && (materials[primarySlot.slot]?.length ?? 0) === 0
          ? `请上传${primarySlot.label}`
          : voiceMode === "replace" && (materials.voice?.length ?? 0) === 0
            ? "选择「替换音频」后需要上传一段音频"
            : !selectedModel
              ? "当前没有可用的视频模型，请先在模型设置里配置渠道"
              : undefined;

    const submit = useCallback(async () => {
        if (!reference || disabledReason) {
            if (disabledReason) message.warning(disabledReason);
            return;
        }
        const payloadMaterials: HypitMaterialSlot[] = Object.entries(materials)
            .map(([slot, items]) => ({ slot, resourceIds: items.map((item) => item.resourceId) }))
            .filter((item) => item.resourceIds.length > 0);

        setSubmitting(true);
        try {
            const task = await createHypitTask({
                mode: typeID,
                referenceResourceId: reference.source === "resource" ? reference.id : undefined,
                referenceUrl: reference.source === "url" ? reference.url : undefined,
                referenceName: reference.name,
                instructions: instructions.trim(),
                model: selectedModel,
                aspectRatio,
                resolution,
                materials: payloadMaterials,
                voiceMode,
                keepMusic,
                talkingHead,
            });
            setTasks((current) => [task, ...current]);
            message.success("已提交复刻任务");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "提交失败");
        } finally {
            setSubmitting(false);
        }
    }, [aspectRatio, disabledReason, instructions, keepMusic, materials, message, reference, resolution, selectedModel, talkingHead, typeID, voiceMode]);

    const cancel = useCallback(
        async (task: HypitTask) => {
            try {
                const next = await cancelHypitTask(task.id);
                setTasks((current) => current.map((item) => (item.id === next.id ? next : item)));
            } catch (error) {
                message.error(error instanceof Error ? error.message : "取消失败");
            }
        },
        [message],
    );

    const busy = submitting;

    return (
        <WorkspacePage fluid scroll={false} className="wb-page">
            <div className="wb-shell" data-kind="video">
                <aside className="wb-composer" aria-label="复刻视频面板">
                    <header className="wb-composer-head">
                        <h1 className="sr-only">复刻视频</h1>
                        <nav className="wb-kind-switch" aria-label="切换创作类型">
                            {features.videoWorkbenchEnabled ? (
                                <button type="button" aria-pressed={false} aria-label="商品视频" onClick={() => navigate("/toolbox/video")}>
                                    <Clapperboard />
                                    商品视频
                                </button>
                            ) : null}
                            <button type="button" aria-pressed aria-label="复刻视频" className="is-active">
                                <Film />
                                复刻视频
                            </button>
                        </nav>
                        <Tooltip title="清空素材、参考视频和参数（结果不受影响）">
                            <button type="button" className="wb-icon-button" data-icon-only aria-label="重置复刻面板" onClick={resetComposer}>
                                <RotateCcw />
                            </button>
                        </Tooltip>
                    </header>

                    <div className="wb-composer-scroll">
                        <ComposerSection title="替换类型" hint="选一种要替换的内容">
                            <ChoiceChips
                                label="替换类型"
                                value={typeID}
                                columns={4}
                                options={REPLICA_TYPES.map((item) => ({ value: item.id, label: item.label, caption: item.slots[0]?.label, title: item.hint }))}
                                onChange={(value) => setTypeID(value as HypitReplicaMode)}
                            />
                            <p className="wb-muted-note">{spec.hint}</p>
                            <div className="hypit-tip">
                                {spec.tip.good.map((text) => (
                                    <span key={text} className="hypit-tip-row is-good">
                                        <Check aria-hidden />
                                        {text}
                                    </span>
                                ))}
                                {spec.tip.bad.map((text) => (
                                    <span key={text} className="hypit-tip-row is-bad">
                                        <XCircle aria-hidden />
                                        {text}
                                    </span>
                                ))}
                            </div>
                        </ComposerSection>

                        <ComposerSection
                            title="素材"
                            hint={`${totalMaterials} / ${MAX_TOTAL_MATERIALS} 张 · 单槽最多 3 张`}
                            aside={
                                totalMaterials ? (
                                    <Button size="small" type="text" className="wb-text-action" onClick={clearMaterials}>
                                        清空
                                    </Button>
                                ) : null
                            }
                        >
                            <div className="wb-slot-list">
                                {activeSlots.map((slot) => (
                                    <MaterialSlot
                                        key={slot.slot}
                                        slot={slot}
                                        items={materials[slot.slot] ?? []}
                                        required={spec.slots[0]?.slot === slot.slot}
                                        uploading={uploadingTarget === slot.slot}
                                        percent={uploadPercent}
                                        onPick={() => openSlotPicker(slot.slot, slot.kind)}
                                        onLibrary={() => {
                                            libraryFileRef.current?.setAttribute("data-slot", slot.slot);
                                            libraryFileRef.current?.click();
                                        }}
                                        onRemove={(resourceId) => removeMaterial(slot.slot, resourceId)}
                                    />
                                ))}
                            </div>
                        </ComposerSection>

                        <ComposerSection
                            title="参考视频"
                            hint="限 1 个 · MP4 / MOV / WebM"
                            aside={
                                reference ? (
                                    <Button size="small" type="text" className="wb-text-action" onClick={() => setReference(null)}>
                                        移除
                                    </Button>
                                ) : null
                            }
                        >
                            <div className="wb-ref-row">
                                {reference ? (
                                    <div className="wb-ref">
                                        <span className="wb-ref-media">
                                            <span className="hypit-ref-glyph">
                                                <Clapperboard aria-hidden />
                                            </span>
                                        </span>
                                    </div>
                                ) : uploadingTarget === "reference" ? (
                                    <div className="wb-ref">
                                        <span className="wb-ref-media">
                                            <span className="wb-ref-progress">{uploadPercent}%</span>
                                        </span>
                                    </div>
                                ) : (
                                    <button type="button" className="wb-drop" onClick={() => refFileRef.current?.click()}>
                                        <span className="wb-drop-icon">
                                            <Upload aria-hidden />
                                        </span>
                                        <span className="wb-drop-copy">
                                            <strong>本地上传参考视频</strong>
                                            <small>建议 30 秒以内，镜头过多时先分镜</small>
                                        </span>
                                    </button>
                                )}
                                <button type="button" className="wb-ref-library" onClick={() => void pasteFromClipboard()}>
                                    <Clapperboard aria-hidden />
                                    <small>粘贴链接</small>
                                </button>
                            </div>
                            {reference ? (
                                <p className="wb-muted-note">
                                    {reference.name}
                                    {reference.source === "url" ? " · 来自链接" : reference.size ? ` · ${formatSize(reference.size)}` : " · 本地上传"}
                                </p>
                            ) : (
                                <div className="hypit-link-row">
                                    <Input size="small" allowClear value={urlDraft} placeholder="或粘贴视频链接，支持抖音 / 小红书 / B 站分享链接" onChange={(event) => setUrlDraft(event.target.value)} onPressEnter={applyUrlReference} />
                                    <Button size="small" disabled={!urlDraft.trim()} onClick={applyUrlReference}>
                                        解析视频
                                    </Button>
                                </div>
                            )}
                        </ComposerSection>

                        <ComposerSection title="声音" hint="怎么处理原片人声">
                            <ChoiceChips label="声音" value={voiceMode} options={VOICE_OPTIONS.map((item) => ({ value: item.value, label: item.label, caption: item.caption }))} onChange={(value) => setVoiceMode(value as HypitVoiceMode)} />
                        </ComposerSection>

                        <ComposerSection title="模型与规格" hint="模型来自画布渠道配置">
                            {models.length ? (
                                <ModelPicker
                                    config={config}
                                    value={selectedModel}
                                    capability="video"
                                    variant="creation"
                                    fullWidth
                                    className="creation-model-picker wb-model-picker"
                                    showSelectedPrice={false}
                                    showOptionPrices
                                    placeholder="选择视频模型"
                                    onChange={(value) => {
                                        setModel(value);
                                        updateConfig("videoModel", value);
                                        updateConfig("model", value);
                                    }}
                                />
                            ) : (
                                <div className="wb-inline-error">
                                    <span>暂无可用的视频模型</span>
                                    <a href="/settings?section=models" target="_blank" rel="noreferrer">
                                        去模型设置
                                    </a>
                                </div>
                            )}
                            <ChoiceChips label="画幅" value={aspectRatio} options={RATIO_OPTIONS} onChange={setAspectRatio} />
                            <ChoiceChips label="清晰度" value={resolution} options={RESOLUTION_OPTIONS} onChange={setResolution} />
                            <SwitchRow
                                items={spec.checks.map((item) => ({
                                    key: item.key,
                                    label: item.label,
                                    detail: item.detail,
                                    checked: item.key === "keepMusic" ? keepMusic : talkingHead,
                                    onChange: (checked: boolean) => {
                                        if (item.key === "keepMusic") setKeepMusic(checked);
                                        else setTalkingHead(checked);
                                    },
                                }))}
                            />
                        </ComposerSection>

                        <ComposerSection title="补充说明" hint="可选">
                            <div className="wb-prompt">
                                <textarea className="wb-prompt-input" value={instructions} maxLength={spec.instructionMax} onChange={(event) => setInstructions(event.target.value)} placeholder={spec.instructionPlaceholder} />
                                <div className="wb-prompt-meta">
                                    <span>
                                        {instructions.length} / {spec.instructionMax}
                                    </span>
                                    <span>描述越具体，替换越稳定</span>
                                </div>
                            </div>
                        </ComposerSection>
                    </div>

                    <footer className="wb-footer">
                        <div className="wb-estimate" aria-live="polite">
                            <strong>{spec.label}</strong>
                            <span>
                                <LayoutGrid />
                                {totalMaterials} 张素材 · {reference ? "参考视频已选" : "待选参考视频"}
                                {selectedModel ? ` · ${selectedModel}` : ""}
                            </span>
                        </div>
                        {disabledReason ? (
                            <Tooltip title={disabledReason}>
                                <span className="wb-submit-wrap">
                                    <Button className="wb-submit" type="primary" size="large" disabled>
                                        <ArrowUp />
                                        <span>开始生成</span>
                                    </Button>
                                </span>
                            </Tooltip>
                        ) : (
                            <Button className="wb-submit" type="primary" size="large" loading={busy} onClick={() => void submit()}>
                                <ArrowUp />
                                <span>开始生成</span>
                            </Button>
                        )}
                    </footer>

                    <input
                        ref={slotFileRef}
                        type="file"
                        hidden
                        accept={IMAGE_ACCEPT}
                        onChange={(event) => {
                            const slot = event.currentTarget.dataset.slot ?? spec.slots[0]?.slot ?? "person";
                            const files = Array.from(event.target.files ?? []);
                            event.target.value = "";
                            if (files.length) void uploadToSlot(slot, files);
                        }}
                    />
                    <input
                        ref={libraryFileRef}
                        type="file"
                        hidden
                        multiple
                        accept={IMAGE_ACCEPT}
                        onChange={(event) => {
                            const slot = event.currentTarget.dataset.slot ?? spec.slots[0]?.slot ?? "person";
                            const files = Array.from(event.target.files ?? []);
                            event.target.value = "";
                            if (files.length) void uploadToSlot(slot, files);
                        }}
                    />
                    <input
                        ref={refFileRef}
                        type="file"
                        hidden
                        accept={REFERENCE_ACCEPT}
                        onChange={(event) => {
                            const file = event.target.files?.[0];
                            event.target.value = "";
                            if (file) void pickReferenceFile(file);
                        }}
                    />
                </aside>

                <section className="wb-gallery" aria-label="复刻结果">
                    <header className="wb-gallery-head">
                        <div className="wb-gallery-title">
                            <h2>结果</h2>
                            <span>
                                {tasks.length} 个任务
                                {activeTask ? " · 1 个进行中" : ""}
                            </span>
                        </div>
                        <div className="wb-gallery-tools">
                            <Tooltip title="刷新">
                                <button type="button" className="wb-icon-button" data-icon-only aria-label="刷新结果" onClick={() => void refreshTasks()}>
                                    <RefreshCw className={loadingTasks ? "animate-spin" : undefined} />
                                </button>
                            </Tooltip>
                        </div>
                    </header>
                    <div className="wb-gallery-scroll">
                        {!nodeOnline ? (
                            <div className="wb-batch-notice">
                                <CircleAlert aria-hidden />
                                <span>执行节点尚未上线：任务会先排队保存，节点接入后自动开始复刻。</span>
                            </div>
                        ) : null}
                        {loadError ? (
                            <div className="wb-batch-notice">
                                <CircleAlert aria-hidden />
                                <span>{loadError}</span>
                            </div>
                        ) : null}

                        {tasks.length === 0 ? (
                            <div className="wb-empty">
                                <WorkspaceState compact icon="assets" title="还没有复刻结果" description="左侧选好替换类型，上传素材和参考视频，点「开始生成」后成片会按任务保存在这里。" />
                            </div>
                        ) : (
                            <div className="wb-batch-list">
                                {tasks.map((task) => (
                                    <article key={task.id} className="wb-batch">
                                        <header className="wb-batch-head">
                                            <div className="wb-batch-meta">
                                                {taskChips(task).map((chip) => (
                                                    <span key={chip}>{chip}</span>
                                                ))}
                                            </div>
                                            <div className="wb-batch-actions">
                                                {hypitTaskIsActive(task) ? (
                                                    <Button size="small" type="text" onClick={() => void cancel(task)}>
                                                        取消
                                                    </Button>
                                                ) : null}
                                                {task.status === "succeeded" && task.outputUrl ? (
                                                    <a href={task.outputUrl} download target="_blank" rel="noreferrer">
                                                        <Tooltip title="下载成片">
                                                            <button type="button" className="wb-icon-button" data-icon-only aria-label="下载成片">
                                                                <Download />
                                                            </button>
                                                        </Tooltip>
                                                    </a>
                                                ) : null}
                                            </div>
                                        </header>

                                        {task.instructions ? <p className="wb-batch-prompt">{task.instructions}</p> : null}

                                        {hypitTaskIsActive(task) ? (
                                            <div>
                                                <Progress percent={task.progress} size="small" showInfo={false} />
                                                <p className="wb-muted-note">
                                                    {HYPIT_STATUS_LABELS[task.status]} · {task.stage || "等待执行节点"} {task.progress}%
                                                </p>
                                            </div>
                                        ) : null}

                                        {task.status === "succeeded" && task.outputUrl ? (
                                            <div className="wb-tiles" style={{ ["--wb-tile-ratio" as string]: "9 / 16", ["--wb-tile-min" as string]: "180px" }}>
                                                <div className="wb-tile is-output">
                                                    <video className="hypit-tile-video" src={task.outputUrl} controls playsInline preload="metadata" />
                                                </div>
                                            </div>
                                        ) : null}

                                        {task.status === "failed" ? (
                                            <div className="wb-batch-notice">
                                                <CircleAlert aria-hidden />
                                                <span>{task.error || "复刻失败，请调整素材或描述后重试"}</span>
                                            </div>
                                        ) : null}

                                        {task.status === "succeeded" && !task.outputUrl ? (
                                            <div className="wb-batch-notice">
                                                <CircleAlert aria-hidden />
                                                <span>任务已完成，但产物链接缺失，请稍后刷新。</span>
                                            </div>
                                        ) : null}

                                        <div className="wb-batch-tags">
                                            <span className="wb-batch-status">{new Date(task.finishedAt || task.createdAt).toLocaleString()}</span>
                                        </div>
                                    </article>
                                ))}
                            </div>
                        )}
                    </div>
                </section>
            </div>
        </WorkspacePage>
    );
}
