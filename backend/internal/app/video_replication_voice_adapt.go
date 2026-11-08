package app

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/model"
)

// 口播自适应：参考视频有口播 / 字幕 / 文案时，Agent 按用户的商品与补充说明改写台词，
// 新台词用原视频说话人的音色说出（用户上传了参考音色时用用户的），口型与台词逐字同步，字幕与台词一致。

// videoReplicationAdaptVoice 判断这次是否要按用户商品改写口播：
//   - 静音、模型不出声时不改（改了也说不出来）；
//   - 保留原声（音乐和节奏）时只有能把原片伴奏与人声分开才改：新台词配原伴奏，否则保持原音轨；
//   - 换了商品 / 服饰，或用户写了补充说明时改；只换人物且没写要求时，原台词本来就对，保持不变。
func videoReplicationAdaptVoice(videoType string, params VideoReplicationParameters, prompt string) bool {
	if params.RewriteVoice {
		return true
	}
	if params.AudioMode == "mute" || !params.GenerateAudio {
		return false
	}
	if (params.KeepOriginalAudio || params.AudioMode == "keep") && videoReplicationSeparatorURL() == "" {
		return false
	}
	return videoReplicationHasTarget(videoType, VideoReplicationTypeProduct) || videoReplicationHasTarget(videoType, VideoReplicationTypeApparel) || strings.TrimSpace(prompt) != ""
}

// videoReplicationHasSubtitles：参考视频画面里有字幕（视频理解的概括或逐镜头字幕原文）。
func videoReplicationHasSubtitles(analysis *VideoReplicationAnalysis) bool {
	if analysis == nil {
		return false
	}
	summary := strings.TrimSpace(analysis.Summary.Subtitles)
	if summary != "" && !videoReplicationNoneText(summary) {
		return true
	}
	for _, video := range analysis.Videos {
		for _, shot := range video.Shots {
			if text := strings.TrimSpace(shot.Subtitle); text != "" && !videoReplicationNoneText(text) {
				return true
			}
		}
	}
	return false
}

func videoReplicationNoneText(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), "。.")
	for _, none := range []string{"无", "没有", "无字幕", "没有字幕", "无法判断", "不确定", "unknown", "none"} {
		if strings.EqualFold(value, none) {
			return true
		}
	}
	return strings.HasPrefix(value, "无字幕") || strings.HasPrefix(value, "没有字幕")
}

// videoReplicationSpeakerHint 是原视频说话人的音色描述：模型收不了参考音频时，用文字让新人声尽量接近原音色。
func videoReplicationSpeakerHint(analysis *VideoReplicationAnalysis) string {
	if analysis == nil || analysis.Voice == nil {
		return ""
	}
	for _, speaker := range analysis.Voice.Speakers {
		parts := []string{}
		for _, value := range []string{speaker.Gender, speaker.Age, speaker.Tone} {
			if value = strings.TrimSpace(value); value != "" && !videoReplicationNoneText(value) {
				parts = append(parts, value)
			}
		}
		if len(parts) > 0 {
			return videoReplicationClipRunes(strings.Join(parts, "，"), 40)
		}
	}
	return ""
}

// videoReplicationSubtitleCues 把一段口播拆成按语速铺开的短字幕：按标点断句，每条不超过 16 个字，
// 时长按字数比例分配到这一段的说话时间里，字幕与人声同步出现、同步结束。
func videoReplicationSubtitleCues(text string, startMs, durationMs int64) []renderClip {
	text = strings.TrimSpace(text)
	if text == "" || durationMs <= 0 {
		return nil
	}
	pieces := []string{}
	var current []rune
	flush := func() {
		if piece := strings.Trim(string(current), " ，,、；;"); piece != "" {
			pieces = append(pieces, piece)
		}
		current = current[:0]
	}
	for _, r := range text {
		switch r {
		case '。', '！', '？', '!', '?', '，', ',', '；', ';', '…', '\n':
			if r == '！' || r == '？' || r == '!' || r == '?' {
				current = append(current, r)
			}
			flush()
			continue
		}
		current = append(current, r)
		if len(current) >= 16 {
			flush()
		}
	}
	flush()
	if len(pieces) == 0 {
		return nil
	}
	total := 0
	for _, piece := range pieces {
		total += utf8.RuneCountInString(piece)
	}
	// 说话时间按约 4.5 字/秒估算，不超过本段时长，开头留 150ms，避免字幕先于声音出现。
	speech := min(durationMs-150, int64(float64(total)/videoReplicationDefaultCharsPerSecond*1000)+400)
	if speech < 600 {
		speech = max(durationMs-150, 1)
	}
	cues := make([]renderClip, 0, len(pieces))
	cursor := startMs + 150
	for index, piece := range pieces {
		length := speech * int64(utf8.RuneCountInString(piece)) / int64(max(total, 1))
		if index == len(pieces)-1 {
			length = startMs + 150 + speech - cursor
		}
		if length <= 0 {
			continue
		}
		cues = append(cues, renderClip{Kind: "subtitle", TrackID: "subtitle", StartMs: cursor, DurationMs: length, Text: piece})
		cursor += length
	}
	return cues
}

// videoReplicationSourceVoiceClip 从原视频截一段只有人声的说话片段（≤15 秒）作为参考音色：
// 先用分离服务去掉背景音乐，再从第一句台词开始截取，没有可用台词时间时从头截取。
// 分离服务不可用或片段太短时返回空，调用方改用文字描述音色。
func (s *Service) videoReplicationSourceVoiceClip(ctx context.Context, userID string, project *model.VideoReplicationProject, analysis *VideoReplicationAnalysis) (string, error) {
	source := videoReplicationPrimaryAudio(project)
	if source == "" || videoReplicationSeparatorURL() == "" {
		return "", nil
	}
	vocals, err := s.separateVideoReplicationStem(ctx, userID, source, videoReplicationStemVocals)
	if err != nil {
		return "", err
	}
	startMs, endMs := int64(0), int64(videoReplicationMaxVoiceMs)
	if voice := videoReplicationVoiceLineSource(analysis); voice != nil {
		first := true
		for _, line := range voice.Segments {
			if strings.TrimSpace(line.Text) == "" || line.EndMs <= line.StartMs {
				continue
			}
			if first {
				startMs, endMs, first = max(0, line.StartMs-100), line.EndMs, false
				continue
			}
			if line.EndMs-startMs > videoReplicationMaxVoiceMs-200 {
				break
			}
			endMs = line.EndMs
		}
	}
	if endMs-startMs > videoReplicationMaxVoiceMs-200 {
		endMs = startMs + videoReplicationMaxVoiceMs - 200
	}
	if endMs-startMs < 2000 {
		endMs = startMs + 2000
	}
	identity := fmt.Sprintf("video-replication-source-voice:v1:%s:%d-%d", vocals, startMs, endMs)
	if resource, err := s.resourceForUploadKey(userID, normalizedResourceUploadKey([]string{identity})); err == nil && resource != nil && resource.Status == model.ResourceStatusReady {
		return resource.ID, nil
	}
	ffmpegBin, err := renderFfmpegBinary()
	if err != nil {
		return "", err
	}
	clipID := ""
	err = s.withVideoReplicationResourceFile(userID, vocals, videoReplicationMaxVideoBytes, func(_ *model.Resource, path string) error {
		output := filepath.Join(filepath.Dir(path), "source-voice.mp3")
		// 只做截取、单声道与响度统一；不做降噪，保留原音色。
		cmd := exec.CommandContext(ctx, ffmpegBin, "-hide_banner", "-loglevel", "error", "-nostdin", "-ss", fmt.Sprintf("%.3f", float64(startMs)/1000), "-t", fmt.Sprintf("%.3f", float64(endMs-startMs)/1000), "-i", path, "-vn", "-ac", "1", "-ar", "44100", "-af", "loudnorm=I=-16:TP=-1.5:LRA=11", "-c:a", "libmp3lame", "-b:a", "128k", "-y", output)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("截取原视频人声失败：%s", clipText(strings.TrimSpace(string(out)), 300))
		}
		saved, err := s.saveVideoReplicationFile(userID, output, "原视频音色.mp3", "audio", 0, 0, endMs-startMs, identity)
		if err != nil {
			return err
		}
		clipID = saved.ID
		return nil
	})
	return clipID, err
}

// videoReplicationCleanVoiceover 清理改写后的口播：去掉括号里的动作提示、引号、表情符号和多余空白，
// 只留下要说出口的文字，避免配音读出「（笑）」或字幕出现乱码符号。
func videoReplicationCleanVoiceover(value string) string {
	value = strings.TrimSpace(value)
	for _, pair := range [][2]rune{{'（', '）'}, {'(', ')'}, {'【', '】'}, {'[', ']'}} {
		for {
			start := strings.IndexRune(value, pair[0])
			if start < 0 {
				break
			}
			end := strings.IndexRune(value[start:], pair[1])
			if end < 0 {
				value = value[:start]
				break
			}
			value = value[:start] + value[start+end+utf8.RuneLen(pair[1]):]
		}
	}
	var b strings.Builder
	space := false
	for _, r := range value {
		switch {
		case r == '“' || r == '”' || r == '"' || r == '「' || r == '」' || r == '《' || r == '》' || r == '*' || r == '#' || r == '~' || r == '～':
			continue
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '　':
			space = true
			continue
		case r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF) || r == '️' || r == '‍' || r == utf8.RuneError:
			continue
		}
		if space && b.Len() > 0 {
			b.WriteRune(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// videoReplicationStoryboardHasLines：分镜里至少有一段要说的台词。
func videoReplicationStoryboardHasLines(storyboard VideoReplicationStoryboard) bool {
	for _, shot := range storyboard.Shots {
		if strings.TrimSpace(shot.Voiceover) != "" {
			return true
		}
	}
	return false
}
