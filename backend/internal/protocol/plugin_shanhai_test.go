package protocol

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestShanhaiPluginLoadsAndBuildsRequests 校验「山海 Open Canvas API」插件：
// 两个 provider（image / video）能被正确加载，create 请求按上游 /generations 结构组装，
// poll 走 /tasks/{taskId}，并且能解析上游同步图片响应与异步视频响应。
func TestShanhaiPluginLoadsAndBuildsRequests(t *testing.T) {
	raw, err := os.ReadFile("../../../plugin-packages/shanhai/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := LoadInstalledProviders(raw, nil)
	if err != nil {
		t.Fatalf("加载插件失败: %v", err)
	}
	if len(adapters) != 2 {
		t.Fatalf("provider 数 = %d，期望 2", len(adapters))
	}
	byID := map[string]Adapter{}
	for _, adapter := range adapters {
		meta := adapter.Metadata()
		byID[meta.ID] = adapter
		t.Logf("provider id=%q label=%q capability=%v", meta.ID, meta.Name, meta.Categories)
	}
	image, ok := byID["shanhai-image"]
	if !ok {
		t.Fatal("缺少 shanhai-image")
	}
	video, ok := byID["shanhai-video"]
	if !ok {
		t.Fatal("缺少 shanhai-video")
	}

	t.Run("图片-纯文生图", func(t *testing.T) {
		spec, err := image.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
			Capability:  CapabilityImage,
			Model:       "shanhai-image-2",
			Prompt:      "a red apple",
			AspectRatio: "1:1",
			Resolution:  "2K",
		}})
		if err != nil {
			t.Fatalf("构建请求失败: %v", err)
		}
		body, _ := json.Marshal(spec.Body)
		t.Logf("path=%s body=%s", spec.Path, string(body))
		if spec.Path != "/generations" {
			t.Errorf("路径不符: %s", spec.Path)
		}
		for _, want := range []string{`"media_type":"image"`, `"scene":"text-to-image"`, `"aspect_ratio":"1:1"`, `"resolution":"2K"`} {
			if !strings.Contains(string(body), want) {
				t.Errorf("body 缺少 %s", want)
			}
		}
		if strings.Contains(string(body), `"inputs"`) {
			t.Error("无参考图时不应提交 inputs")
		}
	})

	t.Run("图片-带参考图", func(t *testing.T) {
		spec, err := image.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
			Capability: CapabilityImage,
			Model:      "shanhai-image-2",
			Prompt:     "edit",
			Images:     []MediaReference{{URL: "https://media.example/ref.png", MIMEType: "image/png"}},
		}})
		if err != nil {
			t.Fatalf("构建请求失败: %v", err)
		}
		body, _ := json.Marshal(spec.Body)
		t.Logf("body=%s", string(body))
		if !strings.Contains(string(body), `"scene":"image-to-image"`) {
			t.Error("有参考图时 scene 应为 image-to-image")
		}
		if !strings.Contains(string(body), `"url":"https://media.example/ref.png"`) {
			t.Error("inputs 缺少参考图地址")
		}
		if strings.Contains(string(body), `"aspect_ratio"`) {
			t.Error("未传比例时不应出现 aspect_ratio")
		}
	})

	t.Run("视频-图生视频", func(t *testing.T) {
		spec, err := video.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
			Capability:  CapabilityVideo,
			Model:       "oc-model-ggj8sz",
			Prompt:      "rotate",
			Duration:    15,
			AspectRatio: "16:9",
			Resolution:  "720p",
			Images:      []MediaReference{{URL: "https://media.example/first.png"}},
		}})
		if err != nil {
			t.Fatalf("构建请求失败: %v", err)
		}
		body, _ := json.Marshal(spec.Body)
		t.Logf("path=%s body=%s", spec.Path, string(body))
		for _, want := range []string{`"media_type":"video"`, `"scene":"image-to-video"`, `"duration":"15"`} {
			if !strings.Contains(string(body), want) {
				t.Errorf("body 缺少 %s", want)
			}
		}
	})

	t.Run("视频-参考视频优先", func(t *testing.T) {
		spec, err := video.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
			Capability: CapabilityVideo,
			Model:      "oc-model-ggj8sz",
			Prompt:     "same motion",
			Videos:     []MediaReference{{URL: "https://media.example/ref.mp4", MIMEType: "video/mp4"}},
			Images:     []MediaReference{{URL: "https://media.example/first.png"}},
		}})
		if err != nil {
			t.Fatalf("构建请求失败: %v", err)
		}
		body, _ := json.Marshal(spec.Body)
		t.Logf("body=%s", string(body))
		if !strings.Contains(string(body), `"scene":"video-to-video"`) {
			t.Error("有参考视频时 scene 应为 video-to-video")
		}
		if !strings.Contains(string(body), `"type":"video"`) || !strings.Contains(string(body), `"type":"image"`) {
			t.Error("inputs 应同时包含 image 与 video")
		}
	})

	t.Run("视频-轮询路径", func(t *testing.T) {
		spec, err := video.BuildPoll(context.Background(), PollContext{TaskID: "0c9a9034-9bef", Model: "oc-model-ggj8sz"})
		if err != nil {
			t.Fatalf("构建轮询失败: %v", err)
		}
		if spec.Path != "/tasks/0c9a9034-9bef" {
			t.Errorf("轮询路径不符: %s", spec.Path)
		}
	})

	t.Run("解析图片终态响应", func(t *testing.T) {
		payload := []byte(`{"id":"aa40a7e3","status":"succeeded","model":"oc-model-16thqw","output":{"url":"https://shanhai.vnshu.cn/api/v1/media/runs/aa40a7e3?download_token=x","mime_type":"image/png","type":"image"},"error":null,"usage":{"credits":0.13}}`)
		result, err := image.ParsePoll(context.Background(), PollContext{TaskID: "aa40a7e3"}, payload)
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		t.Logf("status=%v taskId=%q images=%v", result.Status, result.TaskID, result.Result)
		if result.Status != StatusSucceeded {
			t.Errorf("状态应为 succeeded，实际 %v", result.Status)
		}
		if result.Result == nil || len(result.Result.Images) != 1 {
			t.Fatalf("应解析出 1 张图片，实际 %+v", result.Result)
		}
		if !strings.Contains(result.Result.Images[0].URL, "/media/runs/aa40a7e3") {
			t.Errorf("图片地址不符: %s", result.Result.Images[0].URL)
		}
	})

	t.Run("解析视频终态响应", func(t *testing.T) {
		payload := []byte(`{"id":"0c9a9034","status":"succeeded","output":{"url":"https://shanhai.vnshu.cn/api/v1/media/runs/0c9a9034?download_token=y","mime_type":"video/mp4","type":"video"},"error":null}`)
		result, err := video.ParsePoll(context.Background(), PollContext{TaskID: "0c9a9034"}, payload)
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if result.Status != StatusSucceeded || result.Result == nil || len(result.Result.Videos) != 1 {
			t.Fatalf("应解析出 1 个视频，实际 %+v", result.Result)
		}
	})

	t.Run("解析失败响应", func(t *testing.T) {
		payload := []byte(`{"id":"x","status":"failed","output":null,"error":{"code":"insufficient_scope","message":"Insufficient scope"}}`)
		result, err := video.ParsePoll(context.Background(), PollContext{TaskID: "x"}, payload)
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if result.Status != StatusFailed {
			t.Errorf("状态应为 failed，实际 %v", result.Status)
		}
		if !strings.Contains(result.Message, "Insufficient") {
			t.Errorf("失败原因不符: %q", result.Message)
		}
	})
}
