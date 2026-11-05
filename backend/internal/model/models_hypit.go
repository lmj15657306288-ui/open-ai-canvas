package model

import "time"

// HypitTaskStatus 是复刻任务的生命周期状态。
// queued -> running -> succeeded / failed；用户可在排队或执行中取消为 canceled。
type HypitTaskStatus string

const (
	HypitTaskQueued    HypitTaskStatus = "queued"
	HypitTaskRunning   HypitTaskStatus = "running"
	HypitTaskSucceeded HypitTaskStatus = "succeeded"
	HypitTaskFailed    HypitTaskStatus = "failed"
	HypitTaskCanceled  HypitTaskStatus = "canceled"
)

// HypitTask 是一次视频复刻请求。
//
// 画布只负责排队、状态与产物回链，真正的渲染由独立 Hypit Worker 完成：
// Worker 通过 claim 接口领任务、回报进度与产物，因此本表不持有任何执行上下文。
type HypitTask struct {
	ID                  string          `json:"id" gorm:"primaryKey;size:36"`
	UserID              string          `json:"userId" gorm:"index;size:36"`
	Title               string          `json:"title" gorm:"size:200"`
	Mode                string          `json:"mode" gorm:"size:32;index"`
	ReferenceResourceID string          `json:"referenceResourceId,omitempty" gorm:"index;size:36"`
	ReferenceURL        string          `json:"referenceUrl,omitempty" gorm:"type:text"`
	ReferenceName       string          `json:"referenceName,omitempty" gorm:"size:255"`
	Instructions        string          `json:"instructions" gorm:"type:text"`
	Model               string          `json:"model,omitempty" gorm:"size:120"`
	AspectRatio         string          `json:"aspectRatio,omitempty" gorm:"size:16"`
	Resolution          string          `json:"resolution,omitempty" gorm:"size:16"`
	DurationSec         int             `json:"durationSec,omitempty"`
	Language            string          `json:"language,omitempty" gorm:"size:16"`
	// MaterialsJSON 保存各素材槽位的资源 ID（如 {"model":["res_1"],"product":["res_2","res_3"]}）。
	MaterialsJSON string `json:"materialsJson,omitempty" gorm:"type:text"`
	// VoiceMode: original 保留原声 / replace 按台词重新配音 / mute 静音。
	VoiceMode   string `json:"voiceMode,omitempty" gorm:"size:16"`
	KeepMusic   bool   `json:"keepMusic"`
	TalkingHead bool   `json:"talkingHead"`
	Subject     string `json:"subject,omitempty" gorm:"size:64"`
	Status              HypitTaskStatus `json:"status" gorm:"index;size:24"`
	Progress            int             `json:"progress"`
	Stage               string          `json:"stage,omitempty" gorm:"size:120"`
	OutputURL           string          `json:"outputUrl,omitempty" gorm:"type:text"`
	OutputResourceID    string          `json:"outputResourceId,omitempty" gorm:"index;size:36"`
	Error               string          `json:"error,omitempty" gorm:"type:text"`
	WorkerID            string          `json:"-" gorm:"index;size:64"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
}

// HypitWorker 是执行节点心跳表。页面据最近心跳判断节点是否在线。
type HypitWorker struct {
	ID         string    `json:"id" gorm:"primaryKey;size:64"`
	Host       string    `json:"host" gorm:"size:120"`
	Version    string    `json:"version" gorm:"size:40"`
	LastSeenAt time.Time `json:"lastSeenAt" gorm:"index"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}
