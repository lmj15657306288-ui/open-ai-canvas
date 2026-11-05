import { describe, expect, test } from "bun:test";
import { formatTaskLog, projectBackendSafeTaskLog } from "../src/services/api/task-center";

describe("backend task log projection", () => {
    test("keeps a readable body so the detail dialog is not just stage=backend_event", () => {
        const log = projectBackendSafeTaskLog(
            "task-1",
            {
                level: "error",
                message: "后台任务处理失败",
                payload: "模型服务暂时不可用（HTTP 502）；上游：万相 3.0 创建任务失败: 这段视频是 17.0 秒，超过 15 秒上限，请裁剪到 15 秒内再传",
                createdAt: "2026-11-04T10:11:49.000Z",
            },
            0,
        );
        expect(formatTaskLog(log)).toContain("message=后台任务处理失败");
        expect(formatTaskLog(log)).toContain("超过 15 秒上限");
    });

    test("redacts upstream addresses and flattens whitespace", () => {
        const log = projectBackendSafeTaskLog("task-1", { level: "error", message: "上游请求失败 https://private.test/v1\n  重试无效", createdAt: "2026-11-04T10:11:49.000Z" }, 0);
        expect(log.message).toBe("上游请求失败 [链接已隐藏] 重试无效");
        expect(log.message).not.toContain("private.test");
    });

    test("caps the body length and omits empty bodies", () => {
        const long = projectBackendSafeTaskLog("task-1", { level: "info", message: "x".repeat(400), createdAt: "2026-11-04T10:11:49.000Z" }, 0);
        expect(long.message?.endsWith("…")).toBe(true);
        expect(long.message?.length).toBe(221);
        const empty = projectBackendSafeTaskLog("task-1", { level: "info", createdAt: "2026-11-04T10:11:49.000Z" }, 1);
        expect(empty.message).toBeUndefined();
        expect(formatTaskLog(empty)).not.toContain("message=");
    });
});
