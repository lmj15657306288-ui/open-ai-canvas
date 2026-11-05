import { describe, expect, test } from "bun:test";
import { CONTENT_MODERATION_MESSAGE, generationErrorMessage, generationFailureMetadata } from "../src/lib/generation-error";

describe("safe upstream error details", () => {
    const detail = "非常抱歉，生成的图片可能违反了关于裸露、色情或情色内容的防护限制。请重试或修改提示语。";
    test("drops upstream detail from the card", () => {
        const message = `模型服务拒绝了请求，请检查模型和参数；上游：${detail}`;
        expect(generationErrorMessage(new Error(message))).toBe("模型服务拒绝了请求，请检查模型和参数");
    });
    test("does not replace a safe upstream message with a generic HTTP hint", () => {
        const message = `模型服务暂时不可用（HTTP 500）；上游：${detail}`;
        expect(generationErrorMessage(message)).toBe("模型服务暂时不可用，请稍后再试。");
        expect(generationErrorMessage("模型服务暂时不可用（HTTP 500）")).toBe("模型服务暂时不可用，请稍后再试。");
    });
    test("keeps details when moderation metadata is stored", () => {
        const message = `内容审核未通过；上游：${detail}`;
        const metadata = generationFailureMetadata(message, "test prompt");
        expect(metadata.errorDetails).toBe(CONTENT_MODERATION_MESSAGE);
        expect(metadata.generationErrorCode).toBe("sensitive_words_detected");
    });
    test("gives actionable hints for known upstream failures", () => {
        expect(generationErrorMessage(`模型服务暂时不可用（HTTP 503）；上游：model_not_found request id abc`)).toContain("更换模型");
        expect(generationErrorMessage(`模型服务拒绝请求；上游：image file too large 25MB`)).toContain("压缩图片");
    });
    test("upstream marker does not bypass URL hiding", () => {
        expect(generationErrorMessage("模型服务请求失败；上游：https://private.test")).toBe("模型服务请求失败");
    });
    test("keeps an actionable upstream duration limit instead of the generic HTTP hint", () => {
        // 生产任务 agf3842c…：万相 3.0 因为参考视频 17 秒超过 15 秒上限失败，
        // 平台把它包成了 HTTP 502，旧实现只回「模型服务暂时不可用，请稍后再试。」。
        const message = "模型服务暂时不可用（HTTP 502）；上游：万相 3.0 创建任务失败: 这段视频是 17.0 秒，超过 15 秒上限，请裁剪到 15 秒内再传";
        expect(generationErrorMessage(message)).toBe("参考视频 17 秒，超过 15 秒上限，请裁剪到 15 秒内再上传。");
    });
    test("duration limit without numbers still tells the user to shorten the material", () => {
        expect(generationErrorMessage("生成失败；上游：参考视频时长超过模型限制，请重新上传")).toContain("裁剪");
    });
});
