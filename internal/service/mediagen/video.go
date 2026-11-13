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

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/progress"
	providersvc "app/internal/service/provider"
)

type videoInputs struct {
	start *localImageData
	end   *localImageData
	refs  []localImageData
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

	var inputs videoInputs
	if norm.StartImage != "" {
		img, err := s.readReferenceImage(norm.StartImage)
		if err != nil {
			return domainmedia.Result{}, err
		}
		inputs.start = &img
	}
	if norm.EndImage != "" {
		img, err := s.readReferenceImage(norm.EndImage)
		if err != nil {
			return domainmedia.Result{}, err
		}
		inputs.end = &img
	}
	for _, p := range norm.ReferenceImages {
		img, err := s.readReferenceImage(p)
		if err != nil {
			return domainmedia.Result{}, err
		}
		inputs.refs = append(inputs.refs, img)
	}

	target, limits, err := s.resolveTarget(ctx, domainprovider.PurposeVideo, norm.ModelRef)
	if err != nil {
		return domainmedia.Result{}, err
	}
	if !explicitDuration && len(target.modelFacts.Facts.Durations) > 0 {
		norm.DurationSec = pickNearestInt(target.modelFacts.Facts.Durations, domainmedia.DefaultVideoDurationSec)
	}
	if !explicitResolution && len(target.modelFacts.Facts.Resolutions) > 0 {
		if !slices.Contains(target.modelFacts.Facts.Resolutions, norm.Resolution) {
			norm.Resolution = strings.ToLower(strings.TrimSpace(target.modelFacts.Facts.Resolutions[0]))
		}
	}

	release, err := s.limiter.acquire(ctx, target.ownerID, domainprovider.CapabilityVideo, limits)
	if err != nil {
		return domainmedia.Result{}, apperr.From(err)
	}
	defer release()

	callCtx, cancel := s.requestTimeoutCtx(ctx, true)
	defer cancel()
	stopKA := progress.KeepAlive(callCtx)
	defer stopKA()

	progress.Report(callCtx, progress.Update{
		Percent: 10,
		Message: "Submitting video task…",
		Key:     "mediagen.progress.submitting_video",
	})

	var data []byte
	var mimeType string

	switch target.protocol {
	case "openai-video":
		data, mimeType, err = s.genOpenAIVideo(callCtx, target, norm, inputs)
	case "gemini-aistudio-video", "gemini-vertex-video":
		data, mimeType, err = s.genGeminiVideo(callCtx, target, norm, inputs)
	case "ark-video":
		data, mimeType, err = s.genArkVideo(callCtx, target, norm, inputs)
	case "dashscope-video":
		data, mimeType, err = s.genDashScopeVideo(callCtx, target, norm, inputs)
	case "minimax-video":
		data, mimeType, err = s.genMiniMaxVideo(callCtx, target, norm, inputs)
	case "kling-video":
		data, mimeType, err = s.genKlingVideo(callCtx, target, norm, inputs)
	case "vidu-video":
		data, mimeType, err = s.genViduVideo(callCtx, target, norm, inputs)
	case "grok-video":
		data, mimeType, err = s.genGrokVideo(callCtx, target, norm, inputs)
	case "agnes-video":
		data, mimeType, err = s.genAgnesVideo(callCtx, target, norm, inputs)
	default:
		return domainmedia.Result{}, apperr.Validation("Unsupported video protocol: " + target.protocol)
	}
	if err != nil {
		return domainmedia.Result{}, err
	}

	res, err := s.saveOutput(domainmedia.KindVideo, norm.OutputDir, "vid", mimeType, ".mp4", data, target)
	if err != nil {
		return domainmedia.Result{}, err
	}
	res.Duration = float64(norm.DurationSec)
	progress.Report(callCtx, progress.Update{
		Percent: 100,
		Message: "Video ready",
		Key:     "mediagen.progress.done",
	})
	return res, nil
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

func (s *Service) genOpenAIVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) ([]byte, string, error) {
	base := providersvc.EnsureOpenAIBase(t.baseURL)
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
			return nil, "", err
		}
	} else {
		payload := map[string]any{
			"model":   t.modelID,
			"prompt":  req.Prompt,
			"seconds": seconds,
			"size":    size,
		}
		if err := s.doJSON(ctx, "POST", base+"/videos", headers, payload, &submitResp); err != nil {
			return nil, "", err
		}
	}
	if submitResp.ID == "" {
		return nil, "", apperr.Unavailable("OpenAI video returned empty job id")
	}

	var downloadDirect string
	err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating OpenAI video…", func() (bool, error) {
		var st struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
			URL string `json:"url"`
		}
		if err := s.doJSON(ctx, "GET", base+"/videos/"+url.PathEscape(submitResp.ID), headers, nil, &st); err != nil {
			return false, err
		}
		switch strings.ToLower(st.Status) {
		case "completed", "succeeded":
			downloadDirect = st.URL
			return true, nil
		case "failed", "expired", "cancelled":
			msg := st.Status
			if st.Error != nil && st.Error.Message != "" {
				msg = st.Error.Message
			}
			return true, apperr.Unavailable("OpenAI video failed: " + msg)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	if downloadDirect != "" {
		return s.downloadURL(ctx, downloadDirect, headers)
	}
	return s.doBinary(ctx, "GET", base+"/videos/"+url.PathEscape(submitResp.ID)+"/content", headers, nil)
}

func (s *Service) genGeminiVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) ([]byte, string, error) {
	modelID := geminiModelName(t.modelID)
	isVertex := t.protocol == "gemini-vertex-video"
	headers := map[string]string{}
	var submitURL, host, projectID string

	if isVertex {
		tok, proj, err := s.getVertexToken(ctx, t)
		if err != nil {
			return nil, "", err
		}
		projectID = proj
		host = strings.TrimRight(t.baseURL, "/")
		if host == "" {
			host = "https://aiplatform.googleapis.com"
		}
		headers["Authorization"] = "Bearer " + tok
		submitURL = fmt.Sprintf("%s/v1/projects/%s/locations/global/publishers/google/models/%s:predictLongRunning",
			host, projectID, modelID)
	} else {
		root := ensureRootStrippingSuffixes(t.baseURL, "/v1beta", "/v1")
		host = root
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
		return nil, "", err
	}
	if submitResp.Name == "" {
		return nil, "", apperr.Unavailable("Gemini video returned empty operation name")
	}

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

	var doneResp opResponse
	err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating Gemini/Veo video…", func() (bool, error) {
		var cur opResponse
		if isVertex {
			pollURL := fmt.Sprintf("%s/v1/projects/%s/locations/global/publishers/google/models/%s:fetchPredictOperation",
				host, projectID, modelID)
			if err := s.doJSON(ctx, "POST", pollURL, headers, map[string]any{"operationName": submitResp.Name}, &cur); err != nil {
				return false, err
			}
		} else {
			pollURL := fmt.Sprintf("%s/v1beta/%s", host, strings.TrimPrefix(submitResp.Name, "/"))
			if err := s.doJSON(ctx, "GET", pollURL, headers, nil, &cur); err != nil {
				return false, err
			}
		}
		if !cur.Done {
			return false, nil
		}
		if cur.Error != nil {
			return true, apperr.Unavailable("Gemini video failed: " + cur.Error.Message)
		}
		doneResp = cur
		return true, nil
	})
	if err != nil {
		return nil, "", err
	}
	if doneResp.Response == nil {
		return nil, "", apperr.Unavailable("Gemini operation completed without response payload")
	}
	if doneResp.Response.GenerateVideoResponse != nil {
		samples := doneResp.Response.GenerateVideoResponse.GeneratedSamples
		if len(samples) > 0 && samples[0].Video.URI != "" {
			raw, ct, err := s.downloadGeminiURI(ctx, samples[0].Video.URI, headers)
			if ct == "" {
				ct = samples[0].Video.MimeType
			}
			return raw, ct, err
		}
	}
	if len(doneResp.Response.Videos) > 0 {
		v := doneResp.Response.Videos[0]
		if v.BytesBase64Encoded != "" {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v.BytesBase64Encoded))
			if err != nil {
				return nil, "", apperr.Wrap(apperr.CodeUnavailable, "Invalid video base64 from Vertex AI", err)
			}
			mime := v.MimeType
			if mime == "" {
				mime = "video/mp4"
			}
			return raw, mime, nil
		}
		if v.GCSURI != "" {
			raw, ct, err := s.downloadGeminiURI(ctx, v.GCSURI, headers)
			if ct == "" {
				ct = v.MimeType
			}
			return raw, ct, err
		}
	}
	return nil, "", apperr.Unavailable("Gemini video completed with no downloadable output")
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

func (s *Service) genArkVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) ([]byte, string, error) {
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
		return nil, "", err
	}
	if submitResp.ID == "" {
		return nil, "", apperr.Unavailable("Ark video returned empty task id")
	}

	var videoURL string
	err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating Ark video…", func() (bool, error) {
		var st struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
			Content struct {
				VideoURL string `json:"video_url"`
			} `json:"content"`
		}
		if err := s.doJSON(ctx, "GET", base+"/contents/generations/tasks/"+url.PathEscape(submitResp.ID), headers, nil, &st); err != nil {
			return false, err
		}
		switch strings.ToLower(st.Status) {
		case "succeeded":
			videoURL = st.Content.VideoURL
			if videoURL == "" {
				return true, apperr.Unavailable("Ark task succeeded without video_url")
			}
			return true, nil
		case "failed", "expired":
			msg := st.Status
			if st.Error != nil && st.Error.Message != "" {
				msg = st.Error.Message
			}
			return true, apperr.Unavailable("Ark video failed: " + msg)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return s.downloadURL(ctx, videoURL, nil)
}

func (s *Service) genDashScopeVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) ([]byte, string, error) {
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
		return nil, "", err
	}
	if submitResp.Output.TaskID == "" {
		return nil, "", apperr.Unavailable("DashScope video submit error: " + submitResp.Message)
	}

	pollHeaders := map[string]string{"Authorization": "Bearer " + t.apiKey}
	var videoURL string
	err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating DashScope video…", func() (bool, error) {
		var st struct {
			Output struct {
				TaskStatus string `json:"task_status"`
				VideoURL   string `json:"video_url"`
				Message    string `json:"message"`
			} `json:"output"`
		}
		if err := s.doJSON(ctx, "GET", root+"/api/v1/tasks/"+url.PathEscape(submitResp.Output.TaskID), pollHeaders, nil, &st); err != nil {
			return false, err
		}
		switch strings.ToUpper(st.Output.TaskStatus) {
		case "SUCCEEDED":
			videoURL = st.Output.VideoURL
			if videoURL == "" {
				return true, apperr.Unavailable("DashScope finished without video_url")
			}
			return true, nil
		case "FAILED", "CANCELED", "UNKNOWN":
			return true, apperr.Unavailable("DashScope video failed: " + st.Output.Message)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return s.downloadURL(ctx, videoURL, nil)
}

func (s *Service) genMiniMaxVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) ([]byte, string, error) {
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
	if isH3 {
		submitPath = "/v2/video_generation"
	}
	var submitResp struct {
		TaskID   string `json:"task_id"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := s.doJSON(ctx, "POST", root+submitPath, headers, payload, &submitResp); err != nil {
		return nil, "", err
	}
	if submitResp.BaseResp.StatusCode != 0 {
		return nil, "", apperr.Unavailable(fmt.Sprintf("MiniMax video (%d): %s", submitResp.BaseResp.StatusCode, submitResp.BaseResp.StatusMsg))
	}
	if submitResp.TaskID == "" {
		return nil, "", apperr.Unavailable("MiniMax returned empty task_id")
	}

	var downloadURL string
	if isH3 {
		err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating MiniMax video…", func() (bool, error) {
			var st struct {
				Task struct {
					Status  string `json:"status"`
					Content struct {
						URL string `json:"url"`
					} `json:"content"`
					Error string `json:"error"`
				} `json:"task"`
			}
			if err := s.doJSON(ctx, "GET", root+"/v2/query/video_generation/"+url.PathEscape(submitResp.TaskID), headers, nil, &st); err != nil {
				return false, err
			}
			switch strings.ToLower(st.Task.Status) {
			case "succeeded":
				downloadURL = st.Task.Content.URL
				if downloadURL == "" {
					return true, apperr.Unavailable("MiniMax v2 finished without url")
				}
				return true, nil
			case "failed":
				return true, apperr.Unavailable("MiniMax v2 failed: " + st.Task.Error)
			default:
				return false, nil
			}
		})
		if err != nil {
			return nil, "", err
		}
		return s.downloadURL(ctx, downloadURL, nil)
	}

	var fileID string
	err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating MiniMax video…", func() (bool, error) {
		var st struct {
			Status   string `json:"status"`
			FileID   string `json:"file_id"`
			BaseResp struct {
				StatusCode int    `json:"status_code"`
				StatusMsg  string `json:"status_msg"`
			} `json:"base_resp"`
		}
		if err := s.doJSON(ctx, "GET", root+"/v1/query/video_generation?task_id="+url.QueryEscape(submitResp.TaskID), headers, nil, &st); err != nil {
			return false, err
		}
		switch st.Status {
		case "Success":
			fileID = st.FileID
			if fileID == "" {
				return true, apperr.Unavailable("MiniMax succeeded without file_id")
			}
			return true, nil
		case "Fail":
			return true, apperr.Unavailable("MiniMax video failed: " + st.BaseResp.StatusMsg)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	var ret struct {
		File struct {
			DownloadURL string `json:"download_url"`
		} `json:"file"`
	}
	if err := s.doJSON(ctx, "GET", root+"/v1/files/retrieve?file_id="+url.QueryEscape(fileID), headers, nil, &ret); err != nil {
		return nil, "", err
	}
	if ret.File.DownloadURL == "" {
		return nil, "", apperr.Unavailable("MiniMax file retrieve returned empty download_url")
	}
	return s.downloadURL(ctx, ret.File.DownloadURL, nil)
}

func (s *Service) genKlingVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) ([]byte, string, error) {
	tok, err := s.klingBearer(t)
	if err != nil {
		return nil, "", err
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
		return nil, "", err
	}
	if submitResp.Code != 0 && submitResp.Data.TaskID == "" {
		return nil, "", apperr.Unavailable("Kling video error: " + submitResp.Message)
	}
	taskID := submitResp.Data.TaskID
	if taskID == "" {
		return nil, "", apperr.Unavailable("Kling video returned empty task_id")
	}

	var videoURL string
	err = s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating Kling video…", func() (bool, error) {
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
		if err := s.doJSON(ctx, "GET", root+"/v1/videos/"+subpath+"/"+url.PathEscape(taskID), headers, nil, &st); err != nil {
			return false, err
		}
		switch st.Data.TaskStatus {
		case "succeed":
			if len(st.Data.TaskResult.Videos) == 0 || st.Data.TaskResult.Videos[0].URL == "" {
				return true, apperr.Unavailable("Kling succeeded without video URL")
			}
			videoURL = st.Data.TaskResult.Videos[0].URL
			return true, nil
		case "failed":
			return true, apperr.Unavailable("Kling video failed: " + st.Data.TaskStatusMsg)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return s.downloadURL(ctx, videoURL, nil)
}

func klingModeForResolution(res string) string {
	if strings.EqualFold(res, "1080p") {
		return "pro"
	}
	return "std"
}

func (s *Service) genViduVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) ([]byte, string, error) {
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
		return nil, "", err
	}
	if submitResp.TaskID == "" {
		return nil, "", apperr.Unavailable("Vidu returned empty task_id")
	}

	var videoURL string
	err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating Vidu video…", func() (bool, error) {
		var st struct {
			State     string `json:"state"`
			ErrCode   string `json:"err_code"`
			Creations []struct {
				URL string `json:"url"`
			} `json:"creations"`
		}
		if err := s.doJSON(ctx, "GET", base+"/tasks/"+url.PathEscape(submitResp.TaskID)+"/creations", headers, nil, &st); err != nil {
			return false, err
		}
		switch st.State {
		case "success":
			if len(st.Creations) == 0 || st.Creations[0].URL == "" {
				return true, apperr.Unavailable("Vidu succeeded without video url")
			}
			videoURL = st.Creations[0].URL
			return true, nil
		case "failed":
			return true, apperr.Unavailable("Vidu video failed: " + st.ErrCode)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return s.downloadURL(ctx, videoURL, nil)
}

func (s *Service) genGrokVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) ([]byte, string, error) {
	base := providersvc.EnsureOpenAIBase(t.baseURL)
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
		return nil, "", err
	}
	reqID := submitResp.RequestID
	if reqID == "" {
		reqID = submitResp.ID
	}
	if reqID == "" {
		return nil, "", apperr.Unavailable("Grok video returned empty request_id")
	}

	var videoURL string
	err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating Grok video…", func() (bool, error) {
		var st struct {
			Status string `json:"status"`
			Error  string `json:"error"`
			Video  struct {
				URL string `json:"url"`
			} `json:"video"`
		}
		if err := s.doJSON(ctx, "GET", base+"/videos/"+url.PathEscape(reqID), headers, nil, &st); err != nil {
			return false, err
		}
		switch strings.ToLower(st.Status) {
		case "done", "completed", "succeeded":
			videoURL = st.Video.URL
			if videoURL == "" {
				return true, apperr.Unavailable("Grok finished without video.url")
			}
			return true, nil
		case "failed", "expired":
			return true, apperr.Unavailable("Grok video failed: " + st.Error)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return s.downloadURL(ctx, videoURL, nil)
}

func (s *Service) genAgnesVideo(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) ([]byte, string, error) {
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
		return nil, "", err
	}
	taskID := submitResp.ID
	if taskID == "" {
		taskID = submitResp.TaskID
	}
	if taskID == "" {
		return nil, "", apperr.Unavailable("Agnes returned empty video task id")
	}

	isLegacy := strings.Contains(t.modelID, "2.5")
	var videoURL string
	err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Generating Agnes video…", func() (bool, error) {
		var st struct {
			Status   string `json:"status"`
			State    string `json:"state"`
			URL      string `json:"url"`
			VideoURL string `json:"video_url"`
			Error    string `json:"error"`
		}
		pollURL := root + "/v1/videos/" + url.PathEscape(taskID)
		if isLegacy {
			pollURL = root + "/agnesapi?video_id=" + url.QueryEscape(taskID) + "&model_name=" + url.QueryEscape(t.modelID)
		}
		if err := s.doJSON(ctx, "GET", pollURL, headers, nil, &st); err != nil {
			return false, err
		}
		status := strings.ToLower(st.Status)
		if status == "" {
			status = strings.ToLower(st.State)
		}
		switch status {
		case "completed", "succeeded", "success", "done":
			videoURL = st.URL
			if videoURL == "" {
				videoURL = st.VideoURL
			}
			if videoURL == "" {
				return true, apperr.Unavailable("Agnes completed without video url")
			}
			return true, nil
		case "failed", "error":
			return true, apperr.Unavailable("Agnes video failed: " + st.Error)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return s.downloadURL(ctx, videoURL, nil)
}
