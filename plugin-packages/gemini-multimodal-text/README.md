# Gemini 多模态视频理解

Google Gemini `generateContent` 的视频理解线协议插件。把文本、参考图片和**整段参考视频**放在同一个请求里提交，供需要「看完整条视频」的业务使用（电商 Agent → 商品视频的分镜分析）。

- 插件 ID / Provider ID：`gemini-multimodal-text`
- 能力：`text`
- 默认 Base URL：`https://generativelanguage.googleapis.com`
- 鉴权：`google-api-key`（请求头 `x-goog-api-key`）
- 创建：`POST /v1beta/models/{{model}}:generateContent`（同步）

## 与 `google-gemini-generate-content` 的区别

| 项 | google-gemini-generate-content | 本插件 |
| --- | --- | --- |
| 对话结构 | 每条消息一个 `contents` 项，`parts` 只有 `text` | 合并为单个 `user` 项，`parts` 同时承载文本与媒体 |
| 参考图 | 不支持 | 支持（`inlineData` / `fileData`） |
| 参考视频 | 不支持 | 支持（data URL 走 `inlineData`，公网 URL 走 `fileData`） |
| 适用场景 | 普通文本、看图 | 整片视频理解 |

## 请求映射

- 用户文本取自 `request.prompt`，映射为 `contents[0].parts[0].text`；`request.instructions` 走 `systemInstruction`。
- **不映射 `messages`**：宿主会为参考图/视频自动追加一条 OpenAI 风格消息（`content` 是 `image_url` / `video_url` 数组），按 messages 映射会把该数组塞进 `text` 字段，被上游拒绝。
- `images` / `videos` 逐项映射为 `{"inlineData":{"mimeType":…,"data":…}}` 或 `{"fileData":{"mimeType":…,"fileUri":…}}`；视频 `mimeType` 缺省按 `video/mp4` 兜底。
- `providerOptions.gemini-multimodal-text.generationConfig`、`safetySettings` 原样透传。

## 使用前提

1. 渠道用本协议建模型，模型名必须是上游支持视频输入的 Gemini 模型。
2. 模型能力里勾选「支持视频识别」并把 `references.maxVideos` 设为 ≥1，否则宿主在预授权阶段就会拦下参考视频。
3. 内联视频按 base64 传输，请求体受网关限制；调用方需先把视频压到预算内（商品视频分析压到 12MB 内、640 长边、15fps）。

## 已知边界

- 只支持单轮：所有文本合并进一个 `user` content，不做多轮 role 交替。
- 本插件只保证线协议字段正确，不保证上游模型一定接受视频；是否真能理解视频取决于模型和网关。
