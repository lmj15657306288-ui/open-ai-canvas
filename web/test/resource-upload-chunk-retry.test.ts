import { describe, expect, test } from "bun:test";
import axios, { type AxiosProgressEvent, type AxiosRequestConfig } from "axios";

import { apiClient } from "@/services/api/request";
import { ResourceUploadError, uploadResourceFile } from "@/services/api/resources";

const FILE_SIZE = 51 * 1024 * 1024;
const CHUNK_SIZE = 32 * 1024 * 1024;
const CHUNK_COUNT = 2;

type Failure = number | "abort" | undefined;
type PutCall = { sessionId: string; index: number };

type Harness = {
    stats: { sessions: string[]; puts: PutCall[]; completes: number };
    adapter: (config: AxiosRequestConfig) => Promise<unknown>;
};

function ok(config: AxiosRequestConfig, data: unknown) {
    return { config, status: 200, statusText: "OK", headers: {}, data: { code: 0, data } };
}

function httpFailure(status: number, config: AxiosRequestConfig) {
    return new axios.AxiosError(`Request failed with status code ${status}`, "ERR_BAD_REQUEST", config, undefined, {
        data: { code: status, data: null, msg: "失败" },
        status,
        statusText: "Error",
        headers: {},
        config,
    });
}

function createHarness(failChunk: (sessionId: string, index: number, attempt: number) => Failure, reportProgress = false): Harness {
    const stats = { sessions: [] as string[], puts: [] as PutCall[], completes: 0 };
    const attempts = new Map<string, number>();
    let sessionCounter = 0;

    const adapter = async (config: AxiosRequestConfig) => {
        const url = config.url ?? "";
        const method = (config.method ?? "get").toLowerCase();

        if (method === "post" && url === "/resources/uploads") {
            sessionCounter += 1;
            const uploadId = `session-${sessionCounter}`;
            stats.sessions.push(uploadId);
            return ok(config, { uploadId, chunkSize: CHUNK_SIZE, chunkCount: CHUNK_COUNT });
        }
        if (method === "post" && url.endsWith("/complete")) {
            stats.completes += 1;
            return ok(config, { resource: { id: "res-ok" } });
        }
        if (method === "put" && /\/chunks\/\d+$/.test(url)) {
            const parts = url.split("/");
            const index = Number(parts.at(-1));
            const sessionId = parts.at(-3) ?? "";
            const key = `${sessionId}:${index}`;
            const attempt = (attempts.get(key) ?? 0) + 1;
            attempts.set(key, attempt);
            stats.puts.push({ sessionId, index });

            const failure = failChunk(sessionId, index, attempt);
            if (failure === "abort") throw new DOMException("canceled", "AbortError");
            if (typeof failure === "number") throw httpFailure(failure, config);
            if (reportProgress && attempt === 1) {
                config.onUploadProgress?.({ loaded: CHUNK_SIZE / 2, total: CHUNK_SIZE } as AxiosProgressEvent);
            }
            return ok(config, { index });
        }
        throw new Error(`未预期的请求: ${method} ${url}`);
    };

    return { stats, adapter };
}

async function withHarness<T>(harness: Harness, run: () => Promise<T>): Promise<T> {
    const previous = apiClient.defaults.adapter;
    apiClient.defaults.adapter = harness.adapter as typeof apiClient.defaults.adapter;
    try {
        return await run();
    } finally {
        apiClient.defaults.adapter = previous;
    }
}

function bigFile() {
    return new Blob([new Uint8Array(FILE_SIZE)]);
}

describe("分片上传片级续传", () => {
    test("单片网络抖动只重传该片，不重开会话", async () => {
        const harness = createHarness((_sessionId, index, attempt) => (index === 1 && attempt === 1 ? 503 : undefined));
        await withHarness(harness, async () => {
            const resource = await uploadResourceFile(bigFile(), "video");
            expect(resource.id).toBe("res-ok");
            expect(harness.stats.sessions).toEqual(["session-1"]);
            expect(harness.stats.puts.filter((p) => p.index === 0)).toHaveLength(1);
            expect(harness.stats.puts.filter((p) => p.index === 1)).toHaveLength(2);
            expect(harness.stats.completes).toBe(1);
        });
    });

    test("片级重试耗尽后抛错，且不重开会话整传", async () => {
        const harness = createHarness((_sessionId, index) => (index === 0 ? 502 : undefined));
        await withHarness(harness, async () => {
            await expect(uploadResourceFile(bigFile(), "video")).rejects.toBeInstanceOf(ResourceUploadError);
            expect(harness.stats.sessions).toEqual(["session-1"]);
            expect(harness.stats.puts.filter((p) => p.index === 0)).toHaveLength(3);
            expect(harness.stats.puts.filter((p) => p.index === 1)).toHaveLength(0);
            expect(harness.stats.completes).toBe(0);
        });
    });

    test("会话失效 404 才重开会话，新会话全量重传", async () => {
        const harness = createHarness((sessionId, index, attempt) =>
            sessionId === "session-1" && index === 0 && attempt === 1 ? 404 : undefined,
        );
        await withHarness(harness, async () => {
            const resource = await uploadResourceFile(bigFile(), "video");
            expect(resource.id).toBe("res-ok");
            expect(harness.stats.sessions).toEqual(["session-1", "session-2"]);
            expect(harness.stats.puts.filter((p) => p.sessionId === "session-2" && p.index === 0)).toHaveLength(1);
            expect(harness.stats.puts.filter((p) => p.sessionId === "session-2" && p.index === 1)).toHaveLength(1);
            expect(harness.stats.completes).toBe(1);
        });
    });

    test("片级重试不让进度条回退，最终仍到 100%", async () => {
        const harness = createHarness((_sessionId, index, attempt) => (index === 1 && attempt === 1 ? 502 : undefined), true);
        await withHarness(harness, async () => {
            const progress: number[][] = [];
            await uploadResourceFile(bigFile(), "video", undefined, (loaded, total) => progress.push([loaded, total]));
            expect(progress.length).toBeGreaterThan(0);
            expect(progress.every((point, i) => i === 0 || point[0] >= progress[i - 1][0])).toBe(true);
            expect(progress.at(-1)).toEqual([FILE_SIZE, FILE_SIZE]);
        });
    });

    test("取消请求立即抛出，不触发任何重试", async () => {
        const harness = createHarness((_sessionId, index, attempt) => (index === 0 && attempt === 1 ? "abort" : undefined));
        await withHarness(harness, async () => {
            let caught: unknown;
            try {
                await uploadResourceFile(bigFile(), "video");
            } catch (error) {
                caught = error;
            }
            expect(caught).toBeInstanceOf(DOMException);
            expect((caught as DOMException).name).toBe("AbortError");
            expect(harness.stats.puts).toHaveLength(1);
            expect(harness.stats.sessions).toEqual(["session-1"]);
        });
    });

    test("鉴权等永久失败不重试，直接归一化错误", async () => {
        const harness = createHarness((_sessionId, index) => (index === 0 ? 401 : undefined));
        await withHarness(harness, async () => {
            let caught: unknown;
            try {
                await uploadResourceFile(bigFile(), "video");
            } catch (error) {
                caught = error;
            }
            expect(caught).toBeInstanceOf(ResourceUploadError);
            expect((caught as ResourceUploadError).permanent).toBe(true);
            expect(harness.stats.puts).toHaveLength(1);
            expect(harness.stats.sessions).toEqual(["session-1"]);
        });
    });
});
