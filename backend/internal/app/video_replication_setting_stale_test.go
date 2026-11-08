package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

// 一条已关闭的视频模型指向的渠道模型被停用后，保存设置会移除这一行，不再挡住其他设置；开放中的失效模型报错并点名。
func TestUpdateVideoReplicationSettingDropsStaleDisabledVideoModel(t *testing.T) {
	svc, db := newVideoReplicationTestService(t)
	if err := db.AutoMigrate(&model.ChannelModel{}, &model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []model.ChannelModel{
		{ID: "cm-live", ChannelID: "ch-1", ModelKey: "minimax-live", Capability: "video", Enabled: true},
		{ID: "cm-gone", ChannelID: "ch-2", ModelKey: "wan-gone", Capability: "video", Enabled: true},
	} {
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm-gone").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	admin := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	input := VideoReplicationSetting{
		ASR: &VideoReplicationASRSetting{Mode: videoReplicationASRModeVision},
		VideoModels: []VideoReplicationVideoModel{
			{EcommerceAgentModelRef: EcommerceAgentModelRef{ChannelModelID: "cm-gone", ChannelID: "ch-2", ModelKey: "wan-gone"}, Enabled: false},
			{EcommerceAgentModelRef: EcommerceAgentModelRef{ChannelModelID: "cm-live"}, Enabled: true},
		},
	}
	// 保存后的状态检查依赖更多运行环境，这里只关心写入的设置。
	_, _ = svc.UpdateVideoReplicationSetting(admin, input)
	row, err := svc.repo.SystemSetting(videoReplicationSettingKey)
	if err != nil {
		t.Fatalf("setting not saved: %v", err)
	}
	var saved VideoReplicationSetting
	if err := json.Unmarshal([]byte(row.ValueJSON), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.VideoModels) != 1 || saved.VideoModels[0].ChannelModelID != "cm-live" || !saved.VideoModels[0].Enabled || saved.ASR == nil || saved.ASR.Mode != videoReplicationASRModeVision {
		t.Fatalf("saved = %+v asr=%+v", saved.VideoModels, saved.ASR)
	}

	input.VideoModels[0].Enabled = true
	_, err = svc.UpdateVideoReplicationSetting(admin, input)
	if err == nil || !strings.Contains(err.Error(), "视频生成模型「wan-gone」") {
		t.Fatalf("enabled stale model must be rejected by name, got %v", err)
	}
}
