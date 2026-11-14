package mediagen

import (
	"context"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"strings"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/progress"
)

var markdownImageURLPattern = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)\)`)

// GenerateImage generates or edits a single image through whichever built-in or custom
// provider ModelRef resolves to (or the user's configured default when ModelRef is empty).
func (s *Service) GenerateImage(ctx context.Context, req domainmedia.ImageRequest) (domainmedia.Result, error) {
	norm, err := domainmedia.NormalizeImageRequest(req)
	if err != nil {
		return domainmedia.Result{}, err
	}
	refs := make([]localImageData, 0, len(norm.ReferenceImages))
	for _, p := range norm.ReferenceImages {
		img, err := s.readReferenceImage(p)
		if err != nil {
			return domainmedia.Result{}, err
		}
		refs = append(refs, img)
	}

	target, limits, err := s.resolveTarget(ctx, domainprovider.PurposeImage, norm.ModelRef)
	if err != nil {
		return domainmedia.Result{}, err
	}

	release, err := s.limiter.acquire(ctx, target.ownerID, domainprovider.CapabilityImage, limits)
	if err != nil {
		return domainmedia.Result{}, apperr.From(err)
	}
	defer release()

	callCtx, cancel := s.requestTimeoutCtx(ctx, false)
	defer cancel()
	stopKA := progress.KeepAlive(callCtx)
	defer stopKA()

	progress.Report(callCtx, progress.Update{
		Percent: 10,
		Message: "Generating image…",
		Key:     "mediagen.progress.generating_image",
	})

	var data []byte
	var mimeType string

	switch target.protocol {
	case "openai-images", "openai-images-generations", "openai-images-edits":
		data, mimeType, err = s.genOpenAIImages(callCtx, target, norm, refs)
	case "openai-chat-image":
		data, mimeType, err = s.genOpenAIChatImage(callCtx, target, norm, refs)
	case "openrouter-images":
		data, mimeType, err = s.genOpenRouterImages(callCtx, target, norm, refs)
	case "gemini-aistudio-image", "gemini-vertex-image":
		data, mimeType, err = s.genGeminiImage(callCtx, target, norm, refs)
	case "ark-image":
		data, mimeType, err = s.genArkImage(callCtx, target, norm, refs)
	case "dashscope-image":
		data, mimeType, err = s.genDashScopeImage(callCtx, target, norm, refs)
	case "minimax-image":
		data, mimeType, err = s.genMiniMaxImage(callCtx, target, norm, refs)
	case "kling-image":
		data, mimeType, err = s.genKlingImage(callCtx, target, norm, refs)
	case "vidu-image":
		data, mimeType, err = s.genViduImage(callCtx, target, norm, refs)
	case "grok-image":
		data, mimeType, err = s.genGrokImage(callCtx, target, norm, refs)
	case "agnes-image":
		data, mimeType, err = s.genAgnesImage(callCtx, target, norm, refs)
	default:
		return domainmedia.Result{}, apperr.Validation("Unsupported image protocol: " + target.protocol)
	}
	if err != nil {
		return domainmedia.Result{}, err
	}

	res, err := s.saveOutput(domainmedia.KindImage, norm.OutputDir, "img", mimeType, ".png", data, target)
	if err != nil {
		return domainmedia.Result{}, err
	}
	progress.Report(callCtx, progress.Update{
		Percent: 100,
		Message: "Image ready",
		Key:     "mediagen.progress.done",
	})
	return res, nil
}

type openAIImageResp struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
		URL     string `json:"url"`
	} `json:"data"`
}

func (s *Service) genOpenAIImages(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	size := domainmedia.OpenAIImageSize(req.AspectRatio, req.Resolution)

	if t.protocol == "openai-images-edits" && len(refs) == 0 {
		return nil, "", apperr.Validation("The selected image edit endpoint requires at least one reference image").
			WithMsg("errors.mediagen.reference_required", nil)
	}
	if t.protocol == "openai-images-generations" && len(refs) > 0 {
		return nil, "", apperr.Validation("The selected image generation endpoint does not accept reference images").
			WithMsg("errors.mediagen.reference_not_supported", nil)
	}

	var out openAIImageResp
	if len(refs) == 0 || t.protocol == "openai-images-generations" {
		payload := map[string]any{
			"model":  t.modelID,
			"prompt": req.Prompt,
			"n":      1,
			"size":   size,
		}
		if err := s.doJSON(ctx, "POST", base+"/images/generations", headers, payload, &out); err != nil {
			return nil, "", err
		}
	} else {
		fieldName := "image"
		if len(refs) > 1 {
			fieldName = "image[]"
		}
		err := s.doMultipart(ctx, base+"/images/edits", headers, func(mw *multipart.Writer) error {
			_ = mw.WriteField("model", t.modelID)
			_ = mw.WriteField("prompt", req.Prompt)
			_ = mw.WriteField("size", size)
			for i, ref := range refs {
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="ref_%d%s"`, fieldName, i, domainmedia.ExtForMime(ref.MimeType, ".png")))
				h.Set("Content-Type", ref.MimeType)
				part, err := mw.CreatePart(h)
				if err != nil {
					return apperr.Internal(err)
				}
				if _, err := part.Write(ref.Raw); err != nil {
					return apperr.Internal(err)
				}
			}
			return nil
		}, &out)
		if err != nil {
			return nil, "", err
		}
	}

	if len(out.Data) == 0 {
		return nil, "", apperr.Unavailable("Image provider returned empty data array")
	}
	return s.decodeDataOrDownload(ctx, out.Data[0].B64JSON, out.Data[0].URL, ".png", nil)
}

func (s *Service) genOpenAIChatImage(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	parts := make([]map[string]any, 0, len(refs)+1)
	parts = append(parts, map[string]any{"type": "text", "text": req.Prompt})
	for _, ref := range refs {
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": ref.DataURI},
		})
	}
	imgCfg := map[string]any{"aspect_ratio": req.AspectRatio}
	if req.Resolution == "2K" || req.Resolution == "4K" {
		imgCfg["image_size"] = req.Resolution
	}
	payload := map[string]any{
		"model":        t.modelID,
		"messages":     []map[string]any{{"role": "user", "content": parts}},
		"modalities":   []string{"image", "text"},
		"image_config": imgCfg,
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
				Images  []struct {
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
					URL string `json:"url"`
				} `json:"images"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := s.doJSON(ctx, "POST", base+"/chat/completions", headers, payload, &resp); err != nil {
		return nil, "", err
	}
	if len(resp.Choices) == 0 {
		return nil, "", apperr.Unavailable("Chat-image provider returned no choices")
	}
	msg := resp.Choices[0].Message
	for _, img := range msg.Images {
		u := img.ImageURL.URL
		if u == "" {
			u = img.URL
		}
		if u != "" {
			return s.decodeDataOrDownload(ctx, u, "", ".png", nil)
		}
	}
	switch c := msg.Content.(type) {
	case string:
		if m := markdownImageURLPattern.FindStringSubmatch(c); len(m) == 2 {
			return s.decodeDataOrDownload(ctx, m[1], "", ".png", nil)
		}
	case []any:
		for _, item := range c {
			obj, _ := item.(map[string]any)
			if iu, _ := obj["image_url"].(map[string]any); iu != nil {
				if u, _ := iu["url"].(string); u != "" {
					return s.decodeDataOrDownload(ctx, u, "", ".png", nil)
				}
			}
		}
	}
	return nil, "", apperr.Unavailable("Chat-image response did not contain an image")
}

func (s *Service) genOpenRouterImages(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	payload := map[string]any{
		"model":        t.modelID,
		"prompt":       req.Prompt,
		"n":            1,
		"aspect_ratio": req.AspectRatio,
	}
	if req.Resolution != "" {
		payload["resolution"] = strings.ToLower(req.Resolution)
	}
	if len(refs) > 0 {
		refURIs := make([]string, len(refs))
		for i, r := range refs {
			refURIs[i] = r.DataURI
		}
		payload["input_references"] = refURIs
	}
	if req.Seed != nil {
		payload["seed"] = *req.Seed
	}

	var out openAIImageResp
	if err := s.doJSON(ctx, "POST", base+"/images", headers, payload, &out); err != nil {
		return nil, "", err
	}
	if len(out.Data) == 0 {
		return nil, "", apperr.Unavailable("OpenRouter returned empty image list")
	}
	return s.decodeDataOrDownload(ctx, out.Data[0].B64JSON, out.Data[0].URL, ".png", nil)
}

func (s *Service) genGeminiImage(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	modelID := geminiModelName(t.modelID)
	isVertex := t.protocol == "gemini-vertex-image"
	isImagen := strings.HasPrefix(strings.ToLower(modelID), "imagen-")

	var targetURL string
	headers := map[string]string{}
	if isVertex {
		tok, proj, err := s.getVertexToken(ctx, t)
		if err != nil {
			return nil, "", err
		}
		host := strings.TrimRight(t.baseURL, "/")
		if host == "" {
			host = "https://aiplatform.googleapis.com"
		}
		headers["Authorization"] = "Bearer " + tok
		action := "generateContent"
		if isImagen {
			action = "predict"
		}
		targetURL = fmt.Sprintf("%s/v1beta1/projects/%s/locations/global/publishers/google/models/%s:%s",
			host, proj, modelID, action)
	} else {
		root := ensureRootStrippingSuffixes(t.baseURL, "/v1beta", "/v1")
		headers["x-goog-api-key"] = t.apiKey
		action := "generateContent"
		if isImagen {
			action = "predict"
		}
		targetURL = fmt.Sprintf("%s/v1beta/models/%s:%s", root, modelID, action)
	}

	if isImagen {
		payload := map[string]any{
			"instances": []map[string]any{{"prompt": req.Prompt}},
			"parameters": map[string]any{
				"sampleCount": 1,
				"aspectRatio": req.AspectRatio,
			},
		}
		var predResp struct {
			Predictions []struct {
				BytesBase64Encoded string `json:"bytesBase64Encoded"`
				MimeType           string `json:"mimeType"`
			} `json:"predictions"`
		}
		if err := s.doJSON(ctx, "POST", targetURL, headers, payload, &predResp); err != nil {
			return nil, "", err
		}
		if len(predResp.Predictions) == 0 || predResp.Predictions[0].BytesBase64Encoded == "" {
			return nil, "", apperr.Unavailable("Imagen returned no predictions")
		}
		raw, _, err := s.decodeDataOrDownload(ctx, predResp.Predictions[0].BytesBase64Encoded, "", ".png", nil)
		mime := predResp.Predictions[0].MimeType
		if mime == "" {
			mime = "image/png"
		}
		return raw, mime, err
	}

	parts := make([]map[string]any, 0, len(refs)+1)
	for _, ref := range refs {
		parts = append(parts, map[string]any{
			"inlineData": map[string]any{
				"mimeType": ref.MimeType,
				"data":     ref.Base64,
			},
		})
	}
	parts = append(parts, map[string]any{"text": req.Prompt})

	imgCfg := map[string]any{"aspectRatio": req.AspectRatio}
	if req.Resolution == "2K" || req.Resolution == "4K" {
		imgCfg["imageSize"] = req.Resolution
	}

	payload := map[string]any{
		"contents": []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig": map[string]any{
			"responseModalities": []string{"IMAGE"},
			"imageConfig":        imgCfg,
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
			if p.InlineData != nil && p.InlineData.Data != "" {
				raw, _, err := s.decodeDataOrDownload(ctx, p.InlineData.Data, "", ".png", nil)
				mime := p.InlineData.MimeType
				if mime == "" {
					mime = "image/png"
				}
				return raw, mime, err
			}
		}
	}
	return nil, "", apperr.Unavailable("Gemini returned no image in response")
}

func (s *Service) genArkImage(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	base := ensureArkBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	w, h := domainmedia.ImagePixelSize(req.AspectRatio, req.Resolution)
	size := fmt.Sprintf("%dx%d", w, h)
	if req.Resolution == "2K" || req.Resolution == "4K" {
		if req.AspectRatio == "1:1" {
			size = req.Resolution
		}
	}

	payload := map[string]any{
		"model":           t.modelID,
		"prompt":          req.Prompt,
		"size":            size,
		"watermark":       false,
		"response_format": "b64_json",
	}
	if len(refs) == 1 {
		payload["image"] = refs[0].DataURI
	} else if len(refs) > 1 {
		uris := make([]string, len(refs))
		for i, r := range refs {
			uris[i] = r.DataURI
		}
		payload["image"] = uris
	}
	if req.Seed != nil {
		payload["seed"] = *req.Seed
	}

	var out openAIImageResp
	if err := s.doJSON(ctx, "POST", base+"/images/generations", headers, payload, &out); err != nil {
		return nil, "", err
	}
	if len(out.Data) == 0 {
		return nil, "", apperr.Unavailable("Ark returned empty image data")
	}
	return s.decodeDataOrDownload(ctx, out.Data[0].B64JSON, out.Data[0].URL, ".png", nil)
}

func (s *Service) genDashScopeImage(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/compatible-mode/v1", "/api/v1", "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	w, h := domainmedia.ImagePixelSize(req.AspectRatio, req.Resolution)

	content := make([]map[string]any, 0, len(refs)+1)
	for _, ref := range refs {
		content = append(content, map[string]any{"image": ref.DataURI})
	}
	content = append(content, map[string]any{"text": req.Prompt})

	payload := map[string]any{
		"model": t.modelID,
		"input": map[string]any{
			"messages": []map[string]any{{"role": "user", "content": content}},
		},
		"parameters": map[string]any{
			"size":      fmt.Sprintf("%d*%d", w, h),
			"watermark": false,
		},
	}

	var resp struct {
		Output struct {
			Choices []struct {
				Message struct {
					Content []struct {
						Image string `json:"image"`
					} `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		} `json:"output"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := s.doJSON(ctx, "POST", root+"/api/v1/services/aigc/multimodal-generation/generation", headers, payload, &resp); err != nil {
		return nil, "", err
	}
	for _, ch := range resp.Output.Choices {
		for _, item := range ch.Message.Content {
			if item.Image != "" {
				return s.downloadURL(ctx, item.Image, nil)
			}
		}
	}
	if resp.Message != "" {
		return nil, "", apperr.Unavailable("DashScope: " + resp.Message)
	}
	return nil, "", apperr.Unavailable("DashScope returned no generated image URL")
}

func (s *Service) genMiniMaxImage(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	payload := map[string]any{
		"model":        t.modelID,
		"prompt":       req.Prompt,
		"aspect_ratio": req.AspectRatio,
		"n":            1,
	}
	if len(refs) > 0 {
		subRefs := make([]map[string]any, len(refs))
		for i, r := range refs {
			subRefs[i] = map[string]any{
				"type":       "character",
				"image_file": r.DataURI,
			}
		}
		payload["subject_reference"] = subRefs
	}
	if req.Seed != nil {
		payload["seed"] = *req.Seed
	}

	var resp struct {
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
		Data struct {
			ImageURLs   []string `json:"image_urls"`
			ImageBase64 []string `json:"image_base64"`
		} `json:"data"`
	}
	if err := s.doJSON(ctx, "POST", root+"/v1/image_generation", headers, payload, &resp); err != nil {
		return nil, "", err
	}
	if resp.BaseResp.StatusCode != 0 {
		return nil, "", apperr.Unavailable(fmt.Sprintf("MiniMax (%d): %s", resp.BaseResp.StatusCode, resp.BaseResp.StatusMsg))
	}
	var b64, urlStr string
	if len(resp.Data.ImageBase64) > 0 {
		b64 = resp.Data.ImageBase64[0]
	}
	if len(resp.Data.ImageURLs) > 0 {
		urlStr = resp.Data.ImageURLs[0]
	}
	return s.decodeDataOrDownload(ctx, b64, urlStr, ".png", nil)
}

func (s *Service) genKlingImage(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	tok, err := s.klingBearer(t)
	if err != nil {
		return nil, "", err
	}
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	headers := map[string]string{"Authorization": "Bearer " + tok}

	payload := map[string]any{
		"model_name":   t.modelID,
		"prompt":       req.Prompt,
		"aspect_ratio": req.AspectRatio,
		"n":            1,
	}
	if len(refs) > 0 {
		payload["image"] = refs[0].Base64
	}

	var submitResp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := s.doJSON(ctx, "POST", root+"/v1/images/generations", headers, payload, &submitResp); err != nil {
		return nil, "", err
	}
	if submitResp.Code != 0 && submitResp.Data.TaskID == "" {
		return nil, "", apperr.Unavailable("Kling submit error: " + submitResp.Message)
	}
	taskID := submitResp.Data.TaskID
	if taskID == "" {
		return nil, "", apperr.Unavailable("Kling returned empty task_id")
	}

	var resultURL string
	err = s.pollUntil(ctx, "mediagen.progress.polling", "Waiting for Kling image…", func() (bool, error) {
		var st struct {
			Data struct {
				TaskStatus    string `json:"task_status"`
				TaskStatusMsg string `json:"task_status_msg"`
				TaskResult    struct {
					Images []struct {
						URL string `json:"url"`
					} `json:"images"`
				} `json:"task_result"`
			} `json:"data"`
		}
		if err := s.doJSON(ctx, "GET", root+"/v1/images/generations/"+taskID, headers, nil, &st); err != nil {
			return false, err
		}
		switch st.Data.TaskStatus {
		case "succeed":
			if len(st.Data.TaskResult.Images) == 0 || st.Data.TaskResult.Images[0].URL == "" {
				return true, apperr.Unavailable("Kling finished with no image URL")
			}
			resultURL = st.Data.TaskResult.Images[0].URL
			return true, nil
		case "failed":
			return true, apperr.Unavailable("Kling image failed: " + st.Data.TaskStatusMsg)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return s.downloadURL(ctx, resultURL, nil)
}

func (s *Service) genViduImage(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	base := strings.TrimRight(t.baseURL, "/")
	headers := map[string]string{"Authorization": "Token " + t.apiKey}

	payload := map[string]any{
		"model":        t.modelID,
		"prompt":       req.Prompt,
		"aspect_ratio": req.AspectRatio,
	}
	if req.Resolution != "" {
		payload["resolution"] = strings.ToLower(req.Resolution)
	}
	if len(refs) > 0 {
		imgs := make([]string, len(refs))
		for i, r := range refs {
			imgs[i] = r.DataURI
		}
		payload["images"] = imgs
	}
	if req.Seed != nil {
		payload["seed"] = *req.Seed
	}

	var submitResp struct {
		TaskID string `json:"task_id"`
	}
	if err := s.doJSON(ctx, "POST", base+"/reference2image", headers, payload, &submitResp); err != nil {
		return nil, "", err
	}
	if submitResp.TaskID == "" {
		return nil, "", apperr.Unavailable("Vidu returned empty task_id")
	}

	var resultURL string
	err := s.pollUntil(ctx, "mediagen.progress.polling", "Waiting for Vidu image…", func() (bool, error) {
		var st struct {
			State     string `json:"state"`
			ErrCode   string `json:"err_code"`
			Creations []struct {
				URL string `json:"url"`
			} `json:"creations"`
		}
		if err := s.doJSON(ctx, "GET", base+"/tasks/"+submitResp.TaskID+"/creations", headers, nil, &st); err != nil {
			return false, err
		}
		switch st.State {
		case "success":
			if len(st.Creations) == 0 || st.Creations[0].URL == "" {
				return true, apperr.Unavailable("Vidu succeeded with no creations")
			}
			resultURL = st.Creations[0].URL
			return true, nil
		case "failed":
			return true, apperr.Unavailable("Vidu image failed: " + st.ErrCode)
		default:
			return false, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return s.downloadURL(ctx, resultURL, nil)
}

func (s *Service) genGrokImage(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	base := domainprovider.EnsureOpenAIBase(t.baseURL)
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	payload := map[string]any{
		"model":        t.modelID,
		"prompt":       req.Prompt,
		"aspect_ratio": req.AspectRatio,
	}
	if req.Resolution == "2K" {
		payload["resolution"] = "2k"
	} else {
		payload["resolution"] = "1k"
	}
	if len(refs) == 1 {
		payload["image"] = map[string]any{"url": refs[0].DataURI, "type": "image_url"}
	} else if len(refs) > 1 {
		imgs := make([]map[string]any, len(refs))
		for i, r := range refs {
			imgs[i] = map[string]any{"url": r.DataURI, "type": "image_url"}
		}
		payload["images"] = imgs
	}

	var out openAIImageResp
	if err := s.doJSON(ctx, "POST", base+"/images/generations", headers, payload, &out); err != nil {
		return nil, "", err
	}
	if len(out.Data) == 0 {
		return nil, "", apperr.Unavailable("Grok returned empty image list")
	}
	return s.decodeDataOrDownload(ctx, out.Data[0].B64JSON, out.Data[0].URL, ".png", nil)
}

func (s *Service) genAgnesImage(ctx context.Context, t targetSpec, req domainmedia.ImageRequest, refs []localImageData) ([]byte, string, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}
	w, h := domainmedia.ImagePixelSize(req.AspectRatio, req.Resolution)

	payload := map[string]any{
		"model":  t.modelID,
		"prompt": req.Prompt,
		"n":      1,
		"size":   fmt.Sprintf("%dx%d", w, h),
	}
	if len(refs) > 0 {
		imgs := make([]string, len(refs))
		for i, r := range refs {
			imgs[i] = r.DataURI
		}
		payload["image"] = imgs
	}

	var out openAIImageResp
	if err := s.doJSON(ctx, "POST", root+"/v1/images/generations", headers, payload, &out); err != nil {
		return nil, "", err
	}
	if len(out.Data) == 0 {
		return nil, "", apperr.Unavailable("Agnes returned empty image data")
	}
	return s.decodeDataOrDownload(ctx, out.Data[0].B64JSON, out.Data[0].URL, ".png", nil)
}
