package contracts

import (
	"fmt"
	"strings"

	"app/internal/apperr"
	domainstudio "app/internal/domain/studio"
)

type StudioMediaVersion struct {
	ID         string                       `json:"id"`
	ProjectID  string                       `json:"project_id"`
	EpisodeID  string                       `json:"episode_id,omitempty"`
	TargetKind domainstudio.MediaTargetKind `json:"target_kind"`
	TargetID   string                       `json:"target_id"`
	VersionNum int                          `json:"version_num"`
	MediaPath  string                       `json:"media_path"`
	SourceNode string                       `json:"source_node,omitempty"`
	PromptUsed string                       `json:"prompt_used,omitempty"`
	ModelRef   string                       `json:"model_ref,omitempty"`
	Seed       *int                         `json:"seed,omitempty"`
	CostUSD    float64                      `json:"cost_usd"`
	QCStatus   domainstudio.QCStatus        `json:"qc_status"`
	IsSelected bool                         `json:"is_selected"`
	CreatedAt  int64                        `json:"created_at"`
}

// StudioVersionsListPayload lists versions for a target asset or shot slot.
type StudioVersionsListPayload struct {
	TargetKind domainstudio.MediaTargetKind `json:"target_kind"`
	TargetID   string                       `json:"target_id"`
}

// Validate checks StudioVersionsListPayload.
func (p StudioVersionsListPayload) Validate() error {
	kind := domainstudio.MediaTargetKind(strings.TrimSpace(string(p.TargetKind)))
	if !kind.Valid() || strings.TrimSpace(p.TargetID) == "" {
		return apperr.Validation("valid target_kind and target_id are required")
	}
	return nil
}

// StudioVersionsListResult returns version history plus latest QC report if any.
type StudioVersionsListResult struct {
	Versions []StudioMediaVersion `json:"versions"`
	LatestQC *StudioQCReport      `json:"latest_qc,omitempty"`
}

// StudioVersionSelectPayload activates a specific saved media version.
type StudioVersionSelectPayload struct {
	VersionID string `json:"version_id"`
}

// Validate checks StudioVersionSelectPayload.
func (p StudioVersionSelectPayload) Validate() error {
	if strings.TrimSpace(p.VersionID) == "" {
		return apperr.Validation("version_id is required")
	}
	return nil
}

// StudioVersionSelectResult returns the newly selected version and updated episode state if applicable.
type StudioVersionSelectResult struct {
	Version StudioMediaVersion         `json:"version"`
	Episode *StudioEpisodeDetailResult `json:"episode,omitempty"`
}

// StudioGridSplitPayload splits a grid image via tool.grid_split and records versions.
type StudioGridSplitPayload struct {
	ProjectID  string                       `json:"project_id"`
	EpisodeID  string                       `json:"episode_id,omitempty"`
	TargetKind domainstudio.MediaTargetKind `json:"target_kind,omitempty"`
	TargetID   string                       `json:"target_id,omitempty"`
	ImagePath  string                       `json:"image_path"`
	Rows       int                          `json:"rows"`
	Cols       int                          `json:"cols"`
	MarginPx   int                          `json:"margin_px,omitempty"`
	GutterPx   int                          `json:"gutter_px,omitempty"`
	RowWeights []float64                    `json:"row_weights,omitempty"`
	ColWeights []float64                    `json:"col_weights,omitempty"`
}

// Validate checks StudioGridSplitPayload.
func (p StudioGridSplitPayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" || strings.TrimSpace(p.ImagePath) == "" {
		return apperr.Validation("project_id and image_path are required")
	}
	if tk := domainstudio.MediaTargetKind(strings.TrimSpace(string(p.TargetKind))); tk != "" && !tk.Valid() {
		return apperr.Validation(fmt.Sprintf("invalid target_kind %q", p.TargetKind))
	}
	if p.Rows < 0 || p.Cols < 0 || p.MarginPx < 0 || p.GutterPx < 0 {
		return apperr.Validation("rows, cols, margin_px, and gutter_px must be >= 0")
	}
	return nil
}

// StudioGridSplitResult returns split panels and created version records.
type StudioGridSplitResult struct {
	Panels   []GridPanel          `json:"panels"`
	Versions []StudioMediaVersion `json:"versions"`
}

// StudioMaskEditPayload runs canvas-masked inpainting and selects the resulting image version.
type StudioMaskEditPayload struct {
	ProjectID      string                       `json:"project_id"`
	EpisodeID      string                       `json:"episode_id,omitempty"`
	TargetKind     domainstudio.MediaTargetKind `json:"target_kind,omitempty"`
	TargetID       string                       `json:"target_id,omitempty"`
	ImagePath      string                       `json:"image_path"`
	MaskPath       string                       `json:"mask_path,omitempty"`
	MaskDataBase64 string                       `json:"mask_data_base64,omitempty"`
	Prompt         string                       `json:"prompt"`
	ModelRef       string                       `json:"model_ref,omitempty"`
	AspectRatio    string                       `json:"aspect_ratio,omitempty"`
	Resolution     string                       `json:"resolution,omitempty"`
	Approved       bool                         `json:"approved,omitempty"`
}

// Validate checks StudioMaskEditPayload.
func (p StudioMaskEditPayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" || strings.TrimSpace(p.ImagePath) == "" || strings.TrimSpace(p.Prompt) == "" {
		return apperr.Validation("project_id, image_path, and prompt are required")
	}
	if strings.TrimSpace(p.MaskPath) == "" && strings.TrimSpace(p.MaskDataBase64) == "" {
		return apperr.Validation("mask_path or mask_data_base64 is required")
	}
	if tk := domainstudio.MediaTargetKind(strings.TrimSpace(string(p.TargetKind))); tk != "" && !tk.Valid() {
		return apperr.Validation(fmt.Sprintf("invalid target_kind %q", p.TargetKind))
	}
	return nil
}

// StudioMaskEditResult returns the inpainted file and updated episode state.
type StudioMaskEditResult struct {
	Path    string                     `json:"path"`
	AssetID string                     `json:"asset_id,omitempty"`
	Version *StudioMediaVersion        `json:"version,omitempty"`
	Episode *StudioEpisodeDetailResult `json:"episode,omitempty"`
}

// StudioExtractLastFramePayload extracts the last frame from a shot's video and optionally binds it to the next shot.
type StudioExtractLastFramePayload struct {
	EpisodeID    string `json:"episode_id"`
	SourceShotID string `json:"source_shot_id"`
	TargetShotID string `json:"target_shot_id,omitempty"`
}

// Validate checks StudioExtractLastFramePayload.
func (p StudioExtractLastFramePayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" || strings.TrimSpace(p.SourceShotID) == "" {
		return apperr.Validation("episode_id and source_shot_id are required")
	}
	return nil
}

// StudioExtractLastFrameResult returns the extracted frame and updated episode detail.
type StudioExtractLastFrameResult struct {
	FramePath string                     `json:"frame_path"`
	AssetID   string                     `json:"asset_id,omitempty"`
	Version   *StudioMediaVersion        `json:"version,omitempty"`
	Episode   *StudioEpisodeDetailResult `json:"episode,omitempty"`
}

// StudioQCReviewPayload runs tool.qc_review on a media artifact and saves a studio_qc_reports row.
type StudioQCReviewPayload struct {
	ProjectID      string                       `json:"project_id"`
	EpisodeID      string                       `json:"episode_id,omitempty"`
	TargetKind     domainstudio.MediaTargetKind `json:"target_kind,omitempty"`
	TargetID       string                       `json:"target_id,omitempty"`
	MediaVersionID string                       `json:"media_version_id,omitempty"`
	MediaPath      string                       `json:"media_path"`
	ReferencePaths []string                     `json:"reference_paths,omitempty"`
	PromptExpected string                       `json:"prompt_expected,omitempty"`
	VisionModelRef string                       `json:"vision_model_ref,omitempty"`
	SampleCount    int                          `json:"sample_count,omitempty"`
	SkipVision     bool                         `json:"skip_vision,omitempty"`
	Approved       bool                         `json:"approved,omitempty"`
}

// Validate checks StudioQCReviewPayload.
func (p StudioQCReviewPayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" || strings.TrimSpace(p.MediaPath) == "" {
		return apperr.Validation("project_id and media_path are required")
	}
	if tk := domainstudio.MediaTargetKind(strings.TrimSpace(string(p.TargetKind))); tk != "" && !tk.Valid() {
		return apperr.Validation(fmt.Sprintf("invalid target_kind %q", p.TargetKind))
	}
	if p.SampleCount < 0 {
		return apperr.Validation("sample_count must be >= 0")
	}
	return nil
}

// StudioQCReport represents a saved visual & technical QC report.
type StudioQCReport struct {
	ID             string                       `json:"id"`
	ProjectID      string                       `json:"project_id"`
	EpisodeID      string                       `json:"episode_id,omitempty"`
	TargetKind     domainstudio.MediaTargetKind `json:"target_kind"`
	TargetID       string                       `json:"target_id"`
	MediaVersionID string                       `json:"media_version_id,omitempty"`
	MediaPath      string                       `json:"media_path"`
	Verdict        domainstudio.QCStatus        `json:"verdict"`
	OverallScore   float64                      `json:"overall_score"`
	Issues         []QCReviewIssue              `json:"issues"`
	VisionSummary  string                       `json:"vision_summary,omitempty"`
	VisionModelRef string                       `json:"vision_model_ref,omitempty"`
	SampleFrames   []string                     `json:"sample_frames"`
	CreatedAt      int64                        `json:"created_at"`
}

// StudioBatchPreflightPayload computes readiness and estimated cost for an episode stage batch.
