package app

import (
	"testing"

	"yingce/backend/internal/model"
	protocolcontract "yingce/backend/internal/protocol"
)

// 线上 CHANNEL_000006 的 gpt-image-2.5 系列在数据库里的真实形态：声明式 size 参数
// encoding=pixels、支持值同时含比例与像素、quality 用 low/medium/high 表示档位、
// 没有独立的 imageResolution 参数、size.presets 为空。
// 这类线路必须能自动补齐换算表，并把「比例 + 档位」换成像素，否则审批永远停在
// 「当前线路缺少像素尺寸换算表」。
func enumPixelImageProfile() *ImageCapabilityConfig {
	values := []any{
		"auto", "1:1", "3:2", "2:3", "4:3", "3:4", "16:9", "21:9", "9:16",
		"1024x1024", "1360x1024", "1024x1360", "1536x1024", "1024x1536", "2048x2048",
		"2560x3200", "3200x2560", "3840x2160", "2160x3840",
	}
	declared := make([]string, 0, len(values))
	for _, value := range values {
		declared = append(declared, value.(string))
	}
	return &ImageCapabilityConfig{
		Parameters: []protocolcontract.ImageParameter{{
			Name:     "size",
			Label:    "画幅 / 尺寸",
			Type:     "string",
			Values:   values,
			Default:  "1:1",
			Binding:  "aspectRatio",
			Encoding: "pixels",
		}},
		Size:       ImageSizeConfig{Parameter: "aspect_ratio", Values: declared, Default: "1:1"},
		Resolution: ImageResolutionConfig{Supported: false, Default: "auto"},
		Quality:    ImageQualityConfig{Supported: true, Values: []string{"auto", "low", "medium", "high", "xhigh", "max"}, Default: "auto"},
	}
}

func TestEnumPixelImageLineBackfillsSizePresets(t *testing.T) {
	protocol := string(model.ChannelInterfaceOpenAIImage)
	profile := enumPixelImageProfile()

	if err := imageSizePresetReadyError(profile, protocol); err == nil {
		t.Fatal("前置条件不成立：这条线路本应因缺换算表被判为不可用")
	}
	if !fillMissingImageSizePresets(profile, protocol) {
		t.Fatal("声明式 pixels 线路没有补齐换算表")
	}
	if err := imageSizePresetReadyError(profile, protocol); err != nil {
		t.Fatalf("补齐后仍被判为缺换算表：%v", err)
	}
	if len(profile.Size.Presets) == 0 {
		t.Fatal("补齐后预设为空")
	}
	for _, tc := range []struct{ size, tier, want string }{
		{size: "1:1", tier: "low", want: "1024x1024"},
		{size: "16:9", tier: "medium", want: "2752x1536"},
		{size: "16:9", tier: "high", want: "3840x2160"},
		{size: "9:16", tier: "auto", want: "1024x1824"},
	} {
		if got := resolveImageTaskSize(profile, protocol, tc.size, tc.tier); got != tc.want {
			t.Fatalf("size=%s tier=%s 换算成 %q，期望 %q", tc.size, tc.tier, got, tc.want)
		}
	}
}

// 线上 Agent 的真实调用参数是 {size:"9:16", quality:"auto"}，没有 imageResolution。
// 准入曾经只把 imageResolution 当档位来源，这类线路永远查不到预设，报
// 「当前比例与分辨率缺少明确的像素尺寸映射」并拒绝提交。
func TestEnumPixelImageLineAdmitsAgentPayload(t *testing.T) {
	protocol := string(model.ChannelInterfaceOpenAIImage)
	profile := enumPixelImageProfile()
	if !fillMissingImageSizePresets(profile, protocol) {
		t.Fatal("声明式 pixels 线路没有补齐换算表")
	}
	for _, tc := range []struct{ quality, want string }{
		{quality: "auto", want: "1024x1824"},
		{quality: "low", want: "1024x1824"},
		{quality: "medium", want: "1536x2752"},
		{quality: "high", want: "2160x3840"},
	} {
		config := &providerConfig{InterfaceType: protocol, Size: "9:16", Quality: tc.quality}
		if err := applyImageParameterValues(config, profile); err != nil {
			t.Fatalf("quality=%s 准入失败：%v", tc.quality, err)
		}
		if config.Size != tc.want {
			t.Fatalf("quality=%s 换算成 %q，期望 %q", tc.quality, config.Size, tc.want)
		}
		if params, _ := config.ImageParameters["size"].(string); params != tc.want {
			t.Fatalf("出网参数 size=%q，期望像素值 %q", params, tc.want)
		}
	}
}
