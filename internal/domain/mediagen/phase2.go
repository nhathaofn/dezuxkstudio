package mediagen

import (
	"context"
	"strings"

	"app/internal/apperr"
)

// VideoSubmitToken holds non-secret protocol metadata required to resume polling
// a remote video or lip-sync job across process restarts without re-submitting.
type VideoSubmitToken struct {
	Protocol      string            `json:"protocol"`
	ProviderID    string            `json:"provider_id"`
	ModelRef      string            `json:"model_ref"`
	ModelID       string            `json:"model_id"`
	ProviderJobID string            `json:"provider_job_id"`
	PollURL       string            `json:"poll_url,omitempty"`
	DownloadURL   string            `json:"download_url,omitempty"`
	Subpath       string            `json:"subpath,omitempty"`
	EndpointURL   string            `json:"endpoint_url,omitempty"`
	OutputDir     string            `json:"output_dir,omitempty"`
	DurationSec   int               `json:"duration_sec,omitempty"`
	Extra         map[string]string `json:"extra,omitempty"`
	SubmittedAtMs int64             `json:"submitted_at_ms"`
}

// VideoPollStatus reports the result of one non-blocking poll probe on a remote job.
type VideoPollStatus struct {
	Done        bool
	Failed      bool
	Status      string
	Progress    int
	DownloadURL string
	ErrorText   string
	Result      Result
}

// LipSyncRequest specifies inputs for talking-head / character lip-sync generation.
type LipSyncRequest struct {
	ModelRef  string
	VideoPath string
	AudioPath string
	FaceIndex int
	Prompt    string
	OutputDir string
}

// NormalizeLipSyncRequest validates and trims a LipSyncRequest.
func NormalizeLipSyncRequest(req LipSyncRequest) (LipSyncRequest, error) {
	req.ModelRef = strings.TrimSpace(req.ModelRef)
	req.VideoPath = strings.TrimSpace(req.VideoPath)
	req.AudioPath = strings.TrimSpace(req.AudioPath)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.OutputDir = strings.TrimSpace(req.OutputDir)
	if req.VideoPath == "" {
		return LipSyncRequest{}, apperr.New(apperr.CodeValidation, "lip-sync video_path is required")
	}
	if req.AudioPath == "" {
		return LipSyncRequest{}, apperr.New(apperr.CodeValidation, "lip-sync audio_path is required")
	}
	if req.FaceIndex < 0 {
		req.FaceIndex = 0
	}
	return req, nil
}

// UsageMeta carries optional studio attribution attached to a generation context.
type UsageMeta struct {
	ProjectID    string
	EpisodeID    string
	ShotID       string
	JobID        string
	Operation    string
	EstimatedUSD float64
}

type usageMetaKey struct{}

// ContextWithUsageMeta attaches studio attribution metadata for cost ledger recording.
func ContextWithUsageMeta(ctx context.Context, meta UsageMeta) context.Context {
	return context.WithValue(ctx, usageMetaKey{}, meta)
}

// UsageMetaFromContext extracts studio attribution metadata if present.
func UsageMetaFromContext(ctx context.Context) UsageMeta {
	if ctx == nil {
		return UsageMeta{}
	}
	if v, ok := ctx.Value(usageMetaKey{}).(UsageMeta); ok {
		return v
	}
	return UsageMeta{}
}

// UsageEvent describes provider consumption reported by mediagen calls for billing ledger tracking.
type UsageEvent struct {
	ProjectID    string
	EpisodeID    string
	ShotID       string
	JobID        string
	ProviderID   string
	ModelID      string
	Capability   string
	Operation    string
	InputUnits   float64
	OutputUnits  float64
	Unit         string // "1k_tokens" | "image" | "second" | "1k_chars" | "request"
	EstimatedUSD float64
	ActualUSD    float64
	Failed       bool
}
