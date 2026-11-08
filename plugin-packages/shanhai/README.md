# 山海 Open Canvas API

接入 [山海](https://shanhai.vnshu.cn) 提供的 Open Canvas 公开 API。该上游不是 OpenAI 兼容接口，而是「创建生成任务 → 查询任务 → 下载产物」的异步模型，本插件用声明式线协议把两种形态映射到画布统一的图片 / 视频能力上。

- 插件 ID：`shanhai`
- Provider ID：`shanhai-image`（能力 `image`）、`shanhai-video`（能力 `video`）
- 默认 Base URL：`https://shanhai.vnshu.cn/api/v1`
- 鉴权：`bearer`（请求头 `Authorization: Bearer <apiKey>`）
- 创建：`POST /generations`
- 轮询：`GET /tasks/{{taskId}}`

## 上游接口形态

| 动作 | 请求 | 返回 |
| --- | --- | --- |
| 列出模型 | `GET /models` | 动态目录，含 `type`（text / image / video）与 `capabilities` |
| 创建生成 | `POST /generations` | 图片多为 **200 + `status: succeeded`**（实测 20~28 秒）；视频为 **202 + `status: running`** |
| 查询任务 | `GET /tasks/{taskId}` | 同结构，`status` 为 `queued / running / succeeded / failed` |
| 下载产物 | `GET /media/{path}` | 响应里的 `output.url` 自带 `download_token`，有效期约 24 小时 |

成功的响应形如：

```json
{
  "id": "aa40a7e3-…",
  "status": "succeeded",
  "model": "oc-model-16thqw",
  "output": { "url": "https://shanhai.vnshu.cn/api/v1/media/runs/…?download_token=…", "mime_type": "image/png", "type": "image" },
  "error": null,
  "usage": { "credits": 0.13 }
}
```

## 请求结构

```json
{
  "model": "shanhai-image-2",
  "prompt": "a single red apple on a white table, product photo",
  "media_type": "image",
  "scene": "text-to-image",
  "inputs": [{ "type": "image", "url": "https://…/ref.jpg" }],
  "options": { "aspect_ratio": "1:1", "resolution": "1K" }
}
```

`scene` 由插件按输入自动判定：无参考图 `text-to-image` / `text-to-video`，有图 `image-to-image` / `image-to-video`，有参考视频 `video-to-video`。

## 使用前提

1. 参考图、参考视频必须是**公网可直接抓取的 HTTPS 地址**，上游不接受 HTML 页面地址，也不接受 `data:` URL。插件已声明 `requiresPublicMediaUrls: true`，宿主会先把本地素材转成公网地址再提交。
2. 模型 ID 必须取 `GET /models` 返回的 `data[].id`（例如 `shanhai-image-2`、`oc-model-ggj8sz`），`display_name` 只是展示名。
3. 时长的合法取值以该模型 `capabilities.durations` 为准；分辨率和比例同理，传不支持的档位会被上游拒绝。
4. 上游网关（Tengine）对 **POST /generations 有 30 秒等待上限**。慢模型（如标注「开代理使用」的 `oc-model-1atf67`）会直接 504，此时任务可能在服务端已创建但拿不到任务 ID。接入时优先选实测能同步返回的模型。
