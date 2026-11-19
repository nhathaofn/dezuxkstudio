package mediagen

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
)

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
		return s.downloadVideoOutcome(ctx, st.Content.VideoURL, st.Status)
	case "failed", "expired":
		msg := st.Status
		if st.Error != nil && st.Error.Message != "" {
			msg = st.Error.Message
		}
		err := providerUnavailableErr("Ark video failed", msg)
		return pollProbeOutcome{failed: true, status: st.Status, errText: err.Message}, err
	default:
		return pollProbeOutcome{status: st.Status, progress: defaultPollActiveProgress}, nil
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
		return domainmedia.VideoSubmitToken{}, providerUnavailableErr("DashScope video submit error", submitResp.Message)
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
		return s.downloadVideoOutcome(ctx, st.Output.VideoURL, st.Output.TaskStatus)
	case "FAILED", "CANCELED", "UNKNOWN":
		err := providerUnavailableErr("DashScope video failed", st.Output.Message)
		return pollProbeOutcome{failed: true, status: st.Output.TaskStatus, errText: err.Message}, err
	default:
		return pollProbeOutcome{status: st.Output.TaskStatus, progress: defaultPollActiveProgress}, nil
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
		return domainmedia.VideoSubmitToken{}, providerUnavailableErr(fmt.Sprintf("MiniMax video (%d)", submitResp.BaseResp.StatusCode), submitResp.BaseResp.StatusMsg)
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
			return s.downloadVideoOutcome(ctx, st.Task.Content.URL, st.Task.Status)
		case "failed":
			err := providerUnavailableErr("MiniMax v2 failed", st.Task.Error)
			return pollProbeOutcome{failed: true, status: st.Task.Status, errText: err.Message}, err
		default:
			return pollProbeOutcome{status: st.Task.Status, progress: defaultPollActiveProgress}, nil
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
		return s.downloadVideoOutcome(ctx, ret.File.DownloadURL, st.Status)
	case "Fail":
		err := providerUnavailableErr("MiniMax video failed", st.BaseResp.StatusMsg)
		return pollProbeOutcome{failed: true, status: st.Status, errText: err.Message}, err
	default:
		return pollProbeOutcome{status: st.Status, progress: defaultPollActiveProgress}, nil
	}
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
		return s.downloadVideoOutcome(ctx, videoURL, st.State)
	case "failed":
		err := providerUnavailableErr("Vidu video failed", st.ErrCode)
		return pollProbeOutcome{failed: true, status: st.State, errText: err.Message}, err
	default:
		return pollProbeOutcome{status: st.State, progress: defaultPollActiveProgress}, nil
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
		return s.downloadVideoOutcome(ctx, st.Video.URL, st.Status)
	case "failed", "expired":
		err := providerUnavailableErr("Grok video failed", st.Error)
		return pollProbeOutcome{failed: true, status: st.Status, errText: err.Message}, err
	default:
		return pollProbeOutcome{status: st.Status, progress: defaultPollActiveProgress}, nil
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
		return s.downloadVideoOutcome(ctx, videoURL, status)
	case "failed", "error":
		err := providerUnavailableErr("Agnes video failed", st.Error)
		return pollProbeOutcome{failed: true, status: status, errText: err.Message}, err
	default:
		return pollProbeOutcome{status: status, progress: defaultPollActiveProgress}, nil
	}
}
