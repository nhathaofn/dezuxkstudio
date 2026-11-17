package studio

import (
	"context"
	"fmt"
	"strings"
)

// JobStatus tracks durable background job execution across app restarts.
type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobSubmitted JobStatus = "submitted"
	JobPolling   JobStatus = "polling"
	JobPaused    JobStatus = "paused"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCanceled  JobStatus = "canceled"
)

// Valid reports whether s is a supported job status.
func (s JobStatus) Valid() bool {
	switch s {
	case JobQueued, JobSubmitted, JobPolling, JobPaused, JobSucceeded, JobFailed, JobCanceled:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether the job will perform no further work unless retried.
func (s JobStatus) IsTerminal() bool {
	return s == JobSucceeded || s == JobFailed || s == JobCanceled
}

// JobKind identifies the media or processing operation executed by a studio job.
type JobKind string

const (
	JobImage                JobKind = "image"
	JobVideo                JobKind = "video"
	JobTTS                  JobKind = "tts"
	JobLipSync              JobKind = "lipsync"
	JobMaskEdit             JobKind = "mask_edit"
	JobGridSplit            JobKind = "grid_split"
	JobExtractFrames        JobKind = "extract_frames"
	JobTTSDubMux            JobKind = "tts_dub_mux"
	JobVideoWatermarkRemove JobKind = "video_wm_remove"
	JobQCReview             JobKind = "qc_review"
)

// Valid reports whether k is a known studio job kind.
func (k JobKind) Valid() bool {
	switch k {
	case JobImage, JobVideo, JobTTS, JobLipSync, JobMaskEdit, JobGridSplit, JobExtractFrames, JobTTSDubMux, JobVideoWatermarkRemove, JobQCReview:
		return true
	default:
		return false
	}
}

// Job represents a persisted studio generation or processing task in studio_jobs.
type Job struct {
	ID              string
	ProjectID       string
	EpisodeID       string
	ShotID          string
	AssetID         string
	JobKind         JobKind
	ProviderID      string
	ModelRef        string
	IdempotencyKey  string
	RequestJSON     string
	Status          JobStatus
	ProviderJobID   string // non-empty once provider submit call succeeded; startup resumes polling instead of re-submitting
	SubmitTokenJSON string // serialized polling state (protocol, poll URL, endpoint metadata; never secrets)
	Progress        int
	Attempts        int
	MaxAttempts     int
	NextPollAt      int64
	OutputPath      string
	OutputJSON      string
	ErrorText       string
	EstimatedUSD    float64
	ActualUSD       float64
	CreatedAt       int64
	UpdatedAt       int64
	StartedAt       int64
	FinishedAt      int64
}

// Validate checks invariants and fills default status/attempts on a Job.
func (j *Job) Validate() error {
	if strings.TrimSpace(j.ProjectID) == "" {
		return fmt.Errorf("studio job project_id is required")
	}
	if !j.JobKind.Valid() {
		return fmt.Errorf("invalid studio job_kind %q", j.JobKind)
	}
	if j.Status == "" {
		j.Status = JobQueued
	}
	if !j.Status.Valid() {
		return fmt.Errorf("invalid studio job status %q", j.Status)
	}
	if strings.TrimSpace(j.IdempotencyKey) == "" {
		return fmt.Errorf("studio job idempotency_key is required")
	}
	if j.RequestJSON == "" {
		j.RequestJSON = "{}"
	}
	if j.OutputJSON == "" {
		j.OutputJSON = "{}"
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 3
	}
	return nil
}

// JobFilter constrains ListJobs queries.
type JobFilter struct {
	ProjectID string
	EpisodeID string
	ShotID    string
	Status    JobStatus
	JobKind   JobKind
	Limit     int
}

// PricingEntry stores unit pricing for a provider/model/capability tuple.
type PricingEntry struct {
	ID           string
	ProviderID   string
	ModelID      string
	Capability   string
	Unit         string // "1k_tokens" | "image" | "second" | "1k_chars" | "request"
	UnitPriceUSD float64
	Currency     string
	UpdatedAt    int64
}

// CostStatus identifies whether a ledger row is an estimate, finalized cost, or failed attempt.
type CostStatus string

const (
	CostEstimated CostStatus = "estimated"
	CostRecorded  CostStatus = "recorded"
	CostFailed    CostStatus = "failed"
)

// CostLedgerEntry records actual or estimated provider spend in studio_cost_ledger.
type CostLedgerEntry struct {
	ID           string
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
	Unit         string
	EstimatedUSD float64
	ActualUSD    float64
	Status       CostStatus
	MetadataJSON string
	CreatedAt    int64
}

// CostFilter constrains ListCosts queries.
type CostFilter struct {
	ProjectID string
	EpisodeID string
	JobID     string
	Limit     int
}

// CostSummary aggregates studio cost totals by capability and provider.
type CostSummary struct {
	ProjectID         string
	EpisodeID         string
	Currency          string
	EstimatedTotalUSD float64
	ActualTotalUSD    float64
	EntryCount        int
	ByCapability      map[string]float64
	ByProvider        map[string]float64
}

// MediaTargetKind identifies what studio entity a generated media version belongs to.
type MediaTargetKind string

const (
	TargetAsset        MediaTargetKind = "asset"
	TargetKeyframe     MediaTargetKind = "keyframe"
	TargetVideo        MediaTargetKind = "video"
	TargetDubAudio     MediaTargetKind = "dub_audio"
	TargetLipSyncVideo MediaTargetKind = "lipsync_video"
	TargetGridPanel    MediaTargetKind = "grid_panel"
	TargetCleanVideo   MediaTargetKind = "clean_video"
)

// Valid reports whether k is a recognized MediaTargetKind.
func (k MediaTargetKind) Valid() bool {
	switch k {
	case TargetAsset, TargetKeyframe, TargetVideo, TargetDubAudio, TargetLipSyncVideo, TargetGridPanel, TargetCleanVideo:
		return true
	default:
		return false
	}
}

// QCStatus records automated and multimodal visual QC verdict for media.
type QCStatus string

const (
	QCPending QCStatus = "pending"
	QCPass    QCStatus = "pass"
	QCWarn    QCStatus = "warn"
	QCFail    QCStatus = "fail"
)

// Valid reports whether s is a valid QC verdict.
func (s QCStatus) Valid() bool {
	switch s {
	case QCPending, QCPass, QCWarn, QCFail:
		return true
	default:
		return false
	}
}

// MediaVersion tracks selectable output variants (v1, v2, v3...) for an asset or shot.
type MediaVersion struct {
	ID           string
	ProjectID    string
	EpisodeID    string
	TargetKind   MediaTargetKind
	TargetID     string
	VersionNum   int
	MediaPath    string
	SourceNode   string
	PromptUsed   string
	ModelRef     string
	Seed         *int
	CostUSD      float64
	QCStatus     QCStatus
	IsSelected   bool
	MetadataJSON string
	CreatedAt    int64
}

// QCIssue describes one technical or visual finding surfaced by tool.qc_review.
type QCIssue struct {
	Severity  string `json:"severity"` // "info" | "warn" | "error"
	Category  string `json:"category"` // "technical" | "continuity" | "artifacts" | "audio"
	Message   string `json:"message"`
	FramePath string `json:"frame_path,omitempty"`
}

// QCReport stores technical probe + vision critique output for a target media file.
type QCReport struct {
	ID             string
	ProjectID      string
	EpisodeID      string
	TargetKind     MediaTargetKind
	TargetID       string
	MediaVersionID string
	MediaPath      string
	Verdict        QCStatus
	OverallScore   float64
	FFprobeJSON    string
	Issues         []QCIssue
	VisionSummary  string
	VisionModelRef string
	SampleFrames   []string
	CreatedAt      int64
}

// Phase2Repository defines persistence operations added in Studio Phase 2.
type Phase2Repository interface {
	CreateJob(ctx context.Context, job Job) (Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	GetJobByIdempotencyKey(ctx context.Context, key string) (Job, error)
	ListJobs(ctx context.Context, filter JobFilter) ([]Job, error)
	ListRecoverableJobs(ctx context.Context) ([]Job, error)
	ClaimNextQueuedJob(ctx context.Context, nowMs int64) (Job, bool, error)
	ClaimNextPollJob(ctx context.Context, nowMs, leaseMs int64) (Job, bool, error)
	SetJobProviderSubmission(ctx context.Context, id, providerJobID, submitTokenJSON string, status JobStatus, nextPollAt int64) (Job, error)
	UpdateJob(ctx context.Context, job Job) (Job, error)
	PauseEpisodeJobs(ctx context.Context, projectID, episodeID string) (int, error)
	ResumeEpisodeJobs(ctx context.Context, projectID, episodeID string) (int, error)
	CancelJob(ctx context.Context, id string) (Job, error)

	ListPricing(ctx context.Context) ([]PricingEntry, error)
	UpsertPricing(ctx context.Context, entry PricingEntry) (PricingEntry, error)
	ResolveUnitPrice(ctx context.Context, providerID, modelID, capability, unit string) (PricingEntry, bool, error)
	RecordCost(ctx context.Context, entry CostLedgerEntry) (CostLedgerEntry, error)
	ListCosts(ctx context.Context, filter CostFilter) ([]CostLedgerEntry, error)
	SummarizeCosts(ctx context.Context, projectID, episodeID string) (CostSummary, error)

	CreateMediaVersion(ctx context.Context, v MediaVersion) (MediaVersion, error)
	ListMediaVersions(ctx context.Context, targetKind MediaTargetKind, targetID string) ([]MediaVersion, error)
	GetMediaVersion(ctx context.Context, id string) (MediaVersion, error)
	SelectMediaVersion(ctx context.Context, id string) (MediaVersion, error)
	UpdateMediaVersionQC(ctx context.Context, id string, status QCStatus) error

	CreateQCReport(ctx context.Context, r QCReport) (QCReport, error)
	ListQCReports(ctx context.Context, targetKind MediaTargetKind, targetID string) ([]QCReport, error)
	GetLatestQCReport(ctx context.Context, targetKind MediaTargetKind, targetID string) (QCReport, error)
}
