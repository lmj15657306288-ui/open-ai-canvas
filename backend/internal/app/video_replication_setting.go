package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 爆款复刻后台设置：管理员指定视频理解（识图）与分镜策划模型、开放给用户的视频生成模型及其可选参数。
// 视频时长不在这里配置，也不由用户选择：由参考视频真实时长与所选模型的单次上限自动决定原生直出或智能分段。

const videoReplicationSettingKey = "video_replication_setting"

type VideoReplicationVideoModel struct {
	EcommerceAgentModelRef
	Label   string `json:"label,omitempty"`
	Enabled bool   `json:"enabled"`
	// Ratios / Resolutions 是开放给用户的子集；空表示该模型能力内全部可选。
	Ratios            []string `json:"ratios,omitempty"`
	Resolutions       []string `json:"resolutions,omitempty"`
	DefaultRatio      string   `json:"defaultRatio,omitempty"`
	DefaultResolution string   `json:"defaultResolution,omitempty"`
	GenerateAudio     bool     `json:"generateAudio"`
	AllowAudioToggle  bool     `json:"allowAudioToggle"`
}

type VideoReplicationSetting struct {
	// Vision / Planning 为空时沿用电商 Agent 的识图 / 策划模型。
	Vision      *EcommerceAgentModelRef      `json:"vision,omitempty"`
	Planning    *EcommerceAgentModelRef      `json:"planning,omitempty"`
	VideoModels []VideoReplicationVideoModel `json:"videoModels"`
	// ASR：mode=vision 用视频理解模型听写音轨；mode=model 用后台指定的语音识别模型（OpenAI 兼容 /audio/transcriptions）；mode=env 或空用服务器 whisper 服务。
	ASR *VideoReplicationASRSetting `json:"asr,omitempty"`
	// TTS：配音 / 试听使用的语音合成模型与可选音色（前台只展示音色名与说明，不展示模型）。
	TTS *VideoReplicationTTSSetting `json:"tts,omitempty"`
	// MaxReferenceSeconds 是参考视频时长上限（4–120 秒）；0 表示默认 120 秒。
	MaxReferenceSeconds int `json:"maxReferenceSeconds,omitempty"`
	// DisabledKinds 是电商 Agent 商品视频里关闭的替换类型（replaceMode），前台隐藏、后端拒绝提交；
	// "dance" 表示关闭跳舞复刻。
	DisabledKinds []string `json:"disabledKinds,omitempty"`
	// Knowledge 是 Seedance / 可灵模型提示词知识库的开关与自定义内容；没保存过的知识库默认开启、用内置内容。
	Knowledge []VideoReplicationKnowledgeBase `json:"knowledge,omitempty"`
	UpdatedAt time.Time                       `json:"updatedAt,omitempty"`
	UpdatedBy string                          `json:"updatedBy,omitempty"`
}

type VideoReplicationASRSetting struct {
	Mode     string                 `json:"mode"`
	Model    EcommerceAgentModelRef `json:"model"`
	Language string                 `json:"language,omitempty"`
}

// VideoReplicationVoiceOption 是后台为 TTS 模型登记的一个音色；Key 是模型接口里的 voice 参数。
type VideoReplicationVoiceOption struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Gender   string `json:"gender,omitempty"`
	Age      string `json:"age,omitempty"`
	Style    string `json:"style,omitempty"`
	Language string `json:"language,omitempty"`
}

type VideoReplicationTTSSetting struct {
	Model  EcommerceAgentModelRef        `json:"model"`
	Voices []VideoReplicationVoiceOption `json:"voices"`
	Format string                        `json:"format,omitempty"`
}

// VideoReplicationPublicConfig 是前台读取的配置：只有模型身份、标签与可选参数，不含渠道凭据。
type VideoReplicationPublicConfig struct {
	VideoModels         []VideoReplicationVideoModel  `json:"videoModels"`
	RestrictVideoModels bool                          `json:"restrictVideoModels"`
	MaxReferenceSeconds int                           `json:"maxReferenceSeconds"`
	DisabledKinds       []string                      `json:"disabledKinds"`
	ASRConfigured       bool                          `json:"asrConfigured"`
	TTSConfigured       bool                          `json:"ttsConfigured"`
	Voices              []VideoReplicationVoiceOption `json:"voices"`
}

func (setting VideoReplicationSetting) ttsReady() bool {
	return setting.TTS != nil && !videoReplicationRefEmpty(&setting.TTS.Model) && len(setting.TTS.Voices) > 0
}

func (setting VideoReplicationSetting) asrModelMode() bool {
	return setting.ASR != nil && setting.ASR.Mode == "model" && !videoReplicationRefEmpty(&setting.ASR.Model)
}

func (setting VideoReplicationSetting) voice(key string) (VideoReplicationVoiceOption, bool) {
	if setting.TTS == nil {
		return VideoReplicationVoiceOption{}, false
	}
	for _, item := range setting.TTS.Voices {
		if item.Key == key {
			return item, true
		}
	}
	return VideoReplicationVoiceOption{}, false
}

// resolveVideoReplicationProvider 把后台选的模型（逻辑模型或渠道模型）解析成服务端 Provider 配置；凭据不离开后端。
func (s *Service) resolveVideoReplicationProvider(userID string, ref EcommerceAgentModelRef, capability string) (providerConfig, error) {
	channelID, modelKey := ref.ChannelID, ref.ModelKey
	access, err := s.userGroupAccess(userID)
	if err != nil {
		return providerConfig{}, err
	}
	if ref.LogicalModelID != "" {
		routed, err := s.ResolveLogicalModel(access, ref.LogicalModelID, ModelRequestIntent{Capability: capability})
		if err != nil {
			return providerConfig{}, err
		}
		channelID, modelKey = routed.ChannelModel.ChannelID, routed.ChannelModel.ModelKey
	}
	if channelID == "" || modelKey == "" {
		return providerConfig{}, errors.New("模型未配置")
	}
	if err := access.requireChannel(channelID); err != nil {
		return providerConfig{}, err
	}
	return s.resolveProviderConfig(providerConfig{ChannelID: channelID, ChannelModelKey: modelKey, Model: modelKey})
}

type VideoReplicationAdminCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type VideoReplicationAdminStatus struct {
	Setting   VideoReplicationSetting         `json:"setting"`
	Checks    []VideoReplicationAdminCheck    `json:"checks"`
	Knowledge []VideoReplicationKnowledgeView `json:"knowledge"`
}

func (s *Service) videoReplicationSetting() (VideoReplicationSetting, error) {
	row, err := s.repo.SystemSetting(videoReplicationSettingKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return VideoReplicationSetting{VideoModels: []VideoReplicationVideoModel{}}, nil
	}
	if err != nil {
		return VideoReplicationSetting{}, err
	}
	var setting VideoReplicationSetting
	if err := json.Unmarshal([]byte(row.ValueJSON), &setting); err != nil {
		return VideoReplicationSetting{}, errors.New("爆款复刻设置无效")
	}
	if setting.VideoModels == nil {
		setting.VideoModels = []VideoReplicationVideoModel{}
	}
	return setting, nil
}

func videoReplicationRefEmpty(ref *EcommerceAgentModelRef) bool {
	return ref == nil || (strings.TrimSpace(ref.LogicalModelID) == "" && strings.TrimSpace(ref.ChannelModelID) == "")
}

// normalizeVideoReplicationRef 只接受逻辑模型或渠道模型行 ID；渠道与模型键从目录回填，不信任前端。
func (s *Service) normalizeVideoReplicationRef(ref EcommerceAgentModelRef, capability string) (EcommerceAgentModelRef, error) {
	if id := strings.TrimSpace(ref.LogicalModelID); id != "" {
		return EcommerceAgentModelRef{LogicalModelID: id}, nil
	}
	id := strings.TrimSpace(ref.ChannelModelID)
	if id == "" {
		return EcommerceAgentModelRef{}, BadAuthRequest("请选择模型")
	}
	selected, err := s.repo.ChannelModel(id)
	if err != nil || !selected.Enabled || selected.DeletedAt.Valid {
		return EcommerceAgentModelRef{}, BadAuthRequest("所选渠道模型不存在或已停用")
	}
	if capability != "" && selected.Capability != capability {
		return EcommerceAgentModelRef{}, BadAuthRequest("所选模型能力不匹配")
	}
	return EcommerceAgentModelRef{ChannelModelID: selected.ID, ChannelID: selected.ChannelID, ModelKey: selected.ModelKey}, nil
}

// videoReplicationRefStale 判断配置里引用的渠道模型是否已被删除或停用（逻辑模型引用不会失效）。
func (s *Service) videoReplicationRefStale(ref EcommerceAgentModelRef) bool {
	if strings.TrimSpace(ref.LogicalModelID) != "" {
		return false
	}
	id := strings.TrimSpace(ref.ChannelModelID)
	if id == "" {
		return true
	}
	selected, err := s.repo.ChannelModel(id)
	return err != nil || !selected.Enabled || selected.DeletedAt.Valid
}

// videoReplicationRefError 在模型校验错误前标明是哪一项设置、哪个模型，方便管理员定位要改的那一行。
func videoReplicationRefError(field string, ref EcommerceAgentModelRef, err error) error {
	name := firstNonEmpty(strings.TrimSpace(ref.ModelKey), strings.TrimSpace(ref.ChannelModelID), strings.TrimSpace(ref.LogicalModelID))
	if name != "" {
		field += "「" + name + "」"
	}
	return BadAuthRequest(field + "：" + err.Error() + "，请删除这一项或换成其他模型后再保存")
}

func (s *Service) AdminVideoReplicationStatus(actor *model.User) (*VideoReplicationAdminStatus, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	setting, err := s.videoReplicationSetting()
	if err != nil {
		return nil, err
	}
	checks := []VideoReplicationAdminCheck{}
	add := func(name string, ok bool, detail string) {
		checks = append(checks, VideoReplicationAdminCheck{Name: name, OK: ok, Detail: detail})
	}
	if err := s.RequireFeature(FeatureVideoWorkbench); err != nil {
		add("影棚视频功能开关", false, "功能开关里「视频」未开放，用户看不到爆款复刻")
	} else {
		add("影棚视频功能开关", true, "已开放")
	}
	_, ffmpegErr := exec.LookPath("ffmpeg")
	add("FFmpeg", ffmpegErr == nil, map[bool]string{true: "已安装：镜头检测、关键帧、音轨、合成", false: "未安装，无法解析参考视频与合成"}[ffmpegErr == nil])
	add("FFprobe", mediaProbeAvailable(), map[bool]string{true: "已安装：真实时长与分辨率校验", false: "未安装，参考视频无法登记"}[mediaProbeAvailable()])
	whisper := strings.TrimSpace(os.Getenv(whisperLangEnv)) != ""
	switch {
	case setting.asrVisionMode():
		if _, unavailable := s.videoReplicationVisionTranscriber(actor.ID, ""); unavailable != "" {
			add("语音识别（ASR）", false, unavailable)
		} else {
			add("语音识别（ASR）", true, "视频理解模型听写音轨（与视频理解同一渠道，按其用量计费）")
		}
	case setting.asrModelMode():
		if provider, err := s.resolveVideoReplicationProvider(actor.ID, setting.ASR.Model, ""); err != nil {
			add("语音识别（ASR）", false, "所选语音识别模型不可用："+err.Error())
		} else {
			add("语音识别（ASR）", true, "模型 "+provider.Model+"（/audio/transcriptions）")
		}
	case whisper:
		add("语音识别（ASR）", true, "服务器 whisper 转写服务")
	default:
		add("语音识别（ASR）", false, "未配置：在「分析与声音模型」选择「视频理解模型听写」或语音识别模型，或设置 CANVAS_WHISPER_BASE_URL；口播台词无法提取，分析仍可进行")
	}
	if setting.ttsReady() {
		if provider, err := s.resolveVideoReplicationProvider(actor.ID, setting.TTS.Model, "audio"); err != nil {
			add("配音模型（TTS）", false, "所选语音合成模型不可用："+err.Error())
		} else {
			add("配音模型（TTS）", true, fmt.Sprintf("%s · %d 个音色", provider.Model, len(setting.TTS.Voices)))
		}
	} else {
		add("配音模型（TTS）", false, "未配置：用户无法试听音色和生成 AI 配音")
	}
	for _, vision := range []bool{true, false} {
		name := map[bool]string{true: "视频理解模型", false: "分镜策划模型"}[vision]
		cfg, err := s.videoReplicationTextConfig(actor.ID, vision)
		if err == nil && vision {
			err = validateVideoReplicationVisionProtocol(cfg)
		}
		if err != nil {
			add(name, false, err.Error())
		} else {
			add(name, true, cfg.TextModel)
		}
	}
	enabled := 0
	for _, item := range setting.VideoModels {
		if item.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		add("视频生成模型", true, "未限定：用户可选全部系统视频模型")
	} else {
		add("视频生成模型", true, fmt.Sprintf("已开放 %d 个模型", enabled))
	}
	for _, view := range setting.knowledgeViews() {
		detail := "已关闭：Agent 不调用"
		if view.Enabled {
			detail = "已开启：用户选择对应模型时 Agent 按此规划提示词"
		}
		add(view.Name, true, detail)
	}
	return &VideoReplicationAdminStatus{Setting: setting, Checks: checks, Knowledge: setting.knowledgeViews()}, nil
}

func (s *Service) UpdateVideoReplicationSetting(actor *model.User, input VideoReplicationSetting) (*VideoReplicationAdminStatus, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	next := VideoReplicationSetting{VideoModels: []VideoReplicationVideoModel{}, UpdatedAt: time.Now(), UpdatedBy: actor.ID}
	for _, item := range []struct {
		label  string
		source *EcommerceAgentModelRef
		target **EcommerceAgentModelRef
	}{{"视频理解模型", input.Vision, &next.Vision}, {"分镜策划模型", input.Planning, &next.Planning}} {
		if videoReplicationRefEmpty(item.source) {
			continue
		}
		ref, err := s.normalizeVideoReplicationRef(*item.source, "text")
		if err != nil {
			return nil, videoReplicationRefError(item.label, *item.source, err)
		}
		*item.target = &ref
	}
	if input.MaxReferenceSeconds != 0 && (input.MaxReferenceSeconds < 4 || input.MaxReferenceSeconds > 120) {
		return nil, BadAuthRequest("参考视频时长上限需为 4–120 秒")
	}
	next.MaxReferenceSeconds = input.MaxReferenceSeconds
	// 知识库：请求里没带（旧页面只保存其他设置）时保留已保存的开关与内容。
	if input.Knowledge == nil {
		if current, err := s.videoReplicationSetting(); err == nil {
			next.Knowledge = current.Knowledge
		}
	} else {
		knowledge, err := normalizeVideoReplicationKnowledge(input.Knowledge)
		if err != nil {
			return nil, err
		}
		next.Knowledge = knowledge
	}
	for _, kind := range input.DisabledKinds {
		switch kind {
		case "model", "clothing", "face", "background", "product", "precise-product", "precise-person", "combination", "dance":
			if !containsString(next.DisabledKinds, kind) {
				next.DisabledKinds = append(next.DisabledKinds, kind)
			}
		default:
			return nil, BadAuthRequest("替换类型无效：" + kind)
		}
	}
	if input.ASR != nil {
		asr := VideoReplicationASRSetting{Mode: strings.TrimSpace(input.ASR.Mode), Language: clipText(strings.TrimSpace(input.ASR.Language), 16)}
		switch asr.Mode {
		case "", "env":
			asr.Mode = "env"
		case videoReplicationASRModeVision:
		case "model":
			ref, err := s.normalizeVideoReplicationRef(input.ASR.Model, "")
			if err != nil {
				return nil, videoReplicationRefError("语音识别模型", input.ASR.Model, err)
			}
			asr.Model = ref
		default:
			return nil, BadAuthRequest("语音识别方式无效")
		}
		next.ASR = &asr
	}
	if input.TTS != nil && !videoReplicationRefEmpty(&input.TTS.Model) {
		ref, err := s.normalizeVideoReplicationRef(input.TTS.Model, "audio")
		if err != nil {
			return nil, videoReplicationRefError("配音模型（TTS）", input.TTS.Model, err)
		}
		tts := VideoReplicationTTSSetting{Model: ref, Voices: []VideoReplicationVoiceOption{}, Format: strings.TrimSpace(input.TTS.Format)}
		if tts.Format != "" && tts.Format != "mp3" && tts.Format != "wav" {
			return nil, BadAuthRequest("配音格式只支持 mp3 / wav")
		}
		if len(input.TTS.Voices) > 60 {
			return nil, BadAuthRequest("音色最多 60 个")
		}
		keys := map[string]bool{}
		for _, voice := range input.TTS.Voices {
			voice.Key = strings.TrimSpace(voice.Key)
			voice.Name = clipText(strings.TrimSpace(voice.Name), 40)
			if voice.Key == "" || len(voice.Key) > 120 || voice.Name == "" {
				return nil, BadAuthRequest("每个音色都需要填写音色 ID 与名称")
			}
			if keys[voice.Key] {
				return nil, BadAuthRequest("音色 ID 重复：" + voice.Key)
			}
			keys[voice.Key] = true
			voice.Gender, voice.Age, voice.Style, voice.Language = clipText(strings.TrimSpace(voice.Gender), 8), clipText(strings.TrimSpace(voice.Age), 16), clipText(strings.TrimSpace(voice.Style), 80), clipText(strings.TrimSpace(voice.Language), 16)
			tts.Voices = append(tts.Voices, voice)
		}
		next.TTS = &tts
	}
	if len(input.VideoModels) > 30 {
		return nil, BadAuthRequest("视频模型最多 30 个")
	}
	seen := map[string]bool{}
	clean := func(values []string) []string {
		result := []string{}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" && len(value) <= 24 && !containsString(result, value) {
				result = append(result, value)
			}
		}
		return result
	}
	for _, item := range input.VideoModels {
		ref, err := s.normalizeVideoReplicationRef(item.EcommerceAgentModelRef, "video")
		if err != nil {
			// 已关闭的一行指向的渠道模型后来被停用或下架：前台本来就看不到它，保存时直接移除，不挡住整页其他设置的保存。
			if !item.Enabled && s.videoReplicationRefStale(item.EcommerceAgentModelRef) {
				continue
			}
			return nil, videoReplicationRefError("视频生成模型", item.EcommerceAgentModelRef, err)
		}
		identity := ref.LogicalModelID + "|" + ref.ChannelModelID
		if seen[identity] {
			return nil, BadAuthRequest("视频模型重复")
		}
		seen[identity] = true
		model := VideoReplicationVideoModel{EcommerceAgentModelRef: ref, Label: clipText(strings.TrimSpace(item.Label), 60), Enabled: item.Enabled, Ratios: clean(item.Ratios), Resolutions: clean(item.Resolutions), DefaultRatio: strings.TrimSpace(item.DefaultRatio), DefaultResolution: strings.TrimSpace(item.DefaultResolution), GenerateAudio: item.GenerateAudio, AllowAudioToggle: item.AllowAudioToggle}
		if model.DefaultRatio != "" && len(model.Ratios) > 0 && !containsString(model.Ratios, model.DefaultRatio) {
			return nil, BadAuthRequest("默认比例必须在开放的比例内")
		}
		if model.DefaultResolution != "" && len(model.Resolutions) > 0 && !containsString(model.Resolutions, model.DefaultResolution) {
			return nil, BadAuthRequest("默认清晰度必须在开放的清晰度内")
		}
		next.VideoModels = append(next.VideoModels, model)
	}
	raw, _ := json.Marshal(next)
	if err := s.repo.SaveSystemSetting(&model.SystemSetting{Key: videoReplicationSettingKey, ValueJSON: string(raw)}); err != nil {
		return nil, err
	}
	return s.AdminVideoReplicationStatus(actor)
}

// VideoReplicationConfig 给前台：开放的视频模型与参数、参考视频上限、ASR 是否可用。
func (s *Service) VideoReplicationConfig() (*VideoReplicationPublicConfig, error) {
	if err := s.requireVideoReplication(); err != nil {
		return nil, err
	}
	setting, err := s.videoReplicationSetting()
	if err != nil {
		return nil, err
	}
	config := &VideoReplicationPublicConfig{VideoModels: []VideoReplicationVideoModel{}, MaxReferenceSeconds: videoReplicationMaxReferenceSeconds(setting), ASRConfigured: setting.asrModelMode() || setting.asrVisionMode() || strings.TrimSpace(os.Getenv(whisperLangEnv)) != "", TTSConfigured: setting.ttsReady(), Voices: []VideoReplicationVoiceOption{}, DisabledKinds: append([]string{}, setting.DisabledKinds...)}
	if setting.ttsReady() {
		config.Voices = setting.TTS.Voices
	}
	for _, item := range setting.VideoModels {
		if item.Enabled {
			config.VideoModels = append(config.VideoModels, item)
		}
	}
	config.RestrictVideoModels = len(config.VideoModels) > 0
	return config, nil
}

func videoReplicationMaxReferenceSeconds(setting VideoReplicationSetting) int {
	if setting.MaxReferenceSeconds >= 4 && setting.MaxReferenceSeconds <= 120 {
		return setting.MaxReferenceSeconds
	}
	return int(videoReplicationMaxDurationMs / 1000)
}

// videoReplicationModelAllowed 校验用户提交的视频模型是否在管理员开放的范围与参数内（未限定时全部允许）。
func (s *Service) videoReplicationModelAllowed(params VideoReplicationParameters) error {
	setting, err := s.videoReplicationSetting()
	if err != nil {
		return err
	}
	restricted := false
	for _, item := range setting.VideoModels {
		if !item.Enabled {
			continue
		}
		restricted = true
		match := (item.LogicalModelID != "" && item.LogicalModelID == params.LogicalModelID) ||
			(item.LogicalModelID == "" && params.LogicalModelID == "" && item.ChannelID == params.ChannelID && strings.TrimPrefix(item.ModelKey, "models/") == strings.TrimPrefix(params.Model, "models/"))
		if !match {
			continue
		}
		if len(item.Ratios) > 0 && params.Ratio != "" && !containsString(item.Ratios, params.Ratio) {
			return BadAuthRequest("所选比例未在后台开放")
		}
		if len(item.Resolutions) > 0 && params.Resolution != "" && !containsString(item.Resolutions, params.Resolution) {
			return BadAuthRequest("所选清晰度未在后台开放")
		}
		if !item.AllowAudioToggle && params.GenerateAudio != item.GenerateAudio {
			return BadAuthRequest("生成音频由后台统一设置")
		}
		return nil
	}
	if restricted {
		return BadAuthRequest("所选视频模型未在爆款复刻后台开放，请重新选择")
	}
	return nil
}

type VideoReplicationAdminProject = repository.VideoReplicationAdminProject

type VideoReplicationOverview = repository.VideoReplicationOverview

func (s *Service) AdminVideoReplicationProjects(actor *model.User, limit int) ([]VideoReplicationAdminProject, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.AdminVideoReplicationProjects(limit)
}
