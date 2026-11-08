# OpenAI 兼容视频理解 接口字段

## 协议身份

- 插件 ID：`apimart-gemini-video-text`。
- Provider ID：`apimart-gemini-video-text`。
- 能力：`text`。
- 默认 Base URL：`https://api.apimart.ai`。
- 鉴权驱动：`bearer`（`Authorization: Bearer <apiKey>`）。
- 创建：`POST /v1/chat/completions`。
- 生命周期：同步响应。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | 上游 API Key |

## 统一字段映射

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | `model` | 上游模型 ID，须支持视频输入。 |
| `prompt` | string | 否 | `messages[user].content[0].text` | 用户文本。 |
| `instructions` | string | 否 | `messages[system].content` | 系统指令。 |
| `images` | media[] | 否 | `messages[user].content[].image_url` | 参考图片。 |
| `videos` | media[] | 否 | `messages[user].content[].video_url` | 参考视频。 |
| `providerOptions` | object | 否 | `temperature` / `top_p` / `max_tokens` / `response_format` | 命名空间为 `apimart-gemini-video-text`。 |
| `stream` | boolean | 否 | `stream`（固定 `false`） | 后台任务以最终响应归一。 |

## 上游请求模板逐字段清单

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `"POST"` |
| `create.path` | `"/v1/chat/completions"` |
| `create.contentType` | `"application/json"` |
| `create.body.model` | `{"$ref":"request.model"}` |
| `create.body.messages[0]` | instructions 非空时 `{"role":"system","content":instructions}` |
| `create.body.messages[1].role` | `"user"` |
| `create.body.messages[1].content[0]` | `{"type":"text","text":{"$ref":"request.prompt"}}`（prompt 非空时） |
| `create.body.messages[1].content[].image_url.url` | `{"$coalesce":[{"$ref":"media.url"},{"$ref":"media.dataUrl"}]}` |
| `create.body.messages[1].content[].video_url.url` | `{"$coalesce":[{"$ref":"media.url"},{"$ref":"media.dataUrl"}]}` |
| `create.body.temperature` | `{"$omitEmpty":{"$ref":"request.providerOptions.apimart-gemini-video-text.temperature"}}` |
| `create.body.top_p` | `{"$omitEmpty":{"$ref":"request.providerOptions.apimart-gemini-video-text.top_p"}}` |
| `create.body.max_tokens` | `{"$omitEmpty":{"$coalesce":[{"$ref":"request.extra.max_tokens"},{"$ref":"request.providerOptions.apimart-gemini-video-text.max_tokens"}]}}` |
| `create.body.response_format` | `{"$omitEmpty":{"$ref":"request.providerOptions.apimart-gemini-video-text.response_format"}}` |
| `create.body.stream` | `false` |

## Provider 扩展键

- `providerOptions.apimart-gemini-video-text.temperature`
- `providerOptions.apimart-gemini-video-text.top_p`
- `providerOptions.apimart-gemini-video-text.max_tokens`
- `providerOptions.apimart-gemini-video-text.response_format`

## 响应映射逐字段清单

| 映射位置 | 上游路径或转换表达式 |
| --- | --- |
| `response.status` | `"succeeded"` |
| `response.textPaths[0]` | `"choices.0.message.content"` |
| `response.textPaths[1]` | `"choices.0.text"` |
| `response.reasoningPaths[0]` | `"choices.0.message.reasoning_content"` |
| `response.usage` | `{"$ref":"response.usage"}` |
| `response.errorPaths[0]` | `"error.code"` |
| `response.messagePaths[0]` | `"error.message"` |

## 兼容边界

- 只覆盖 OpenAI 兼容 `chat/completions` 一种线协议；`responses` 端点、Anthropic Messages、Gemini 原生 `generateContent` 必须使用各自的插件。
- `video_url` 内容块不是 OpenAI 官方字段，属各家网关的扩展；网关不支持时会返回参数错误，本插件不做降级。
- 单轮请求：一条 system（可选）+ 一条 user（文本 + 媒体）。

<!-- YINGCE_MANIFEST_CONTRACT_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "yingce.plugin/v2",
  "id": "apimart-gemini-video-text",
  "name": "OpenAI 兼容视频理解",
  "version": "1.0.0",
  "author": "影策",
  "description": "OpenAI 兼容 chat/completions 的视频理解线协议：把整段参考视频作为 video_url 内容块随文本提交，适用于支持视频输入的 OpenAI 兼容网关（APIMart、自建中转等）。",
  "documentation": "<当前插件的完整 documentation，由 README.md 与 docs/interface.md 拼接而成；为避免 JSON 递归，此处不重复展开正文。>",
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
        "id": "apimart-gemini-video-text",
        "label": "OpenAI 兼容视频理解",
        "capabilities": [
          "text"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "https://api.apimart.ai",
        "requiresPublicMediaUrls": false,
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
            "description": "上游模型 ID，须支持视频输入。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": false,
            "mapping": "messages[0].content[0].text",
            "description": "用户文本。"
          },
          {
            "name": "instructions",
            "type": "string",
            "required": false,
            "mapping": "messages[0].content[0].text（前置系统段）",
            "description": "系统指令，作为独立 system 消息发送。"
          },
          {
            "name": "images",
            "type": "media[]",
            "required": false,
            "mapping": "messages[].content[].image_url",
            "description": "参考图片。"
          },
          {
            "name": "videos",
            "type": "media[]",
            "required": false,
            "mapping": "messages[].content[].video_url",
            "description": "参考视频；data URL 与公网 URL 都放进 video_url.url。"
          },
          {
            "name": "temperature",
            "type": "number",
            "required": false,
            "mapping": "temperature",
            "description": "采样温度。"
          },
          {
            "name": "top_p",
            "type": "number",
            "required": false,
            "mapping": "top_p",
            "description": "核采样参数。"
          },
          {
            "name": "max_tokens",
            "type": "integer",
            "required": false,
            "mapping": "max_tokens",
            "description": "最大输出 token。"
          },
          {
            "name": "response_format",
            "type": "object",
            "required": false,
            "mapping": "response_format",
            "description": "结构化输出配置。"
          },
          {
            "name": "stream",
            "type": "boolean",
            "required": false,
            "mapping": "stream",
            "description": "流式开关；后台任务以最终响应归一。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/v1/chat/completions",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "messages": {
              "$concatArrays": [
                {
                  "$if": {
                    "condition": {
                      "$ref": "request.instructions"
                    },
                    "then": [
                      {
                        "role": "system",
                        "content": {
                          "$ref": "request.instructions"
                        }
                      }
                    ],
                    "else": []
                  }
                },
                [
                  {
                    "role": "user",
                    "content": {
                      "$concatArrays": [
                        {
                          "$if": {
                            "condition": {
                              "$ref": "request.prompt"
                            },
                            "then": [
                              {
                                "type": "text",
                                "text": {
                                  "$ref": "request.prompt"
                                }
                              }
                            ],
                            "else": []
                          }
                        },
                        {
                          "$map": {
                            "from": {
                              "$ref": "request.images"
                            },
                            "as": "media",
                            "in": {
                              "type": "image_url",
                              "image_url": {
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
                        {
                          "$map": {
                            "from": {
                              "$ref": "request.videos"
                            },
                            "as": "media",
                            "in": {
                              "type": "video_url",
                              "video_url": {
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
                        }
                      ]
                    }
                  }
                ]
              ]
            },
            "temperature": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.apimart-gemini-video-text.temperature"
              }
            },
            "top_p": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.apimart-gemini-video-text.top_p"
              }
            },
            "max_tokens": {
              "$omitEmpty": {
                "$coalesce": [
                  {
                    "$ref": "request.extra.max_tokens"
                  },
                  {
                    "$ref": "request.providerOptions.apimart-gemini-video-text.max_tokens"
                  }
                ]
              }
            },
            "response_format": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.apimart-gemini-video-text.response_format"
              }
            },
            "stream": false
          }
        },
        "response": {
          "status": "succeeded",
          "textPaths": [
            "choices.0.message.content",
            "choices.0.text"
          ],
          "reasoningPaths": [
            "choices.0.message.reasoning_content"
          ],
          "usage": {
            "$ref": "response.usage"
          },
          "errorPaths": [
            "error.code"
          ],
          "messagePaths": [
            "error.message"
          ]
        }
      }
    ]
  }
}
```
<!-- YINGCE_MANIFEST_CONTRACT_END -->
