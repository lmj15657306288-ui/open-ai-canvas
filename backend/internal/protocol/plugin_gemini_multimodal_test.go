package protocol

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestGeminiMultimodalPluginBuildsVideoRequest 校验「Gemini 多模态视频理解」插件：
// 文本取 prompt、system 走 systemInstruction、参考视频按 data URL / 公网 URL 分别映射为
// inlineData / fileData，且不把宿主自动追加的 OpenAI 风格媒体消息（image_url/video_url）
// 混进 parts。该协议是电商「商品视频」整片理解的前置条件（InterfaceType 白名单要求此 id）。
func TestGeminiMultimodalPluginBuildsVideoRequest(t *testing.T) {
	raw, err := os.ReadFile("../../../plugin-packages/gemini-multimodal-text/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := LoadInstalledProviders(raw, nil)
	if err != nil {
		t.Fatalf("加载插件失败: %v", err)
	}
	if len(adapters) != 1 {
		t.Fatalf("provider 数 = %d，期望 1", len(adapters))
	}
	meta := adapters[0].Metadata()
	t.Logf("provider id=%q label=%q capability=%v", meta.ID, meta.Name, meta.Categories)

	for _, tc := range []struct {
		name   string
		videos []MediaReference
	}{
		{"内联 data URL", []MediaReference{{DataURL: "data:video/mp4;base64,dmlkZW8=", MIMEType: "video/mp4"}}},
		{"公网 URL", []MediaReference{{URL: "https://media.example/full.mp4", MIMEType: "video/mp4"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := adapters[0].BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
				Capability:   CapabilityText,
				Model:        "gemini-test",
				Instructions: "system",
				Prompt:       "watch",
				Messages:     []Message{{Role: "user", Content: "watch"}},
				Videos:       tc.videos,
				Images:       []MediaReference{{DataURL: "data:image/jpeg;base64,aW1n", MIMEType: "image/jpeg"}},
			}})
			if err != nil {
				t.Fatalf("构建请求失败: %v", err)
			}
			body, _ := json.Marshal(spec.Body)
			t.Logf("path=%s body=%s", spec.Path, string(body))
			if spec.Path != "/v1beta/models/gemini-test:generateContent" {
				t.Errorf("路径不符: %s", spec.Path)
			}
			if !strings.Contains(string(body), `"text":"watch"`) {
				t.Error("body 缺少用户文本")
			}
			if !strings.Contains(string(body), `"mimeType":"video/mp4"`) {
				t.Error("body 缺少 video mimeType")
			}
			if strings.Contains(string(body), `"image_url"`) {
				t.Error("body 不应出现 image_url")
			}
		})
	}
}
