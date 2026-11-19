package mediagen

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
)

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
			host = domainprovider.VertexAIBaseURL
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
	var trustedHosts []string
	if isVertex {
		vtok, proj, err := s.getVertexToken(ctx, t)
		if err != nil {
			return pollProbeOutcome{}, err
		}
		host := strings.TrimRight(t.baseURL, "/")
		if host == "" {
			host = domainprovider.VertexAIBaseURL
		}
		trustedHosts = []string{host, domainprovider.GCSObjectBaseURL}
		headers["Authorization"] = "Bearer " + vtok
		pollURL := fmt.Sprintf("%s/v1/projects/%s/locations/global/publishers/google/models/%s:fetchPredictOperation",
			host, proj, modelID)
		if err := s.doJSON(ctx, "POST", pollURL, headers, map[string]any{"operationName": tok.ProviderJobID}, &cur); err != nil {
			return pollProbeOutcome{}, err
		}
	} else {
		host := ensureRootStrippingSuffixes(t.baseURL, "/v1beta", "/v1")
		trustedHosts = []string{host}
		headers["x-goog-api-key"] = t.apiKey
		pollURL := fmt.Sprintf("%s/v1beta/%s", host, strings.TrimPrefix(tok.ProviderJobID, "/"))
		if err := s.doJSON(ctx, "GET", pollURL, headers, nil, &cur); err != nil {
			return pollProbeOutcome{}, err
		}
	}

	if !cur.Done {
		return pollProbeOutcome{status: "running", progress: defaultPollActiveProgress}, nil
	}
	if cur.Error != nil {
		err := providerUnavailableErr("Gemini video failed", cur.Error.Message)
		return pollProbeOutcome{failed: true, status: "failed", errText: err.Message}, err
	}
	if cur.Response == nil {
		err := apperr.Unavailable("Gemini operation completed without response payload")
		return pollProbeOutcome{failed: true, status: "failed", errText: err.Error()}, err
	}
	if cur.Response.GenerateVideoResponse != nil {
		samples := cur.Response.GenerateVideoResponse.GeneratedSamples
		if len(samples) > 0 && samples[0].Video.URI != "" {
			out, err := s.downloadGeminiURI(ctx, samples[0].Video.URI, headers, trustedHosts...)
			if err != nil {
				out.cleanup()
				return pollProbeOutcome{failed: true, errText: err.Error()}, err
			}
			ct := out.mimeType
			if ct == "" {
				ct = samples[0].Video.MimeType
			}
			return pollProbeOutcome{done: true, status: "done", progress: 100, filePath: out.filePath, mimeType: ct, downloadURL: samples[0].Video.URI}, nil
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
			out, err := s.downloadGeminiURI(ctx, v.GCSURI, headers, trustedHosts...)
			if err != nil {
				out.cleanup()
				return pollProbeOutcome{failed: true, errText: err.Error()}, err
			}
			ct := out.mimeType
			if ct == "" {
				ct = v.MimeType
			}
			return pollProbeOutcome{done: true, status: "done", progress: 100, filePath: out.filePath, mimeType: ct, downloadURL: v.GCSURI}, nil
		}
	}
	err := apperr.Unavailable("Gemini video completed with no downloadable output")
	return pollProbeOutcome{failed: true, status: "failed", errText: err.Error()}, err
}

func escapeGCSObjectPath(obj string) string {
	segs := strings.Split(strings.TrimPrefix(obj, "/"), "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/")
}

func (s *Service) downloadGeminiURI(ctx context.Context, uri string, authHeaders map[string]string, trustedURLs ...string) (mediaOutput, error) {
	if strings.HasPrefix(uri, "gs://") {
		trimmed := strings.TrimPrefix(uri, "gs://")
		parts := strings.SplitN(trimmed, "/", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return mediaOutput{}, apperr.Unavailable("Malformed GCS URI from provider")
		}
		uri = fmt.Sprintf("%s/%s/%s", domainprovider.GCSObjectBaseURL, url.PathEscape(parts[0]), escapeGCSObjectPath(parts[1]))
	}
	return s.downloadURLToFile(ctx, uri, authHeaders, trustedURLs...)
}

func (s *Service) downloadVideoOutcome(ctx context.Context, videoURL, status string) (pollProbeOutcome, error) {
	out, err := s.downloadURLToFile(ctx, videoURL, nil)
	if err != nil {
		out.cleanup()
		return pollProbeOutcome{failed: true, errText: err.Error()}, err
	}
	return pollProbeOutcome{done: true, status: status, progress: 100, filePath: out.filePath, mimeType: out.mimeType, downloadURL: videoURL}, nil
}
