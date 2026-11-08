package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"yingce/backend/internal/model"
)

// 结构对齐：用户的商品 / 服装和参考视频里的往往结构不同（例如原片吹风机是环形出风口、从手柄底部拔出长滤芯，
// 新吹风机是格栅出风口、尾部旋拧滤网盖；原片外套有侧袋、模特把手插进口袋，新外套没有口袋）。只给模型一句
// 「替换成参考图里的商品 / 服装」时，它会照搬原片的动作，把原商品的部件画到新商品上、让手插进不存在的口袋。
// 这里在分段规划前让识图模型同时看「每段原片的关键帧」和「新商品 / 新服装的参考图」，逐段写出：原片这段在展示
// 哪个部件、新商品上对应的是什么、这一段改成怎样的动作与镜头；服装还给出正确穿法和整体搭配，再写进每段的生成提示词。

const (
	// videoReplicationStructureImageLimit 是一次结构对齐请求的图片总数上限（APIMart 单条消息最多 16 张）。
	videoReplicationStructureImageLimit = 16
	// videoReplicationStructureFrameLimit 是参考视频关键帧的上限，剩余名额留给商品图。
	videoReplicationStructureFrameLimit = 10
	// videoReplicationStructureAssetLimit 是商品 / 服装 / 细节 / 包装 / 模特图合计上限，保证关键帧至少有 8 张。
	videoReplicationStructureAssetLimit = 8
	videoReplicationStructureProductMax = 3
	videoReplicationStructureDetailMax  = 3
	videoReplicationStructureApparelMax = 3
)

type VideoReplicationStructure struct {
	SourceProduct string `json:"sourceProduct,omitempty"`
	NewProduct    string `json:"newProduct,omitempty"`
	Packaging     string `json:"packaging,omitempty"`
	// SourceLook 是原商品在画面里的外观（形状、颜色、盖子、标签），原片编辑时用它让视频模型认准要换掉的东西；
	// 只写「主要商品」时模型常认不出，只给原商品调个色就交差。
	SourceLook string `json:"sourceLook,omitempty"`
	// NewSubject 是新商品图里真正要放进视频的商品本体；ReferenceProps 是图里不属于商品的东西（包装盒、道具、桌面、背景），
	// 商品图是带场景的宣传图时，模型会把整张图的布景搬进视频，必须点名排除。
	NewSubject     string `json:"newSubject,omitempty"`
	ReferenceProps string `json:"referenceProps,omitempty"`
	// Apparel 是新服装的结构（口袋、门襟、领型、袖长、衣长、版型等）；Wearing 是正确穿法；Styling 是整体搭配。
	Apparel  string                             `json:"apparel,omitempty"`
	Wearing  string                             `json:"wearing,omitempty"`
	Styling  string                             `json:"styling,omitempty"`
	Segments []VideoReplicationStructureSegment `json:"segments"`
	Model    string                             `json:"model,omitempty"`
}

type VideoReplicationStructureSegment struct {
	Index  int    `json:"index"`
	Source string `json:"source"`
	Match  string `json:"match"`
	Action string `json:"action"`
	// Camera 是这一段需要调整的镜头（原片特写的部位新商品上没有时改拍哪里）；不用调整时为空。
	Camera    string `json:"camera,omitempty"`
	Packaging bool   `json:"packaging"`
	// Presence 是原商品在这一段的存在感：hero / hand / minor / none。
	Presence string `json:"presence,omitempty"`
}

// 原商品在一段画面里的存在感。商品只在边缘或背景出现时，视频模型拿到商品图后常把整段改成商品展示特写，
// 所以每段按存在感约束新商品出现的位置和大小。
const (
	videoReplicationPresenceHero  = "hero"
	videoReplicationPresenceHand  = "hand"
	videoReplicationPresenceMinor = "minor"
	videoReplicationPresenceNone  = "none"
)

func normalizeVideoReplicationPresence(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case videoReplicationPresenceHero, videoReplicationPresenceHand, videoReplicationPresenceMinor, videoReplicationPresenceNone:
		return value
	}
	return ""
}

// videoReplicationNeedsStructure：替换商品或服装时才需要结构对齐。
func videoReplicationNeedsStructure(videoType string) bool {
	return videoReplicationHasTarget(videoType, VideoReplicationTypeProduct) || videoReplicationHasTarget(videoType, VideoReplicationTypeApparel)
}

// videoReplicationStructureFrames 为每段挑选参考视频关键帧：先保证每段一张（与本段重叠最长的镜头），名额有剩再轮流补第二、第三张。
func videoReplicationStructureFrames(video VideoReplicationVideoAnalysis, segments []videoReplicationAutoSegment, budget int) [][]VideoReplicationSourceShot {
	candidates := make([][]VideoReplicationSourceShot, len(segments))
	for index, segment := range segments {
		type scored struct {
			shot    VideoReplicationSourceShot
			overlap int64
		}
		items := []scored{}
		for _, shot := range video.Shots {
			overlap := min(shot.EndMs, segment.EndMs) - max(shot.StartMs, segment.StartMs)
			if shot.KeyframeResourceID != "" && overlap > 0 {
				items = append(items, scored{shot, overlap})
			}
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].overlap > items[j].overlap })
		for _, item := range items {
			candidates[index] = append(candidates[index], item.shot)
		}
	}
	picked := make([][]VideoReplicationSourceShot, len(segments))
	total := 0
	for round := 0; total < budget; round++ {
		added := false
		for index := range segments {
			if round < len(candidates[index]) && total < budget {
				picked[index] = append(picked[index], candidates[index][round])
				total++
				added = true
			}
		}
		if !added {
			break
		}
	}
	for index := range picked {
		sort.SliceStable(picked[index], func(i, j int) bool { return picked[index][i].StartMs < picked[index][j].StartMs })
	}
	return picked
}

// alignVideoReplicationStructure 调用识图模型做逐段结构对齐。没有商品图也没有服装图时返回 nil。
func (s *Service) alignVideoReplicationStructure(ctx context.Context, project *model.VideoReplicationProject, params VideoReplicationParameters, assets []model.VideoReplicationAsset, analysis *VideoReplicationAnalysis, segments []videoReplicationAutoSegment) (*VideoReplicationStructure, error) {
	if len(analysis.Videos) == 0 || len(segments) == 0 {
		return nil, nil
	}
	swapProduct := videoReplicationHasTarget(project.VideoType, VideoReplicationTypeProduct)
	swapApparel := videoReplicationHasTarget(project.VideoType, VideoReplicationTypeApparel)
	var products, details, packaging, apparel, wearers []VideoReplicationAssetView
	for _, view := range videoReplicationAssetViews(assets) {
		switch view.Category {
		case model.VideoReplicationAssetProductImage:
			if swapProduct {
				products = append(products, view)
			}
		case model.VideoReplicationAssetProductDetailImage:
			if swapProduct {
				details = append(details, view)
			}
		case model.VideoReplicationAssetPackagingImage:
			if swapProduct {
				packaging = append(packaging, view)
			}
		case model.VideoReplicationAssetApparelImage:
			if swapApparel {
				apparel = append(apparel, view)
			}
		case model.VideoReplicationAssetModelImage:
			wearers = append(wearers, view)
		}
	}
	if len(products) == 0 && len(apparel) == 0 {
		return nil, nil
	}
	primaryFirst := func(views []VideoReplicationAssetView) {
		sort.SliceStable(views, func(i, j int) bool { return views[i].IsPrimary && !views[j].IsPrimary })
	}
	primaryFirst(products)
	primaryFirst(apparel)
	primaryFirst(wearers)
	products = products[:min(len(products), videoReplicationStructureProductMax)]
	apparel = apparel[:min(len(apparel), videoReplicationStructureApparelMax)]
	details = details[:min(len(details), videoReplicationStructureDetailMax)]
	packaging = packaging[:min(len(packaging), 1)]
	// 服装搭配要看穿在谁身上：替换服装且有模特图时带一张。
	if len(apparel) == 0 {
		wearers = nil
	}
	wearers = wearers[:min(len(wearers), 1)]

	cfg, err := s.videoReplicationTextConfig(project.UserID, true)
	if err != nil {
		return nil, err
	}
	if err := validateVideoReplicationVisionProtocol(cfg); err != nil {
		return nil, err
	}
	cfg.TextTimeout = 3 * time.Minute

	images, labels := []string{}, []string{}
	addImage := func(resourceID, label string) error {
		dataURL, err := s.videoReplicationImageDataURL(ctx, project.UserID, resourceID)
		if err != nil {
			return fmt.Errorf("%s：%w", label, err)
		}
		images = append(images, dataURL)
		labels = append(labels, fmt.Sprintf("图%d=%s", len(images), label))
		return nil
	}
	names := map[string]string{
		model.VideoReplicationAssetProductImage:       "新商品图",
		model.VideoReplicationAssetApparelImage:       "新服装图",
		model.VideoReplicationAssetProductDetailImage: "新商品细节/拆解图",
		model.VideoReplicationAssetPackagingImage:     "新商品包装图",
		model.VideoReplicationAssetModelImage:         "模特图",
	}
	added := 0
	for _, group := range [][]VideoReplicationAssetView{products, apparel, details, packaging, wearers} {
		for _, view := range group {
			if added >= videoReplicationStructureAssetLimit {
				break
			}
			if err := addImage(view.ResourceID, names[view.Category]+"（"+view.Label+"）"); err != nil {
				return nil, err
			}
			added++
		}
	}
	budget := min(videoReplicationStructureFrameLimit, videoReplicationStructureImageLimit-len(images))
	type segmentContext struct {
		Index  int      `json:"index"`
		Start  float64  `json:"startSec"`
		End    float64  `json:"endSec"`
		Frames []string `json:"frames"`
		Shots  []string `json:"referenceShots"`
	}
	contexts := make([]segmentContext, 0, len(segments))
	primary := analysis.Videos[0]
	for index, frames := range videoReplicationStructureFrames(primary, segments, budget) {
		segment := segments[index]
		item := segmentContext{Index: index + 1, Start: segment.Start, End: segment.End}
		for _, shot := range frames {
			// 关键帧读取失败只少一张图，不影响其他段。
			if err := addImage(shot.KeyframeResourceID, fmt.Sprintf("原片第%d段 %.1fs 关键帧", index+1, float64((shot.StartMs+shot.EndMs)/2)/1000)); err != nil {
				slog.Warn("video replication structure keyframe skipped", "projectId", project.ID, "error", err)
				continue
			}
			item.Frames = append(item.Frames, fmt.Sprintf("图%d", len(images)))
		}
		for _, shot := range primary.Shots {
			if shot.StartMs < segment.EndMs && shot.EndMs > segment.StartMs {
				parts := []string{}
				for _, value := range []string{shot.ShotSize, shot.Action, shot.ProductDisplay, shot.PersonAction} {
					if value = strings.TrimSpace(value); value != "" && value != "无" {
						parts = append(parts, value)
					}
				}
				if len(parts) > 0 {
					item.Shots = append(item.Shots, fmt.Sprintf("%.1f-%.1fs：%s", float64(shot.StartMs)/1000, float64(shot.EndMs)/1000, strings.Join(parts, "；")))
				}
			}
		}
		contexts = append(contexts, item)
	}
	targets := []string{}
	if len(products) > 0 {
		targets = append(targets, "商品")
	}
	if len(apparel) > 0 {
		targets = append(targets, "服装")
	}
	brief := map[string]any{
		"replace": strings.Join(targets, "、"), "segments": contexts, "sourceProductHint": strings.TrimSpace(params.SubjectHint), "lockedTargets": analysis.RegionTarget,
		"userBrief": videoReplicationReadablePrompt(project.Prompt, assets), "newProductProfile": analysis.Product, "modelProfile": analysis.Model,
	}
	contextJSON, _ := json.Marshal(brief)
	system := "你是电商商品视频的结构对齐师兼服装搭配师，熟悉服装、鞋包、美妆个护、家电数码、食品、家居、母婴、玩具等各品类。对比参考视频里的原商品 / 原服装和用户的新商品 / 新服装，只描述画面里看得到的结构与动作，不复述品牌名，不识别真实人物身份。输出严格 JSON。"
	prompt := fmt.Sprintf(`附带图片：%s。
任务：参考视频要逐段复刻成新%s的视频，场景、人物、节奏都沿用原片，但原来的和新的结构可能不同：
- 商品：外形、开口 / 出风口 / 喷头、按键位置、盖子与开合方式（旋盖 / 翻盖 / 按压泵 / 滴管 / 拉链 / 磁吸）、可拆部件、配件、使用方式等；
- 服装鞋包：有无口袋及位置、门襟（拉链 / 纽扣 / 套头）、领型、帽子、袖长、衣长、版型松紧、腰带、开衩、鞋带 / 魔术贴 / 一脚蹬、包的开合与背法等。
上下文（JSON）：%s
请逐段判断并改写：
1. source：原片这一段人物 / 手在对原商品或原服装做什么（如「从手柄底部拔出长条滤芯」「双手插进外套侧袋」「拉上拉链」「按压泵头挤出乳液」）。
2. match：新商品 / 新服装上对应的部件（以新图为准）；没有对应部件时写「无」。
3. action：这一段改写后的动作（不超过 80 字），手部位置、节奏、走位尽量沿用原片：有对应部件就改成新商品自己的结构和用法（如「改为拧下尾部圆形滤网盖」）；没有对应部件就改成同样节奏下自然、合理的动作（如原片插口袋而新衣服没有口袋 → 双手自然垂放、轻抚衣摆或叉腰；原片拉拉链而新衣服是套头 → 整理领口），绝不凭空画出新商品 / 新服装没有的部件；原片只是常规握持、走动或展示，新旧结构没有差异时写「沿用原片动作」。
4. camera：原片这一段特写的部位在新商品 / 新服装上不存在或展示不出卖点时，写改拍哪里（如「特写口袋 → 改为特写新外套的领口刺绣」），不超过 50 字；不用调整写空串。
5. packaging：这一段原片画面里是否出现包装盒 / 包装袋。
6. presence（替换商品时必填）：原商品在这一段画面里的存在感——hero：原商品是画面主体（特写、静物展示、占画面主要位置）；hand：被人拿在手里 / 使用，人物仍是画面主体；minor：只在画面边缘、远处、背景里小面积出现或被虚化；none：这一段看不到原商品。
7. userBrief 里关于结构、穿法、搭配、动作的说明优先，必须遵守。
替换服装时另外给出：
- apparel：新服装的结构概述（口袋有无与位置、门襟、领型、袖长、衣长、版型、面料厚薄），不超过 150 字；
- wearing：这件衣服正常、合身的穿法（如「拉链拉到胸口」「下摆塞进裤腰」「袖口自然垂到手腕」「外套敞开露出内搭」），不超过 100 字；
- styling：整体搭配：新服装是上装就搭下装和鞋，是下装就搭上装和鞋，连衣裙搭鞋和简单配饰；选和新服装风格、颜色协调的常见款（写清单品、颜色、款式），简洁不抢主体；原片里与新服装风格冲突或被它取代的单品要换掉，不超过 120 字。
不替换服装时 apparel、wearing、styling 写空串。
替换商品时另外给出（不替换商品写空串）：
- sourceLook：原商品在画面里的外观，用来在视频里认出它（形状、材质、颜色、盖子、标签颜色与版式、大小），不写品牌名，不超过 60 字；
- newSubject：新商品图里真正要放进视频的商品本体（如「金色旋盖的玻璃罐装浓浆，红色标签」）；图里同时摆着包装盒、赠品时只写商品本体，不超过 60 字；
- referenceProps：新商品图里除商品本体以外的所有东西（包装盒、水果、杯子、勺子、花、桌面、布料、窗格、背景景物等），这些不能被搬进视频，不超过 80 字，没有写空串。
输出 JSON：{"sourceProduct":"原商品 / 原服装结构概述","newProduct":"新商品结构概述（不超过 150 字，不替换商品写空串）","sourceLook":"","newSubject":"","referenceProps":"","apparel":"","wearing":"","styling":"","packaging":"原片包装出现情况与新包装建议，没有包装写无","segments":[{"index":1,"source":"","match":"","action":"","camera":"","packaging":false,"presence":"hand"}]}
segments 必须有 %d 项，index 从 1 开始与上下文的段一一对应。`, strings.Join(labels, "，"), strings.Join(targets, "、"), string(contextJSON), len(segments))
	var result VideoReplicationStructure
	if err := s.videoReplicationChatJSON(ctx, cfg, "structure", project.ID, system, prompt, images, nil, &result); err != nil {
		return nil, err
	}
	byIndex := map[int]VideoReplicationStructureSegment{}
	for position, item := range result.Segments {
		if item.Index <= 0 {
			item.Index = position + 1
		}
		item.Source, item.Match, item.Action = videoReplicationClipRunes(strings.TrimSpace(item.Source), 200), videoReplicationClipRunes(strings.TrimSpace(item.Match), 120), videoReplicationClipRunes(strings.TrimSpace(item.Action), 240)
		item.Camera = videoReplicationClipRunes(strings.TrimSpace(item.Camera), 120)
		item.Presence = normalizeVideoReplicationPresence(item.Presence)
		byIndex[item.Index] = item
	}
	clip := func(value string, limit int) string {
		return videoReplicationClipRunes(strings.TrimSpace(value), limit)
	}
	aligned := &VideoReplicationStructure{SourceProduct: clip(result.SourceProduct, 400), Packaging: clip(result.Packaging, 200), Model: cfg.TextModel}
	if len(products) > 0 {
		aligned.NewProduct = clip(result.NewProduct, 400)
		aligned.SourceLook, aligned.NewSubject, aligned.ReferenceProps = clip(result.SourceLook, 80), clip(result.NewSubject, 80), clip(result.ReferenceProps, 120)
	}
	if len(apparel) > 0 {
		aligned.Apparel, aligned.Wearing, aligned.Styling = clip(result.Apparel, 300), clip(result.Wearing, 200), clip(result.Styling, 240)
	}
	for index := range segments {
		item := byIndex[index+1]
		item.Index = index + 1
		if len(products) == 0 {
			item.Packaging, item.Presence = false, ""
		}
		aligned.Segments = append(aligned.Segments, item)
	}
	return aligned, nil
}

// videoReplicationStructureAdaptation 把结构对齐结果写成这一段的动作与镜头说明；与原片一致时不写，免得提示词里多出无效要求。
func videoReplicationStructureAdaptation(item VideoReplicationStructureSegment) string {
	parts := []string{}
	if action := strings.TrimSpace(item.Action); action != "" && !strings.Contains(action, "沿用原片动作") {
		if source := strings.TrimSpace(item.Source); source != "" {
			action = "原片「" + source + "」→ " + action
		}
		parts = append(parts, action)
	}
	if camera := strings.TrimSpace(item.Camera); camera != "" {
		parts = append(parts, "镜头："+camera)
	}
	return videoReplicationClipRunes(strings.Join(parts, "；"), 300)
}

// videoReplicationPackagingRule 是原片出现包装时的处理：有包装图换成包装图，没有就换成印着新商品的素白包装盒。
func videoReplicationPackagingRule(packagingTokens string) string {
	if packagingTokens != "" {
		return "原视频出现包装盒 / 包装袋时，换成 " + packagingTokens + " 中的包装，不保留原包装上的品牌、文字和图案。"
	}
	return "原视频出现包装盒 / 包装袋时，换成素白包装盒，盒面只印新商品的外观图，不保留原包装上的品牌、文字和图案。"
}

// videoReplicationSubtitleRule：原片烧录的字幕讲的是原商品，成片里去掉。
const videoReplicationSubtitleRule = "去掉原视频画面里烧录的字幕、贴纸文字和原商品的品牌文字 / Logo，该区域还原成自然画面；不要新增字幕、文字、水印或其他品牌标识。"
