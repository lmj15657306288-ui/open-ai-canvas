package app

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"yingce/backend/internal/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	hypitMaxInstructions    = 600
	hypitMaxTitleLen        = 200
	hypitWorkerOnlineWindow = 90 * time.Second
)

// hypitReplicaModes 是允许的复刻类型，与前端 chips 一一对应。
//
// 前 8 个是工作台展示的槽位类型；后 6 个是早期版本的类型，只为历史任务回显标题保留。
var hypitReplicaModes = map[string]string{
	"model-swap":      "模特替换",
	"outfit-swap":     "服饰替换",
	"face-swap":       "人脸替换",
	"background-swap": "换背景",
	"product-swap":    "商品替换",
	"product-precise": "精准商品",
	"person-precise":  "精准人物",
	"combo-swap":      "组合替换",
	"full-clone":      "整片复刻",
	"swap-host":       "换主角",
	"swap-script":     "换文案",
	"swap-broll":      "换 B-roll",
	"localize":        "换语言",
	"combo":           "组合替换",
}

var hypitAspectRatios = map[string]bool{"9:16": true, "16:9": true, "1:1": true, "3:4": true}
var hypitLanguages = map[string]bool{"source": true, "zh": true, "en": true, "ja": true}

type HypitTaskCreateRequest struct {
	Mode                string              `json:"mode"`
	ReferenceResourceID string              `json:"referenceResourceId"`
	ReferenceURL        string              `json:"referenceUrl"`
	ReferenceName       string              `json:"referenceName"`
	Instructions        string              `json:"instructions"`
	Model               string              `json:"model"`
	AspectRatio         string              `json:"aspectRatio"`
	Resolution          string              `json:"resolution"`
	DurationSec         int                 `json:"durationSec"`
	Language            string              `json:"language"`
	Materials           []HypitMaterialSlot `json:"materials"`
	VoiceMode           string              `json:"voiceMode"`
	KeepMusic           bool                `json:"keepMusic"`
	TalkingHead         bool                `json:"talkingHead"`
	Subject             string              `json:"subject"`
}

// HypitMaterialSlot 是一个素材槽位提交的资源列表（人物图、商品图、背景图、参考音色等）。
type HypitMaterialSlot struct {
	Slot        string   `json:"slot"`
	ResourceIDs []string `json:"resourceIds"`
}

var hypitVoiceModes = map[string]bool{"original": true, "replace": true, "mute": true}
var hypitResolutions = map[string]bool{"480p": true, "720p": true, "1080p": true}

type HypitTaskPage struct {
	Items        []model.HypitTask `json:"items"`
	Total        int64             `json:"total"`
	Page         int               `json:"page"`
	Limit        int               `json:"limit"`
	WorkerOnline bool              `json:"workerOnline"`
}

type HypitWorkerReportRequest struct {
	Status           string `json:"status"`
	Progress         int    `json:"progress"`
	Stage            string `json:"stage"`
	OutputURL        string `json:"outputUrl"`
	OutputResourceID string `json:"outputResourceId"`
	Error            string `json:"error"`
}

// CreateHypitTask 创建一次复刻请求。参考视频必须来自本地上传的资源或可访问的 http(s) 链接。
func (s *Service) CreateHypitTask(actor *model.User, req HypitTaskCreateRequest) (*model.HypitTask, error) {
	if err := s.RequireFeature(FeatureHypitWorkbench); err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(req.Mode)
	label, ok := hypitReplicaModes[mode]
	if !ok {
		return nil, BadAuthRequest("请选择有效的复刻类型")
	}
	resourceID := strings.TrimSpace(req.ReferenceResourceID)
	referenceURL := strings.TrimSpace(req.ReferenceURL)
	if resourceID == "" && referenceURL == "" {
		return nil, BadAuthRequest("请先上传参考视频或填入视频链接")
	}
	if referenceURL != "" && !isHTTPURL(referenceURL) {
		return nil, BadAuthRequest("参考视频链接必须是 http 或 https 地址")
	}
	if resourceID != "" {
		if _, err := s.repo.ResourceForUser(actor.ID, resourceID); err != nil {
			return nil, BadAuthRequest("参考视频资源不存在或无权访问")
		}
	}
	instructions := strings.TrimSpace(req.Instructions)
	if len([]rune(instructions)) > hypitMaxInstructions {
		return nil, BadAuthRequest("补充说明最多 600 字")
	}
	materialsJSON, err := s.validateHypitMaterials(actor.ID, req.Materials)
	if err != nil {
		return nil, err
	}
	voiceMode := strings.TrimSpace(req.VoiceMode)
	if voiceMode == "" {
		voiceMode = "original"
	}
	if !hypitVoiceModes[voiceMode] {
		return nil, BadAuthRequest("不支持的声音模式")
	}
	resolution := strings.TrimSpace(req.Resolution)
	if resolution == "" {
		resolution = "480p"
	}
	if !hypitResolutions[resolution] {
		return nil, BadAuthRequest("不支持的清晰度")
	}
	aspectRatio := strings.TrimSpace(req.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = "9:16"
	}
	if !hypitAspectRatios[aspectRatio] {
		return nil, BadAuthRequest("不支持的画面比例")
	}
	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = "source"
	}
	if !hypitLanguages[language] {
		return nil, BadAuthRequest("不支持的语言")
	}
	if req.DurationSec < 0 || req.DurationSec > 600 {
		return nil, BadAuthRequest("时长需要在 0-600 秒之间")
	}

	referenceName := strings.TrimSpace(req.ReferenceName)
	title := label
	if referenceName != "" {
		title = label + " · " + referenceName
	}
	if len([]rune(title)) > hypitMaxTitleLen {
		title = string([]rune(title)[:hypitMaxTitleLen])
	}

	task := &model.HypitTask{
		ID:                  uuid.NewString(),
		UserID:              actor.ID,
		Title:               title,
		Mode:                mode,
		ReferenceResourceID: resourceID,
		ReferenceURL:        referenceURL,
		ReferenceName:       truncateRunes(referenceName, 255),
		Instructions:        instructions,
		Model:               strings.TrimSpace(req.Model),
		AspectRatio:         aspectRatio,
		Resolution:          resolution,
		DurationSec:         req.DurationSec,
		Language:            language,
		MaterialsJSON:       materialsJSON,
		VoiceMode:           voiceMode,
		KeepMusic:           req.KeepMusic,
		TalkingHead:         req.TalkingHead,
		Subject:             truncateRunes(strings.TrimSpace(req.Subject), 64),
		Status:              model.HypitTaskQueued,
		Progress:            0,
		Stage:               "排队中",
	}
	if err := s.repo.CreateHypitTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

// UserHypitTaskPage 返回当前用户的复刻任务分页结果，并附带执行节点在线状态。
func (s *Service) UserHypitTaskPage(actor *model.User, page int, limit int) (*HypitTaskPage, error) {
	if err := s.RequireFeature(FeatureHypitWorkbench); err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	items, total, err := s.repo.HypitTasks(actor.ID, limit, (page-1)*limit)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []model.HypitTask{}
	}
	online, err := s.repo.HypitWorkerSeenSince(time.Now().Add(-hypitWorkerOnlineWindow))
	if err != nil {
		return nil, err
	}
	return &HypitTaskPage{Items: items, Total: total, Page: page, Limit: limit, WorkerOnline: online}, nil
}

// UserHypitTask 读取单条任务，限制归属。
func (s *Service) UserHypitTask(actor *model.User, id string) (*model.HypitTask, error) {
	if err := s.RequireFeature(FeatureHypitWorkbench); err != nil {
		return nil, err
	}
	task, err := s.repo.HypitTask(strings.TrimSpace(id), actor.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("复刻任务不存在")
	}
	if err != nil {
		return nil, err
	}
	return task, nil
}

// CancelUserHypitTask 只允许取消仍在排队或执行中的任务。
func (s *Service) CancelUserHypitTask(actor *model.User, id string) (*model.HypitTask, error) {
	task, err := s.UserHypitTask(actor, id)
	if err != nil {
		return nil, err
	}
	if task.Status != model.HypitTaskQueued && task.Status != model.HypitTaskRunning {
		return nil, BadAuthRequest("任务已结束，无法取消")
	}
	now := time.Now()
	task.Status = model.HypitTaskCanceled
	task.Stage = "已取消"
	task.FinishedAt = &now
	if err := s.repo.SaveHypitTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

// ---- 执行节点接口 ----

// HypitWorkerHeartbeat 记录节点心跳；页面据此判断执行节点是否在线。
func (s *Service) HypitWorkerHeartbeat(workerID string, host string, version string) error {
	if strings.TrimSpace(workerID) == "" {
		return BadAuthRequest("缺少节点标识")
	}
	now := time.Now()
	return s.repo.TouchHypitWorker(&model.HypitWorker{ID: workerID, Host: truncateRunes(host, 120), Version: truncateRunes(version, 40), LastSeenAt: now})
}

// ClaimHypitTask 让执行节点领取一条排队任务；无任务时返回 nil。
func (s *Service) ClaimHypitTask(workerID string) (*model.HypitTask, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, BadAuthRequest("缺少节点标识")
	}
	_ = s.HypitWorkerHeartbeat(workerID, "", "")
	return s.repo.ClaimHypitTask(workerID)
}

// ReportHypitTask 由执行节点回报进度与产物。
func (s *Service) ReportHypitTask(workerID string, id string, req HypitWorkerReportRequest) (*model.HypitTask, error) {
	_ = s.HypitWorkerHeartbeat(workerID, "", "")
	task, err := s.repo.HypitTask(strings.TrimSpace(id), "")
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("复刻任务不存在")
	}
	if err != nil {
		return nil, err
	}
	if task.Status == model.HypitTaskCanceled {
		return task, nil
	}
	status := strings.TrimSpace(req.Status)
	if status != "" {
		switch model.HypitTaskStatus(status) {
		case model.HypitTaskRunning, model.HypitTaskSucceeded, model.HypitTaskFailed:
			task.Status = model.HypitTaskStatus(status)
		default:
			return nil, BadAuthRequest("无效的任务状态")
		}
	}
	task.Progress = min(max(req.Progress, 0), 100)
	if stage := strings.TrimSpace(req.Stage); stage != "" {
		task.Stage = truncateRunes(stage, 120)
	}
	if outputURL := strings.TrimSpace(req.OutputURL); outputURL != "" {
		task.OutputURL = outputURL
	}
	if resourceID := strings.TrimSpace(req.OutputResourceID); resourceID != "" {
		task.OutputResourceID = resourceID
	}
	if message := strings.TrimSpace(req.Error); message != "" {
		task.Error = truncateRunes(message, 2000)
	}
	if task.Status == model.HypitTaskSucceeded {
		task.Progress = 100
		if task.Stage == "" {
			task.Stage = "已完成"
		}
	}
	if task.Status == model.HypitTaskSucceeded || task.Status == model.HypitTaskFailed {
		if task.FinishedAt == nil {
			now := time.Now()
			task.FinishedAt = &now
		}
	}
	if err := s.repo.SaveHypitTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

// validateHypitMaterials 校验每个槽位的资源归属与数量，返回入库用的 JSON。
// 槽位名称由前端定义（model/product/background/voice...），后端只做安全与数量约束。
func (s *Service) validateHypitMaterials(userID string, slots []HypitMaterialSlot) (string, error) {
	if len(slots) == 0 {
		return "", nil
	}
	result := make(map[string][]string, len(slots))
	total := 0
	for _, slot := range slots {
		key := strings.TrimSpace(slot.Slot)
		if key == "" || len(key) > 32 {
			return "", BadAuthRequest("素材槽位无效")
		}
		ids := make([]string, 0, len(slot.ResourceIDs))
		for _, raw := range slot.ResourceIDs {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			if _, err := s.repo.ResourceForUser(userID, id); err != nil {
				return "", BadAuthRequest("素材资源不存在或无权访问")
			}
			ids = append(ids, id)
			total++
		}
		if len(ids) > 4 {
			return "", BadAuthRequest("单个素材槽位最多 4 张")
		}
		if len(ids) > 0 {
			result[key] = ids
		}
	}
	if total > 12 {
		return "", BadAuthRequest("素材总数过多")
	}
	if len(result) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func isHTTPURL(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}
