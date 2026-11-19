package studio

import (
	"context"
	"fmt"
	"strings"

	"app/internal/apperr"
	domainprovider "app/internal/domain/provider"
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

// JobStatuses returns all supported durable studio job statuses.
func JobStatuses() []JobStatus {
	return []JobStatus{JobQueued, JobSubmitted, JobPolling, JobPaused, JobSucceeded, JobFailed, JobCanceled}
}

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

// JobKinds returns all supported studio background job kinds.
func JobKinds() []JobKind {
	return []JobKind{
		JobImage, JobVideo, JobTTS, JobLipSync,
		JobMaskEdit, JobGridSplit, JobExtractFrames, JobTTSDubMux,
		JobVideoWatermarkRemove, JobQCReview,
	}
}

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

// DefaultJobMaxAttempts is the baseline retry budget for a durable studio job when unset.
const DefaultJobMaxAttempts = 3

// Validate checks invariants and fills default status/attempts on a Job.
func (j *Job) Validate() error {
	return j.ValidateWithMaxAttempts(DefaultJobMaxAttempts)
}

// ValidateWithMaxAttempts checks invariants and applies defaultMaxAttempts when j.MaxAttempts <= 0.
func (j *Job) ValidateWithMaxAttempts(defaultMaxAttempts int) error {
	if defaultMaxAttempts <= 0 {
		defaultMaxAttempts = DefaultJobMaxAttempts
	}
	if strings.TrimSpace(j.ProjectID) == "" {
		return apperr.Validation("studio job project_id is required").
			WithMsg("errors.studio.job_project_required", nil)
	}
	if !j.JobKind.Valid() {
		return apperr.Validation(fmt.Sprintf("invalid studio job_kind %q", j.JobKind)).
			WithMsg("errors.studio.invalid_job_kind", map[string]any{"value": string(j.JobKind)})
	}
	if j.Status == "" {
		j.Status = JobQueued
	}
	if !j.Status.Valid() {
		return apperr.Validation(fmt.Sprintf("invalid studio job status %q", j.Status)).
			WithMsg("errors.studio.invalid_job_status", map[string]any{"value": string(j.Status)})
	}
	if strings.TrimSpace(j.IdempotencyKey) == "" {
		return apperr.Validation("studio job idempotency_key is required").
			WithMsg("errors.studio.job_idempotency_required", nil)
	}
	if j.RequestJSON == "" {
		j.RequestJSON = "{}"
	}
	if j.OutputJSON == "" {
		j.OutputJSON = "{}"
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = defaultMaxAttempts
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

// CapabilityPricingSpec defines canonical billing units, throughput estimates, and baseline fallback rates.
// Keep aligned with migration 019_studio_queue_billing.sql seed rows.
type CapabilityPricingSpec struct {
	Capability           domainprovider.Capability
	Unit                 string
	DefaultUnitsPerItem  float64
	EstimatedSecPerItem  int
	FallbackUnitPriceUSD float64
}

const (
	// DefaultCatalogCurrency is the canonical currency code for studio billing and pricing entries.
	DefaultCatalogCurrency = "USD"

	defaultFallbackUnit       = "request"
	defaultFallbackUnitsPer   = 1.0
	defaultFallbackSecPerItem = 10
	defaultFallbackPriceUSD   = 0.01
	defaultCatalogCurrency    = DefaultCatalogCurrency
	defaultPricingSeedUpdated = int64(1763337600000)
)

var capabilityPricingCatalog = []CapabilityPricingSpec{
	{
		Capability:           domainprovider.CapabilityLLM,
		Unit:                 "1k_tokens",
		DefaultUnitsPerItem:  2.0,
		EstimatedSecPerItem:  4,
		FallbackUnitPriceUSD: 0.005,
	},
	{
		Capability:           domainprovider.CapabilityImage,
		Unit:                 "image",
		DefaultUnitsPerItem:  1.0,
		EstimatedSecPerItem:  8,
		FallbackUnitPriceUSD: 0.04,
	},
	{
		Capability:           domainprovider.CapabilityVideo,
		Unit:                 "second",
		DefaultUnitsPerItem:  5.0,
		EstimatedSecPerItem:  35,
		FallbackUnitPriceUSD: 0.08,
	},
	{
		Capability:           domainprovider.CapabilityTTS,
		Unit:                 "1k_chars",
		DefaultUnitsPerItem:  0.5,
		EstimatedSecPerItem:  4,
		FallbackUnitPriceUSD: 0.015,
	},
	{
		Capability:           domainprovider.CapabilityLipSync,
		Unit:                 "second",
		DefaultUnitsPerItem:  5.0,
		EstimatedSecPerItem:  45,
		FallbackUnitPriceUSD: 0.12,
	},
}

// PreflightSpecForCapability returns the single canonical billing unit, throughput, and baseline rate spec for capName.
func PreflightSpecForCapability(capName string) CapabilityPricingSpec {
	normalized := domainprovider.Capability(strings.ToLower(strings.TrimSpace(capName)))
	for _, spec := range capabilityPricingCatalog {
		if spec.Capability == normalized {
			return spec
		}
	}
	return CapabilityPricingSpec{
		Capability:           normalized,
		Unit:                 defaultFallbackUnit,
		DefaultUnitsPerItem:  defaultFallbackUnitsPer,
		EstimatedSecPerItem:  defaultFallbackSecPerItem,
		FallbackUnitPriceUSD: defaultFallbackPriceUSD,
	}
}

// PreflightSpecForCapabilityWithDuration returns the capability pricing spec overriding video/lipsync default shot seconds when > 0.
func PreflightSpecForCapabilityWithDuration(capName string, defaultShotDurationSec int) CapabilityPricingSpec {
	spec := PreflightSpecForCapability(capName)
	if defaultShotDurationSec > 0 && (spec.Capability == domainprovider.CapabilityVideo || spec.Capability == domainprovider.CapabilityLipSync) {
		spec.DefaultUnitsPerItem = float64(defaultShotDurationSec)
	}
	return spec
}

// DefaultPricingCatalog returns the canonical baseline pricing catalog entries shared across preflight, billing, and storage seeding
// (kept aligned with 019_studio_queue_billing.sql and 022_studio_pricing_fixes.sql).
func DefaultPricingCatalog() []PricingEntry {
	out := make([]PricingEntry, 0, len(capabilityPricingCatalog)+12)
	for _, spec := range capabilityPricingCatalog {
		out = append(out, PricingEntry{
			ID:           "price_default_" + string(spec.Capability),
			ProviderID:   "*",
			ModelID:      "*",
			Capability:   string(spec.Capability),
			Unit:         spec.Unit,
			UnitPriceUSD: spec.FallbackUnitPriceUSD,
			Currency:     defaultCatalogCurrency,
			UpdatedAt:    defaultPricingSeedUpdated,
		})
	}
	providerSeeds := []PricingEntry{
		{ID: "price_openai_gpt_image", ProviderID: domainprovider.IDOpenAI, ModelID: "gpt-image-1", Capability: string(domainprovider.CapabilityImage), Unit: "image", UnitPriceUSD: 0.040},
		{ID: "price_openai_sora2", ProviderID: domainprovider.IDOpenAI, ModelID: "sora-2", Capability: string(domainprovider.CapabilityVideo), Unit: "second", UnitPriceUSD: 0.100},
		{ID: "price_openai_tts1", ProviderID: domainprovider.IDOpenAI, ModelID: "tts-1", Capability: string(domainprovider.CapabilityTTS), Unit: "1k_chars", UnitPriceUSD: 0.015},
		{ID: "price_gemini_aistudio_veo", ProviderID: domainprovider.IDGeminiAIStudio, ModelID: "*", Capability: string(domainprovider.CapabilityVideo), Unit: "second", UnitPriceUSD: 0.150},
		{ID: "price_gemini_vertex_veo", ProviderID: domainprovider.IDGeminiVertex, ModelID: "*", Capability: string(domainprovider.CapabilityVideo), Unit: "second", UnitPriceUSD: 0.150},
		{ID: "price_kling_video", ProviderID: domainprovider.IDKling, ModelID: "*", Capability: string(domainprovider.CapabilityVideo), Unit: "second", UnitPriceUSD: 0.070},
		{ID: "price_kling_lipsync", ProviderID: domainprovider.IDKling, ModelID: "*", Capability: string(domainprovider.CapabilityLipSync), Unit: "second", UnitPriceUSD: 0.100},
		{ID: "price_minimax_video", ProviderID: domainprovider.IDMiniMax, ModelID: "*", Capability: string(domainprovider.CapabilityVideo), Unit: "second", UnitPriceUSD: 0.060},
		{ID: "price_minimax_lipsync", ProviderID: domainprovider.IDMiniMax, ModelID: "*", Capability: string(domainprovider.CapabilityLipSync), Unit: "second", UnitPriceUSD: 0.090},
		{ID: "price_vidu_video", ProviderID: domainprovider.IDVidu, ModelID: "*", Capability: string(domainprovider.CapabilityVideo), Unit: "second", UnitPriceUSD: 0.060},
		{ID: "price_ark_video", ProviderID: domainprovider.IDArk, ModelID: "*", Capability: string(domainprovider.CapabilityVideo), Unit: "second", UnitPriceUSD: 0.055},
		{ID: "price_dashscope_video", ProviderID: domainprovider.IDDashScope, ModelID: "*", Capability: string(domainprovider.CapabilityVideo), Unit: "second", UnitPriceUSD: 0.055},
	}
	for _, p := range providerSeeds {
		p.Currency = defaultCatalogCurrency
		p.UpdatedAt = defaultPricingSeedUpdated
		out = append(out, p)
	}
	return out
}

// SplitModelRefForPricing normalizes "provider/model" (or legacy "provider:model") references for catalog lookups.
func SplitModelRefForPricing(ref string) (providerID, modelID string) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "*", "*"
	}
	if parsed, err := domainprovider.ParseModelRef(trimmed); err == nil {
		return parsed.ProviderID, parsed.ModelID
	}
	if prov, mod, ok := strings.Cut(trimmed, ":"); ok && strings.TrimSpace(prov) != "" && strings.TrimSpace(mod) != "" {
		return strings.ToLower(strings.TrimSpace(prov)), strings.TrimSpace(mod)
	}
	return "*", trimmed
}

// CostStatus identifies whether a ledger row is an estimate, finalized cost, or failed attempt.
type CostStatus string

const (
	CostEstimated CostStatus = "estimated"
	CostRecorded  CostStatus = "recorded"
	CostFailed    CostStatus = "failed"
)

// CostStatuses returns all valid studio cost ledger statuses.
func CostStatuses() []CostStatus {
	return []CostStatus{CostEstimated, CostRecorded, CostFailed}
}

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

// MediaTargetKinds returns all supported studio media version target kinds.
func MediaTargetKinds() []MediaTargetKind {
	return []MediaTargetKind{
		TargetAsset, TargetKeyframe, TargetVideo, TargetDubAudio,
		TargetLipSyncVideo, TargetGridPanel, TargetCleanVideo,
	}
}

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

// QCStatuses returns all valid QC verdicts.
func QCStatuses() []QCStatus {
	return []QCStatus{QCPending, QCPass, QCWarn, QCFail}
}

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
	Severity  string            `json:"severity"` // "info" | "warn" | "error"
	Category  string            `json:"category"` // "technical" | "continuity" | "artifacts" | "audio"
	Message   string            `json:"message"`
	MsgKey    string            `json:"msg_key,omitempty"`
	MsgParams map[string]string `json:"msg_params,omitempty"`
	FramePath string            `json:"frame_path,omitempty"`
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
	TransitionJobControl(ctx context.Context, job Job) (Job, error)
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
