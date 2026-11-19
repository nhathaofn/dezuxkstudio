package mediagen

import (
	"context"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
)

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
			if err := writeMultipartField(mw, "model", t.modelID); err != nil {
				return err
			}
			if err := writeMultipartField(mw, "prompt", req.Prompt); err != nil {
				return err
			}
			if err := writeMultipartField(mw, "seconds", seconds); err != nil {
				return err
			}
			if err := writeMultipartField(mw, "size", size); err != nil {
				return err
			}
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
		var out mediaOutput
		var dlErr error
		if st.URL != "" {
			out, dlErr = s.downloadURLToFile(ctx, st.URL, headers, base)
		} else {
			out, dlErr = s.doBinaryToFile(ctx, "GET", base+"/videos/"+url.PathEscape(tok.ProviderJobID)+"/content", headers, nil)
		}
		if dlErr != nil {
			out.cleanup()
			return pollProbeOutcome{failed: true, status: st.Status, errText: dlErr.Error()}, dlErr
		}
		return pollProbeOutcome{done: true, status: st.Status, progress: 100, filePath: out.filePath, mimeType: out.mimeType, downloadURL: st.URL}, nil
	case "failed", "expired", "cancelled":
		msg := st.Status
		if st.Error != nil && st.Error.Message != "" {
			msg = st.Error.Message
		}
		err := providerUnavailableErr("OpenAI video failed", msg)
		return pollProbeOutcome{failed: true, status: st.Status, errText: err.Message}, err
	default:
		return pollProbeOutcome{status: st.Status, progress: openAIPollActiveProgress}, nil
	}
}
