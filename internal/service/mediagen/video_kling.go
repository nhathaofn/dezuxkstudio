package mediagen

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
)

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
		return domainmedia.VideoSubmitToken{}, providerUnavailableErr("Kling video error", submitResp.Message)
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
		return s.downloadVideoOutcome(ctx, videoURL, st.Data.TaskStatus)
	case "failed":
		err := providerUnavailableErr("Kling video failed", st.Data.TaskStatusMsg)
		return pollProbeOutcome{failed: true, status: st.Data.TaskStatus, errText: err.Message}, err
	default:
		return pollProbeOutcome{status: st.Data.TaskStatus, progress: defaultPollActiveProgress}, nil
	}
}

func klingModeForResolution(res string) string {
	if strings.EqualFold(res, "1080p") {
		return "pro"
	}
	return "std"
}
