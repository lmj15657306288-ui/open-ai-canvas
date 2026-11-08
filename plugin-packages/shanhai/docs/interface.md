# 山海 Open Canvas API 接口字段

## 协议身份

- 插件 ID：`shanhai`。
- Provider ID：`shanhai-image`（能力 `image`）、`shanhai-video`（能力 `video`）。
- 默认 Base URL：`https://shanhai.vnshu.cn/api/v1`。
- 鉴权驱动：`bearer`（`Authorization: Bearer <apiKey>`）。
- 创建：`POST /generations`。
- 轮询：`GET /tasks/{{taskId}}`。
- 生命周期：图片以同步为主（200 + 终态），视频异步（202 + `running`，实测 15 秒成片约 3.5 分钟）。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | 山海 API Key，形如 `oc_live_…` |

## 统一字段映射

### 图片（`shanhai-image`）

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | `model` | `GET /models` 的 `data[].id` |
| `prompt` | string | 是 | `prompt` | 上限 20000 字符 |
| `images` | media[] | 否 | `inputs[]`（`type: image`） | 参考图或编辑源图 |
| `aspectRatio` | string | 否 | `options.aspect_ratio` | `1:1` / `16:9` / `9:16` / `4:3` / `3:4` |
| `resolution` | string | 否 | `options.resolution` | `1K` / `2K` / `4K` |

### 视频（`shanhai-video`）

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | `model` | `GET /models` 的 `data[].id` |
| `prompt` | string | 是 | `prompt` | 上限 20000 字符 |
| `images` | media[] | 否 | `inputs[]`（`type: image`） | 首帧或参考图 |
| `videos` | media[] | 否 | `inputs[]`（`type: video`） | 参考视频 |
| `duration` | integer | 否 | `options.duration` | 上游要求字符串，插件用 `$toString` 转换 |
| `aspectRatio` | string | 否 | `options.aspect_ratio` | 按模型 `capabilities.aspect_ratios` |
| `resolution` | string | 否 | `options.resolution` | 目前视频模型均为 `720p` |

## 上游特有字段

| 字段 | 取值 | 来源 |
| --- | --- | --- |
| `media_type` | 固定 `image` / `video` | 由 Provider 决定，不对外暴露 |
| `scene` | `text-to-text`、`text-to-image`、`image-to-image`、`text-to-video`、`image-to-video`、`video-to-video`、`image-to-text` | 插件按输入自动判定 |
| `inputs[].type` | `image` / `video` / `audio` | 按媒体类型 |
| `inputs[].url` | 公网 HTTPS 直链 | 宿主转存后的地址 |

## 媒体约束

- 单次最多 10 个参考输入（`inputs.maxItems: 10`）。
- 所有输入 URL 必须是上游服务器能直接抓取的 HTTPS 直链；HTML 页面地址与 `data:` URL 都会被拒绝。
- 插件声明 `requiresPublicMediaUrls: true`，宿主在提交前把本地素材转成公网地址。

## 响应映射

| 统一字段 | 上游路径 | 说明 |
| --- | --- | --- |
| 任务 ID | `response.id` → `taskId` | 图片与视频都有 `id` |
| 状态 | `response.status`，缺省 `pending` | `queued/pending`→排队，`running`→生成中，`succeeded`→成功，`failed`→失败 |
| 图片结果 | `response.output.url` | 图片单值，宿主按单条媒体处理 |
| 视频结果 | `response.output.url` | 同上 |
| 失败原因 | `response.error.message` / `response.message` / `response.error` | 依次取第一个非空值 |
| 错误判定 | `errorPaths: ["error"]` | 成功时上游返回 `error: null`，不会误判为失败 |

产物地址形如 `https://shanhai.vnshu.cn/api/v1/media/runs/<taskId>?download_token=…`，令牌有效期约 24 小时，因此结果标记为 `resultEphemeral`，宿主会及时落库到自有存储。

## 已知限制

1. 上游网关（Tengine）对 `POST /generations` 的等待上限约 30 秒。慢模型会返回 504，且此时无法拿到任务 ID，任务在服务端可能已计费。接入时优先选实测能同步返回的模型（如 `shanhai-image-2`、`oc-model-16thqw`）。
2. `GET /models` 的目录是动态的：模型上下架会直接改变可用列表，不要硬编码模型名。
3. 部分模型标注「开代理使用」「测试中」，稳定性和耗时差异较大，建议仅启用已验证的模型。

<!-- YINGCE_MANIFEST_CONTRACT_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "yingce.plugin/v2",
  "id": "shanhai",
  "name": "山海 Open Canvas API",
  "version": "1.0.0",
  "author": "山海 / 影策",
  "description": "接入 shanhai.vnshu.cn 的 Open Canvas 公开 API。图片通常同步返回，视频异步返回任务 ID 后轮询。",
  "permissions": [
    "generation.run",
    "media.read"
  ],
  "configuration": {
    "fields": [
      {
        "name": "apiKey",
        "type": "secret",
        "label": "API Key",
        "required": true
      }
    ]
  },
  "contributes": {
    "providers": [
      {
        "id": "shanhai-image",
        "label": "山海 · 图片",
        "capabilities": [
          "image"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "https://shanhai.vnshu.cn/api/v1",
        "requiresPublicMediaUrls": true,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "GET /models 返回的稳定模型 ID，例如 shanhai-image-2。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "图片提示词。"
          },
          {
            "name": "images",
            "type": "media[]",
            "required": false,
            "mapping": "inputs[]",
            "description": "参考图或编辑源图，需要公网可访问的 HTTPS 地址。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "required": false,
            "mapping": "options.aspect_ratio",
            "description": "画幅比例。"
          },
          {
            "name": "resolution",
            "type": "string",
            "required": false,
            "mapping": "options.resolution",
            "description": "分辨率档位，1K / 2K / 4K。"
          }
        ],
        "imageParameters": [
          {
            "name": "size",
            "label": "画幅比例",
            "type": "string",
            "binding": "aspectRatio",
            "values": [
              "1:1",
              "16:9",
              "9:16",
              "4:3",
              "3:4"
            ],
            "default": "1:1"
          },
          {
            "name": "imageResolution",
            "label": "分辨率",
            "type": "string",
            "binding": "resolution",
            "values": [
              "1K",
              "2K",
              "4K"
            ],
            "default": "1K",
            "pricing": true
          }
        ],
        "create": {
          "method": "POST",
          "path": "/generations",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "prompt": {
              "$ref": "request.prompt"
            },
            "media_type": "image",
            "scene": {
              "$if": {
                "condition": {
                  "$gt": [
                    {
                      "$len": {
                        "$ref": "request.images"
                      }
                    },
                    0
                  ]
                },
                "then": "image-to-image",
                "else": "text-to-image"
              }
            },
            "inputs": {
              "$omitEmpty": {
                "$map": {
                  "from": {
                    "$ref": "request.images"
                  },
                  "as": "media",
                  "in": {
                    "type": "image",
                    "url": {
                      "$coalesce": [
                        {
                          "$ref": "media.url"
                        },
                        {
                          "$ref": "media.dataUrl"
                        }
                      ]
                    }
                  }
                }
              }
            },
            "options": {
              "aspect_ratio": {
                "$omitEmpty": {
                  "$ref": "request.aspectRatio"
                }
              },
              "resolution": {
                "$omitEmpty": {
                  "$ref": "request.resolution"
                }
              }
            }
          }
        },
        "poll": {
          "method": "GET",
          "path": "/tasks/{{taskId}}"
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$coalesce": [
              {
                "$ref": "response.status"
              },
              "pending"
            ]
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.error"
              }
            ]
          },
          "images": {
            "$ref": "response.output.url"
          },
          "errorPaths": [
            "error"
          ],
          "resultEphemeral": true
        }
      },
      {
        "id": "shanhai-video",
        "label": "山海 · 视频",
        "capabilities": [
          "video"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "https://shanhai.vnshu.cn/api/v1",
        "requiresPublicMediaUrls": true,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "GET /models 返回的稳定模型 ID，例如 oc-model-ggj8sz。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "视频提示词。"
          },
          {
            "name": "images",
            "type": "media[]",
            "required": false,
            "mapping": "inputs[] type=image",
            "description": "首帧或参考图，需要公网可访问的 HTTPS 地址。"
          },
          {
            "name": "videos",
            "type": "media[]",
            "required": false,
            "mapping": "inputs[] type=video",
            "description": "参考视频，需要公网可访问的 HTTPS 地址。"
          },
          {
            "name": "duration",
            "type": "integer",
            "required": false,
            "mapping": "options.duration",
            "description": "时长秒数，取值见 GET /models 的 capabilities.durations。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "required": false,
            "mapping": "options.aspect_ratio",
            "description": "画幅比例。"
          },
          {
            "name": "resolution",
            "type": "string",
            "required": false,
            "mapping": "options.resolution",
            "description": "分辨率档位。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/generations",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "prompt": {
              "$ref": "request.prompt"
            },
            "media_type": "video",
            "scene": {
              "$if": {
                "condition": {
                  "$gt": [
                    {
                      "$len": {
                        "$ref": "request.videos"
                      }
                    },
                    0
                  ]
                },
                "then": "video-to-video",
                "else": {
                  "$if": {
                    "condition": {
                      "$gt": [
                        {
                          "$len": {
                            "$ref": "request.images"
                          }
                        },
                        0
                      ]
                    },
                    "then": "image-to-video",
                    "else": "text-to-video"
                  }
                }
              }
            },
            "inputs": {
              "$omitEmpty": {
                "$concatArrays": [
                  {
                    "$map": {
                      "from": {
                        "$ref": "request.images"
                      },
                      "as": "media",
                      "in": {
                        "type": "image",
                        "url": {
                          "$coalesce": [
                            {
                              "$ref": "media.url"
                            },
                            {
                              "$ref": "media.dataUrl"
                            }
                          ]
                        }
                      }
                    }
                  },
                  {
                    "$map": {
                      "from": {
                        "$ref": "request.videos"
                      },
                      "as": "media",
                      "in": {
                        "type": "video",
                        "url": {
                          "$coalesce": [
                            {
                              "$ref": "media.url"
                            },
                            {
                              "$ref": "media.dataUrl"
                            }
                          ]
                        }
                      }
                    }
                  }
                ]
              }
            },
            "options": {
              "aspect_ratio": {
                "$omitEmpty": {
                  "$ref": "request.aspectRatio"
                }
              },
              "resolution": {
                "$omitEmpty": {
                  "$ref": "request.resolution"
                }
              },
              "duration": {
                "$omitEmpty": {
                  "$toString": {
                    "$ref": "request.duration"
                  }
                }
              }
            }
          }
        },
        "poll": {
          "method": "GET",
          "path": "/tasks/{{taskId}}"
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$coalesce": [
              {
                "$ref": "response.status"
              },
              "pending"
            ]
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.error"
              }
            ]
          },
          "videos": {
            "$ref": "response.output.url"
          },
          "errorPaths": [
            "error"
          ],
          "resultEphemeral": true
        }
      }
    ]
  },
  "documentation": "<当前插件的完整 documentation，由 README.md 与 docs/interface.md 拼接而成；为避免 JSON 递归，此处不重复展开正文。>"
}
```
<!-- YINGCE_MANIFEST_CONTRACT_END -->
