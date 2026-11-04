import { App, Button } from "antd";
import {
    ArrowLeftRight,
    Check,
    ChevronDown,
    Clapperboard,
    Download,
    Info,
    LibraryBig,
    Link2,
    Loader2,
    Plus,
    Sparkles,
    TriangleAlert,
    Upload,
    Wand2,
    X,
    XCircle,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { Select } from "@/components/ui/base/select";
import {
    HYPIT_MODE_LABELS,
    HYPIT_STATUS_LABELS,
    HYPIT_VOICE_LABELS,
    cancelHypitTask,
    createHypitTask,
    hypitTaskIsActive,
    listHypitTasks,
    type HypitMaterialSlot,
    type HypitReplicaMode,
    type HypitTask,
    type HypitVoiceMode,
} from "@/services/api/hypit";
import { uploadResourceFile } from "@/services/api/resources";

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
    checks: Array<{ key: "keepMusic" | "talkingHead"; label: string }>;
};

const CHECK_KEEP_MUSIC = { key: "keepMusic" as const, label: "保留参考视频的背景音乐" };
const CHECK_TALKING = { key: "talkingHead" as const, label: "生成人物口播（重新配音，时长可能变长）" };

/** 8 种替换类型：槽位、提示、字数上限都跟着类型走，和后端 hypitReplicaModes 一一对应。 */
const REPLICA_TYPES: ReplicaSpec[] = [
    {
        id: "model-swap",
        label: "模特替换",
        hint: "保留原片的动作、镜头与节奏，只把出镜模特换成你上传的人物",
        slots: [
            { slot: "person", label: "人物全身图", hint: "正面全身", kind: "image", max: 3 },
            { slot: "outfit", label: "服装参考", hint: "可选", kind: "image", max: 1 },
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
            { slot: "person", label: "人物图", hint: "可选", kind: "image", max: 1 },
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
        slots: [{ slot: "face", label: "人脸图", hint: "正面清晰", kind: "image", max: 3 }],
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
        slots: [{ slot: "background", label: "背景图", hint: "横图更佳", kind: "image", max: 3 }],
        tip: {
            good: ["场景完整、无人物，光线方向明确", "与人物景别匹配（室内 / 街景 / 纯色）"],
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
            { slot: "product", label: "商品图", hint: "多角度更佳", kind: "image", max: 3 },
            { slot: "scene", label: "场景参考", hint: "可选", kind: "image", max: 1 },
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
            { slot: "product", label: "商品图", hint: "正面 + 细节", kind: "image", max: 3 },
            { slot: "label", label: "包装文字", hint: "可选", kind: "image", max: 1 },
        ],
        tip: {
            good: ["同一商品的正反面各一张", "包装上的文字单独拍一张特写"],
            bad: ["压缩过的电商截图，文字已糊"],
        },
        instructionPlaceholder: "例如：包装文字原样保留，不要改写 logo",
        instructionMax: 10,
        checks: [CHECK_KEEP_MUSIC],
    },
    {
        id: "person-precise",
        label: "精准人物",
        hint: "强约束五官与身材比例，适合固定出镜人设",
        slots: [
            { slot: "person", label: "人物全身图", hint: "正面全身", kind: "image", max: 3 },
            { slot: "face", label: "人脸特写", hint: "可选", kind: "image", max: 1 },
        ],
        tip: {
            good: ["同一人的全身图 + 一张正脸特写", "服装简单，避免大面积花纹"],
            bad: ["不同人的照片混着传", "重度美颜或换脸滤镜"],
        },
        instructionPlaceholder: "例如：保持五官一致，不要改变脸型",
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

const VOICE_OPTIONS: Array<{ value: HypitVoiceMode; label: string; hint: string }> = [
    { value: "replace", label: HYPIT_VOICE_LABELS.replace, hint: "上传音频后替换原片人声" },
    { value: "original", label: HYPIT_VOICE_LABELS.original, hint: "保留原片人声与语气" },
    { value: "mute", label: HYPIT_VOICE_LABELS.mute, hint: "去掉人声，只留环境音" },
];

const MODEL_OPTIONS = [
    { value: "veo-3.1-fast", label: "Veo 3.1 Fast" },
    { value: "veo-3.1-pro", label: "Veo 3.1 Pro" },
    { value: "kling-2.5-turbo", label: "Kling 2.5 Turbo" },
    { value: "sora-2", label: "Sora 2" },
];

/** 画幅与清晰度合成一个下拉，值形如 `9:16|720p`。 */
const RATIO_RESOLUTION_OPTIONS = [
    { value: "9:16|480p", label: "竖屏 9:16 · 480P" },
    { value: "9:16|720p", label: "竖屏 9:16 · 720P" },
    { value: "9:16|1080p", label: "竖屏 9:16 · 1080P" },
    { value: "16:9|720p", label: "横屏 16:9 · 720P" },
    { value: "1:1|720p", label: "方形 1:1 · 720P" },
    { value: "3:4|720p", label: "竖版 3:4 · 720P" },
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

function taskBrief(task: HypitTask): string {
    const parts = [HYPIT_MODE_LABELS[task.mode] ?? task.mode];
    if (task.aspectRatio) parts.push(`${task.aspectRatio}${task.resolution ? ` · ${task.resolution}` : ""}`);
    if (task.voiceMode) parts.push(HYPIT_VOICE_LABELS[task.voiceMode] ?? task.voiceMode);
    if (task.instructions.trim()) parts.push(task.instructions.trim());
    else if (task.referenceName) parts.push(task.referenceName);
    return parts.join(" · ");
}

function SectionTitle({ title, hint, extra }: { title: string; hint?: string; extra?: ReactNode }) {
    return (
        <div className="hp-block-head">
            <div className="hp-block-title">
                <h3>{title}</h3>
                {hint ? <span title={hint}>{hint}</span> : null}
            </div>
            {extra}
        </div>
    );
}

export default function HypitWorkbenchPage() {
    const { message } = App.useApp();
    const refFileRef = useRef<HTMLInputElement | null>(null);
    const slotFileRef = useRef<HTMLInputElement | null>(null);
    const libraryFileRef = useRef<HTMLInputElement | null>(null);
    /** 记录待释放的本地预览地址，避免 objectURL 泄漏。 */
    const previewsRef = useRef<string[]>([]);

    const [typeID, setTypeID] = useState<HypitReplicaMode>("model-swap");
    const [pickerOpen, setPickerOpen] = useState(true);
    const [tipOpen, setTipOpen] = useState(true);
    const [materials, setMaterials] = useState<Record<string, MaterialItem[]>>({});

    const [reference, setReference] = useState<Reference | null>(null);
    const [urlDraft, setUrlDraft] = useState("");
    const [voiceMode, setVoiceMode] = useState<HypitVoiceMode>("original");
    const [keepMusic, setKeepMusic] = useState(true);
    const [talkingHead, setTalkingHead] = useState(false);
    const [model, setModel] = useState(MODEL_OPTIONS[0].value);
    const [ratioResolution, setRatioResolution] = useState(RATIO_RESOLUTION_OPTIONS[1].value);
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
    const voiceSlots: SlotSpec[] = useMemo(
        () => (voiceMode === "replace" ? [{ slot: "voice", label: "音频文件", hint: "mp3 / wav", kind: "audio", max: 1 }] : []),
        [voiceMode],
    );
    const activeSlots = useMemo(() => [...spec.slots, ...voiceSlots], [spec, voiceSlots]);

    const slotLimit = useCallback(
        (slot: string): number => activeSlots.find((item) => item.slot === slot)?.max ?? MAX_PER_SLOT,
        [activeSlots],
    );

    const appendMaterials = useCallback((slot: string, items: MaterialItem[]) => {
        setMaterials((current) => {
            const existing = current[slot] ?? [];
            const limit = Math.min(slotLimit(slot), MAX_TOTAL_MATERIALS - (totalMaterials - existing.length));
            const next = [...existing, ...items].slice(0, Math.max(limit, existing.length));
            return { ...current, [slot]: next };
        });
    }, [slotLimit, totalMaterials]);

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

    const removeSlot = useCallback((slot: string) => {        setMaterials((current) => {
            (current[slot] ?? []).forEach((item) => {
                if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
            });
            return { ...current, [slot]: [] };
        });
    }, []);

    /** 素材库按钮：一次选多张图，按当前类型的槽位顺序自动分配。 */
    const importFromLibrary = useCallback(
        async (files: File[]) => {
            if (files.length === 0) return;
            const firstSlot = spec.slots[0]?.slot;
            if (!firstSlot) return;
            await uploadToSlot(firstSlot, files);
        },
        [spec, uploadToSlot],
    );

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

    const submit = useCallback(async () => {
        if (!reference) {
            message.warning("请先上传参考视频，或粘贴视频链接后点「解析视频」");
            return;
        }
        const primary = spec.slots[0];
        if (primary && (materials[primary.slot]?.length ?? 0) === 0) {
            message.warning(`请先上传${primary.label}`);
            return;
        }
        if (voiceMode === "replace" && (materials.voice?.length ?? 0) === 0) {
            message.warning("选择「替换音频」后需要上传一段音频");
            return;
        }
        const [aspectRatio, resolution] = ratioResolution.split("|");
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
                model,
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
    }, [instructions, keepMusic, materials, message, model, ratioResolution, reference, spec, talkingHead, typeID, voiceMode]);

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

    const busy = uploadingTarget !== null || submitting;
    const renderSlots = activeSlots;

    return (
        <WorkspacePage fluid scroll={false} className="wb-page">
            <div className="hypit-shell">
                <aside className="hp-form" aria-label="视频复刻面板">
                    <div className="hp-form-scroll">
                        <div className="hp-func">
                            <span className="hp-func-icon">
                                <Wand2 aria-hidden />
                            </span>
                            <div className="hp-func-body">
                                <strong>{spec.label}</strong>
                                <span>{spec.hint}</span>
                            </div>
                            <button type="button" className="hp-func-switch" onClick={() => setPickerOpen((open) => !open)}>
                                <ArrowLeftRight aria-hidden />
                                {pickerOpen ? "收起" : "切换"}
                            </button>
                        </div>

                        {pickerOpen ? (
                            <div className="hp-block">
                                <SectionTitle title="替换类型" hint={`${REPLICA_TYPES.length} 种`} />
                                <div className="hp-chips">
                                    {REPLICA_TYPES.map((item) => (
                                        <button
                                            key={item.id}
                                            type="button"
                                            className="hp-chip"
                                            aria-pressed={item.id === typeID}
                                            title={item.hint}
                                            onClick={() => setTypeID(item.id)}
                                        >
                                            {item.label}
                                        </button>
                                    ))}
                                </div>
                            </div>
                        ) : null}

                        <div className="hp-block">
                            <SectionTitle title="素材" hint={`共 ${totalMaterials}/${MAX_TOTAL_MATERIALS} 张`} />
                            <div className="hp-slots">
                                {renderSlots.map((slot) => {
                                    const items = materials[slot.slot] ?? [];
                                    const uploading = uploadingTarget === slot.slot;
                                    return (
                                        <div
                                            key={slot.slot}
                                            className={`hp-slot${items.length ? " is-filled" : ""}`}
                                            role="button"
                                            tabIndex={0}
                                            aria-label={`上传${slot.label}`}
                                            onClick={() => {
                                                if (uploading) return;
                                                openSlotPicker(slot.slot, slot.kind);
                                            }}
                                            onKeyDown={(event) => {
                                                if (event.key === "Enter" || event.key === " ") {
                                                    openSlotPicker(slot.slot, slot.kind);
                                                }
                                            }}
                                        >
                                            {uploading ? (
                                                <>
                                                    <Loader2 className="animate-spin" aria-hidden />
                                                    <span className="hp-slot-label">{uploadPercent}%</span>
                                                    <span className="hp-slot-note">上传中</span>
                                                </>
                                            ) : items.length ? (
                                                <>
                                                    {items[0].previewUrl ? <img src={items[0].previewUrl} alt={items[0].name} /> : <Clapperboard aria-hidden />}
                                                    <span className="hp-slot-badge">{slot.label}</span>
                                                    {slot.max > 1 ? (
                                                        <span className="hp-slot-count">
                                                            {items.length}/{slot.max}
                                                        </span>
                                                    ) : null}
                                                    <button
                                                        type="button"
                                                        className="hp-slot-remove"
                                                        aria-label={`清空${slot.label}`}
                                                        onClick={(event) => {
                                                            event.stopPropagation();
                                                            removeSlot(slot.slot);
                                                        }}
                                                    >
                                                        <X aria-hidden />
                                                    </button>
                                                </>
                                            ) : (
                                                <>
                                                    <span className="hp-slot-plus">
                                                        <Plus aria-hidden />
                                                    </span>
                                                    <span className="hp-slot-label">{slot.label}</span>
                                                    <span className="hp-slot-note">{slot.hint}</span>
                                                </>
                                            )}
                                        </div>
                                    );
                                })}
                            </div>
                            <div className="hp-material-foot">
                                <button
                                    type="button"
                                    className="hp-library-button"
                                    onClick={() => libraryFileRef.current?.click()}
                                >
                                    <LibraryBig aria-hidden />
                                    素材库
                                </button>
                                <span className="hp-slot-note">单个槽位最多 3 张，素材库支持多选批量导入</span>
                            </div>
                        </div>

                        <div className="hp-block">
                            <div className={`hp-tip${tipOpen ? " is-open" : ""}`}>
                                <button type="button" className="hp-tip-head" onClick={() => setTipOpen((open) => !open)}>
                                    <Info aria-hidden />
                                    素材怎么选，效果才稳
                                    <ChevronDown className="hp-tip-chevron" aria-hidden />
                                </button>
                                {tipOpen ? (
                                    <div className="hp-tip-body">
                                        {spec.tip.good.map((text) => (
                                            <div key={text} className="hp-tip-row is-good">
                                                <Check aria-hidden />
                                                <b>推荐</b>
                                                <span>{text}</span>
                                            </div>
                                        ))}
                                        {spec.tip.bad.map((text) => (
                                            <div key={text} className="hp-tip-row is-bad">
                                                <XCircle aria-hidden />
                                                <b>避免</b>
                                                <span>{text}</span>
                                            </div>
                                        ))}
                                    </div>
                                ) : null}
                            </div>
                        </div>

                        <div className="hp-block">
                            <SectionTitle title="参考视频" hint="限 1 个 · MP4 / MOV" />
                            <div className="hp-ref">
                                {reference ? (
                                    <div className="hp-ref-selected">
                                        <span className="hp-slot-badge" style={{ position: "static" }}>
                                            {reference.source === "url" ? "链接" : "本地"}
                                        </span>
                                        <span className="hp-ref-selected-meta">
                                            <strong title={reference.name}>{reference.name}</strong>
                                            <span>{reference.source === "url" ? "由链接解析" : `本地上传${reference.size ? ` · ${formatSize(reference.size)}` : ""}`}</span>
                                        </span>
                                        <button type="button" className="hp-mini-button" onClick={() => setReference(null)}>
                                            <X aria-hidden />
                                            移除
                                        </button>
                                    </div>
                                ) : (
                                    <>
                                        <div
                                            className="hp-ref-upload"
                                            role="button"
                                            tabIndex={0}
                                            onClick={() => refFileRef.current?.click()}
                                            onKeyDown={(event) => {
                                                if (event.key === "Enter" || event.key === " ") refFileRef.current?.click();
                                            }}
                                        >
                                            {uploadingTarget === "reference" ? (
                                                <>
                                                    <Loader2 className="animate-spin" aria-hidden />
                                                    <b>{uploadPercent}%</b>
                                                    <small>上传中</small>
                                                </>
                                            ) : (
                                                <>
                                                    <Upload aria-hidden />
                                                    <b>本地上传</b>
                                                    <small>MP4 / MOV</small>
                                                </>
                                            )}
                                        </div>
                                        <span className="hp-ref-sep">或</span>
                                        <div className="hp-ref-link">
                                            <textarea
                                                value={urlDraft}
                                                onChange={(event) => setUrlDraft(event.target.value)}
                                                placeholder="粘贴视频链接，支持抖音 / 小红书 / B 站分享链接"
                                                aria-label="参考视频链接"
                                            />
                                            <div className="hp-ref-actions">
                                                <button type="button" onClick={() => void pasteFromClipboard()}>
                                                    一键粘贴
                                                </button>
                                                <button type="button" className="is-parse" disabled={!urlDraft.trim()} onClick={applyUrlReference}>
                                                    <Link2 aria-hidden style={{ width: 12, height: 12, verticalAlign: "-1px" }} /> 解析视频
                                                </button>
                                            </div>
                                        </div>
                                    </>
                                )}
                            </div>
                        </div>

                        <div className="hp-block">
                            <SectionTitle title="声音" hint="怎么处理原片人声" />
                            <div className="hp-voice">
                                {VOICE_OPTIONS.map((item) => (
                                    <button
                                        key={item.value}
                                        type="button"
                                        className="hp-voice-card"
                                        aria-pressed={voiceMode === item.value}
                                        onClick={() => setVoiceMode(item.value)}
                                    >
                                        <b>{item.label}</b>
                                        <span>{item.hint}</span>
                                    </button>
                                ))}
                            </div>
                            {voiceMode === "replace" ? (
                                <p className="hp-block-hint">选好「替换音频」后，在上方「音频文件」槽位上传要用的配音。</p>
                            ) : null}
                        </div>

                        <div className="hp-block">
                            <SectionTitle title="输出设置" />
                            <div className="hp-selects">
                                <label className="hp-field">
                                    <span className="hp-field-label">视频模型</span>
                                    <Select ariaLabel="视频模型" value={model} onChange={setModel} options={MODEL_OPTIONS} />
                                </label>
                                <label className="hp-field">
                                    <span className="hp-field-label">画幅 · 清晰度</span>
                                    <Select ariaLabel="画幅与清晰度" value={ratioResolution} onChange={setRatioResolution} options={RATIO_RESOLUTION_OPTIONS} />
                                </label>
                            </div>
                            {spec.checks.map((item) => (
                                <label key={item.key} className="hp-check">
                                    <input
                                        type="checkbox"
                                        checked={item.key === "keepMusic" ? keepMusic : talkingHead}
                                        onChange={(event) => {
                                            if (item.key === "keepMusic") setKeepMusic(event.target.checked);
                                            else setTalkingHead(event.target.checked);
                                        }}
                                    />
                                    <span>{item.label}</span>
                                </label>
                            ))}
                        </div>

                        <div className="hp-block">
                            <SectionTitle title="补充说明" hint="可选" />
                            <div className="hp-inst">
                                <textarea
                                    value={instructions}
                                    maxLength={spec.instructionMax}
                                    onChange={(event) => setInstructions(event.target.value)}
                                    placeholder={spec.instructionPlaceholder}
                                />
                                <span className="hp-inst-count">
                                    {instructions.length}/{spec.instructionMax}
                                </span>
                            </div>
                        </div>
                    </div>

                    <div className="hp-submit">
                        <button type="button" disabled={busy} onClick={() => void submit()}>
                            {submitting ? <Loader2 className="animate-spin" aria-hidden style={{ width: 15, height: 15, verticalAlign: "-2px" }} /> : <Sparkles aria-hidden style={{ width: 15, height: 15, verticalAlign: "-2px" }} />} 开始生成
                        </button>
                    </div>

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
                            const files = Array.from(event.target.files ?? []);
                            event.target.value = "";
                            void importFromLibrary(files);
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

                <section className="hp-results" aria-label="复刻结果">
                    <header className="hp-results-head">
                        <div className="hp-results-title">
                            视频结果
                            <small>{tasks.length ? `${tasks.length} 个任务` : ""}</small>
                        </div>
                        <Button size="small" onClick={() => void refreshTasks()} loading={loadingTasks}>
                            刷新
                        </Button>
                    </header>
                    <div className="hp-results-body">
                        {!nodeOnline ? (
                            <div className="hp-node">
                                <TriangleAlert aria-hidden />
                                <span>执行节点尚未上线：任务会先排队保存，节点接入后自动开始复刻。现在可以正常提交任务。</span>
                            </div>
                        ) : null}

                        {loadError ? <div className="hp-task-error">{loadError}</div> : null}

                        {tasks.length === 0 ? (
                            <div className="hp-empty">
                                <span className="hp-empty-icon">
                                    <Clapperboard aria-hidden />
                                </span>
                                <h3>还没有复刻结果</h3>
                                <p>左侧选一种替换类型，上传素材和参考视频，点「开始生成」后成片会出现在这里。</p>
                            </div>
                        ) : (
                            <div className="hp-tasks">
                                {tasks.map((task) => (
                                    <article key={task.id} className="hp-task">
                                        <div className="hp-task-top">
                                            <span className="hp-task-title">
                                                <Clapperboard aria-hidden style={{ width: 14, height: 14 }} />
                                                <span title={taskBrief(task)}>{taskBrief(task)}</span>
                                            </span>
                                            <span className={`hp-status is-${task.status}`}>{HYPIT_STATUS_LABELS[task.status]}</span>
                                        </div>

                                        {hypitTaskIsActive(task) ? (
                                            <>
                                                <div className="hp-progress" role="progressbar" aria-valuenow={task.progress} aria-valuemin={0} aria-valuemax={100}>
                                                    <i style={{ width: `${Math.max(4, Math.min(100, task.progress))}%` }} />
                                                </div>
                                                <div className="hp-task-foot">
                                                    <span>{task.stage || "等待执行节点"}</span>
                                                    <Button size="small" type="text" onClick={() => void cancel(task)}>
                                                        取消
                                                    </Button>
                                                </div>
                                            </>
                                        ) : null}

                                        {task.status === "succeeded" && task.outputUrl ? (
                                            <>
                                                <video src={task.outputUrl} controls playsInline preload="metadata" />
                                                <div className="hp-task-foot">
                                                    <span>{new Date(task.finishedAt || task.createdAt).toLocaleString()}</span>
                                                    <a href={task.outputUrl} download target="_blank" rel="noreferrer">
                                                        <Button size="small">
                                                            <Download aria-hidden style={{ width: 14, height: 14 }} />
                                                            下载
                                                        </Button>
                                                    </a>
                                                </div>
                                            </>
                                        ) : null}

                                        {task.status === "failed" ? <div className="hp-task-error">{task.error || "复刻失败，请调整素材或描述后重试"}</div> : null}

                                        {task.status === "succeeded" && !task.outputUrl ? <div className="hp-task-error">任务已完成，但产物链接缺失，请稍后刷新。</div> : null}
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
