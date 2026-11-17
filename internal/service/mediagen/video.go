package mediagen

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/progress"
)

type videoInputs struct {
	start *localImageData
	end   *localImageData
	refs  []localImageData
}

type pollProbeOutcome struct {
	done        bool
	failed      bool
	status      string
	progress    int
	data        []byte
	mimeType    string
	downloadURL string
	errText     string
}

func (s *Service) loadVideoInputs(norm domainmedia.VideoRequest) (videoInputs, func(), error) {
	releaseLeases := s.leaseInputs(append([]string{norm.StartImage, norm.EndImage}, norm.ReferenceImages...)...)
	var inputs videoInputs
	if norm.StartImage != "" {
		img, err := s.readReferenceImage(norm.StartImage)
		if err != nil {
			releaseLeases()
			return videoInputs{}, func() {}, err
		}
		inputs.start = &img
	}
	if norm.EndImage != "" {
		img, err := s.readReferenceImage(norm.EndImage)
		if err != nil {
			releaseLeases()
			return videoInputs{}, func() {}, err
		}
		inputs.end = &img
	}
	refs, err := s.readReferenceImages(norm.ReferenceImages)
	if err != nil {
		releaseLeases()
		return videoInputs{}, func() {}, err
	}
	inputs.refs = refs
	return inputs, releaseLeases, nil
}

func applyVideoModelDefaults(norm *domainmedia.VideoRequest, facts domainprovider.ModelFacts, explicitDur, explicitRes bool) {
	if !explicitDur && len(facts.Durations) > 0 {
		norm.DurationSec = pickNearestInt(facts.Durations, domainmedia.DefaultVideoDurationSec)
	}
	if !explicitRes && len(facts.Resolutions) > 0 {
		if !slices.Contains(facts.Resolutions, norm.Resolution) {
			norm.Resolution = strings.ToLower(strings.TrimSpace(facts.Resolutions[0]))
		}
	}
}

// SubmitVideoJob submits a remote video task without waiting for completion,
// returning a non-secret VideoSubmitToken that PollVideoJobOnce can resume across restarts.
func (s *Service) SubmitVideoJob(ctx context.Context, req domainmedia.VideoRequest) (domainmedia.VideoSubmitToken, error) {
	explicitDuration := req.DurationSec > 0
	explicitResolution := strings.TrimSpace(req.Resolution) != ""

	norm, err := domainmedia.NormalizeVideoRequest(req)
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	inputs, releaseLeases, err := s.loadVideoInputs(norm)
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	defer releaseLeases()

	target, limits, err := s.resolveTarget(ctx, domainprovider.PurposeVideo, norm.ModelRef)
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	applyVideoModelDefaults(&norm, target.modelFacts.Facts, explicitDuration, explicitResolution)

	release, err := s.acquireLimit(ctx, target, domainprovider.CapabilityVideo, limits)
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	defer release()

	callCtx, cancel := s.requestTimeoutCtx(ctx, false)
	defer cancel()

	return s.submitVideoTarget(callCtx, target, norm, inputs)
}

// PollVideoJobOnce checks a previously submitted video job exactly once using fresh
// provider credentials resolved from token.ModelRef, downloading and saving output when complete.
func (s *Service) PollVideoJobOnce(ctx context.Context, token domainmedia.VideoSubmitToken) (domainmedia.VideoPollStatus, error) {
	if strings.TrimSpace(token.ProviderJobID) == "" || strings.TrimSpace(token.Protocol) == "" {
		return domainmedia.VideoPollStatus{Failed: true, ErrorText: "invalid video submit token"}, apperr.Validation("Invalid video submit token: provider_job_id and protocol are required")
	}

	target, _, err := s.resolveTarget(ctx, domainprovider.PurposeVideo, token.ModelRef)
	if err != nil {
		return domainmedia.VideoPollStatus{}, err
	}
	if token.Protocol != "" {
		target.protocol = token.Protocol
	}

	callCtx, cancel := s.requestTimeoutCtx(ctx, true)
	defer cancel()

	out, err := s.pollVideoTargetOnce(callCtx, target, token)
	if err != nil {
		return domainmedia.VideoPollStatus{
			Failed:    out.failed,
			Status:    out.status,
			Progress:  out.progress,
			ErrorText: out.errText,
		}, err
	}
	if !out.done {
		return domainmedia.VideoPollStatus{
			Done:     false,
			Status:   out.status,
			Progress: out.progress,
		}, nil
	}

	return s.finalizeVideoPoll(ctx, target, token, out)
}

func (s *Service) finalizeVideoPoll(ctx context.Context, target targetSpec, token domainmedia.VideoSubmitToken, out pollProbeOutcome) (domainmedia.VideoPollStatus, error) {
	res, err := s.saveOutput(domainmedia.KindVideo, token.OutputDir, "vid", out.mimeType, ".mp4", out.data, target)
	if err != nil {
		return domainmedia.VideoPollStatus{}, err
	}
	res.Duration = float64(token.DurationSec)
	units := float64(token.DurationSec)
	if units <= 0 {
		units = 1
	}
	s.emitUsage(ctx, domainmedia.UsageEvent{
		ProviderID:  target.ownerID,
		ModelID:     target.modelID,
		Capability:  string(domainprovider.CapabilityVideo),
		Operation:   "generate_video",
		OutputUnits: units,
		Unit:        "second",
	})
	return domainmedia.VideoPollStatus{
		Done:        true,
		Status:      out.status,
		Progress:    100,
		DownloadURL: domainmedia.SanitizeRemoteURL(out.downloadURL),
		Result:      res,
	}, nil
}

// GenerateVideo submits, polls, and downloads a video through whichever built-in or custom
// provider ModelRef resolves to (or the user's configured default when ModelRef is empty).
func (s *Service) GenerateVideo(ctx context.Context, req domainmedia.VideoRequest) (domainmedia.Result, error) {
	explicitDuration := req.DurationSec > 0
	explicitResolution := strings.TrimSpace(req.Resolution) != ""

	norm, err := domainmedia.NormalizeVideoRequest(req)
	if err != nil {
		return domainmedia.Result{}, err
	}
	inputs, releaseLeases, err := s.loadVideoInputs(norm)
	if err != nil {
		return domainmedia.Result{}, err
	}
	defer releaseLeases()

	target, limits, err := s.resolveTarget(ctx, domainprovider.PurposeVideo, norm.ModelRef)
	if err != nil {
		return domainmedia.Result{}, err
	}
	applyVideoModelDefaults(&norm, target.modelFacts.Facts, explicitDuration, explicitResolution)

	release, err := s.acquireLimit(ctx, target, domainprovider.CapabilityVideo, limits)
	if err != nil {
		return domainmedia.Result{}, err
	}
	defer release()

	callCtx, cleanup := s.prepareJobContext(ctx, true)
	defer cleanup()

	progress.Report(callCtx, progress.Update{
		Percent: 10,
		Message: "Submitting video task…",
		Key:     "mediagen.progress.submitting_video",
	})

	token, err := s.submitVideoTarget(callCtx, target, norm, inputs)
	if err != nil {
		return domainmedia.Result{}, err
	}

	var doneOut pollProbeOutcome
	err = s.pollUntil(callCtx, "mediagen.progress.polling_video", videoPollMessage(target.protocol), func() (bool, error) {
		out, pollErr := s.pollVideoTargetOnce(callCtx, target, token)
		if pollErr != nil {
			if out.failed {
				return true, pollErr
			}
			return false, pollErr
		}
		if !out.done {
			return false, nil
		}
		doneOut = out
		return true, nil
	})
	if err != nil {
		return domainmedia.Result{}, err
	}

	res, err := s.saveOutput(domainmedia.KindVideo, norm.OutputDir, "vid", doneOut.mimeType, ".mp4", doneOut.data, target)
	if err != nil {
		return domainmedia.Result{}, err
	}
	res.Duration = float64(norm.DurationSec)
	units := float64(norm.DurationSec)
	if units <= 0 {
		units = 1
	}
	s.emitUsage(ctx, domainmedia.UsageEvent{
		ProviderID:  target.ownerID,
		ModelID:     target.modelID,
		Capability:  string(domainprovider.CapabilityVideo),
		Operation:   "generate_video",
		OutputUnits: units,
		Unit:        "second",
	})
	progress.Report(callCtx, progress.Update{
		Percent: 100,
		Message: "Video ready",
		Key:     "mediagen.progress.done",
	})
	return res, nil
}

func videoPollMessage(protocol string) string {
	switch protocol {
	case "openai-video":
		return "Generating OpenAI video…"
	case "gemini-aistudio-video", "gemini-vertex-video":
		return "Generating Gemini/Veo video…"
	case "ark-video":
		return "Generating Ark video…"
	case "dashscope-video":
		return "Generating DashScope video…"
	case "minimax-video":
		return "Generating MiniMax video…"
	case "kling-video":
		return "Generating Kling video…"
	case "vidu-video":
		return "Generating Vidu video…"
	case "grok-video":
		return "Generating Grok video…"
	case "agnes-video":
		return "Generating Agnes video…"
	default:
		return "Generating video…"
	}
}

func (s *Service) submitVideoTarget(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	var tok domainmedia.VideoSubmitToken
	var err error
	switch t.protocol {
	case "openai-video":
		tok, err = s.submitOpenAIVideo(ctx, t, req, in)
	case "gemini-aistudio-video", "gemini-vertex-video":
		tok, err = s.submitGeminiVideo(ctx, t, req, in)
	case "ark-video":
		tok, err = s.submitArkVideo(ctx, t, req, in)
	case "dashscope-video":
		tok, err = s.submitDashScopeVideo(ctx, t, req, in)
	case "minimax-video":
		tok, err = s.submitMiniMaxVideo(ctx, t, req, in)
	case "kling-video":
		tok, err = s.submitKlingVideo(ctx, t, req, in)
	case "vidu-video":
		tok, err = s.submitViduVideo(ctx, t, req, in)
	case "grok-video":
		tok, err = s.submitGrokVideo(ctx, t, req, in)
	case "agnes-video":
		tok, err = s.submitAgnesVideo(ctx, t, req, in)
	default:
		return domainmedia.VideoSubmitToken{}, apperr.Validation("Unsupported video protocol: " + t.protocol)
	}
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	tok.Protocol = t.protocol
	tok.ProviderID = t.ownerID
	tok.ModelRef = t.resolvedRef
	tok.ModelID = t.modelID
	tok.OutputDir = req.OutputDir
	tok.DurationSec = req.DurationSec
	tok.SubmittedAtMs = time.Now().UnixMilli()
	return tok, nil
}

func (s *Service) pollVideoTargetOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	switch tok.Protocol {
	case "openai-video":
		return s.pollOpenAIVideoOnce(ctx, t, tok)
	case "gemini-aistudio-video", "gemini-vertex-video":
		return s.pollGeminiVideoOnce(ctx, t, tok)
	case "ark-video":
		return s.pollArkVideoOnce(ctx, t, tok)
	case "dashscope-video":
		return s.pollDashScopeVideoOnce(ctx, t, tok)
	case "minimax-video":
		return s.pollMiniMaxVideoOnce(ctx, t, tok)
	case "kling-video":
		return s.pollKlingVideoOnce(ctx, t, tok)
	case "vidu-video":
		return s.pollViduVideoOnce(ctx, t, tok)
	case "grok-video":
		return s.pollGrokVideoOnce(ctx, t, tok)
	case "agnes-video":
		return s.pollAgnesVideoOnce(ctx, t, tok)
	default:
		return pollProbeOutcome{failed: true, errText: "unsupported video protocol"}, apperr.Validation("Unsupported video protocol: " + tok.Protocol)
	}
}

func pickNearestInt(choices []int, target int) int {
	best := choices[0]
	bestDiff := absInt(best - target)
	for _, c := range choices[1:] {
		if c == target {
			return c
		}
		if d := absInt(c - target); d < bestDiff {
			best = c
			bestDiff = d
		}
	}
	return best
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (s *Service) submitOpenAIVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	size := domainmedia.OpenAIVideoSize(req.AspectRatio, req.Resolution)
	seconds := strconv.Itoa(req.DurationSec)

	var submitResp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if in.start != nil {
		err := s.doMultipart(ctx, base+"/videos", headers, func(mw *multipart.Writer) error {
			_ = mw.WriteField("model", t.modelID)
			_ = mw.WriteField("prompt", req.Prompt)
			_ = mw.WriteField("seconds", seconds)
			_ = mw.WriteField("size", size)
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="input_reference"; filename="start%s"`, domainmedia.ExtForMime(in.start.MimeType, ".png")))
			h.Set("Content-Type", in.start.MimeType)
			part, err := mw.CreatePart(h)
			if err != nil {
				return apperr.Internal(err)
			}
			_, err = part.Write(in.start.Raw)
			return err
		}, &submitResp)
		if err != nil {
			return domainmedia.VideoSubmitToken{}, err
		}
	} else {
		payload := map[string]any{
			"model":   t.modelID,
			"prompt":  req.Prompt,
			"seconds": seconds,
			"size":    size,
		}
		if err := s.doJSON(ctx, "POST", base+"/videos", headers, payload, &submitResp); err != nil {
			return domainmedia.VideoSubmitToken{}, err
		}
	}
	if submitResp.ID == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("OpenAI video returned empty job id")
	}
	return domainmedia.VideoSubmitToken{ProviderJobID: submitResp.ID}, nil
}

func (s *Service) pollOpenAIVideoOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	var st struct {
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
		URL string `json:"url"`
	}
	if err := s.doJSON(ctx, "GET", base+"/videos/"+url.PathEscape(tok.ProviderJobID), headers, nil, &st); err != nil {
		return pollProbeOutcome{}, err
	}
	switch strings.ToLower(st.Status) {
	case "completed", "succeeded":
		var raw []byte
		var ct string
		var dlErr error
		if st.URL != "" {
			raw, ct, dlErr = s.downloadURL(ctx, st.URL, headers)
		} else {
			raw, ct, dlErr = s.doBinary(ctx, "GET", base+"/videos/"+url.PathEscape(tok.ProviderJobID)+"/content", headers, nil)
		}
		if dlErr != nil {
			return pollProbeOutcome{failed: true, status: st.Status, errText: dlErr.Error()}, dlErr
		}
		return pollProbeOutcome{done: true, status: st.Status, progress: 100, data: raw, mimeType: ct, downloadURL: st.URL}, nil
	case "failed", "expired", "cancelled":
		msg := st.Status
		if st.Error != nil && st.Error.Message != "" {
			msg = st.Error.Message
		}
		err := apperr.Unavailable("OpenAI video failed: " + msg)
		return pollProbeOutcome{failed: true, status: st.Status, errText: err.Error()}, err
	default:
		return pollProbeOutcome{status: st.Status, progress: 40}, nil
	}
}

func (s *Service) submitGeminiVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	modelID := geminiModelName(t.modelID)
	isVertex := t.protocol == "gemini-vertex-video"
	headers := map[string]string{}
	var submitURL string

	if isVertex {
		tok, proj, err := s.getVertexToken(ctx, t)
		if err != nil {
			return domainmedia.VideoSubmitToken{}, err
		}
		host := strings.TrimRight(t.baseURL, "/")
		if host == "" {
			host = "https://aiplatform.googleapis.com"
		}
		headers["Authorization"] = "Bearer " + tok
		submitURL = fmt.Sprintf("%s/v1/projects/%s/locations/global/publishers/google/models/%s:predictLongRunning",
			host, proj, modelID)
	} else {
		root := ensureRootStrippingSuffixes(t.baseURL, "/v1beta", "/v1")
		headers["x-goog-api-key"] = t.apiKey
		submitURL = fmt.Sprintf("%s/v1beta/models/%s:predictLongRunning", root, modelID)
	}

	instance := map[string]any{"prompt": req.Prompt}
	if in.start != nil {
		instance["image"] = map[string]any{
			"bytesBase64Encoded": in.start.Base64,
			"mimeType":           in.start.MimeType,
		}
	}
	if in.end != nil {
		instance["lastFrame"] = map[string]any{
			"bytesBase64Encoded": in.end.Base64,
			"mimeType":           in.end.MimeType,
		}
	}
	if len(in.refs) > 0 {
		refObjs := make([]map[string]any, len(in.refs))
		for i, r := range in.refs {
			refObjs[i] = map[string]any{
				"image": map[string]any{
					"bytesBase64Encoded": r.Base64,
					"mimeType":           r.MimeType,
				},
				"referenceType": "asset",
			}
		}
		instance["referenceImages"] = refObjs
	}

	params := map[string]any{
		"aspectRatio":     req.AspectRatio,
		"durationSeconds": req.DurationSec,
	}
	if isVertex {
		params["generateAudio"] = req.GenerateAudio
	}
	if req.Resolution == "720p" || req.Resolution == "1080p" {
		params["resolution"] = req.Resolution
	}
	if req.Seed != nil {
		params["seed"] = *req.Seed
	}

	var submitResp struct {
		Name string `json:"name"`
	}
	payload := map[string]any{
		"instances":  []map[string]any{instance},
		"parameters": params,
	}
	if err := s.doJSON(ctx, "POST", submitURL, headers, payload, &submitResp); err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	if submitResp.Name == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("Gemini video returned empty operation name")
	}
	return domainmedia.VideoSubmitToken{ProviderJobID: submitResp.Name}, nil
}

func (s *Service) pollGeminiVideoOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	modelID := geminiModelName(tok.ModelID)
	if modelID == "" {
		modelID = geminiModelName(t.modelID)
	}
	isVertex := tok.Protocol == "gemini-vertex-video"
	headers := map[string]string{}

	type opResponse struct {
		Done  bool `json:"done"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Response *struct {
			GenerateVideoResponse *struct {
				GeneratedSamples []struct {
					Video struct {
						URI      string `json:"uri"`
						MimeType string `json:"mimeType"`
					} `json:"video"`
				} `json:"generatedSamples"`
			} `json:"generateVideoResponse"`
			Videos []struct {
				BytesBase64Encoded string `json:"bytesBase64Encoded"`
				GCSURI             string `json:"gcsUri"`
				MimeType           string `json:"mimeType"`
			} `json:"videos"`
		} `json:"response"`
	}

	var cur opResponse
	if isVertex {
		vtok, proj, err := s.getVertexToken(ctx, t)
		if err != nil {
			return pollProbeOutcome{}, err
		}
		host := strings.TrimRight(t.baseURL, "/")
		if host == "" {
			host = "https://aiplatform.googleapis.com"
		}
		headers["Authorization"] = "Bearer " + vtok
		pollURL := fmt.Sprintf("%s/v1/projects/%s/locations/global/publishers/google/models/%s:fetchPredictOperation",
			host, proj, modelID)
		if err := s.doJSON(ctx, "POST", pollURL, headers, map[string]any{"operationName": tok.ProviderJobID}, &cur); err != nil {
			return pollProbeOutcome{}, err
		}
	} else {
		host := ensureRootStrippingSuffixes(t.baseURL, "/v1beta", "/v1")
		headers["x-goog-api-key"] = t.apiKey
		pollURL := fmt.Sprintf("%s/v1beta/%s", host, strings.TrimPrefix(tok.ProviderJobID, "/"))
		if err := s.doJSON(ctx, "GET", pollURL, headers, nil, &cur); err != nil {
			return pollProbeOutcome{}, err
		}
	}

	if !cur.Done {
		return pollProbeOutcome{status: "running", progress: 45}, nil
	}
	if cur.Error != nil {
		err := apperr.Unavailable("Gemini video failed: " + cur.Error.Message)
		return pollProbeOutcome{failed: true, status: "failed", errText: err.Error()}, err
	}
	if cur.Response == nil {
		err := apperr.Unavailable("Gemini operation completed without response payload")
		return pollProbeOutcome{failed: true, status: "failed", errText: err.Error()}, err
	}
	if cur.Response.GenerateVideoResponse != nil {
		samples := cur.Response.GenerateVideoResponse.GeneratedSamples
		if len(samples) > 0 && samples[0].Video.URI != "" {
			raw, ct, err := s.downloadGeminiURI(ctx, samples[0].Video.URI, headers)
			if err != nil {
				return pollProbeOutcome{failed: true, errText: err.Error()}, err
			}
			if ct == "" {
				ct = samples[0].Video.MimeType
			}
			return pollProbeOutcome{done: true, status: "done", progress: 100, data: raw, mimeType: ct, downloadURL: samples[0].Video.URI}, nil
		}
	}
	if len(cur.Response.Videos) > 0 {
		v := cur.Response.Videos[0]
		if v.BytesBase64Encoded != "" {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v.BytesBase64Encoded))
			if err != nil {
				wrapErr := apperr.Wrap(apperr.CodeUnavailable, "Invalid video base64 from Vertex AI", err)
				return pollProbeOutcome{failed: true, errText: wrapErr.Error()}, wrapErr
			}
			mime := v.MimeType
			if mime == "" {
				mime = "video/mp4"
			}
			return pollProbeOutcome{done: true, status: "done", progress: 100, data: raw, mimeType: mime}, nil
		}
		if v.GCSURI != "" {
			raw, ct, err := s.downloadGeminiURI(ctx, v.GCSURI, headers)
			if err != nil {
				return pollProbeOutcome{failed: true, errText: err.Error()}, err
			}
			if ct == "" {
				ct = v.MimeType
			}
			return pollProbeOutcome{done: true, status: "done", progress: 100, data: raw, mimeType: ct, downloadURL: v.GCSURI}, nil
		}
	}
	err := apperr.Unavailable("Gemini video completed with no downloadable output")
	return pollProbeOutcome{failed: true, status: "failed", errText: err.Error()}, err
}

func (s *Service) downloadGeminiURI(ctx context.Context, uri string, authHeaders map[string]string) ([]byte, string, error) {
	if strings.HasPrefix(uri, "gs://") {
		trimmed := strings.TrimPrefix(uri, "gs://")
		parts := strings.SplitN(trimmed, "/", 2)
		if len(parts) != 2 {
			return nil, "", apperr.Unavailable("Malformed GCS URI: " + uri)
		}
		uri = fmt.Sprintf("https://storage.googleapis.com/%s/%s", parts[0], parts[1])
	}
	return s.downloadURL(ctx, uri, authHeaders)
}

func (s *Service) submitArkVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	base := ensureArkBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	content := make([]map[string]any, 0, 4)
	if req.Prompt != "" {
		content = append(content, map[string]any{"type": "text", "text": req.Prompt})
	}
	if in.start != nil {
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": in.start.DataURI},
			"role":      "first_frame",
		})
	}
	if in.end != nil {
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": in.end.DataURI},
			"role":      "last_frame",
		})
	}
	for _, r := range in.refs {
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": r.DataURI},
			"role":      "reference_image",
		})
	}

	payload := map[string]any{
		"model":          t.modelID,
		"content":        content,
		"ratio":          req.AspectRatio,
		"duration":       req.DurationSec,
		"resolution":     req.Resolution,
		"generate_audio": req.GenerateAudio,
		"watermark":      false,
	}
	if req.Seed != nil {
		payload["seed"] = *req.Seed
	}

	var submitResp struct {
		ID string `json:"id"`
	}
	if err := s.doJSON(ctx, "POST", base+"/contents/generations/tasks", headers, payload, &submitResp); err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	if submitResp.ID == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("Ark video returned empty task id")
	}
	return domainmedia.VideoSubmitToken{ProviderJobID: submitResp.ID}, nil
}

func (s *Service) pollArkVideoOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	base := ensureArkBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	var st struct {
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
		Content struct {
			VideoURL string `json:"video_url"`
		} `json:"content"`
	}
	if err := s.doJSON(ctx, "GET", base+"/contents/generations/tasks/"+url.PathEscape(tok.ProviderJobID), headers, nil, &st); err != nil {
		return pollProbeOutcome{}, err
	}
	switch strings.ToLower(st.Status) {
	case "succeeded":
		if st.Content.VideoURL == "" {
			err := apperr.Unavailable("Ark task succeeded without video_url")
			return pollProbeOutcome{failed: true, status: st.Status, errText: err.Error()}, err
		}
		raw, ct, err := s.downloadURL(ctx, st.Content.VideoURL, nil)
		if err != nil {
			return pollProbeOutcome{failed: true, errText: err.Error()}, err
		}
		return pollProbeOutcome{done: true, status: st.Status, progress: 100, data: raw, mimeType: ct, downloadURL: st.Content.VideoURL}, nil
	case "failed", "expired":
		msg := st.Status
		if st.Error != nil && st.Error.Message != "" {
			msg = st.Error.Message
		}
		err := apperr.Unavailable("Ark video failed: " + msg)
		return pollProbeOutcome{failed: true, status: st.Status, errText: err.Error()}, err
	default:
		return pollProbeOutcome{status: st.Status, progress: 45}, nil
	}
}

func (s *Service) submitDashScopeVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/compatible-mode/v1", "/api/v1", "/v1")
	headers := map[string]string{
		"Authorization":     "Bearer " + t.apiKey,
		"X-DashScope-Async": "enable",
	}

	inputObj := map[string]any{"prompt": req.Prompt}
	if in.start != nil {
		inputObj["img_url"] = in.start.DataURI
	}
	params := map[string]any{
		"duration":  req.DurationSec,
		"watermark": false,
		"audio":     req.GenerateAudio,
	}
	if req.Resolution != "" {
		params["resolution"] = strings.ToUpper(req.Resolution)
	}
	if req.Seed != nil {
		params["seed"] = *req.Seed
	}

	payload := map[string]any{
		"model":      t.modelID,
		"input":      inputObj,
		"parameters": params,
	}
	var submitResp struct {
		Output struct {
			TaskID string `json:"task_id"`
		} `json:"output"`
		Message string `json:"message"`
	}
	if err := s.doJSON(ctx, "POST", root+"/api/v1/services/aigc/video-generation/video-synthesis", headers, payload, &submitResp); err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	if submitResp.Output.TaskID == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("DashScope video submit error: " + submitResp.Message)
	}
	return domainmedia.VideoSubmitToken{ProviderJobID: submitResp.Output.TaskID}, nil
}

func (s *Service) pollDashScopeVideoOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/compatible-mode/v1", "/api/v1", "/v1")
	pollHeaders := map[string]string{"Authorization": "Bearer " + t.apiKey}

	var st struct {
		Output struct {
			TaskStatus string `json:"task_status"`
			VideoURL   string `json:"video_url"`
			Message    string `json:"message"`
		} `json:"output"`
	}
	if err := s.doJSON(ctx, "GET", root+"/api/v1/tasks/"+url.PathEscape(tok.ProviderJobID), pollHeaders, nil, &st); err != nil {
		return pollProbeOutcome{}, err
	}
	switch strings.ToUpper(st.Output.TaskStatus) {
	case "SUCCEEDED":
		if st.Output.VideoURL == "" {
			err := apperr.Unavailable("DashScope finished without video_url")
			return pollProbeOutcome{failed: true, status: st.Output.TaskStatus, errText: err.Error()}, err
		}
		raw, ct, err := s.downloadURL(ctx, st.Output.VideoURL, nil)
		if err != nil {
			return pollProbeOutcome{failed: true, errText: err.Error()}, err
		}
		return pollProbeOutcome{done: true, status: st.Output.TaskStatus, progress: 100, data: raw, mimeType: ct, downloadURL: st.Output.VideoURL}, nil
	case "FAILED", "CANCELED", "UNKNOWN":
		err := apperr.Unavailable("DashScope video failed: " + st.Output.Message)
		return pollProbeOutcome{failed: true, status: st.Output.TaskStatus, errText: err.Error()}, err
	default:
		return pollProbeOutcome{status: st.Output.TaskStatus, progress: 45}, nil
	}
}

func (s *Service) submitMiniMaxVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v2", "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	isH3 := strings.Contains(strings.ToLower(t.modelID), "h3")

	payload := map[string]any{
		"model":      t.modelID,
		"prompt":     req.Prompt,
		"duration":   req.DurationSec,
		"resolution": strings.ToUpper(req.Resolution),
	}
	if isH3 {
		payload["aspect_ratio"] = req.AspectRatio
	}
	if in.start != nil {
		payload["first_frame_image"] = in.start.DataURI
	}
	if in.end != nil {
		payload["last_frame_image"] = in.end.DataURI
	}

	submitPath := "/v1/video_generation"
	subpath := "v1"
	if isH3 {
		submitPath = "/v2/video_generation"
		subpath = "v2"
	}
	var submitResp struct {
		TaskID   string `json:"task_id"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := s.doJSON(ctx, "POST", root+submitPath, headers, payload, &submitResp); err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	if submitResp.BaseResp.StatusCode != 0 {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable(fmt.Sprintf("MiniMax video (%d): %s", submitResp.BaseResp.StatusCode, submitResp.BaseResp.StatusMsg))
	}
	if submitResp.TaskID == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("MiniMax returned empty task_id")
	}
	return domainmedia.VideoSubmitToken{ProviderJobID: submitResp.TaskID, Subpath: subpath}, nil
}

func (s *Service) pollMiniMaxVideoOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v2", "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	isH3 := tok.Subpath == "v2" || strings.Contains(strings.ToLower(tok.ModelID), "h3")

	if isH3 {
		var st struct {
			Task struct {
				Status  string `json:"status"`
				Content struct {
					URL string `json:"url"`
				} `json:"content"`
				Error string `json:"error"`
			} `json:"task"`
		}
		if err := s.doJSON(ctx, "GET", root+"/v2/query/video_generation/"+url.PathEscape(tok.ProviderJobID), headers, nil, &st); err != nil {
			return pollProbeOutcome{}, err
		}
		switch strings.ToLower(st.Task.Status) {
		case "succeeded":
			if st.Task.Content.URL == "" {
				err := apperr.Unavailable("MiniMax v2 finished without url")
				return pollProbeOutcome{failed: true, status: st.Task.Status, errText: err.Error()}, err
			}
			raw, ct, err := s.downloadURL(ctx, st.Task.Content.URL, nil)
			if err != nil {
				return pollProbeOutcome{failed: true, errText: err.Error()}, err
			}
			return pollProbeOutcome{done: true, status: st.Task.Status, progress: 100, data: raw, mimeType: ct, downloadURL: st.Task.Content.URL}, nil
		case "failed":
			err := apperr.Unavailable("MiniMax v2 failed: " + st.Task.Error)
			return pollProbeOutcome{failed: true, status: st.Task.Status, errText: err.Error()}, err
		default:
			return pollProbeOutcome{status: st.Task.Status, progress: 45}, nil
		}
	}

	var st struct {
		Status   string `json:"status"`
		FileID   string `json:"file_id"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := s.doJSON(ctx, "GET", root+"/v1/query/video_generation?task_id="+url.QueryEscape(tok.ProviderJobID), headers, nil, &st); err != nil {
		return pollProbeOutcome{}, err
	}
	switch st.Status {
	case "Success":
		if st.FileID == "" {
			err := apperr.Unavailable("MiniMax succeeded without file_id")
			return pollProbeOutcome{failed: true, status: st.Status, errText: err.Error()}, err
		}
		var ret struct {
			File struct {
				DownloadURL string `json:"download_url"`
			} `json:"file"`
		}
		if err := s.doJSON(ctx, "GET", root+"/v1/files/retrieve?file_id="+url.QueryEscape(st.FileID), headers, nil, &ret); err != nil {
			return pollProbeOutcome{failed: true, errText: err.Error()}, err
		}
		if ret.File.DownloadURL == "" {
			err := apperr.Unavailable("MiniMax file retrieve returned empty download_url")
			return pollProbeOutcome{failed: true, errText: err.Error()}, err
		}
		raw, ct, err := s.downloadURL(ctx, ret.File.DownloadURL, nil)
		if err != nil {
			return pollProbeOutcome{failed: true, errText: err.Error()}, err
		}
		return pollProbeOutcome{done: true, status: st.Status, progress: 100, data: raw, mimeType: ct, downloadURL: ret.File.DownloadURL}, nil
	case "Fail":
		err := apperr.Unavailable("MiniMax video failed: " + st.BaseResp.StatusMsg)
		return pollProbeOutcome{failed: true, status: st.Status, errText: err.Error()}, err
	default:
		return pollProbeOutcome{status: st.Status, progress: 45}, nil
	}
}

func (s *Service) submitKlingVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	tok, err := s.klingBearer(t)
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	headers := map[string]string{"Authorization": "Bearer " + tok}

	subpath := "text2video"
	payload := map[string]any{
		"model_name":   t.modelID,
		"prompt":       req.Prompt,
		"duration":     strconv.Itoa(req.DurationSec),
		"aspect_ratio": req.AspectRatio,
		"mode":         klingModeForResolution(req.Resolution),
	}
	if req.GenerateAudio {
		payload["sound"] = "on"
	} else {
		payload["sound"] = "off"
	}

	if len(in.refs) > 0 {
		subpath = "multi-image2video"
		imgs := make([]map[string]any, len(in.refs))
		for i, r := range in.refs {
			imgs[i] = map[string]any{"image": r.Base64}
		}
		payload["image_list"] = imgs
	} else if in.start != nil {
		subpath = "image2video"
		payload["image"] = in.start.Base64
		if in.end != nil {
			payload["image_tail"] = in.end.Base64
		}
	}

	var submitResp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := s.doJSON(ctx, "POST", root+"/v1/videos/"+subpath, headers, payload, &submitResp); err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	if submitResp.Code != 0 && submitResp.Data.TaskID == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("Kling video error: " + submitResp.Message)
	}
	taskID := submitResp.Data.TaskID
	if taskID == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("Kling video returned empty task_id")
	}
	return domainmedia.VideoSubmitToken{ProviderJobID: taskID, Subpath: subpath}, nil
}

func (s *Service) pollKlingVideoOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	bearer, err := s.klingBearer(t)
	if err != nil {
		return pollProbeOutcome{}, err
	}
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	headers := map[string]string{"Authorization": "Bearer " + bearer}
	subpath := tok.Subpath
	if subpath == "" {
		subpath = "text2video"
	}

	var st struct {
		Data struct {
			TaskStatus    string `json:"task_status"`
			TaskStatusMsg string `json:"task_status_msg"`
			TaskResult    struct {
				Videos []struct {
					URL string `json:"url"`
				} `json:"videos"`
			} `json:"task_result"`
		} `json:"data"`
	}
	if err := s.doJSON(ctx, "GET", root+"/v1/videos/"+subpath+"/"+url.PathEscape(tok.ProviderJobID), headers, nil, &st); err != nil {
		return pollProbeOutcome{}, err
	}
	switch st.Data.TaskStatus {
	case "succeed":
		if len(st.Data.TaskResult.Videos) == 0 || st.Data.TaskResult.Videos[0].URL == "" {
			err := apperr.Unavailable("Kling succeeded without video URL")
			return pollProbeOutcome{failed: true, status: st.Data.TaskStatus, errText: err.Error()}, err
		}
		videoURL := st.Data.TaskResult.Videos[0].URL
		raw, ct, err := s.downloadURL(ctx, videoURL, nil)
		if err != nil {
			return pollProbeOutcome{failed: true, errText: err.Error()}, err
		}
		return pollProbeOutcome{done: true, status: st.Data.TaskStatus, progress: 100, data: raw, mimeType: ct, downloadURL: videoURL}, nil
	case "failed":
		err := apperr.Unavailable("Kling video failed: " + st.Data.TaskStatusMsg)
		return pollProbeOutcome{failed: true, status: st.Data.TaskStatus, errText: err.Error()}, err
	default:
		return pollProbeOutcome{status: st.Data.TaskStatus, progress: 45}, nil
	}
}

func klingModeForResolution(res string) string {
	if strings.EqualFold(res, "1080p") {
		return "pro"
	}
	return "std"
}

func (s *Service) submitViduVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	base := strings.TrimRight(t.baseURL, "/")
	headers := map[string]string{"Authorization": "Token " + t.apiKey}

	payload := map[string]any{
		"model":        t.modelID,
		"prompt":       req.Prompt,
		"duration":     req.DurationSec,
		"aspect_ratio": req.AspectRatio,
		"resolution":   req.Resolution,
	}
	if req.GenerateAudio {
		payload["audio"] = true
	}
	if req.Seed != nil {
		payload["seed"] = *req.Seed
	}

	subpath := "/text2video"
	switch {
	case len(in.refs) > 0:
		subpath = "/reference2video"
		imgs := make([]string, len(in.refs))
		for i, r := range in.refs {
			imgs[i] = r.DataURI
		}
		payload["images"] = imgs
	case in.start != nil && in.end != nil:
		subpath = "/start-end2video"
		payload["images"] = []string{in.start.DataURI, in.end.DataURI}
	case in.start != nil:
		subpath = "/img2video"
		payload["images"] = []string{in.start.DataURI}
	}

	var submitResp struct {
		TaskID string `json:"task_id"`
	}
	if err := s.doJSON(ctx, "POST", base+subpath, headers, payload, &submitResp); err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	if submitResp.TaskID == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("Vidu returned empty task_id")
	}
	return domainmedia.VideoSubmitToken{ProviderJobID: submitResp.TaskID}, nil
}

func (s *Service) pollViduVideoOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	base := strings.TrimRight(t.baseURL, "/")
	headers := map[string]string{"Authorization": "Token " + t.apiKey}

	var st struct {
		State     string `json:"state"`
		ErrCode   string `json:"err_code"`
		Creations []struct {
			URL string `json:"url"`
		} `json:"creations"`
	}
	if err := s.doJSON(ctx, "GET", base+"/tasks/"+url.PathEscape(tok.ProviderJobID)+"/creations", headers, nil, &st); err != nil {
		return pollProbeOutcome{}, err
	}
	switch st.State {
	case "success":
		if len(st.Creations) == 0 || st.Creations[0].URL == "" {
			err := apperr.Unavailable("Vidu succeeded without video url")
			return pollProbeOutcome{failed: true, status: st.State, errText: err.Error()}, err
		}
		videoURL := st.Creations[0].URL
		raw, ct, err := s.downloadURL(ctx, videoURL, nil)
		if err != nil {
			return pollProbeOutcome{failed: true, errText: err.Error()}, err
		}
		return pollProbeOutcome{done: true, status: st.State, progress: 100, data: raw, mimeType: ct, downloadURL: videoURL}, nil
	case "failed":
		err := apperr.Unavailable("Vidu video failed: " + st.ErrCode)
		return pollProbeOutcome{failed: true, status: st.State, errText: err.Error()}, err
	default:
		return pollProbeOutcome{status: st.State, progress: 45}, nil
	}
}

func (s *Service) submitGrokVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	payload := map[string]any{
		"model":        t.modelID,
		"prompt":       req.Prompt,
		"duration":     req.DurationSec,
		"aspect_ratio": req.AspectRatio,
		"resolution":   req.Resolution,
	}
	if in.start != nil {
		payload["image"] = map[string]any{"url": in.start.DataURI}
	}

	var submitResp struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
	}
	if err := s.doJSON(ctx, "POST", base+"/videos/generations", headers, payload, &submitResp); err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	reqID := submitResp.RequestID
	if reqID == "" {
		reqID = submitResp.ID
	}
	if reqID == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("Grok video returned empty request_id")
	}
	return domainmedia.VideoSubmitToken{ProviderJobID: reqID}, nil
}

func (s *Service) pollGrokVideoOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	var st struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Video  struct {
			URL string `json:"url"`
		} `json:"video"`
	}
	if err := s.doJSON(ctx, "GET", base+"/videos/"+url.PathEscape(tok.ProviderJobID), headers, nil, &st); err != nil {
		return pollProbeOutcome{}, err
	}
	switch strings.ToLower(st.Status) {
	case "done", "completed", "succeeded":
		if st.Video.URL == "" {
			err := apperr.Unavailable("Grok finished without video.url")
			return pollProbeOutcome{failed: true, status: st.Status, errText: err.Error()}, err
		}
		raw, ct, err := s.downloadURL(ctx, st.Video.URL, nil)
		if err != nil {
			return pollProbeOutcome{failed: true, errText: err.Error()}, err
		}
		return pollProbeOutcome{done: true, status: st.Status, progress: 100, data: raw, mimeType: ct, downloadURL: st.Video.URL}, nil
	case "failed", "expired":
		err := apperr.Unavailable("Grok video failed: " + st.Error)
		return pollProbeOutcome{failed: true, status: st.Status, errText: err.Error()}, err
	default:
		return pollProbeOutcome{status: st.Status, progress: 45}, nil
	}
}

func (s *Service) submitAgnesVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	payload := map[string]any{
		"model":        t.modelID,
		"prompt":       req.Prompt,
		"duration":     req.DurationSec,
		"aspect_ratio": req.AspectRatio,
	}
	if in.start != nil {
		payload["image"] = in.start.DataURI
	}

	var submitResp struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
	}
	if err := s.doJSON(ctx, "POST", root+"/v1/videos", headers, payload, &submitResp); err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	taskID := submitResp.ID
	if taskID == "" {
		taskID = submitResp.TaskID
	}
	if taskID == "" {
		return domainmedia.VideoSubmitToken{}, apperr.Unavailable("Agnes returned empty video task id")
	}
	return domainmedia.VideoSubmitToken{ProviderJobID: taskID}, nil
}

func (s *Service) pollAgnesVideoOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	modelID := tok.ModelID
	if modelID == "" {
		modelID = t.modelID
	}

	var st struct {
		Status   string `json:"status"`
		State    string `json:"state"`
		URL      string `json:"url"`
		VideoURL string `json:"video_url"`
		Error    string `json:"error"`
	}
	pollURL := root + "/v1/videos/" + url.PathEscape(tok.ProviderJobID)
	if strings.Contains(modelID, "2.5") {
		pollURL = root + "/agnesapi?video_id=" + url.QueryEscape(tok.ProviderJobID) + "&model_name=" + url.QueryEscape(modelID)
	}
	if err := s.doJSON(ctx, "GET", pollURL, headers, nil, &st); err != nil {
		return pollProbeOutcome{}, err
	}
	status := strings.ToLower(st.Status)
	if status == "" {
		status = strings.ToLower(st.State)
	}
	switch status {
	case "completed", "succeeded", "success", "done":
		videoURL := st.URL
		if videoURL == "" {
			videoURL = st.VideoURL
		}
		if videoURL == "" {
			err := apperr.Unavailable("Agnes completed without video url")
			return pollProbeOutcome{failed: true, status: status, errText: err.Error()}, err
		}
		raw, ct, err := s.downloadURL(ctx, videoURL, nil)
		if err != nil {
			return pollProbeOutcome{failed: true, errText: err.Error()}, err
		}
		return pollProbeOutcome{done: true, status: status, progress: 100, data: raw, mimeType: ct, downloadURL: videoURL}, nil
	case "failed", "error":
		err := apperr.Unavailable("Agnes video failed: " + st.Error)
		return pollProbeOutcome{failed: true, status: status, errText: err.Error()}, err
	default:
		return pollProbeOutcome{status: status, progress: 45}, nil
	}
}
