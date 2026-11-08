# Gemini 多模态视频理解 接口字段

## 协议身份

- 插件 ID：`gemini-multimodal-text`。
- Provider ID：`gemini-multimodal-text`。
- 能力：`text`。
- 默认 Base URL：`https://generativelanguage.googleapis.com`。
- 鉴权驱动：`google-api-key`（请求头 `x-goog-api-key`）。
- 创建：`POST /v1beta/models/{{model}}:generateContent`。
- 生命周期：同步响应。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | API Key |

## 统一字段映射

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | `model`（路径参数） | 上游模型 ID，须支持视频输入。 |
| `prompt` | string | 否 | `contents[0].parts[0].text` | 用户文本。 |
| `instructions` | string | 否 | `systemInstruction.parts[0].text` | 系统指令。 |
| `images` | media[] | 否 | `contents[0].parts[].inlineData` / `fileData` | data URL 走 `inlineData`，公网地址走 `fileData`。 |
| `videos` | media[] | 否 | `contents[0].parts[].inlineData` / `fileData` | 同上；`mimeType` 缺省按 `video/mp4`。 |
| `messages` | message[] | 否 | 未映射 | 宿主自动追加的媒体消息不参与映射。 |
| `providerOptions` | object | 否 | `generationConfig` / `safetySettings` | 命名空间为 `gemini-multimodal-text`。 |
| `stream` | boolean | 否 | `generationConfig.stream` | 后台任务以最终响应归一。 |

## 上游请求模板逐字段清单

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `"POST"` |
| `create.path` | `"/v1beta/models/{{model}}:generateContent"` |
| `create.contentType` | `"application/json"` |
| `create.body.contents[0].role` | `"user"` |
| `create.body.contents[0].parts` | `$concatArrays[ prompt 文本, images, videos ]` |
| `create.body.contents[0].parts[].text` | `{"$ref":"request.prompt"}`（prompt 非空时） |
| `create.body.contents[0].parts[].inlineData` | `{"mimeType":{"$dataMime":{"$ref":"media.dataUrl"}},"data":{"$dataPayload":{"$ref":"media.dataUrl"}}}` |
| `create.body.contents[0].parts[].fileData` | `{"mimeType":{"$ref":"media.mimeType"},"fileUri":{"$ref":"media.url"}}` |
| `create.body.systemInstruction.parts[0].text` | `{"$ref":"request.instructions"}` |
| `create.body.generationConfig` | `{"$omitEmpty":{"$ref":"request.providerOptions.gemini-multimodal-text.generationConfig"}}` |
| `create.body.safetySettings` | `{"$omitEmpty":{"$ref":"request.providerOptions.gemini-multimodal-text.safetySettings"}}` |

## Provider 扩展键

- `providerOptions.gemini-multimodal-text.generationConfig`
- `providerOptions.gemini-multimodal-text.safetySettings`
- `providerOptions.gemini-multimodal-text.stream`

## 响应映射逐字段清单

| 映射位置 | 上游路径或转换表达式 |
| --- | --- |
| `response.status` | `"succeeded"` |
| `response.text` | `{"$map":{"from":{"$ref":"response.candidates.0.content.parts"},"as":"part","in":{"$omitEmpty":{"$ref":"part.text"}}}}` |
| `response.usage` | `{"$ref":"response.usageMetadata"}` |
| `response.errorPaths[0]` | `"error.code"` |
| `response.messagePaths[0]` | `"error.message"` |

## 兼容边界

- 只代表 Google Generative Language `generateContent` 单一 profile；同一品牌的其他 endpoint、Vertex 区域端点或网关包装必须使用独立插件，不能按模型名猜测。
- 单轮请求：文本只有 `request.prompt`，不做多轮 role 交替。
- 内联视频按 base64 传输，请求体大小受网关限制，调用方需先把视频压到预算内（商品视频分析：12MB 内、640 长边、15fps）。

<!-- YINGCE_MANIFEST_CONTRACT_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "yingce.plugin/v2",
  "id": "gemini-multimodal-text",
  "name": "Gemini 多模态视频理解",
  "version": "1.0.0",
  "author": "影策",
  "description": "Google Gemini generateContent 的视频理解线协议：把整段参考视频以内联 inlineData 随文本一次提交，用于电商商品视频的整片理解。",
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
        "id": "gemini-multimodal-text",
        "label": "Gemini 多模态视频理解",
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
        "baseUrl": "https://generativelanguage.googleapis.com",
        "requiresPublicMediaUrls": false,
        "auth": {
          "type": "google-api-key",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游模型 ID，必须是支持视频输入的 Gemini 模型。"
          },
          {
            "name": "messages",
            "type": "message[]",
            "required": true,
            "mapping": "未使用（宿主自动追加的媒体消息不参与映射）",
            "description": "宿主生成的对话消息；本协议不映射，用户文本请放在 prompt。"
          },
          {
            "name": "instructions",
            "type": "string",
            "required": false,
            "mapping": "systemInstruction.parts[0].text",
            "description": "系统指令。"
          },
          {
            "name": "images",
            "type": "media[]",
            "required": false,
            "mapping": "contents[0].parts[].inlineData|fileData",
            "description": "参考图片，data URL 走 inlineData，公网地址走 fileData。"
          },
          {
            "name": "videos",
            "type": "media[]",
            "required": false,
            "mapping": "contents[0].parts[].inlineData|fileData",
            "description": "参考视频，data URL 走 inlineData，公网地址走 fileData。"
          },
          {
            "name": "temperature",
            "type": "number",
            "required": false,
            "mapping": "providerOptions.gemini-multimodal-text.generationConfig.temperature",
            "description": "采样温度。"
          },
          {
            "name": "top_p",
            "type": "number",
            "required": false,
            "mapping": "providerOptions.gemini-multimodal-text.generationConfig.topP",
            "description": "核采样参数。"
          },
          {
            "name": "max_tokens",
            "type": "integer",
            "required": false,
            "mapping": "providerOptions.gemini-multimodal-text.generationConfig.maxOutputTokens",
            "description": "最大输出 token。"
          },
          {
            "name": "response_format",
            "type": "object",
            "required": false,
            "mapping": "providerOptions.gemini-multimodal-text.generationConfig.responseMimeType|responseSchema",
            "description": "结构化输出配置。"
          },
          {
            "name": "stream",
            "type": "boolean",
            "required": false,
            "mapping": "generationConfig.stream",
            "description": "流式开关；后台任务以最终响应归一。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/v1beta/models/{{model}}:generateContent",
          "contentType": "application/json",
          "body": {
            "contents": [
              {
                "role": "user",
                "parts": {
                  "$concatArrays": [
                    {
                      "$if": {
                        "condition": {
                          "$ref": "request.prompt"
                        },
                        "then": [
                          {
                            "text": {
                              "$ref": "request.prompt"
                            }
                          }
                        ],
                        "else": []
                      }
                    },
                    {
                      "$if": {
                        "condition": {
                          "$or": [
                            {
                              "$gt": [
                                {
                                  "$len": {
                                    "$ref": "request.images"
                                  }
                                },
                                0
                              ]
                            },
                            {
                              "$gt": [
                                {
                                  "$len": {
                                    "$ref": "request.videos"
                                  }
                                },
                                0
                              ]
                            }
                          ]
                        },
                        "then": {
                          "$concatArrays": [
                            {
                              "$map": {
                                "from": {
                                  "$ref": "request.images"
                                },
                                "as": "media",
                                "in": {
                                  "$if": {
                                    "condition": {
                                      "$ref": "media.dataUrl"
                                    },
                                    "then": {
                                      "inlineData": {
                                        "mimeType": {
                                          "$dataMime": {
                                            "$ref": "media.dataUrl"
                                          }
                                        },
                                        "data": {
                                          "$dataPayload": {
                                            "$ref": "media.dataUrl"
                                          }
                                        }
                                      }
                                    },
                                    "else": {
                                      "fileData": {
                                        "mimeType": {
                                          "$coalesce": [
                                            {
                                              "$ref": "media.mimeType"
                                            },
                                            "image/jpeg"
                                          ]
                                        },
                                        "fileUri": {
                                          "$ref": "media.url"
                                        }
                                      }
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
                                  "$if": {
                                    "condition": {
                                      "$ref": "media.dataUrl"
                                    },
                                    "then": {
                                      "inlineData": {
                                        "mimeType": {
                                          "$coalesce": [
                                            {
                                              "$ref": "media.mimeType"
                                            },
                                            "video/mp4"
                                          ]
                                        },
                                        "data": {
                                          "$dataPayload": {
                                            "$ref": "media.dataUrl"
                                          }
                                        }
                                      }
                                    },
                                    "else": {
                                      "fileData": {
                                        "mimeType": {
                                          "$coalesce": [
                                            {
                                              "$ref": "media.mimeType"
                                            },
                                            "video/mp4"
                                          ]
                                        },
                                        "fileUri": {
                                          "$ref": "media.url"
                                        }
                                      }
                                    }
                                  }
                                }
                              }
                            }
                          ]
                        },
                        "else": []
                      }
                    }
                  ]
                }
              }
            ],
            "systemInstruction": {
              "$if": {
                "condition": {
                  "$ref": "request.instructions"
                },
                "then": {
                  "parts": [
                    {
                      "text": {
                        "$ref": "request.instructions"
                      }
                    }
                  ]
                },
                "else": null
              }
            },
            "generationConfig": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.gemini-multimodal-text.generationConfig"
              }
            },
            "safetySettings": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.gemini-multimodal-text.safetySettings"
              }
            }
          }
        },
        "response": {
          "status": "succeeded",
          "text": {
            "$map": {
              "from": {
                "$ref": "response.candidates.0.content.parts"
              },
              "as": "part",
              "in": {
                "$omitEmpty": {
                  "$ref": "part.text"
                }
              }
            }
          },
          "usage": {
            "$ref": "response.usageMetadata"
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
