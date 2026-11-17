package mediagen

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/config"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/progress"
)

func (s *Service) readBinaryInput(path, fallbackMime string) ([]byte, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", apperr.Wrap(apperr.CodeValidation, "Cannot read lip-sync input "+filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }()

	maxBytes := s.cfg.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = config.ResolveMediaGenConfig(s.cfg).MaxOutputBytes
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, "", apperr.Wrap(apperr.CodeInternal, "Failed reading lip-sync input", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, "", apperr.Validation("Lip-sync input file exceeds maximum allowed size")
	}
	ct := http.DetectContentType(raw)
	if ct == "application/octet-stream" && fallbackMime != "" {
		ct = fallbackMime
	}
	return raw, ct, nil
}

// GenerateLipSync submits and polls a lip-sync video task (built-in Kling/MiniMax or custom EndpointLipSync).
func (s *Service) GenerateLipSync(ctx context.Context, req domainmedia.LipSyncRequest) (domainmedia.Result, error) {
	norm, err := domainmedia.NormalizeLipSyncRequest(req)
	if err != nil {
		return domainmedia.Result{}, err
	}
	releaseLeases := s.leaseInputs(norm.VideoPath, norm.AudioPath)
	defer releaseLeases()

	vidRaw, vidMime, err := s.readBinaryInput(norm.VideoPath, "video/mp4")
	if err != nil {
		return domainmedia.Result{}, err
	}
	audRaw, audMime, err := s.readBinaryInput(norm.AudioPath, "audio/mpeg")
	if err != nil {
		return domainmedia.Result{}, err
	}

	target, limits, err := s.resolveTarget(ctx, domainprovider.PurposeLipSync, norm.ModelRef)
	if err != nil {
		return domainmedia.Result{}, err
	}

	release, err := s.acquireLimit(ctx, target, domainprovider.CapabilityLipSync, limits)
	if err != nil {
		return domainmedia.Result{}, err
	}
	defer release()

	callCtx, cleanup := s.prepareJobContext(ctx, true)
	defer cleanup()

	progress.Report(callCtx, progress.Update{
		Percent: 10,
		Message: "Generating lip-sync video…",
		Key:     "mediagen.progress.lipsync",
	})

	var data []byte
	var mimeType string

	switch target.protocol {
	case "kling-lipsync":
		data, mimeType, err = s.genKlingLipSync(callCtx, target, norm, vidRaw, audRaw)
	case "minimax-lipsync":
		data, mimeType, err = s.genMiniMaxLipSync(callCtx, target, norm, vidRaw, audRaw)
	case "openai-lipsync":
		data, mimeType, err = s.genOpenAILipSync(callCtx, target, norm, vidRaw, vidMime, audRaw, audMime)
	default:
		return domainmedia.Result{}, apperr.Validation("Unsupported lip-sync protocol: " + target.protocol)
	}
	if err != nil {
		return domainmedia.Result{}, err
	}

	res, err := s.saveOutput(domainmedia.KindVideo, norm.OutputDir, "lipsync", mimeType, ".mp4", data, target)
	if err != nil {
		return domainmedia.Result{}, err
	}
	s.emitUsage(ctx, domainmedia.UsageEvent{
		ProviderID:  target.ownerID,
		ModelID:     target.modelID,
		Capability:  string(domainprovider.CapabilityLipSync),
		Operation:   "lipsync_generate",
		OutputUnits: 1,
		Unit:        "second",
	})
	progress.Report(callCtx, progress.Update{
		Percent: 100,
		Message: "Lip-sync ready",
		Key:     "mediagen.progress.done",
	})
	return res, nil
}

func (s *Service) genKlingLipSync(ctx context.Context, t targetSpec, req domainmedia.LipSyncRequest, vidRaw, audRaw []byte) ([]byte, string, error) {
	bearer, err := s.klingBearer(t)
	if err != nil {
		return nil, "", err
	}
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	headers := map[string]string{"Authorization": "Bearer " + bearer}

	payload := map[string]any{
		"input": map[string]any{
			"video":      base64.StdEncoding.EncodeToString(vidRaw),
			"mode":       "audio2video",
			"audio_type": "file",
			"audio_file": base64.StdEncoding.EncodeToString(audRaw),
			"face_index": req.FaceIndex,
		},
	}
	if t.modelID != "" {
		payload["model_name"] = t.modelID
	}
	if req.Prompt != "" {
		payload["prompt"] = req.Prompt
	}

	var submitResp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := s.doJSON(ctx, "POST", root+"/v1/videos/lip-sync", headers, payload, &submitResp); err != nil {
		return nil, "", err
	}
	if submitResp.Code != 0 && submitResp.Data.TaskID == "" {
		return nil, "", apperr.Unavailable("Kling lip-sync error: " + submitResp.Message)
	}
	if submitResp.Data.TaskID == "" {
		return nil, "", apperr.Unavailable("Kling lip-sync returned empty task_id")
	}

	var outURL string
	err = s.pollUntil(ctx, "mediagen.progress.polling_video", "Waiting for Kling lip-sync…", func() (bool, error) {
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
		if err := s.doJSON(ctx, "GET", root+"/v1/videos/lip-sync/"+url.PathEscape(submitResp.Data.TaskID), headers, nil, &st); err != nil {
			return false, err
		}
		switch st.Data.TaskStatus {
		case "succeed":
			if len(st.Data.TaskResult.Videos) == 0 || st.Data.TaskResult.Videos[0].URL == "" {
				return true, apperr.Unavailable("Kling lip-sync finished with no video URL")
			}
			outURL = st.Data.TaskResult.Videos[0].URL
			return true, nil
		case "failed":
			return true, apperr.Unavailable("Kling lip-sync failed: " + st.Data.TaskStatusMsg)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return s.downloadURL(ctx, outURL, nil)
}

func (s *Service) genMiniMaxLipSync(ctx context.Context, t targetSpec, req domainmedia.LipSyncRequest, vidRaw, audRaw []byte) ([]byte, string, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v2", "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	payload := map[string]any{
		"model":      t.modelID,
		"video":      base64.StdEncoding.EncodeToString(vidRaw),
		"audio":      base64.StdEncoding.EncodeToString(audRaw),
		"face_index": req.FaceIndex,
		"prompt":     req.Prompt,
	}
	var submitResp struct {
		TaskID   string `json:"task_id"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := s.doJSON(ctx, "POST", root+"/v1/video_generation", headers, payload, &submitResp); err != nil {
		return nil, "", err
	}
	if submitResp.BaseResp.StatusCode != 0 {
		return nil, "", apperr.Unavailable(fmt.Sprintf("MiniMax lip-sync (%d): %s", submitResp.BaseResp.StatusCode, submitResp.BaseResp.StatusMsg))
	}
	if submitResp.TaskID == "" {
		return nil, "", apperr.Unavailable("MiniMax lip-sync returned empty task_id")
	}

	var fileID string
	err := s.pollUntil(ctx, "mediagen.progress.polling_video", "Waiting for MiniMax lip-sync…", func() (bool, error) {
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
			return true, nil
		case "Fail":
			return true, apperr.Unavailable("MiniMax lip-sync failed: " + st.BaseResp.StatusMsg)
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
		return nil, "", apperr.Unavailable("MiniMax lip-sync retrieve returned empty download_url")
	}
	return s.downloadURL(ctx, ret.File.DownloadURL, nil)
}

func (s *Service) genOpenAILipSync(ctx context.Context, t targetSpec, req domainmedia.LipSyncRequest, vidRaw []byte, vidMime string, audRaw []byte, audMime string) ([]byte, string, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	var resp struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		URL     string `json:"url"`
		B64JSON string `json:"b64_json"`
	}
	err := s.doMultipart(ctx, base+"/videos/lipsync", headers, func(mw *multipart.Writer) error {
		if t.modelID != "" {
			_ = mw.WriteField("model", t.modelID)
		}
		if req.Prompt != "" {
			_ = mw.WriteField("prompt", req.Prompt)
		}
		_ = mw.WriteField("face_index", strconv.Itoa(req.FaceIndex))
		writePart := func(field, fname, mime string, data []byte) error {
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, fname))
			h.Set("Content-Type", mime)
			p, err := mw.CreatePart(h)
			if err != nil {
				return apperr.Internal(err)
			}
			_, err = p.Write(data)
			return err
		}
		if err := writePart("video", "video.mp4", vidMime, vidRaw); err != nil {
			return err
		}
		return writePart("audio", "audio"+domainmedia.ExtForMime(audMime, ".mp3"), audMime, audRaw)
	}, &resp)
	if err != nil {
		return nil, "", err
	}

	if resp.URL != "" || resp.B64JSON != "" {
		return s.decodeDataOrDownload(ctx, resp.B64JSON, resp.URL, ".mp4", headers)
	}
	if resp.ID == "" {
		return nil, "", apperr.Unavailable("Lip-sync endpoint returned empty id and url")
	}

	var outURL string
	err = s.pollUntil(ctx, "mediagen.progress.polling_video", "Waiting for lip-sync job…", func() (bool, error) {
		var st struct {
			Status string `json:"status"`
			URL    string `json:"url"`
			Error  string `json:"error"`
		}
		if err := s.doJSON(ctx, "GET", base+"/videos/lipsync/"+url.PathEscape(resp.ID), headers, nil, &st); err != nil {
			return false, err
		}
		switch strings.ToLower(st.Status) {
		case "completed", "succeeded", "done":
			outURL = st.URL
			return true, nil
		case "failed", "error":
			return true, apperr.Unavailable("Lip-sync failed: " + st.Error)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	if outURL != "" {
		return s.downloadURL(ctx, outURL, headers)
	}
	return s.doBinary(ctx, "GET", base+"/videos/"+url.PathEscape(resp.ID)+"/content", headers, nil)
}
