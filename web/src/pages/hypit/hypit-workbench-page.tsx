import { App, Button } from "antd";
import { Clapperboard, Download, Link2, Loader2, RotateCcw, Sparkles, TriangleAlert, Upload, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { Select } from "@/components/ui/base/select";
import { ComposerSection, ChoiceChips } from "@/pages/workbench/workbench-composer";
import {
    HYPIT_ASPECT_RATIOS,
    HYPIT_DURATIONS,
    HYPIT_LANGUAGES,
    HYPIT_REPLICA_MODES,
    HYPIT_STATUS_LABELS,
    cancelHypitTask,
    createHypitTask,
    hypitTaskIsActive,
    listHypitTasks,
    type HypitReplicaMode,
    type HypitTask,
} from "@/services/api/hypit";
import { uploadResourceFile } from "@/services/api/resources";

import "./hypit-workbench.css";

const POLL_INTERVAL_MS = 4000;
const REFERENCE_KINDS = "video/mp4,video/quicktime,video/webm";
const REFERENCE_ACCEPT = ".mp4,.mov,.webm,video/*";

type Reference = { source: "resource" | "url"; id?: string; url?: string; name: string; size?: number };

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
    const parts = [HYPIT_REPLICA_MODES.find((item) => item.value === task.mode)?.label ?? task.mode];
    if (task.aspectRatio) parts.push(task.aspectRatio);
    if (task.language && task.language !== "source") parts.push(HYPIT_LANGUAGES.find((item) => item.value === task.language)?.label ?? task.language);
    if (task.instructions.trim()) parts.push(task.instructions.trim());
    else if (task.referenceName) parts.push(task.referenceName);
    return parts.join(" · ");
}

export default function HypitWorkbenchPage() {
    const { message } = App.useApp();
    const fileRef = useRef<HTMLInputElement | null>(null);

    const [mode, setMode] = useState<HypitReplicaMode>("full-clone");
    const [reference, setReference] = useState<Reference | null>(null);
    const [urlDraft, setUrlDraft] = useState("");
    const [instructions, setInstructions] = useState("");
    const [aspectRatio, setAspectRatio] = useState("9:16");
    const [duration, setDuration] = useState("0");
    const [language, setLanguage] = useState("source");

    const [uploading, setUploading] = useState(false);
    const [uploadPercent, setUploadPercent] = useState(0);
    const [dragging, setDragging] = useState(false);
    const [submitting, setSubmitting] = useState(false);

    const [tasks, setTasks] = useState<HypitTask[]>([]);
    const [loadingTasks, setLoadingTasks] = useState(true);
    const [nodeOnline, setNodeOnline] = useState(true);
    const [loadError, setLoadError] = useState<string | null>(null);

    const activeTask = useMemo(() => tasks.find(hypitTaskIsActive) ?? null, [tasks]);

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

    const pickReferenceFile = useCallback(
        async (file: File) => {
            if (!file.type.startsWith("video/")) {
                message.error("参考视频需要是 mp4 / mov / webm");
                return;
            }
            setUploading(true);
            setUploadPercent(0);
            try {
                const resource = await uploadResourceFile(file, "video", { fileName: file.name }, (uploaded, total) => {
                    setUploadPercent(total > 0 ? Math.round((uploaded / total) * 100) : 0);
                });
                setReference({ source: "resource", id: resource.id, name: file.name, size: file.size });
                setUrlDraft("");
            } catch (error) {
                message.error(error instanceof Error ? error.message : "参考视频上传失败");
            } finally {
                setUploading(false);
                setUploadPercent(0);
            }
        },
        [message],
    );

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
        setMode("full-clone");
        setReference(null);
        setUrlDraft("");
        setInstructions("");
        setAspectRatio("9:16");
        setDuration("0");
        setLanguage("source");
    }, []);

    const submit = useCallback(async () => {
        if (!reference) {
            message.warning("请先上传参考视频或填入视频链接");
            return;
        }
        setSubmitting(true);
        try {
            const task = await createHypitTask({
                mode,
                referenceResourceId: reference.source === "resource" ? reference.id : undefined,
                referenceUrl: reference.source === "url" ? reference.url : undefined,
                referenceName: reference.name,
                instructions: instructions.trim(),
                aspectRatio,
                durationSec: Number(duration) || undefined,
                language,
            });
            setTasks((current) => [task, ...current]);
            message.success("已提交复刻任务");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "提交失败");
        } finally {
            setSubmitting(false);
        }
    }, [aspectRatio, duration, instructions, language, message, mode, reference]);

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

    const busy = uploading || submitting;

    return (
        <WorkspacePage fluid scroll={false} className="wb-page">
            <div className="wb-shell">
                <aside className="wb-composer" aria-label="Hypit 视频复刻面板">
                    <header className="wb-composer-head">
                        <div>
                            <strong className="wb-composer-title">视频复刻</strong>
                            <p className="wb-muted-note" style={{ marginTop: 2 }}>
                                参考片进，成片出：结构、字幕与节奏整体复用
                            </p>
                        </div>
                        <button type="button" className="wb-icon-button" data-icon-only aria-label="重置复刻面板" onClick={resetComposer}>
                            <RotateCcw />
                        </button>
                    </header>

                    <div className="wb-composer-scroll">
                        <ComposerSection title="复刻类型" hint="选一种要替换的内容">
                            <ChoiceChips label="复刻类型" value={mode} options={HYPIT_REPLICA_MODES.map((item) => ({ value: item.value, label: item.label, caption: item.caption, title: item.hint }))} onChange={(value) => setMode(value as HypitReplicaMode)} />
                            <p className="wb-muted-note">{HYPIT_REPLICA_MODES.find((item) => item.value === mode)?.hint}</p>
                        </ComposerSection>

                        <ComposerSection title="参考视频" hint="限 1 个 · MP4 / MOV / WebM">
                            {reference ? (
                                <div className="hypit-ref">
                                    <span className="hypit-ref-thumb">
                                        <Clapperboard aria-hidden />
                                    </span>
                                    <span className="hypit-ref-meta">
                                        <strong title={reference.name}>{reference.name}</strong>
                                        <span>
                                            {reference.source === "url" ? "视频链接" : "本地上传"}
                                            {reference.size ? ` · ${formatSize(reference.size)}` : ""}
                                        </span>
                                    </span>
                                    <button type="button" className="wb-icon-button" data-icon-only aria-label="移除参考视频" onClick={() => setReference(null)}>
                                        <X />
                                    </button>
                                </div>
                            ) : (
                                <>
                                    <div
                                        className={`hypit-drop${dragging ? " is-dragging" : ""}`}
                                        role="button"
                                        tabIndex={0}
                                        onClick={() => fileRef.current?.click()}
                                        onKeyDown={(event) => {
                                            if (event.key === "Enter" || event.key === " ") fileRef.current?.click();
                                        }}
                                        onDragOver={(event) => {
                                            event.preventDefault();
                                            setDragging(true);
                                        }}
                                        onDragLeave={() => setDragging(false)}
                                        onDrop={(event) => {
                                            event.preventDefault();
                                            setDragging(false);
                                            const file = event.dataTransfer.files?.[0];
                                            if (file) void pickReferenceFile(file);
                                        }}
                                    >
                                        {uploading ? (
                                            <>
                                                <Loader2 className="animate-spin" aria-hidden />
                                                <span>上传中 {uploadPercent}%</span>
                                            </>
                                        ) : (
                                            <>
                                                <Upload aria-hidden />
                                                <span>拖入或点击上传参考视频</span>
                                                <small>建议 30 秒以内，镜头过多时先分镜再单独复刻</small>
                                            </>
                                        )}
                                    </div>
                                    <div className="hypit-url-row">
                                        <input value={urlDraft} onChange={(event) => setUrlDraft(event.target.value)} placeholder="或粘贴视频链接，一键导入" aria-label="参考视频链接" onKeyDown={(event) => event.key === "Enter" && applyUrlReference()} />
                                        <Button size="small" onClick={applyUrlReference} disabled={!urlDraft.trim() || uploading}>
                                            <Link2 aria-hidden style={{ width: 14, height: 14 }} />
                                            解析
                                        </Button>
                                    </div>
                                </>
                            )}
                            <input
                                ref={fileRef}
                                type="file"
                                accept={REFERENCE_ACCEPT}
                                hidden
                                onChange={(event) => {
                                    const file = event.target.files?.[0];
                                    event.target.value = "";
                                    if (file) void pickReferenceFile(file);
                                }}
                            />
                        </ComposerSection>

                        <ComposerSection title="输出设置">
                            <label className="wb-field">
                                <span className="wb-field-label">画面比例</span>
                                <Select ariaLabel="画面比例" value={aspectRatio} onChange={setAspectRatio} options={HYPIT_ASPECT_RATIOS.map((item) => ({ value: item.value, label: item.label }))} />
                            </label>
                            <label className="wb-field">
                                <span className="wb-field-label">时长</span>
                                <Select ariaLabel="时长" value={duration} onChange={setDuration} options={HYPIT_DURATIONS.map((item) => ({ value: item.value, label: item.label }))} />
                            </label>
                            <label className="wb-field">
                                <span className="wb-field-label">语言</span>
                                <Select ariaLabel="语言" value={language} onChange={setLanguage} options={HYPIT_LANGUAGES.map((item) => ({ value: item.value, label: item.label }))} />
                            </label>
                        </ComposerSection>

                        <ComposerSection title="补充说明" hint="可选">
                            <textarea
                                className="wb-prompt-input"
                                style={{ color: "var(--wb-ink)", minHeight: 96, resize: "vertical" }}
                                value={instructions}
                                maxLength={600}
                                onChange={(event) => setInstructions(event.target.value)}
                                placeholder="例如：把出镜人物换成橘猫，文案改成介绍我们的咖啡机，结尾加一句「点击左下角下单」"
                            />
                            <p className="wb-muted-note">{instructions.length}/600</p>
                        </ComposerSection>
                    </div>

                    <div className="hypit-submit">
                        <Button type="primary" size="large" block loading={submitting} disabled={busy && !submitting} onClick={() => void submit()}>
                            <Sparkles aria-hidden style={{ width: 16, height: 16 }} />
                            开始复刻
                        </Button>
                        <span className="hypit-submit-hint">提交后成片出现在右侧，可下载或在画布继续编辑。</span>
                    </div>
                </aside>

                <section className="hypit-stage" aria-label="复刻结果">
                    <header className="hypit-stage-head">
                        <div className="hypit-stage-title">
                            视频结果
                            <small>{tasks.length ? `${tasks.length} 个任务` : ""}</small>
                        </div>
                        <Button size="small" onClick={() => void refreshTasks()} loading={loadingTasks}>
                            刷新
                        </Button>
                    </header>
                    <div className="hypit-stage-body">
                        {!nodeOnline ? (
                            <div className="hypit-node">
                                <TriangleAlert aria-hidden />
                                <span>Hypit 执行节点尚未上线：任务会先排队保存，节点接入后自动开始复刻。可先在左侧正常提交任务。</span>
                            </div>
                        ) : null}

                        {loadError ? <div className="hypit-task-error">{loadError}</div> : null}

                        {tasks.length === 0 ? (
                            <div className="hypit-empty">
                                <span className="hypit-empty-icon">
                                    <Clapperboard aria-hidden />
                                </span>
                                <h3>还没有复刻结果</h3>
                                <p>左侧提交参考视频和替换要求，成片会显示在这里。</p>
                                <ol className="hypit-steps">
                                    <li>
                                        <b>1</b>
                                        <span>上传一支想复刻的爆款视频，或粘贴链接导入。</span>
                                    </li>
                                    <li>
                                        <b>2</b>
                                        <span>选复刻类型：换主角、换文案、换 B-roll，或整片复刻。</span>
                                    </li>
                                    <li>
                                        <b>3</b>
                                        <span>补充一句具体要求，点击「开始复刻」等待成片。</span>
                                    </li>
                                </ol>
                            </div>
                        ) : (
                            <div className="hypit-tasks">
                                {tasks.map((task) => (
                                    <article key={task.id} className="hypit-task">
                                        <div className="hypit-task-top">
                                            <span className="hypit-task-mode">
                                                <Clapperboard aria-hidden style={{ width: 15, height: 15 }} />
                                                <span title={taskBrief(task)}>{taskBrief(task)}</span>
                                            </span>
                                            <span className={`hypit-status is-${task.status}`}>{HYPIT_STATUS_LABELS[task.status]}</span>
                                        </div>

                                        {hypitTaskIsActive(task) ? (
                                            <>
                                                <div className="hypit-progress" role="progressbar" aria-valuenow={task.progress} aria-valuemin={0} aria-valuemax={100}>
                                                    <i style={{ width: `${Math.max(4, Math.min(100, task.progress))}%` }} />
                                                </div>
                                                <div className="hypit-task-foot">
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
                                                <div className="hypit-task-foot">
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

                                        {task.status === "failed" ? <div className="hypit-task-error">{task.error || "复刻失败，请调整参考视频或描述后重试"}</div> : null}

                                        {task.status === "succeeded" && !task.outputUrl ? <div className="hypit-task-error">任务已完成，但产物链接缺失，请稍后刷新。</div> : null}
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
