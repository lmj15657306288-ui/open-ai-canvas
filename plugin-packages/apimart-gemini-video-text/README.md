# OpenAI 兼容视频理解

OpenAI 兼容 `chat/completions` 的视频理解线协议插件。把整段参考视频作为 `video_url` 内容块随文本提交，适用于任何支持视频输入的 OpenAI 兼容网关（APIMart、自建中转、Magpie 等）。

- 插件 ID / Provider ID：`apimart-gemini-video-text`
- 能力：`text`
- 默认 Base URL：`https://api.apimart.ai`（可在渠道里改成自己的网关地址）
- 鉴权：`bearer`（请求头 `Authorization: Bearer <apiKey>`）
- 创建：`POST /v1/chat/completions`（同步）

> Provider ID 沿用上游「商品视频」功能里硬编码的线协议名（`apimart-gemini-video-text`），便于该功能直接识别；实际对接的网关不受 APIMart 限制。

## 请求结构

```json
{
  "model": "…",
  "messages": [
    {"role": "system", "content": "<instructions>"},
    {"role": "user", "content": [
      {"type": "text", "text": "<prompt>"},
      {"type": "image_url", "image_url": {"url": "data:image/jpeg;base64,…"}},
      {"type": "video_url", "video_url": {"url": "https://…/full.mp4"}}
    ]}
  ],
  "stream": false
}
```

## 使用前提

1. 渠道用本协议建模型，`baseUrl` 指向实际网关，模型名须支持视频输入。
2. 模型能力里勾选「支持视频识别」并把 `references.maxVideos` 设为 ≥1，否则宿主在预授权阶段就会拦下参考视频。
3. 网关是否真的接受 `video_url` 内容块取决于其实现；本插件只保证请求字段正确。

## 与 `gemini-multimodal-text` 的区别

| 项 | gemini-multimodal-text | 本插件 |
| --- | --- | --- |
| 上游形态 | Google 原生 `generateContent`（`inlineData`） | OpenAI 兼容 `chat/completions`（`video_url`） |
| 鉴权 | `x-goog-api-key` | `Authorization: Bearer` |
| 适用网关 | Google 官方 / Vertex 兼容 | APIMart、自建中转、Magpie 等 |
