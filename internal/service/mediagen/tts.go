package mediagen

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/progress"
)

// GenerateTTS synthesizes speech audio through whichever built-in or custom provider ModelRef
// resolves to (or the user's configured default when ModelRef is empty).
func (s *Service) GenerateTTS(ctx context.Context, req domainmedia.TTSRequest) (domainmedia.Result, error) {
	norm, err := domainmedia.NormalizeTTSRequest(req)
	if err != nil {
		return domainmedia.Result{}, err
	}

	target, limits, err := s.resolveTarget(ctx, domainprovider.PurposeTTS, norm.ModelRef)
	if err != nil {
		return domainmedia.Result{}, err
	}

	release, err := s.limiter.acquire(ctx, target.ownerID, domainprovider.CapabilityTTS, limits)
	if err != nil {
		return domainmedia.Result{}, apperr.From(err)
	}
	defer release()

	callCtx, cancel := s.requestTimeoutCtx(ctx, false)
	defer cancel()
	stopKA := progress.KeepAlive(callCtx)
	defer stopKA()

	progress.Report(callCtx, progress.Update{
		Percent: 15,
		Message: "Synthesizing speech…",
		Key:     "mediagen.progress.generating_tts",
	})

	var data []byte
	var mimeType string

	switch target.protocol {
	case "openai-tts":
		data, mimeType, err = s.genOpenAITTS(callCtx, target, norm)
	case "gemini-aistudio-tts", "gemini-vertex-tts":
		data, mimeType, err = s.genGeminiTTS(callCtx, target, norm)
	case "dashscope-tts":
		data, mimeType, err = s.genDashScopeTTS(callCtx, target, norm)
	case "minimax-tts":
		data, mimeType, err = s.genMiniMaxTTS(callCtx, target, norm)
	default:
		return domainmedia.Result{}, apperr.Validation("Unsupported TTS protocol: " + target.protocol)
	}
	if err != nil {
		return domainmedia.Result{}, err
	}

	fallbackExt := "." + norm.Format
	res, err := s.saveOutput(domainmedia.KindTTS, norm.OutputDir, "tts", mimeType, fallbackExt, data, target)
	if err != nil {
		return domainmedia.Result{}, err
	}
	progress.Report(callCtx, progress.Update{
		Percent: 100,
		Message: "Audio ready",
		Key:     "mediagen.progress.done",
	})
	return res, nil
}

func defaultVoice(requested, fallback string) string {
	if strings.TrimSpace(requested) != "" {
		return strings.TrimSpace(requested)
	}
	return fallback
}

func (s *Service) genOpenAITTS(ctx context.Context, t targetSpec, req domainmedia.TTSRequest) ([]byte, string, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	voice := defaultVoice(req.Voice, "alloy")

	payload := map[string]any{
		"model":           t.modelID,
		"input":           req.Text,
		"voice":           voice,
		"response_format": req.Format,
		"speed":           req.Speed,
	}
	raw, ct, err := s.doBinary(ctx, "POST", base+"/audio/speech", headers, payload)
	if err != nil {
		return nil, "", err
	}
	if ct == "" || strings.HasPrefix(ct, "application/octet-stream") {
		ct = domainmedia.MimeForExt(req.Format)
	}
	return raw, strings.Split(ct, ";")[0], nil
}

func (s *Service) genGeminiTTS(ctx context.Context, t targetSpec, req domainmedia.TTSRequest) ([]byte, string, error) {
	modelID := geminiModelName(t.modelID)
	voice := defaultVoice(req.Voice, "Kore")
	headers := map[string]string{}
	var targetURL string

	if t.protocol == "gemini-vertex-tts" {
		tok, proj, err := s.getVertexToken(ctx, t)
		if err != nil {
			return nil, "", err
		}
		host := strings.TrimRight(t.baseURL, "/")
		if host == "" {
			host = "https://aiplatform.googleapis.com"
		}
		headers["Authorization"] = "Bearer " + tok
		targetURL = fmt.Sprintf("%s/v1beta1/projects/%s/locations/global/publishers/google/models/%s:generateContent",
			host, proj, modelID)
	} else {
		root := ensureRootStrippingSuffixes(t.baseURL, "/v1beta", "/v1")
		headers["x-goog-api-key"] = t.apiKey
		targetURL = fmt.Sprintf("%s/v1beta/models/%s:generateContent", root, modelID)
	}

	payload := map[string]any{
		"contents": []map[string]any{{
			"role":  "user",
			"parts": []map[string]any{{"text": req.Text}},
		}},
		"generationConfig": map[string]any{
			"responseModalities": []string{"AUDIO"},
			"speechConfig": map[string]any{
				"voiceConfig": map[string]any{
					"prebuiltVoiceConfig": map[string]any{
						"voiceName": voice,
					},
				},
			},
		},
	}

	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData *struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := s.doJSON(ctx, "POST", targetURL, headers, payload, &resp); err != nil {
		return nil, "", err
	}

	for _, cand := range resp.Candidates {
		for _, p := range cand.Content.Parts {
			if p.InlineData == nil || p.InlineData.Data == "" {
				continue
			}
			raw, _, err := s.decodeDataOrDownload(ctx, p.InlineData.Data, "", ".wav", nil)
			if err != nil {
				return nil, "", err
			}
			mime := p.InlineData.MimeType
			if _, _, _, isPCM := domainmedia.ParsePCMMime(mime); isPCM || mime == "" {
				return domainmedia.WrapPCMToWAV(raw, mime), "audio/wav", nil
			}
			return raw, strings.Split(mime, ";")[0], nil
		}
	}
	return nil, "", apperr.Unavailable("Gemini TTS returned no audio data")
}

func (s *Service) genDashScopeTTS(ctx context.Context, t targetSpec, req domainmedia.TTSRequest) ([]byte, string, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/compatible-mode/v1", "/api/v1", "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	voice := defaultVoice(req.Voice, "Cherry")

	inputObj := map[string]any{
		"text":  req.Text,
		"voice": voice,
	}
	if req.Language != "" {
		inputObj["language_type"] = req.Language
	}
	payload := map[string]any{
		"model": t.modelID,
		"input": inputObj,
	}

	var resp struct {
		Output struct {
			Audio struct {
				URL  string `json:"url"`
				Data string `json:"data"`
			} `json:"audio"`
		} `json:"output"`
		Message string `json:"message"`
	}
	if err := s.doJSON(ctx, "POST", root+"/api/v1/services/aigc/multimodal-generation/generation", headers, payload, &resp); err != nil {
		return nil, "", err
	}
	if resp.Output.Audio.Data != "" {
		return s.decodeDataOrDownload(ctx, resp.Output.Audio.Data, "", ".wav", nil)
	}
	if resp.Output.Audio.URL != "" {
		return s.downloadURL(ctx, resp.Output.Audio.URL, nil)
	}
	if resp.Message != "" {
		return nil, "", apperr.Unavailable("DashScope TTS: " + resp.Message)
	}
	return nil, "", apperr.Unavailable("DashScope TTS returned no audio payload")
}

func (s *Service) genMiniMaxTTS(ctx context.Context, t targetSpec, req domainmedia.TTSRequest) ([]byte, string, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	voice := defaultVoice(req.Voice, "male-qn-qingse")
	format := req.Format
	if format != "mp3" && format != "pcm" && format != "flac" && format != "wav" {
		format = "mp3"
	}

	payload := map[string]any{
		"model":         t.modelID,
		"text":          req.Text,
		"stream":        false,
		"output_format": "hex",
		"voice_setting": map[string]any{
			"voice_id": voice,
			"speed":    req.Speed,
		},
		"audio_setting": map[string]any{
			"format": format,
		},
	}

	var resp struct {
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
		Data struct {
			Audio string `json:"audio"`
		} `json:"data"`
	}
	if err := s.doJSON(ctx, "POST", root+"/v1/t2a_v2", headers, payload, &resp); err != nil {
		return nil, "", err
	}
	if resp.BaseResp.StatusCode != 0 {
		return nil, "", apperr.Unavailable(fmt.Sprintf("MiniMax TTS (%d): %s", resp.BaseResp.StatusCode, resp.BaseResp.StatusMsg))
	}
	if resp.Data.Audio == "" {
		return nil, "", apperr.Unavailable("MiniMax TTS returned empty audio hex")
	}
	raw, err := hex.DecodeString(strings.TrimSpace(resp.Data.Audio))
	if err != nil {
		return nil, "", apperr.Wrap(apperr.CodeUnavailable, "Invalid hex audio from MiniMax", err)
	}
	return raw, domainmedia.MimeForExt(format), nil
}
