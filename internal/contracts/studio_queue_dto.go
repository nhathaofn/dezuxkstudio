package contracts

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"app/internal/apperr"
	domainstudio "app/internal/domain/studio"
)

type StudioJob struct {
	ID            string                 `json:"id"`
	ProjectID     string                 `json:"project_id"`
	EpisodeID     string                 `json:"episode_id,omitempty"`
	ShotID        string                 `json:"shot_id,omitempty"`
	AssetID       string                 `json:"asset_id,omitempty"`
	JobKind       domainstudio.JobKind   `json:"job_kind"`
	ProviderID    string                 `json:"provider_id,omitempty"`
	ModelRef      string                 `json:"model_ref,omitempty"`
	Status        domainstudio.JobStatus `json:"status"`
	ProviderJobID string                 `json:"provider_job_id,omitempty"`
	Progress      int                    `json:"progress"`
	Attempts      int                    `json:"attempts"`
	MaxAttempts   int                    `json:"max_attempts"`
	NextPollAt    int64                  `json:"next_poll_at,omitempty"`
	OutputPath    string                 `json:"output_path,omitempty"`
	ErrorText     string                 `json:"error_text,omitempty"`
	EstimatedUSD  float64                `json:"estimated_usd"`
	ActualUSD     float64                `json:"actual_usd"`
	CreatedAt     int64                  `json:"created_at"`
	UpdatedAt     int64                  `json:"updated_at"`
	StartedAt     int64                  `json:"started_at,omitempty"`
	FinishedAt    int64                  `json:"finished_at,omitempty"`
}

// StudioQueueEnqueuePayload queues durable background jobs for an episode stage or target list.
type StudioQueueEnqueuePayload struct {
	EpisodeID string   `json:"episode_id"`
	Stage     string   `json:"stage"` // generate_assets | generate_keyframes | generate_videos | generate_tts | lipsync
	AssetIDs  []string `json:"asset_ids,omitempty"`
	ShotIDs   []string `json:"shot_ids,omitempty"`
	Model     string   `json:"model,omitempty"`
	Force     bool     `json:"force,omitempty"`
	Approved  bool     `json:"approved,omitempty"`
}

// StudioQueueStageActions returns the supported stage names for studio:queue_enqueue.
func StudioQueueStageActions() []string {
	return []string{
		string(StudioStageGenerateAssets),
		string(StudioStageGenerateKeyframes),
		string(StudioStageGenerateVideos),
		string(StudioStageGenerateTTS),
		"lipsync",
		"tts_dub_mux",
		"video_watermark_remove",
		"mask_edit",
		"grid_split",
		"extract_frames",
		"qc_review",
	}
}

// Validate checks StudioQueueEnqueuePayload fields.
func (p StudioQueueEnqueuePayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	stage := strings.ToLower(strings.TrimSpace(p.Stage))
	if stage == "" {
		return apperr.Validation("stage is required")
	}
	if !slices.Contains(StudioQueueStageActions(), stage) {
		return apperr.Validation(fmt.Sprintf("unsupported studio queue stage %q", p.Stage))
	}
	return nil
}

// StudioQueueEnqueueResult returns queued jobs and preflight total cost.
type StudioQueueEnqueueResult struct {
	Jobs         []StudioJob `json:"jobs"`
	Skipped      int         `json:"skipped"`
	EstimatedUSD float64     `json:"estimated_usd"`
	Summary      string      `json:"summary"`
}

// StudioQueueListPayload filters durable studio jobs.
type StudioQueueListPayload struct {
	ProjectID string                 `json:"project_id,omitempty"`
	EpisodeID string                 `json:"episode_id,omitempty"`
	Status    domainstudio.JobStatus `json:"status,omitempty"`
	Limit     int                    `json:"limit,omitempty"`
}

// Validate checks StudioQueueListPayload.
func (p StudioQueueListPayload) Validate() error {
	if st := domainstudio.JobStatus(strings.TrimSpace(string(p.Status))); st != "" && !st.Valid() {
		return apperr.Validation(fmt.Sprintf("invalid job status %q", p.Status))
	}
	if p.Limit < 0 {
		return apperr.Validation("limit must be >= 0")
	}
	return nil
}

// StudioQueueListResult returns matching studio jobs.
type StudioQueueListResult struct {
	Jobs []StudioJob `json:"jobs"`
}

// StudioQueueControlPayload pauses, resumes, cancels, or retries queue jobs.
type StudioQueueControlPayload struct {
	Action    StudioQueueAction `json:"action"` // pause | resume | cancel | retry
	ProjectID string            `json:"project_id,omitempty"`
	EpisodeID string            `json:"episode_id,omitempty"`
	JobID     string            `json:"job_id,omitempty"`
	Approved  bool              `json:"approved,omitempty"`
}

// Validate checks StudioQueueControlPayload.
func (p StudioQueueControlPayload) Validate() error {
	switch StudioQueueAction(strings.ToLower(strings.TrimSpace(string(p.Action)))) {
	case QueueActionPause, QueueActionResume, QueueActionCancel, QueueActionRetry:
		return nil
	default:
		return apperr.Validation("action must be pause, resume, cancel, or retry")
	}
}

// StudioQueueControlResult returns affected count and optional updated job.
type StudioQueueControlResult struct {
	Affected int        `json:"affected"`
	Job      *StudioJob `json:"job,omitempty"`
}

// StudioPricingEntry represents a rate in studio_pricing_catalog.
type StudioPricingEntry struct {
	ID           string  `json:"id"`
	ProviderID   string  `json:"provider_id"`
	ModelID      string  `json:"model_id"`
	Capability   string  `json:"capability"`
	Unit         string  `json:"unit"`
	UnitPriceUSD float64 `json:"unit_price_usd"`
	Currency     string  `json:"currency"`
	UpdatedAt    int64   `json:"updated_at"`
}

// StudioCostLedgerEntry represents one cost entry in studio_cost_ledger.
type StudioCostLedgerEntry struct {
	ID           string                  `json:"id"`
	ProjectID    string                  `json:"project_id,omitempty"`
	EpisodeID    string                  `json:"episode_id,omitempty"`
	ShotID       string                  `json:"shot_id,omitempty"`
	JobID        string                  `json:"job_id,omitempty"`
	ProviderID   string                  `json:"provider_id"`
	ModelID      string                  `json:"model_id,omitempty"`
	Capability   string                  `json:"capability"`
	Operation    string                  `json:"operation,omitempty"`
	InputUnits   float64                 `json:"input_units"`
	OutputUnits  float64                 `json:"output_units"`
	Unit         string                  `json:"unit"`
	EstimatedUSD float64                 `json:"estimated_usd"`
	ActualUSD    float64                 `json:"actual_usd"`
	Status       domainstudio.CostStatus `json:"status"`
	CreatedAt    int64                   `json:"created_at"`
}

// StudioCostSummaryPayload queries cost totals and recent ledger entries.
type StudioCostSummaryPayload struct {
	ProjectID string `json:"project_id"`
	EpisodeID string `json:"episode_id,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// Validate validates StudioCostSummaryPayload.
func (p StudioCostSummaryPayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" {
		return apperr.Validation("project_id is required")
	}
	if p.Limit < 0 {
		return apperr.Validation("limit must be >= 0")
	}
	return nil
}

// StudioCostSummaryResult returns cost totals, breakdown maps, recent ledger rows, and catalog pricing.
type StudioCostSummaryResult struct {
	ProjectID         string                  `json:"project_id"`
	EpisodeID         string                  `json:"episode_id,omitempty"`
	Currency          string                  `json:"currency"`
	EstimatedTotalUSD float64                 `json:"estimated_total_usd"`
	ActualTotalUSD    float64                 `json:"actual_total_usd"`
	EntryCount        int                     `json:"entry_count"`
	ByCapability      map[string]float64      `json:"by_capability"`
	ByProvider        map[string]float64      `json:"by_provider"`
	Entries           []StudioCostLedgerEntry `json:"entries"`
	Pricing           []StudioPricingEntry    `json:"pricing"`
}

// StudioPricingUpsertPayload updates or adds a pricing rate in studio_pricing_catalog.
type StudioPricingUpsertPayload struct {
	ProviderID   string  `json:"provider_id"`
	ModelID      string  `json:"model_id"`
	Capability   string  `json:"capability"`
	Unit         string  `json:"unit"`
	UnitPriceUSD float64 `json:"unit_price_usd"`
}

// Validate checks StudioPricingUpsertPayload.
func (p StudioPricingUpsertPayload) Validate() error {
	if strings.TrimSpace(p.ProviderID) == "" || strings.TrimSpace(p.ModelID) == "" || strings.TrimSpace(p.Capability) == "" || strings.TrimSpace(p.Unit) == "" {
		return apperr.Validation("provider_id, model_id, capability, and unit are required")
	}
	if math.IsNaN(p.UnitPriceUSD) || math.IsInf(p.UnitPriceUSD, 0) || p.UnitPriceUSD < 0 {
		return apperr.Validation("unit_price_usd must be >= 0")
	}
	return nil
}

// StudioMediaVersion represents an output version in studio_media_versions.

type StudioBatchPreflightPayload struct {
	ProjectID    string  `json:"project_id"`
	EpisodeID    string  `json:"episode_id,omitempty"`
	Stage        string  `json:"stage"`
	Model        string  `json:"model,omitempty"`
	ItemCount    int     `json:"item_count,omitempty"`
	UnitsPerItem float64 `json:"units_per_item,omitempty"`
}

// Validate checks StudioBatchPreflightPayload.
func (p StudioBatchPreflightPayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" || strings.TrimSpace(p.Stage) == "" {
		return apperr.Validation("project_id and stage are required")
	}
	if p.ItemCount < 0 {
		return apperr.Validation("item_count must be >= 0")
	}
	if math.IsNaN(p.UnitsPerItem) || math.IsInf(p.UnitsPerItem, 0) || p.UnitsPerItem < 0 {
		return apperr.Validation("units_per_item must be >= 0")
	}
	return nil
}

// StudioQueueUpdatedEvent is pushed when queue jobs change state.
type StudioQueueUpdatedEvent struct {
	ProjectID string     `json:"project_id"`
	EpisodeID string     `json:"episode_id,omitempty"`
	Job       *StudioJob `json:"job,omitempty"`
	Summary   string     `json:"summary,omitempty"`
}

// StudioCostUpdatedEvent is pushed when ledger spend updates.
type StudioCostUpdatedEvent struct {
	ProjectID         string  `json:"project_id"`
	EpisodeID         string  `json:"episode_id,omitempty"`
	ActualTotalUSD    float64 `json:"actual_total_usd"`
	EstimatedTotalUSD float64 `json:"estimated_total_usd"`
	EntryCount        int     `json:"entry_count"`
}

// StudioTimelineClip is the transport DTO for one trimmed media clip on a timeline track.
