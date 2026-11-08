package app

import (
	"encoding/json"
	"os"
	"testing"
)

// 声明式像素线路的线上配置（size 只列比例、像素在 presets 里、档位由 quality 承载）。
// 审批卡会把像素值原样回传，准入必须接受它，否则用户点确认就被自己的枚举拦下。
func TestDeclaredPixelSizeAdmissionWithRealChannelConfig(t *testing.T) {
	raw, err := os.ReadFile("testdata/panda_gpt_image_25.json")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg ModelCapabilityConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	normalized, err := NormalizeModelCapabilityConfigForModel("image", "openai-image", "gpt-image-2.5", &cfg, loadOfficialFallbackRegistry())
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	profile := normalized.Image
	fillMissingImageSizePresets(profile, "openai-image")
	if err := imageSizePresetReadyError(profile, "openai-image"); err != nil {
		t.Fatalf("preset ready: %v", err)
	}

	cases := []struct {
		name       string
		size       string
		quality    string
		parameters map[string]any
		wantSize   string
	}{
		{"像素参数 1K", "1024x1824", "auto", map[string]any{"size": "1024x1824", "quality": "auto"}, "1024x1824"},
		{"比例参数 4K", "2160x3840", "high", map[string]any{"size": "9:16", "quality": "high"}, "2160x3840"},
		{"无参数 4K", "2160x3840", "high", nil, "2160x3840"},
		{"比例入参 转像素", "9:16", "medium", map[string]any{"size": "9:16", "quality": "medium"}, "1536x2752"},
	}
	for _, c := range cases {
		config := map[string]any{
			"channelId":       "CHANNEL_000006",
			"channelModelKey": "gpt-image-2.5",
			"interfaceType":   "openai-image",
			"model":           "gpt-image-2.5",
			"size":            c.size,
			"quality":         c.quality,
			"count":           "1",
		}
		if c.parameters != nil {
			config["imageParameters"] = c.parameters
		}
		input := map[string]any{"mode": "image", "prompt": "一朵白玫瑰", "config": config}
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("%s: marshal: %v", c.name, err)
		}
		var taskInput canvasGenerationInput
		if err := json.Unmarshal(encoded, &taskInput); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		if err := validateImageTaskWithResolvedSize(input, &taskInput, profile); err != nil {
			t.Fatalf("%s: 准入被拒绝: %v", c.name, err)
		}
		if taskInput.Config.Size != c.wantSize {
			t.Fatalf("%s: 出网尺寸 = %q，期望 %q", c.name, taskInput.Config.Size, c.wantSize)
		}
		if got := imageParameterText(taskInput.Config.ImageParameters["size"]); got != c.wantSize {
			t.Fatalf("%s: 出网参数尺寸 = %q，期望 %q", c.name, got, c.wantSize)
		}
	}
}
