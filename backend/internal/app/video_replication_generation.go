package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
	"yingce/backend/internal/workbench"
)

// 爆款复刻的逐镜头生成与合成。每个镜头版本对应一个普通工作台视频任务：
// 预扣、失败退款、超时、重启恢复与取消都由任务系统负责，这里只维护镜头版本与任务的关联。
// 合成使用时间线渲染任务（本地 ffmpeg，不计费）。

var videoReplicationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// videoReplicationSubmitGrace 是提交中版本等待任务回执的时间；超时后按 RequestID 找回任务，找不到才判定未提交。
const videoReplicationSubmitGrace = 2 * time.Minute

type VideoReplicationGenerateInput struct {
	IdempotencyKey string   `json:"idempotencyKey"`
	ShotIDs        []string `json:"shotIds,omitempty"`
	// Regenerate 为 true 时为 ShotIDs 中的镜头新建版本，即使已有成功版本；否则只补齐还没有成功版本的镜头。
	Regenerate bool `json:"regenerate,omitempty"`
}

type VideoReplicationComposeInput struct {
	IdempotencyKey string `json:"idempotencyKey"`
}

func videoReplicationRunActive(status string) bool {
	return status == model.VideoReplicationRunSubmitting || status == model.VideoReplicationRunQueued || status == model.VideoReplicationRunRunning
}

func videoReplicationOutputActive(status string) bool {
	return status == "queued" || status == "running" || status == "submitting"
}

type videoReplicationRunParameters struct {
	Seconds    int                       `json:"seconds"`
	Ratio      string                    `json:"ratio,omitempty"`
	Resolution string                    `json:"resolution,omitempty"`
	Model      string                    `json:"model,omitempty"`
	ModelLabel string                    `json:"modelLabel,omitempty"`
	VideoType  string                    `json:"videoType"`
	References []map[string]string       `json:"references"`
	Mentions   []VideoReplicationMention `json:"mentions"`
	Strategy   string                    `json:"strategy"`
	TailFrame  string                    `json:"tailFrameResourceId,omitempty"`
}

func (s *Service) GenerateVideoReplication(userID, projectID string, input VideoReplicationGenerateInput, traceID string) (*VideoReplicationProjectView, error) {
	if err := s.requireVideoReplication(); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if !videoReplicationKeyPattern.MatchString(key) {
		return nil, BadAuthRequest("提交标识无效")
	}
	project, err := s.repo.VideoReplicationProject(userID, projectID)
	if err != nil {
		return nil, videoReplicationNotFound(err)
	}
	if project.Status == model.VideoReplicationStatusAnalyzing {
		return nil, BadAuthRequest("参考视频分析中，请等待分镜生成")
	}
	// 同一提交标识重放（网络重试、双击）：已经处理过就直接返回当前状态，不继续提交后续片段。
	if used, err := s.repo.VideoReplicationKeyUsed(projectID, key); err != nil {
		return nil, err
	} else if used {
		return s.VideoReplicationProject(userID, projectID)
	}
	plan, err := requireConfirmedVideoReplicationPlan(project)
	if err != nil {
		return nil, err
	}
	if input.Regenerate && project.Paused {
		// 用户主动重新生成单个镜头视为继续；预算仍会在提交前检查。
		_ = s.repo.UpdateVideoReplicationProject(userID, projectID, map[string]any{"paused": false, "pause_reason": ""})
		project.Paused = false
	}
	submitted, failure, err := s.submitVideoReplication(project, plan, key, input.ShotIDs, input.Regenerate, traceID)
	if err != nil {
		return nil, err
	}
	// AI 配音与画面并行提交：每个有口播的镜头一个 TTS 任务，已成功或进行中的不重复提交。
	if plan.AudioMode == videoReplicationAudioTTS && failure == "" {
		voiced, voiceFailure, err := s.submitVideoReplicationVoiceRuns(project, plan, key)
		if err != nil {
			return nil, err
		}
		submitted += voiced
		failure = voiceFailure
	}
	view, err := s.VideoReplicationProject(userID, projectID)
	if err != nil {
		return nil, err
	}
	if submitted == 0 && failure != "" {
		return nil, BadAuthRequest(failure)
	}
	return view, nil
}

type videoReplicationTarget struct {
	shotID  string
	index   int
	seconds int
	prompt  string
	tailOf  string
	// sourceStartMs / sourceEndMs：原片编辑模式下这一段对应的参考视频区间。
	sourceStartMs int64
	sourceEndMs   int64
}

// submitVideoReplication 按已确认方案提交视频生成任务。
// 原生直出只会提交一个整片任务；智能分段在「尾帧衔接」时按顺序一次只提交下一段，否则并行提交缺失片段。
// 每次提交前按真实账单累计检查预算，超出即暂停，不会静默追加付费任务。
func (s *Service) submitVideoReplication(project *model.VideoReplicationProject, plan *VideoReplicationPlan, key string, shotIDs []string, regenerate bool, traceID string) (int, string, error) {
	userID, projectID := project.UserID, project.ID
	storyboard := videoReplicationStoryboard(project)
	if storyboard == nil || len(storyboard.Shots) == 0 {
		return 0, "", BadAuthRequest("请先分析参考视频并确认分镜")
	}
	params := videoReplicationParameters(project)
	params = videoReplicationWithRegionTarget(project, params)
	assets, err := s.repo.VideoReplicationAssets(userID, projectID)
	if err != nil {
		return 0, "", err
	}
	if missing := videoReplicationRequirement(project.VideoType, params, assets); missing != "" {
		return 0, "", BadAuthRequest(missing)
	}
	if err := s.videoReplicationModelAllowed(params); err != nil {
		return 0, "", err
	}
	mentions, err := videoReplicationMentions(project.Prompt, assets)
	if err != nil {
		return 0, "", err
	}
	references := videoReplicationReferences(project.VideoType, params, assets, mentions)
	if len(references) == 0 && len(videoReplicationTargets(project.VideoType)) > 0 {
		return 0, "", BadAuthRequest("所选视频模型不支持参考图输入，无法完成商品/人物替换。请换用支持图生视频或参考图生视频的模型")
	}
	// 替换音频：参考音色随每个片段一起发送，并在提示词里绑定到画面人物；模型收不了参考音频时直接拒绝，不静默换成随机音色。
	voices := []string{}
	if plan.AudioMode == videoReplicationAudioNative {
		voices = videoReplicationVoiceAudios(assets, videoReplicationMaxVoiceAudios)
		// 用户没上传参考音色：用一键生成时从原视频截出的说话人人声，新台词保持原视频的音色。
		if len(voices) == 0 && plan.SourceVoice != "" && params.ReferenceMode == "reference" && params.MaxReferenceAudios > 0 {
			voices = []string{plan.SourceVoice}
		}
		if len(voices) > 0 && (params.ReferenceMode != "reference" || params.MaxReferenceAudios <= 0) {
			return 0, "", BadAuthRequest("所选视频模型不支持参考音频，成片无法使用你上传的参考音色；请换用支持参考音频的模型（如 Seedance 2.0），或把声音改为保留原声 / 静音")
		}
		if len(voices) > params.MaxReferenceAudios {
			return 0, "", BadAuthRequest(fmt.Sprintf("所选视频模型最多接收 %d 段参考音频，你上传了 %d 段参考音色；请删掉多余的，或换用支持更多参考音频的模型", params.MaxReferenceAudios, len(voices)))
		}
	}
	runs, err := s.repo.VideoReplicationRuns(userID, projectID)
	if err != nil {
		return 0, "", err
	}
	succeeded := map[string]model.VideoReplicationShotRun{}
	active := map[string]bool{}
	for _, run := range runs {
		if run.Kind == "voice" {
			continue
		}
		if run.Status == model.VideoReplicationRunSucceeded && run.OutputResourceID != "" {
			if current, ok := succeeded[run.ShotID]; !ok || run.Selected || (!current.Selected && run.Version > current.Version) {
				succeeded[run.ShotID] = run
			}
		}
		if videoReplicationRunActive(run.Status) {
			active[run.ShotID] = true
		}
	}
	wanted := map[string]bool{}
	for _, id := range shotIDs {
		wanted[strings.TrimSpace(id)] = true
	}
	brief := videoReplicationReadablePrompt(project.Prompt, assets)
	negative := videoReplicationKnowledgeNegative(storyboard.KnowledgeID)
	targets := []videoReplicationTarget{}
	if plan.Strategy == VideoReplicationStrategyNative {
		_, done := succeeded[videoReplicationNativeShotID]
		if (!done || regenerate) && !active[videoReplicationNativeShotID] {
			targets = append(targets, videoReplicationTarget{shotID: videoReplicationNativeShotID, seconds: plan.TargetSeconds, prompt: videoReplicationWithUserDirective(videoReplicationNativePrompt(project.VideoType, params, *storyboard, plan.TargetSeconds, "", references), brief, storyboard.Changes)})
		}
	} else {
		chain := plan.Continuity == "tail_frame"
		for index, shot := range storyboard.Shots {
			if chain && len(active) > 0 {
				break
			}
			if len(wanted) > 0 && !wanted[shot.ID] {
				continue
			}
			if _, done := succeeded[shot.ID]; (done && !regenerate) || active[shot.ID] {
				continue
			}
			shotPrompt := videoReplicationShotPrompt(project.VideoType, params, shot, "", references)
			if videoReplicationHasTarget(project.VideoType, VideoReplicationTypeProduct) && len(references) > 0 {
				// 逐段生成同样按存在感约束商品大小，并排除商品图里的布景，免得每段都变成商品图的翻拍。
				if rule := videoReplicationPresenceRule(normalizeVideoReplicationPresence(shot.ProductPresence), "参考视频"); rule != "" {
					shotPrompt += "\n" + rule
				}
				shotPrompt += "\n" + videoReplicationReferenceOnlyRule("商品参考图", *storyboard)
			}
			target := videoReplicationTarget{shotID: shot.ID, index: index, seconds: snapVideoReplicationSeconds(shot.DurationSec, params.ShotSeconds), prompt: videoReplicationWithUserDirective(strings.ToValidUTF8(clipText(shotPrompt, 3800), ""), brief, storyboard.Changes)}
			if videoReplicationEditMode(params) && shot.SourceEndMs > shot.SourceStartMs {
				// 原片编辑：不再按文字重画场景，以原视频片段为底片只替换目标。
				target.prompt = videoReplicationEditPrompt(project.VideoType, params, shot, *storyboard, brief, references)
				target.sourceStartMs, target.sourceEndMs = shot.SourceStartMs, shot.SourceEndMs
			}
			if chain && index > 0 {
				previous, ok := succeeded[storyboard.Shots[index-1].ID]
				if !ok {
					// 尾帧衔接必须等上一段完成；这里不提交，等上一段成功后由同步阶段继续。
					break
				}
				target.tailOf = previous.OutputResourceID
			}
			targets = append(targets, target)
			if chain {
				break
			}
		}
	}
	if len(targets) == 0 {
		return 0, "", nil
	}
	spent, err := s.videoReplicationSpentCredits(userID, runs)
	if err != nil {
		return 0, "", err
	}
	submitted := 0
	for _, target := range targets {
		estimate := videoReplicationTaskEstimate(plan, target.seconds)
		if plan.BudgetCredits > 0 && spent+estimate > plan.BudgetCredits+1e-6 {
			reason := fmt.Sprintf("已暂停：已消耗约 %.2f 积分，继续提交将超过确认预算 %.2f 积分；提高预算并重新确认后继续", spent, plan.BudgetCredits)
			_ = s.repo.UpdateVideoReplicationProject(userID, projectID, map[string]any{"paused": true, "pause_reason": reason})
			return submitted, reason, nil
		}
		runKey := key + ":" + target.shotID
		if _, err := s.repo.VideoReplicationRunByKey(projectID, runKey); err == nil {
			continue
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return submitted, "", err
		}
		taskReferences := make([]workbench.TaskReference, 0, len(references))
		tailFrame := ""
		if target.tailOf != "" {
			tail, err := s.extractDramaTailFrame(userID, target.tailOf)
			if err != nil {
				return submitted, "上一段尾帧抽取失败：" + err.Error(), nil
			}
			tailFrame = tail
			taskReferences = append(taskReferences, workbench.TaskReference{ResourceID: tail, Kind: "image", Role: "first_frame"})
		} else {
			if target.sourceEndMs > target.sourceStartMs {
				clip, err := s.videoReplicationSourceClip(project, params, target.shotID, target.sourceStartMs, target.seconds)
				if err != nil {
					return submitted, "截取原视频片段失败：" + err.Error(), nil
				}
				taskReferences = append(taskReferences, workbench.TaskReference{ResourceID: clip, Kind: "video", Role: "reference"})
			}
			for _, reference := range references {
				taskReferences = append(taskReferences, workbench.TaskReference{ResourceID: reference["resourceId"], Kind: "image", Role: reference["role"]})
			}
			for _, voice := range voices {
				taskReferences = append(taskReferences, workbench.TaskReference{ResourceID: voice, Kind: "audio", Role: "reference"})
			}
		}
		prompt := target.prompt
		if negative != "" {
			prompt = strings.ToValidUTF8(clipText(prompt, 3800-len(negative)-1), "") + "\n" + negative
		}
		if plan.AudioMode == videoReplicationAudioNative {
			sentVoices := len(voices)
			if target.tailOf != "" {
				sentVoices = 0
			}
			// 不勾选「保留原声」也没传参考音色的普通视频：模型按画面重新生成声音，不强行让人物说原台词；口播视频照常说原台词。
			lines := ""
			if params.AudioMode != "generate" || params.Talking || plan.SpeakLines {
				lines = videoReplicationTargetLines(*storyboard, target)
			}
			sound := videoReplicationSound{Edit: target.sourceEndMs > target.sourceStartMs, Music: videoReplicationMusicNote(project), SpeechSegment: params.AudioMode == "replace" || params.Talking || plan.SpeakLines, Stems: plan.Instrumental != "", SourceVoice: sentVoices > 0 && len(videoReplicationVoiceAudios(assets, 1)) == 0, VoiceHint: plan.VoiceHint, Subtitled: storyboard.Subtitles}
			prompt = videoReplicationWithVoicePrompt(prompt, lines, sentVoices, references, sound)
		} else if plan.AudioMode == videoReplicationAudioOriginal && params.Talking {
			prompt = videoReplicationWithLipSyncPrompt(prompt, videoReplicationTargetLines(*storyboard, target))
		}
		runParams, _ := json.Marshal(videoReplicationRunParameters{Seconds: target.seconds, Ratio: params.Ratio, Resolution: params.Resolution, Model: params.Model, ModelLabel: params.ModelLabel, VideoType: project.VideoType, References: references, Mentions: mentions, Strategy: plan.Strategy, TailFrame: tailFrame})
		run := &model.VideoReplicationShotRun{ID: newID(), ProjectID: projectID, UserID: userID, ShotID: target.shotID, IdempotencyKey: runKey, Status: model.VideoReplicationRunSubmitting, Prompt: prompt, ParametersJSON: string(runParams)}
		created, err := s.repo.ReserveVideoReplicationRun(run)
		if err != nil {
			if existing, lookupErr := s.repo.VideoReplicationRunByKey(projectID, runKey); lookupErr == nil && existing != nil {
				continue
			}
			return submitted, "", err
		}
		if !created {
			continue
		}
		parameters := map[string]any{"seconds": target.seconds, "generateAudio": params.GenerateAudio && plan.AudioMode == videoReplicationAudioNative, "watermark": params.Watermark}
		if params.Ratio != "" {
			parameters["ratio"] = params.Ratio
		}
		if params.Resolution != "" {
			parameters["resolution"] = params.Resolution
		}
		request := WorkbenchTaskInput{Kind: WorkbenchKindVideo, Prompt: prompt, Model: params.Model, Parameters: parameters, References: taskReferences}
		if target.sourceEndMs > target.sourceStartMs && target.tailOf == "" {
			// 原片编辑：TopView 上的 Seedance 会把「以原片为底只替换」判为视频编辑，此时时长必须为 -1（跟随参考片段），
			// 发正整数会在排队数分钟后失败。只写 TopView 命名空间，Lec 等只收正整数时长的渠道不受影响；
			// 片段本身按 seconds 截取，计费秒数不变。
			request.ProviderOptions = map[string]map[string]any{videoReplicationTopViewProtocol: {"duration": -1}}
		}
		if params.LogicalModelID != "" {
			request.LogicalModelID = params.LogicalModelID
		} else {
			request.ChannelID = params.ChannelID
		}
		// RequestID 取镜头版本 ID：进程在建任务后、写回 TaskID 前中断时，同步阶段据此找回任务，避免误判为未提交。
		task, err := s.CreateWorkbenchTask(userID, request, traceID, run.ID)
		if err != nil {
			message := err.Error()
			_ = s.repo.UpdateVideoReplicationRun(run.ID, map[string]any{"status": model.VideoReplicationRunFailed, "error": "提交失败（未扣费）：" + clipText(message, 400)})
			// 积分不足、并发上限、模型不可用时后续任务大概率同样失败：停止提交，由用户处理后重试。
			label := "完整视频"
			if target.shotID != videoReplicationNativeShotID {
				label = fmt.Sprintf("片段 %d", target.index+1)
			}
			return submitted, label + "：" + message, nil
		}
		submitted++
		spent += estimate
		_ = s.repo.UpdateVideoReplicationRun(run.ID, map[string]any{"task_id": task.ID, "status": videoReplicationRunStatus(task.Status)})
	}
	return submitted, "", nil
}

func videoReplicationRunStatus(status model.TaskStatus) string {
	switch status {
	case model.TaskStatusQueued:
		return model.VideoReplicationRunQueued
	case model.TaskStatusRunning:
		return model.VideoReplicationRunRunning
	case model.TaskStatusSucceeded:
		return model.VideoReplicationRunSucceeded
	case model.TaskStatusCancelled:
		return model.VideoReplicationRunCancelled
	}
	return model.VideoReplicationRunFailed
}

// videoReplicationReferences 按视频类型挑选参考图：被 @ 引用的素材优先，其次主素材，再按顺序；
// 数量与角色受所选模型能力约束（参考图生视频 / 首帧图生视频）。
func videoReplicationReferences(videoType string, params VideoReplicationParameters, assets []model.VideoReplicationAsset, mentions []VideoReplicationMention) []map[string]string {
	if params.ReferenceMode == "none" || params.ReferenceMode == "" {
		return nil
	}
	mentioned := map[string]bool{}
	for _, mention := range mentions {
		mentioned[mention.AssetID] = true
	}
	ordered := func(category string) []VideoReplicationAssetView {
		items := []VideoReplicationAssetView{}
		for _, view := range videoReplicationAssetViews(assets) {
			if view.Category == category {
				items = append(items, view)
			}
		}
		rank := func(view VideoReplicationAssetView) int {
			if mentioned[view.ID] {
				return 0
			}
			if view.IsPrimary {
				return 1
			}
			return 2
		}
		for i := 1; i < len(items); i++ {
			for j := i; j > 0 && rank(items[j]) < rank(items[j-1]); j-- {
				items[j], items[j-1] = items[j-1], items[j]
			}
		}
		return items
	}
	targets := videoReplicationTargets(videoType)
	limit := params.MaxReferenceImages
	role := "reference"
	if params.ReferenceMode == "first_frame" {
		limit, role = 1, "first_frame"
	}
	if limit <= 0 {
		limit = 1
	}
	// 多个替换目标时轮流取图：先保证每个目标至少一张（被 @ 引用的、主素材优先），再补齐到模型上限。
	queues := make([][]VideoReplicationAssetView, 0, len(targets))
	for _, target := range targets {
		queues = append(queues, ordered(videoReplicationTargetCategory[target]))
	}
	// 替换商品时，细节 / 拆解图和包装图排在所有替换目标之后参与轮流，名额不够时先让给替换目标本身的图。
	if containsString(targets, VideoReplicationTypeProduct) && role == "reference" {
		queues = append(queues, ordered(model.VideoReplicationAssetProductDetailImage), ordered(model.VideoReplicationAssetPackagingImage))
	}
	references := []map[string]string{}
	for round := 0; len(references) < limit; round++ {
		added := false
		for _, queue := range queues {
			if round < len(queue) && len(references) < limit {
				candidate := queue[round]
				references = append(references, map[string]string{"assetId": candidate.ID, "resourceId": candidate.ResourceID, "label": candidate.Label, "category": candidate.Category, "role": role})
				added = true
			}
		}
		if !added {
			break
		}
	}
	return references
}

// videoReplicationUserDirective 是所有复刻提示词（原片编辑、逐段生成、整片直出，任何品类）共用的用户要求段：
// 用户补充要求原文 + 分段规划整理出的画面改动清单，声明为最高优先级，压过「保持不变 / 与原片一致 / 其余物品保持原样」等通用规则。
// source 是原片在提示词里的称呼（原片编辑为 @视频1）。都为空时返回空串。
func videoReplicationUserDirective(brief, changes, source string) string {
	brief, changes = strings.TrimSpace(brief), strings.TrimSpace(changes)
	if brief == "" && changes == "" {
		return ""
	}
	if source == "" {
		source = "原视频"
	}
	var b strings.Builder
	if brief != "" {
		b.WriteString("用户补充要求（最高优先级，必须执行；与下文任何「保持不变 / 与原视频一致 / 保持原样」的规则冲突时以它为准）：" + brief + "\n")
	}
	if changes != "" {
		b.WriteString("画面改动清单（必须执行）：" + changes + "。" + source + "里对应的内容每一帧都按清单改干净，不残留原来的元素、配色和风格；被改元素的位置、大小比例、运动轨迹以及与人物的互动沿用" + source + "中的对应元素，光线氛围与改动后的画面协调统一。\n")
	}
	return b.String()
}

// videoReplicationWithUserDirective 把用户要求段放在逐段生成 / 整片直出提示词的开头：超长截断时先截掉末尾的通用细则，不是用户的要求。
func videoReplicationWithUserDirective(prompt, brief, changes string) string {
	directive := videoReplicationUserDirective(brief, changes, "")
	if directive == "" {
		return prompt
	}
	return strings.ToValidUTF8(clipText(directive+prompt, 3800), "")
}

func videoReplicationShotPrompt(videoType string, params VideoReplicationParameters, shot VideoReplicationShot, brief string, references []map[string]string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(shot.Prompt))
	language := []string{}
	for _, item := range [][2]string{{"景别", shot.ShotSize}, {"运镜", firstNonEmpty(shot.Camera, params.Camera)}, {"光线", firstNonEmpty(shot.Lighting, params.Lighting)}, {"氛围", firstNonEmpty(shot.Mood, params.Mood)}, {"节奏", params.Rhythm}} {
		if strings.TrimSpace(item[1]) != "" {
			language = append(language, item[0]+"："+strings.TrimSpace(item[1]))
		}
	}
	if len(language) > 0 {
		b.WriteString("\n镜头语言：" + strings.Join(language, "；") + "。")
	}
	labels := make([]string, 0, len(references))
	for index, reference := range references {
		labels = append(labels, fmt.Sprintf("参考图%d=%s", index+1, reference["label"]))
	}
	if len(labels) > 0 {
		b.WriteString("\n参考图：" + strings.Join(labels, "，") + "。")
	}
	rules := map[string]string{
		VideoReplicationTypeProduct:    "画面中的商品必须与商品参考图完全一致，保持外观、结构、颜色与细节，不得变形或替换为其他商品。",
		VideoReplicationTypeModel:      "画面人物必须是模特参考图中的人物，保持面部、发型与身份一致。",
		VideoReplicationTypeApparel:    "人物穿着的服装必须与服饰参考图一致，保持款式、颜色、面料与细节；正常合身地穿好，不穿模、不走光、无原服装残留；新服装没有的口袋、拉链等结构不出现，原片针对它们的动作改为自然动作；未给出的单品搭配风格颜色协调的简洁款。",
		VideoReplicationTypeFace:       "画面人物面部必须与人脸参考图保持一致。",
		VideoReplicationTypeBackground: "场景与背景必须换成背景参考图中的环境，人物、商品、动作与运镜保持与原视频一致，光线和透视与新背景匹配。",
	}
	for _, target := range videoReplicationTargets(videoType) {
		b.WriteString("\n" + rules[target])
	}
	if videoReplicationHasTarget(videoType, VideoReplicationTypeModel) || videoReplicationHasTarget(videoType, VideoReplicationTypeFace) {
		source := "参考图只提供人物长相，不照搬图中的表情、姿势和头部角度"
		// 首帧模型：人物照片就是开场画面，只能要求从这一帧开始动起来，不能要求不用照片里的姿势。
		if len(references) > 0 && references[0]["role"] == "first_frame" && (references[0]["category"] == model.VideoReplicationAssetModelImage || references[0]["category"] == model.VideoReplicationAssetFaceImage) {
			source = "参考图是开场画面，人物从这一帧开始自然动起来，不要一直保持照片上的表情和姿势"
		}
		b.WriteString("\n表情：" + source + "；人物表情随动作和情绪自然变化，自然眨眼、转动眼神，" + videoReplicationNaturalFaceRule)
	}
	if hint := videoReplicationReplaceModeRule(params); hint != "" {
		b.WriteString("\n" + hint)
	}
	if rule := videoReplicationRegionRule(params); rule != "" {
		b.WriteString("\n" + rule)
	}
	if adaptation := strings.TrimSpace(shot.Adaptation); adaptation != "" {
		b.WriteString("\n结构：本段按新商品 / 新服装结构改写动作与镜头：" + adaptation + "；不要画出新商品 / 新服装没有的部件。")
	}
	if shot.Packaging && videoReplicationHasTarget(videoType, VideoReplicationTypeProduct) {
		packaging := ""
		for index, reference := range references {
			if reference["category"] == model.VideoReplicationAssetPackagingImage {
				packaging = fmt.Sprintf("参考图%d", index+1)
				break
			}
		}
		b.WriteString("\n" + videoReplicationPackagingRule(packaging))
	}
	if params.KeepOriginalAudio || params.AudioMode == "keep" {
		b.WriteString("\n节奏：成片沿用原视频的音乐，人物动作、转身、商品展示和镜头切换的时间点与原视频一致，卡准原音乐节拍，不加快也不放慢。")
	}
	if params.Talking {
		b.WriteString("\n口播：这是口播视频，人物面向镜头自然开口说话，表情、手势与说话节奏跟随原视频。")
	}
	if strings.TrimSpace(brief) != "" {
		b.WriteString("\n用户要求：" + strings.TrimSpace(brief))
	}
	b.WriteString("\n不要出现字幕、水印或其他品牌标识。")
	return strings.ToValidUTF8(clipText(b.String(), 3800), "")
}

// syncVideoReplication 把镜头版本与成片版本对齐到任务的真实状态，并据此刷新项目状态。
func (s *Service) syncVideoReplication(ctx context.Context, project *model.VideoReplicationProject) error {
	runs, err := s.repo.VideoReplicationRuns(project.UserID, project.ID)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, run := range runs {
		if run.Selected {
			selected[run.Kind+":"+run.ShotID] = true
		}
	}
	active, done := 0, 0
	for index := range runs {
		run := &runs[index]
		// A failed delivery may be restored on its original task. Reconcile failed
		// runs too, so a recovered task becomes visible instead of submitting a new
		// paid generation. Explicitly cancelled runs retain the user's choice.
		if !videoReplicationRunActive(run.Status) && run.Status != model.VideoReplicationRunFailed {
			if run.Status == model.VideoReplicationRunSucceeded {
				done++
			}
			continue
		}
		var task *model.Task
		previousStatus, previousTaskID := run.Status, run.TaskID
		if run.TaskID == "" {
			if time.Since(run.CreatedAt) < videoReplicationSubmitGrace {
				active++
				continue
			}
			found, lookupErr := s.repo.TaskForUserByRequestID(project.UserID, run.ID)
			if lookupErr != nil {
				run.Status = model.VideoReplicationRunFailed
				_ = s.repo.UpdateVideoReplicationRun(run.ID, map[string]any{"status": run.Status, "error": "提交中断，未创建生成任务（未扣费），请重新生成"})
				continue
			}
			task = found
			run.TaskID = task.ID
		} else {
			found, lookupErr := s.repo.TaskForUser(project.UserID, run.TaskID)
			if lookupErr != nil {
				run.Status = model.VideoReplicationRunFailed
				_ = s.repo.UpdateVideoReplicationRun(run.ID, map[string]any{"status": run.Status, "error": "生成任务记录不存在"})
				continue
			}
			task = found
		}
		status := videoReplicationRunStatus(task.Status)
		values := map[string]any{"task_id": task.ID, "status": status}
		switch status {
		case model.VideoReplicationRunSucceeded:
			values["error"] = ""
			resourceID := dramaTaskResource(task)
			if resourceID == "" {
				status = model.VideoReplicationRunFailed
				values["status"] = status
				values["error"] = "任务已完成但没有返回视频文件"
				break
			}
			values["output_resource_id"] = resourceID
			if !selected[run.Kind+":"+run.ShotID] {
				values["selected"] = true
				selected[run.Kind+":"+run.ShotID] = true
			}
			done++
		case model.VideoReplicationRunFailed, model.VideoReplicationRunCancelled:
			values["error"] = clipText(firstNonEmpty(task.Error, task.Stage, "生成失败"), 600)
		default:
			values["error"] = ""
			active++
		}
		if status != previousStatus || task.ID != previousTaskID {
			_ = s.repo.UpdateVideoReplicationRun(run.ID, values)
		}
	}
	plan := videoReplicationPlan(project)
	planValid := plan != nil && project.PlanConfirmedAt != nil && plan.Digest == videoReplicationDigest(project) && plan.StoryboardRevision == project.StoryboardRevision
	storyboard := videoReplicationStoryboard(project)
	// 原生直出：整片任务成功后，模型返回的视频直接成为成片版本，不经过 FFmpeg 剪辑；随后只读质量检查。
	if plan != nil && plan.Strategy == VideoReplicationStrategyNative && plan.AudioMode != videoReplicationAudioTTS {
		latest, err := s.repo.VideoReplicationRuns(project.UserID, project.ID)
		if err != nil {
			return err
		}
		for _, run := range latest {
			if run.ShotID != videoReplicationNativeShotID || run.Status != model.VideoReplicationRunSucceeded || run.OutputResourceID == "" {
				continue
			}
			key := "native:" + run.ID
			if _, err := s.repo.VideoReplicationOutputByKey(project.ID, key); err == nil {
				continue
			}
			runIDs, _ := json.Marshal([]string{run.ID})
			_ = s.repo.CreateVideoReplicationOutput(&model.VideoReplicationOutput{ID: newID(), ProjectID: project.ID, UserID: project.UserID, IdempotencyKey: key, Mode: VideoReplicationStrategyNative, TaskID: run.TaskID, Status: "checking", ResourceID: run.OutputResourceID, RunIDsJSON: string(runIDs), DurationMs: int64(plan.TargetSeconds) * 1000})
		}
	}
	// 智能分段 + 尾帧衔接：上一段成功后自动提交下一段（仍受预算检查）。失败过的片段不自动重试，由用户手动重新生成。
	if planValid && plan.Strategy == VideoReplicationStrategySegmented && plan.Continuity == "tail_frame" && !project.Paused && active == 0 && project.Status != model.VideoReplicationStatusAnalyzing && storyboard != nil {
		latest, err := s.repo.VideoReplicationRuns(project.UserID, project.ID)
		if err != nil {
			return err
		}
		tried := map[string]bool{}
		done := map[string]bool{}
		for _, run := range latest {
			if run.Kind == "voice" {
				continue
			}
			tried[run.ShotID] = true
			if run.Status == model.VideoReplicationRunSucceeded {
				done[run.ShotID] = true
			}
		}
		for index, shot := range storyboard.Shots {
			if done[shot.ID] {
				continue
			}
			if index > 0 && !tried[shot.ID] && done[storyboard.Shots[index-1].ID] {
				submitted, failure, err := s.submitVideoReplication(project, plan, fmt.Sprintf("chain-%d", plan.ConfirmedAt.Unix()), []string{shot.ID}, false, "")
				if err != nil {
					failure = err.Error()
				}
				active += submitted
				if submitted == 0 && failure != "" && !strings.HasPrefix(failure, "已暂停") {
					_ = s.repo.UpdateVideoReplicationProject(project.UserID, project.ID, map[string]any{"paused": true, "pause_reason": "自动续接下一段失败：" + clipText(failure, 240)})
				}
				if refreshed, err := s.repo.VideoReplicationProject(project.UserID, project.ID); err == nil {
					project.Paused, project.PauseReason = refreshed.Paused, refreshed.PauseReason
				}
			}
			break
		}
	}
	// 一键生成：任一段失败立即把整个项目标记为失败，并写明是哪一段、什么原因；
	// 其它段还在生成时也立即报错，不让前端继续等待。
	params := videoReplicationParameters(project)
	autoFailure := ""
	if planValid && params.Auto && storyboard != nil && !project.Paused && project.Status != model.VideoReplicationStatusAnalyzing {
		retried, failure := s.retryVideoReplicationAuto(project, plan, storyboard, active > 0)
		active += retried
		autoFailure = failure
	}
	// 替换音频且分离出了原片伴奏：每段一生成完就开始在后台分离人声，等全部完成时多数段已经分离好。
	if planValid && plan.Instrumental != "" {
		if latest, err := s.repo.VideoReplicationRuns(project.UserID, project.ID); err == nil {
			chosen, _ := videoReplicationChosenRuns(latest)
			s.videoReplicationSegmentVocals(project.UserID, chosen)
		}
	}
	// 全部片段（与配音）成功后自动合成完整视频，用户不需要再点合成；某段重新生成后版本变化会再合成一次。
	if planValid && storyboard != nil && active == 0 && !project.Paused && (plan.Strategy == VideoReplicationStrategySegmented || plan.AudioMode == videoReplicationAudioTTS) {
		if latest, err := s.repo.VideoReplicationRuns(project.UserID, project.ID); err == nil && videoReplicationComposeReady(*storyboard, plan, latest) == "" {
			if err := s.composeVideoReplicationOutput(project, plan, videoReplicationAutoComposeKey(*storyboard, plan, latest)); err != nil && err != errVideoReplicationStemsPending {
				slog.Warn("video replication auto compose failed", "projectId", project.ID, "error", err)
			}
		}
	}
	outputs, err := s.repo.VideoReplicationOutputs(project.UserID, project.ID)
	if err != nil {
		return err
	}
	wantAudio := plan != nil && (plan.AudioMode == videoReplicationAudioNative || plan.AudioMode == videoReplicationAudioOriginal || plan.AudioMode == videoReplicationAudioTTS)
	composing, checking := false, false
	for index := range outputs {
		output := &outputs[index]
		if (output.Status == "queued" || output.Status == "running") && output.TaskID != "" {
			task, lookupErr := s.repo.TaskForUser(project.UserID, output.TaskID)
			if lookupErr != nil {
				output.Status = "failed"
				_ = s.repo.UpdateVideoReplicationOutput(output.ID, map[string]any{"status": "failed", "error": "合成任务记录不存在"})
				continue
			}
			values := map[string]any{"status": string(task.Status)}
			switch task.Status {
			case model.TaskStatusSucceeded:
				var result timelineRenderResult
				if json.Unmarshal([]byte(task.ResultJSON), &result) != nil || result.ResourceID == "" {
					values["status"], values["error"] = "failed", "合成完成但没有返回成片文件"
				} else {
					// 渲染成功不等于成片合格：先进入质量检查。
					values["status"], values["resource_id"], values["subtitle_srt"], values["duration_ms"] = "checking", result.ResourceID, result.SubtitleSRT, result.DurationMs
					output.ResourceID, output.DurationMs = result.ResourceID, result.DurationMs
				}
			case model.TaskStatusFailed, model.TaskStatusCancelled:
				values["error"] = clipText(firstNonEmpty(task.Error, task.Stage, "合成失败"), 600)
			}
			output.Status = values["status"].(string)
			_ = s.repo.UpdateVideoReplicationOutput(output.ID, values)
		}
		if output.Status == "checking" || output.Status == "inspecting" {
			checking = true
			s.runVideoReplicationQuality(project.UserID, *output, wantAudio)
		}
		if output.Status == "queued" || output.Status == "running" {
			composing = true
		}
	}
	if project.Status == model.VideoReplicationStatusAnalyzing {
		return nil
	}
	native := plan != nil && plan.Strategy == VideoReplicationStrategyNative
	latestStatus := ""
	if len(outputs) > 0 {
		latestStatus = outputs[0].Status
	}
	next, stage, progress := project.Status, project.Stage, project.Progress
	switch {
	case autoFailure != "":
		next, stage, progress = model.VideoReplicationStatusFailed, clipText(autoFailure, 300), 100
	case active > 0:
		next = model.VideoReplicationStatusGenerating
		if native {
			stage, progress = "完整视频生成中（原生直出，1 个任务）", 50
		} else {
			stage = fmt.Sprintf("片段生成中：%d 个进行中", active)
			if storyboard != nil && len(storyboard.Shots) > 0 {
				videoDone := 0
				for key := range selected {
					if strings.HasPrefix(key, "video:") {
						videoDone++
					}
				}
				progress = min(99, videoDone*100/len(storyboard.Shots))
				if params.Auto {
					stage = fmt.Sprintf("视频生成中：已完成 %d/%d 段", videoDone, len(storyboard.Shots))
				}
			}
		}
	case composing:
		next, stage, progress = model.VideoReplicationStatusComposing, "自动合成中（FFmpeg 拼接片段）", 85
	case checking:
		next, stage, progress = model.VideoReplicationStatusComposing, "质量检查中", 95
	case latestStatus == "succeeded":
		next, stage, progress = model.VideoReplicationStatusCompleted, "完整视频已完成", 100
	case latestStatus == "quality_failed":
		next, stage, progress = model.VideoReplicationStatusFailed, "成片质量检查未通过："+clipText(outputs[0].Error, 200), 100
	case project.Paused:
		next, stage = model.VideoReplicationStatusAnalyzed, project.PauseReason
	case project.Status == model.VideoReplicationStatusFailed && latestStatus == "":
	case storyboard != nil:
		next = model.VideoReplicationStatusAnalyzed
		if params.Auto && planValid {
			stage = "正在提交生成任务"
		} else if done > 0 {
			stage = fmt.Sprintf("已生成 %d 个片段", done)
		} else {
			stage = "分镜已生成，等待确认方案"
		}
		progress = 100
	default:
		next, stage, progress = model.VideoReplicationStatusDraft, "", 0
	}
	if next != project.Status || stage != project.Stage || progress != project.Progress {
		project.Status, project.Stage, project.Progress = next, stage, progress
		values := map[string]any{"status": next, "stage": stage, "progress": progress}
		if autoFailure != "" {
			values["error"] = clipText(autoFailure, 600)
		}
		return s.repo.UpdateVideoReplicationProject(project.UserID, project.ID, values)
	}
	return nil
}

func (s *Service) SelectVideoReplicationRun(userID, projectID, runID string) (*VideoReplicationProjectView, error) {
	if err := s.requireVideoReplication(); err != nil {
		return nil, err
	}
	if err := s.repo.SelectVideoReplicationRun(userID, projectID, runID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("镜头版本不存在")
		}
		return nil, BadAuthRequest(err.Error())
	}
	return s.VideoReplicationProject(userID, projectID)
}

// CancelVideoReplication 取消进行中的镜头与合成任务；费用按任务系统规则结算（未执行的预扣退回）。
func (s *Service) CancelVideoReplication(ctx context.Context, userID, projectID string) (*VideoReplicationProjectView, error) {
	if err := s.requireVideoReplication(); err != nil {
		return nil, err
	}
	project, err := s.repo.VideoReplicationProject(userID, projectID)
	if err != nil {
		return nil, videoReplicationNotFound(err)
	}
	if err := s.syncVideoReplication(ctx, project); err != nil {
		return nil, err
	}
	runs, err := s.repo.VideoReplicationRuns(userID, projectID)
	if err != nil {
		return nil, err
	}
	var failures []string
	for _, run := range runs {
		if !videoReplicationRunActive(run.Status) || run.TaskID == "" {
			continue
		}
		if _, err := s.CancelTask(ctx, userID, run.TaskID); err != nil {
			failures = append(failures, err.Error())
		}
	}
	outputs, err := s.repo.VideoReplicationOutputs(userID, projectID)
	if err != nil {
		return nil, err
	}
	for _, output := range outputs {
		if (output.Status == "queued" || output.Status == "running") && output.TaskID != "" {
			if _, err := s.CancelTask(ctx, userID, output.TaskID); err != nil {
				failures = append(failures, err.Error())
			}
		}
	}
	view, err := s.VideoReplicationProject(userID, projectID)
	if err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		return nil, BadAuthRequest("部分任务取消失败：" + failures[0])
	}
	return view, nil
}

func videoReplicationCanvasSize(ratio, resolution string) (int, int) {
	short := 720
	if strings.HasPrefix(strings.TrimSpace(resolution), "1080") {
		short = 1080
	} else if strings.HasPrefix(strings.TrimSpace(resolution), "480") {
		short = 480
	}
	parts := strings.Split(strings.TrimSpace(ratio), ":")
	if len(parts) != 2 {
		return short * 16 / 9 &^ 1, short
	}
	var w, h float64
	if _, err := fmt.Sscanf(parts[0]+" "+parts[1], "%g %g", &w, &h); err != nil || w <= 0 || h <= 0 {
		return short * 16 / 9 &^ 1, short
	}
	if w >= h {
		return int(float64(short)*w/h) &^ 1, short
	}
	return short, int(float64(short)*h/w) &^ 1
}

// videoReplicationChosenRuns 返回每个分镜采用的画面版本（采用版本优先，否则最新成功版本）与每个分镜的配音版本。
func videoReplicationChosenRuns(runs []model.VideoReplicationShotRun) (map[string]model.VideoReplicationShotRun, map[string]model.VideoReplicationShotRun) {
	video := map[string]model.VideoReplicationShotRun{}
	voice := map[string]model.VideoReplicationShotRun{}
	for _, run := range runs {
		if run.Status != model.VideoReplicationRunSucceeded || run.OutputResourceID == "" {
			continue
		}
		target := video
		if run.Kind == "voice" {
			target = voice
		}
		if current, ok := target[run.ShotID]; !ok || run.Selected || (!current.Selected && run.Version > current.Version) {
			target[run.ShotID] = run
		}
	}
	return video, voice
}

type videoReplicationMedia = struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	StorageKey string `json:"storageKey"`
}

func videoReplicationMediaRef(id, kind string) *videoReplicationMedia {
	return &videoReplicationMedia{ID: id, Kind: kind, StorageKey: "resource:" + id}
}

// buildVideoReplicationTimeline 把画面版本按分镜顺序拼成时间线。
// 分段在参考视频自己的镜头切点处衔接，拼接处就是原片的硬切；配音按镜头起点叠加成一条连续音轨，
// 有配音时画面片段静音，避免每段模型音频在切点处断开。native 方案只有一个整片片段。
func buildVideoReplicationTimeline(storyboard VideoReplicationStoryboard, params VideoReplicationParameters, plan *VideoReplicationPlan, runs []model.VideoReplicationShotRun, durationOf func(resourceID string) int64) (renderProject, []string, []string) {
	video, voice := videoReplicationChosenRuns(runs)
	width, height := videoReplicationCanvasSize(params.Ratio, params.Resolution)
	timeline := renderProject{Version: 1, Width: width, Height: height, Fit: "crop", Tracks: []renderTrack{{ID: "video", Kind: "video"}, {ID: "subtitle", Kind: "subtitle"}}}
	dubbed := plan != nil && plan.AudioMode == videoReplicationAudioTTS
	if dubbed || (plan != nil && plan.AudioMode == videoReplicationAudioMute) {
		timeline.Tracks[0].Muted = true
	}
	if dubbed {
		timeline.Tracks = append(timeline.Tracks, renderTrack{ID: "voice", Kind: "audio"})
	}
	addVoice := func(shotID string, startMs, maxMs int64) []string {
		run, ok := voice[shotID]
		if !dubbed || !ok {
			return nil
		}
		length := durationOf(run.OutputResourceID)
		if length <= 0 || length > maxMs {
			length = maxMs
		}
		timeline.Clips = append(timeline.Clips, renderClip{ID: "voice-" + shotID, Kind: "audio", TrackID: "voice", StartMs: startMs, DurationMs: length, SourceDurationMs: length, Volume: 1, DirectMedia: videoReplicationMediaRef(run.OutputResourceID, "audio")})
		return []string{run.ID}
	}
	runIDs, missing := []string{}, []string{}
	if plan != nil && plan.Strategy == VideoReplicationStrategyNative {
		run, ok := video[videoReplicationNativeShotID]
		if !ok {
			return timeline, runIDs, []string{"完整视频"}
		}
		total := durationOf(run.OutputResourceID)
		if total <= 0 {
			total = int64(plan.TargetSeconds) * 1000
		}
		timeline.Clips = append(timeline.Clips, renderClip{ID: "full", Kind: "video", TrackID: "video", StartMs: 0, DurationMs: total, SourceDurationMs: total, Volume: 1, DirectMedia: videoReplicationMediaRef(run.OutputResourceID, "video")})
		runIDs = append(runIDs, run.ID)
		// 原生整片按分镜时间轴把每句配音放到对应时间点。
		cursor := int64(0)
		for _, shot := range storyboard.Shots {
			if cursor >= total {
				break
			}
			length := int64(snapVideoReplicationSeconds(shot.DurationSec, nil)) * 1000
			runIDs = append(runIDs, addVoice(shot.ID, cursor, min(length, total-cursor))...)
			cursor += length
		}
		timeline.DurationMs = total
		return timeline, runIDs, nil
	}
	cursor := int64(0)
	for index, shot := range storyboard.Shots {
		run, ok := video[shot.ID]
		if !ok {
			missing = append(missing, fmt.Sprint(index+1))
			continue
		}
		durationMs := int64(snapVideoReplicationSeconds(shot.DurationSec, params.ShotSeconds)) * 1000
		if actual := durationOf(run.OutputResourceID); actual > 0 {
			durationMs = actual
		}
		// 模型只能出整秒的视频（14.77 秒的原片段会生成 15 秒）：按原片段长度截掉多出的尾巴，
		// 让每段在成片里的起点与原视频一致，叠加原音轨时声音和画面不会越拼越错位。
		if span := shot.SourceEndMs - shot.SourceStartMs; span >= videoReplicationMinSegmentSeconds*500 && durationMs > span {
			durationMs = span
		}
		clip := renderClip{ID: "shot-" + shot.ID, Kind: "video", TrackID: "video", StartMs: cursor, DurationMs: durationMs, SourceDurationMs: durationMs, Volume: 1, DirectMedia: videoReplicationMediaRef(run.OutputResourceID, "video")}
		if params.Transition == "fade" {
			clip.FadeInMs, clip.FadeInColor, clip.FadeOutMs, clip.FadeOutColor = 200, "black", 200, "black"
		}
		timeline.Clips = append(timeline.Clips, clip)
		if params.Subtitles && strings.TrimSpace(shot.Subtitle) != "" {
			timeline.Clips = append(timeline.Clips, renderClip{ID: "sub-" + shot.ID, Kind: "subtitle", TrackID: "subtitle", StartMs: cursor, DurationMs: durationMs, Text: shot.Subtitle})
		} else if storyboard.Subtitles && strings.TrimSpace(shot.Voiceover) != "" {
			// 参考视频带字幕：按本段台词断句生成字幕轨，字幕与口播逐字一致。
			for cueIndex, cue := range videoReplicationSubtitleCues(shot.Voiceover, cursor, durationMs) {
				cue.ID = fmt.Sprintf("sub-%s-%d", shot.ID, cueIndex+1)
				timeline.Clips = append(timeline.Clips, cue)
			}
		}
		runIDs = append(runIDs, run.ID)
		runIDs = append(runIDs, addVoice(shot.ID, cursor, durationMs)...)
		cursor += durationMs
	}
	timeline.DurationMs = cursor
	return timeline, runIDs, missing
}

// videoReplicationComposeReady 判断画面（与需要时的配音）是否全部成功、没有进行中的任务；返回缺失说明。
func videoReplicationComposeReady(storyboard VideoReplicationStoryboard, plan *VideoReplicationPlan, runs []model.VideoReplicationShotRun) string {
	video, voice := videoReplicationChosenRuns(runs)
	for _, run := range runs {
		if videoReplicationRunActive(run.Status) {
			return "仍有任务在生成"
		}
	}
	if plan.Strategy == VideoReplicationStrategyNative {
		if _, ok := video[videoReplicationNativeShotID]; !ok {
			return "完整视频还没有生成成功"
		}
	} else {
		for index, shot := range storyboard.Shots {
			if _, ok := video[shot.ID]; !ok {
				return fmt.Sprintf("片段 %d 还没有成功版本", index+1)
			}
		}
	}
	if plan.AudioMode == videoReplicationAudioTTS {
		for index, shot := range storyboard.Shots {
			if strings.TrimSpace(shot.Voiceover) == "" {
				continue
			}
			if _, ok := voice[shot.ID]; !ok {
				return fmt.Sprintf("片段 %d 的配音还没有成功", index+1)
			}
		}
	}
	return ""
}

// composeVideoReplicationOutput 组装时间线并提交渲染任务，同一提交标识只合成一次。
func (s *Service) composeVideoReplicationOutput(project *model.VideoReplicationProject, plan *VideoReplicationPlan, key string) error {
	if _, err := s.repo.VideoReplicationOutputByKey(project.ID, key); err == nil {
		return nil
	}
	storyboard := videoReplicationStoryboard(project)
	if storyboard == nil || len(storyboard.Shots) == 0 {
		return BadAuthRequest("还没有分镜")
	}
	runs, err := s.repo.VideoReplicationRuns(project.UserID, project.ID)
	if err != nil {
		return err
	}
	if reason := videoReplicationComposeReady(*storyboard, plan, runs); reason != "" {
		return BadAuthRequest(reason)
	}
	timeline, runIDs, missing := buildVideoReplicationTimeline(*storyboard, videoReplicationParameters(project), plan, runs, func(resourceID string) int64 {
		if resource, err := s.repo.ResourceForUser(project.UserID, resourceID); err == nil {
			return resource.DurationMs
		}
		return 0
	})
	if len(missing) > 0 {
		return BadAuthRequest("以下片段还没有成功生成的版本：" + strings.Join(missing, "、"))
	}
	// 替换音频 + 原片伴奏：各段只取分离出的新人声，背景音乐用整条原伴奏。
	if plan.Instrumental != "" && plan.AudioMode == videoReplicationAudioNative && plan.Strategy == VideoReplicationStrategySegmented {
		chosen, _ := videoReplicationChosenRuns(runs)
		vocals, pending := s.videoReplicationSegmentVocals(project.UserID, chosen)
		if pending {
			return errVideoReplicationStemsPending
		}
		runByClip := map[string]string{}
		for shotID, run := range chosen {
			runByClip["shot-"+shotID] = run.ID
		}
		applyVideoReplicationStems(&timeline, runByClip, vocals, plan.Instrumental)
	}
	// 保留原声（已确认授权）：片段静音，原参考视频音轨作为整条音频叠加。属于音频后处理，不是模型原生音频。
	if plan.AudioMode == videoReplicationAudioOriginal {
		if audioID := videoReplicationPrimaryAudio(project); audioID != "" {
			timeline.Tracks[0].Muted = true
			timeline.Tracks = append(timeline.Tracks, renderTrack{ID: "original-audio", Kind: "audio"})
			timeline.Clips = append(timeline.Clips, renderClip{ID: "original-audio", Kind: "audio", TrackID: "original-audio", StartMs: 0, DurationMs: timeline.DurationMs, SourceDurationMs: timeline.DurationMs, Volume: 1, DirectMedia: videoReplicationMediaRef(audioID, "audio")})
		}
	}
	task, err := s.CreateTimelineRenderTask(project.UserID, TimelineRenderCreateRequest{Timeline: timeline})
	if err != nil {
		return err
	}
	mode := VideoReplicationStrategySegmented
	if plan.Strategy == VideoReplicationStrategyNative {
		// 原生整片 + 配音后处理：画面仍是一次生成的完整视频，只叠加了配音音轨。
		mode = "native_dubbed"
	}
	runIDsJSON, _ := json.Marshal(runIDs)
	output := &model.VideoReplicationOutput{ID: newID(), ProjectID: project.ID, UserID: project.UserID, IdempotencyKey: key, Mode: mode, TaskID: task.ID, Status: "queued", RunIDsJSON: string(runIDsJSON), DurationMs: timeline.DurationMs}
	if err := s.repo.CreateVideoReplicationOutput(output); err != nil {
		// 并发的同键请求已经写入：本次渲染任务是重复的（不计费），取消它。
		if _, lookupErr := s.repo.VideoReplicationOutputByKey(project.ID, key); lookupErr == nil {
			_, _ = s.CancelTask(context.Background(), project.UserID, task.ID)
			return nil
		}
		return err
	}
	return nil
}

// videoReplicationAutoComposeKey 由参与合成的版本决定：重新生成某一段后版本变化，会自动再合成一次。
func videoReplicationAutoComposeKey(storyboard VideoReplicationStoryboard, plan *VideoReplicationPlan, runs []model.VideoReplicationShotRun) string {
	_, runIDs, _ := buildVideoReplicationTimeline(storyboard, VideoReplicationParameters{}, plan, runs, func(string) int64 { return 0 })
	sum := sha256.Sum256([]byte(strings.Join(runIDs, ",") + "|" + plan.Digest))
	return "auto-" + hex.EncodeToString(sum[:12])
}

// ComposeVideoReplication 手动重新合成（例如切换了某段的采用版本）；一般情况下片段完成后会自动合成。
func (s *Service) ComposeVideoReplication(userID, projectID string, input VideoReplicationComposeInput) (*VideoReplicationProjectView, error) {
	if err := s.requireVideoReplication(); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if !videoReplicationKeyPattern.MatchString(key) {
		return nil, BadAuthRequest("提交标识无效")
	}
	project, err := s.repo.VideoReplicationProject(userID, projectID)
	if err != nil {
		return nil, videoReplicationNotFound(err)
	}
	if err := s.syncVideoReplication(context.Background(), project); err != nil {
		return nil, err
	}
	plan, err := requireConfirmedVideoReplicationPlan(project)
	if err != nil {
		return nil, err
	}
	if plan.Strategy == VideoReplicationStrategyNative && plan.AudioMode != videoReplicationAudioTTS {
		return nil, BadAuthRequest("原生直出由模型一次输出完整视频，不经过剪辑合成")
	}
	if err := s.composeVideoReplicationOutput(project, plan, key); err != nil {
		return nil, err
	}
	return s.VideoReplicationProject(userID, projectID)
}

// videoReplicationNaturalFaceRule 是替换人物 / 人脸时对面部的共同要求（原片编辑与逐段生成都用）：
// 只强调长相一致时，模型容易把整段的脸做成参考照片上那一个表情，显得僵硬。
const videoReplicationNaturalFaceRule = "微笑、说话时嘴部和面部肌肉自然联动，不要僵硬、呆板、面无表情或像贴上去的面具。"

// videoReplicationReplaceModeRule 把电商 Agent 商品视频选择的替换方式写成逐段画面的一致性要求。
func videoReplicationReplaceModeRule(params VideoReplicationParameters) string {
	parts := []string{}
	switch params.ReplaceMode {
	case "precise-product":
		parts = append(parts, "精准商品替换：商品的轮廓、比例、颜色、材质、Logo 与文字位置都必须严格按商品参考图还原，镜头靠近商品时细节清晰，不得出现原视频商品的任何残留。")
	case "precise-person":
		parts = append(parts, "精准人物替换：人物的五官、脸型、发型、肤色、身材比例都必须严格按人物参考图还原，正脸、侧脸与转身镜头保持同一人物，动作与表情跟随原视频。")
	case "combination":
		parts = append(parts, "组合替换：按每张参考图的用途同时替换对应元素，各元素之间的大小比例、遮挡关系和光线要协调统一。")
	}
	if hint := strings.TrimSpace(params.SubjectHint); hint != "" {
		parts = append(parts, "需要替换的是原视频中的「"+hint+"」，其余物品保持不变。")
	}
	return strings.Join(parts, "")
}

// videoReplicationVoiceAudios 返回按顺序排列的参考音色资源，最多 limit 段。
func videoReplicationVoiceAudios(assets []model.VideoReplicationAsset, limit int) []string {
	voices := []model.VideoReplicationAsset{}
	for _, asset := range assets {
		if asset.Category == model.VideoReplicationAssetVoiceAudio {
			voices = append(voices, asset)
		}
	}
	sort.SliceStable(voices, func(i, j int) bool { return voices[i].SortOrder < voices[j].SortOrder })
	ids := []string{}
	for _, voice := range voices {
		if len(ids) >= limit {
			break
		}
		ids = append(ids, voice.ResourceID)
	}
	return ids
}

const (
	// videoReplicationMaxVoiceAudios：替换音频只用 1 段参考音色。多段录音的电平、底噪和语气不一致，模型会在段间混出杂音或忽男忽女。
	videoReplicationMaxVoiceAudios = 1
	// videoReplicationMaxVoiceMs：视频模型参考音频的时长上限（Seedance 2.0 多段合计 15 秒）。
	videoReplicationMaxVoiceMs = 15000
)

// videoReplicationTargetLines 返回这次提交要说的原视频台词：分段取本段口播，整片直出按时间顺序拼接全部口播。
func videoReplicationTargetLines(storyboard VideoReplicationStoryboard, target videoReplicationTarget) string {
	if target.shotID != videoReplicationNativeShotID {
		if target.index >= 0 && target.index < len(storyboard.Shots) {
			return strings.TrimSpace(storyboard.Shots[target.index].Voiceover)
		}
		return ""
	}
	lines := []string{}
	for _, shot := range storyboard.Shots {
		if line := strings.TrimSpace(shot.Voiceover); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " ")
}

// videoReplicationSound 是生成声音时的附加信息。
type videoReplicationSound struct {
	// Edit：原片编辑模式，素材按 Seedance 的 @图片N / @音频N 写法引用。
	Edit bool
	// Music：视频理解对原片背景音乐的描述，用来让模型配出同风格的音乐。
	Music string
	// SpeechSegment：替换音频 / 口播视频，本段没有台词时要明确不开口，避免模型自己编台词、嘴型对不上。
	SpeechSegment bool
	// Stems：成片会叠加原视频分离出的伴奏，本段只要人声，不要生成背景音乐。
	Stems bool
	// SourceVoice：参考音频是从原视频截出的说话人人声（不是用户上传的）。
	SourceVoice bool
	// VoiceHint：原视频说话人的音色描述；没有发送参考音频时用文字保持音色。
	VoiceHint string
	// Subtitled：成片另外叠加与台词一致的字幕轨，画面里不要画字。
	Subtitled bool
}

// videoReplicationMusicNote 返回视频理解对原片背景音乐的描述；没有音乐或识别不出时返回空。
func videoReplicationMusicNote(project *model.VideoReplicationProject) string {
	if project == nil || strings.TrimSpace(project.AnalysisJSON) == "" {
		return ""
	}
	var analysis VideoReplicationAnalysis
	if json.Unmarshal([]byte(project.AnalysisJSON), &analysis) != nil || analysis.Voice == nil {
		return ""
	}
	music := strings.TrimSpace(analysis.Voice.Music)
	for _, none := range []string{"无", "没有", "无背景音乐", "无法判断", "不确定", "unknown"} {
		if music == none {
			return ""
		}
	}
	return videoReplicationClipRunes(music, 50)
}

// videoReplicationWithVoicePrompt 在画面提示词后写明声音要求：参考音频是画面人物（模特 / 人脸参考图里的人）的声音，
// 台词逐字沿用原视频，口型与新生成的人声同步，背景音乐沿用原片风格（用户补充要求换音乐时除外），声音干净无杂音。
// 画面部分过长时只截画面描述，声音与台词要求总是完整保留。
func videoReplicationWithVoicePrompt(prompt, lines string, voiceCount int, references []map[string]string, sound videoReplicationSound) string {
	imageName, audioName := "参考图%d", "参考音频%d"
	if sound.Edit {
		imageName, audioName = "@图片%d", "@音频%d"
	}
	parts := []string{}
	lines = strings.TrimSpace(lines)
	if voiceCount > 0 {
		people := []string{}
		for index, reference := range references {
			if reference["category"] == model.VideoReplicationAssetModelImage || reference["category"] == model.VideoReplicationAssetFaceImage {
				people = append(people, fmt.Sprintf(imageName, index+1))
			}
		}
		speaker := "画面中说话的人物"
		if len(people) > 0 {
			speaker = "画面中的人物（即" + strings.Join(people, "、") + "中的人）"
		}
		names := make([]string, 0, voiceCount)
		for index := 1; index <= voiceCount; index++ {
			names = append(names, fmt.Sprintf(audioName, index))
		}
		audio := strings.Join(names, "、")
		if sound.SourceVoice {
			parts = append(parts, "声音："+audio+" 是原视频说话人的声音，"+speaker+"用这个声音说话，音色、语调、语速、口音与它完全一致，全程同一个声音；只借用音色，不照搬"+audio+"里说的内容，不出现其他人声或旁白。")
		} else {
			parts = append(parts, "声音："+audio+" 是"+speaker+"的声音，说话的音色、语调、语速与它完全一致，全程同一个声音；不用原视频人声，不出现其他人声或旁白。")
		}
	} else if lines != "" && sound.VoiceHint != "" {
		parts = append(parts, "声音：人物的音色与原视频说话人一致（"+sound.VoiceHint+"），全程同一个声音，不出现其他人声或旁白。")
	}
	if lines != "" {
		parts = append(parts, "台词：人物逐字说「"+videoReplicationClipRunes(lines, 400)+"」，不增减改字，语气节奏跟随原视频。")
		parts = append(parts, "口型：嘴型与人声逐字同步，开口、停顿、收尾与声音同时发生；说话人正脸或侧脸可见，嘴部不被商品或手遮挡；不说话时嘴自然合上，面部表情照常自然变化。发音标准清晰，吐字完整，不吞字、不含糊。")
		if sound.Subtitled {
			parts = append(parts, "字幕：画面里不要生成任何字幕或文字，字幕由后期按台词逐字叠加。")
		}
	} else if sound.SpeechSegment {
		parts = append(parts, "本段没有台词：人物不开口说话，不生成人声，表情照常自然变化（可以微笑、眨眼）。")
	}
	music := "背景音乐：沿用原视频背景音乐的风格、节奏和速度"
	if sound.Music != "" {
		music += "（原片：" + sound.Music + "）"
	}
	music += "，各段一致，音量低于人声；补充要求没说换音乐就不换，原视频没有音乐就不加。"
	if sound.Stems {
		music = "背景音乐：不要生成任何背景音乐、配乐和音效，只生成人物说话的干净人声（成片会另外叠加原视频的背景音乐）。"
	}
	parts = append(parts, music)
	parts = append(parts, "音质：干净清晰，无电流声、滋滋声、底噪、爆音、回声或机器人声，音量平稳。")
	suffix := strings.Join(parts, "\n")
	room := 3900 - len(suffix) - 1
	for len(prompt) > room && prompt != "" {
		_, size := utf8.DecodeLastRuneInString(prompt)
		prompt = prompt[:len(prompt)-size]
	}
	return prompt + "\n" + suffix
}

func videoReplicationClipRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
