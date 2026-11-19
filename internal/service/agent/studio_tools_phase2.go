package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type studioQueueEnqueueToolInput struct {
	EpisodeID string   `json:"episode_id" jsonschema:"Studio episode ID"`
	Stage     string   `json:"stage" jsonschema:"generate_assets|generate_keyframes|generate_videos|generate_tts|lipsync"`
	AssetIDs  []string `json:"asset_ids,omitempty" jsonschema:"Optional asset IDs filter"`
	ShotIDs   []string `json:"shot_ids,omitempty" jsonschema:"Optional shot IDs filter"`
	Model     string   `json:"model,omitempty" jsonschema:"Optional provider/model override"`
	Force     bool     `json:"force,omitempty" jsonschema:"Re-enqueue even if target already has output"`
}

type studioQueueListToolInput struct {
	ProjectID string `json:"project_id,omitempty" jsonschema:"Optional studio project ID filter"`
	EpisodeID string `json:"episode_id,omitempty" jsonschema:"Optional episode ID filter"`
	Status    string `json:"status,omitempty" jsonschema:"queued|submitted|polling|paused|succeeded|failed|canceled"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Optional max queue rows to return"`
}

type studioQueueControlToolInput struct {
	Action    string `json:"action" jsonschema:"pause|resume|cancel|retry"`
	ProjectID string `json:"project_id,omitempty" jsonschema:"Project ID for episode/project pause/resume"`
	EpisodeID string `json:"episode_id,omitempty" jsonschema:"Episode ID for batch pause/resume"`
	JobID     string `json:"job_id,omitempty" jsonschema:"Specific job ID for single-job control"`
}

type studioCostSummaryToolInput struct {
	ProjectID string `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID string `json:"episode_id,omitempty" jsonschema:"Optional episode ID filter"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Optional max ledger rows to return"`
}

type studioVersionsListToolInput struct {
	TargetKind string `json:"target_kind" jsonschema:"asset|keyframe|video|dub_audio|lipsync_video|grid_panel|clean_video"`
	TargetID   string `json:"target_id" jsonschema:"Asset ID or Shot ID"`
}

type studioVersionSelectToolInput struct {
	VersionID string `json:"version_id" jsonschema:"Media version ID to activate"`
}

type studioQCReviewToolInput struct {
	ProjectID      string   `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID      string   `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	TargetKind     string   `json:"target_kind,omitempty" jsonschema:"asset|keyframe|video|dub_audio|lipsync_video|grid_panel|clean_video"`
	TargetID       string   `json:"target_id,omitempty" jsonschema:"Target asset or shot ID"`
	MediaVersionID string   `json:"media_version_id,omitempty" jsonschema:"Optional linked media version ID"`
	MediaPath      string   `json:"media_path" jsonschema:"Path to the image or video file to review"`
	ReferencePaths []string `json:"reference_paths,omitempty" jsonschema:"Optional canonical reference images for continuity checking"`
	PromptExpected string   `json:"prompt_expected,omitempty" jsonschema:"Expected visual content or prompt description"`
	VisionModelRef string   `json:"vision_model_ref,omitempty" jsonschema:"Optional multimodal Vision provider/model override"`
	SampleCount    int      `json:"sample_count,omitempty" jsonschema:"Optional sampled video frame count"`
	SkipVision     bool     `json:"skip_vision,omitempty" jsonschema:"Run FFprobe technical inspection only without Vision LLM"`
}

type studioBatchPreflightToolInput struct {
	ProjectID    string  `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID    string  `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	Stage        string  `json:"stage" jsonschema:"generate_assets|generate_keyframes|generate_videos|generate_tts|lipsync"`
	Model        string  `json:"model,omitempty" jsonschema:"Optional provider/model override"`
	ItemCount    int     `json:"item_count,omitempty" jsonschema:"Optional explicit item count"`
	UnitsPerItem float64 `json:"units_per_item,omitempty" jsonschema:"Optional units per item override (e.g. seconds per clip or 1k chars)"`
}

type studioProjectUpdateToolInput struct {
	ID                 string  `json:"id" jsonschema:"Studio project ID or slug"`
	Title              *string `json:"title,omitempty" jsonschema:"Updated project title"`
	Genre              *string `json:"genre,omitempty" jsonschema:"drama|narration"`
	ProductionMode     *string `json:"production_mode,omitempty" jsonschema:"storyboard_i2v|r2v"`
	AspectRatio        *string `json:"aspect_ratio,omitempty" jsonschema:"Target aspect ratio e.g. 16:9 or 9:16"`
	StylePrompt        *string `json:"style_prompt,omitempty" jsonschema:"Updated style bible prompt"`
	AutoApproveBatches *bool   `json:"auto_approve_batches,omitempty" jsonschema:"Whether trusted workspace studio batches skip approval prompts"`
	DefaultTextModel   *string `json:"default_text_model,omitempty"`
	DefaultImageModel  *string `json:"default_image_model,omitempty"`
	DefaultVideoModel  *string `json:"default_video_model,omitempty"`
	DefaultTTSModel    *string `json:"default_tts_model,omitempty"`
}

type studioShotReorderToolInput struct {
	EpisodeID string   `json:"episode_id" jsonschema:"Studio episode ID"`
	ShotIDs   []string `json:"shot_ids" jsonschema:"Ordered list of shot IDs"`
}

type studioPricingUpsertToolInput struct {
	ProviderID   string  `json:"provider_id" jsonschema:"Provider ID or * wildcard"`
	ModelID      string  `json:"model_id" jsonschema:"Model ID or * wildcard"`
	Capability   string  `json:"capability" jsonschema:"llm|image|video|tts|lipsync"`
	Unit         string  `json:"unit" jsonschema:"Pricing unit e.g. image|second|1k_chars|1k_tokens"`
	UnitPriceUSD float64 `json:"unit_price_usd" jsonschema:"USD unit rate"`
}

type studioGridSplitToolInput struct {
	ProjectID  string    `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID  string    `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	TargetKind string    `json:"target_kind,omitempty" jsonschema:"asset|keyframe|grid_panel"`
	TargetID   string    `json:"target_id,omitempty" jsonschema:"Target asset or shot ID"`
	ImagePath  string    `json:"image_path" jsonschema:"Path to the storyboard grid image inside the workspace"`
	Rows       int       `json:"rows" jsonschema:"Grid row count"`
	Cols       int       `json:"cols" jsonschema:"Grid column count"`
	MarginPx   int       `json:"margin_px,omitempty" jsonschema:"Optional outer pixel margin"`
	GutterPx   int       `json:"gutter_px,omitempty" jsonschema:"Optional gutter pixels between cells"`
	RowWeights []float64 `json:"row_weights,omitempty" jsonschema:"Optional relative row proportions"`
	ColWeights []float64 `json:"col_weights,omitempty" jsonschema:"Optional relative column proportions"`
}

type studioMaskEditToolInput struct {
	ProjectID      string `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID      string `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	TargetKind     string `json:"target_kind,omitempty" jsonschema:"asset|keyframe"`
	TargetID       string `json:"target_id,omitempty" jsonschema:"Target asset or shot ID"`
	ImagePath      string `json:"image_path" jsonschema:"Source image path inside the workspace"`
	MaskPath       string `json:"mask_path,omitempty" jsonschema:"Binary mask PNG path inside the workspace (white edits, black preserves)"`
	MaskDataBase64 string `json:"mask_data_base64,omitempty" jsonschema:"Optional base64 PNG mask when mask_path is omitted"`
	Prompt         string `json:"prompt" jsonschema:"Inpainting / repair prompt for masked region"`
	ModelRef       string `json:"model_ref,omitempty" jsonschema:"Optional image provider/model override"`
	AspectRatio    string `json:"aspect_ratio,omitempty" jsonschema:"Optional output aspect ratio override"`
	Resolution     string `json:"resolution,omitempty" jsonschema:"Optional output resolution override"`
}

type studioExtractLastFrameToolInput struct {
	EpisodeID    string `json:"episode_id" jsonschema:"Studio episode ID"`
	SourceShotID string `json:"source_shot_id" jsonschema:"Source shot ID whose final frame will be extracted"`
	TargetShotID string `json:"target_shot_id,omitempty" jsonschema:"Optional next shot ID to bind the extracted frame as keyframe"`
}

func (s *Service) registerStudioPhase2Tools(server *mcpToolset) {
	addMCPTool(server, "studio_batch_preflight",
		"Verify active provider credentials, pricing, and USD cost estimate before running a batch stage or enqueuing jobs.",
		func(ctx context.Context, in studioBatchPreflightToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 preflight is unavailable")
			}
			out, err := p2.RunBatchPreflight(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioBatchPreflightPayload{
				ProjectID:    strings.TrimSpace(in.ProjectID),
				EpisodeID:    strings.TrimSpace(in.EpisodeID),
				Stage:        strings.TrimSpace(in.Stage),
				Model:        strings.TrimSpace(in.Model),
				ItemCount:    in.ItemCount,
				UnitsPerItem: in.UnitsPerItem,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(out)
		})

	addMCPTool(server, "studio_queue_enqueue",
		"Enqueue durable crash-recoverable studio generation jobs into the background worker queue (respecting project approval gate).",
		func(ctx context.Context, in studioQueueEnqueueToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 queue is unavailable")
			}
			p := contracts.StudioQueueEnqueuePayload{
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				Stage:     strings.TrimSpace(in.Stage),
				AssetIDs:  in.AssetIDs,
				ShotIDs:   in.ShotIDs,
				Model:     strings.TrimSpace(in.Model),
				Force:     in.Force,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			prep := s.studioStageTargets(epDetail, contracts.StudioEpisodeRunStagePayload{
				EpisodeID: p.EpisodeID,
				Stage:     contracts.StudioRunStageAction(p.Stage),
				AssetIDs:  p.AssetIDs,
				ShotIDs:   p.ShotIDs,
			})
			if epDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				p.Approved = true
				res, qerr := p2.EnqueueBatch(guardedCtx, p)
				if qerr != nil {
					return mcpError(qerr.Error())
				}
				return mcpJSON(res)
			}
			title := fmt.Sprintf("Enqueue durable batch %s", p.Stage)
			summary := fmt.Sprintf("Enqueue durable %q jobs on %s / %s", p.Stage, epDetail.Project.Title, epDetail.Episode.Code)
			return s.needsApproval(ctx, "studio_queue_enqueue", "", p, title, summary, func(execCtx context.Context) (any, error) {
				p.Approved = true
				return p2.EnqueueBatch(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
			})
		})

	addMCPTool(server, "studio_queue_list",
		"List durable studio queue jobs filtered by project, episode, or status.",
		func(ctx context.Context, in studioQueueListToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 queue is unavailable")
			}
			res, err := p2.ListJobs(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioQueueListPayload{
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				Status:    domainstudio.JobStatus(strings.TrimSpace(in.Status)),
				Limit:     in.Limit,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_queue_control",
		"Pause, resume, cancel, or retry durable studio queue jobs without duplicating already-submitted remote video tasks.",
		func(ctx context.Context, in studioQueueControlToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 queue is unavailable")
			}
			p := contracts.StudioQueueControlPayload{
				Action:    contracts.StudioQueueAction(strings.TrimSpace(in.Action)),
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				JobID:     strings.TrimSpace(in.JobID),
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			if strings.EqualFold(string(p.Action), string(contracts.QueueActionRetry)) {
				var (
					proj    contracts.StudioProject
					hasProj bool
				)
				if p.ProjectID != "" {
					if pd, err := s.studio.GetProject(guardedCtx, p.ProjectID); err == nil {
						proj = pd.Project
						hasProj = true
					}
				} else if p.EpisodeID != "" {
					if ep, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID); err == nil {
						proj = ep.Project
						hasProj = true
					}
				} else if p.JobID != "" {
					if jobs, err := p2.ListJobs(guardedCtx, contracts.StudioQueueListPayload{}); err == nil {
						for _, j := range jobs.Jobs {
							if j.ID == p.JobID && j.ProjectID != "" {
								if pd, perr := s.studio.GetProject(guardedCtx, j.ProjectID); perr == nil {
									proj = pd.Project
									hasProj = true
								}
								break
							}
						}
					}
				}
				if !hasProj || !proj.AutoApproveBatches || !allFileTargetsTrusted(s.policy, s.studioProjectPrep(proj)) {
					targetLabel := p.JobID
					if targetLabel == "" {
						targetLabel = p.EpisodeID
					}
					if targetLabel == "" {
						targetLabel = p.ProjectID
					}
					return s.needsApproval(ctx, "studio_queue_control", "", p, "Retry studio queue jobs",
						fmt.Sprintf("Retry failed/canceled studio queue jobs (%s)", targetLabel),
						func(execCtx context.Context) (any, error) {
							return p2.ControlQueue(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
						})
				}
			}
			res, err := p2.ControlQueue(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_cost_summary",
		"Get cumulative USD cost totals, per-capability breakdown, ledger rows, and unit pricing for a studio project or episode.",
		func(ctx context.Context, in studioCostSummaryToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio billing ledger is unavailable")
			}
			res, err := p2.GetCostSummary(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioCostSummaryPayload{
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				Limit:     in.Limit,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_versions_list",
		"List version history (v1, v2, ...) and latest QC report for an asset, keyframe, video, or audio slot.",
		func(ctx context.Context, in studioVersionsListToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio media versions unavailable")
			}
			res, err := p2.ListVersions(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioVersionsListPayload{
				TargetKind: domainstudio.MediaTargetKind(strings.TrimSpace(in.TargetKind)),
				TargetID:   strings.TrimSpace(in.TargetID),
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_version_select",
		"Switch the active selected output version for an asset or shot slot.",
		func(ctx context.Context, in studioVersionSelectToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio media versions unavailable")
			}
			res, err := p2.SelectVersion(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioVersionSelectPayload{
				VersionID: strings.TrimSpace(in.VersionID),
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_qc_review",
		"Inspect an image or MP4 clip with FFprobe + sampled FFmpeg frames + multimodal Vision critique and record the QC report.",
		func(ctx context.Context, in studioQCReviewToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio QC review unavailable")
			}
			mediaPath := strings.TrimSpace(in.MediaPath)
			if mediaPath != "" && s.policy != nil {
				resolved, verr := s.policy.ValidateRead(mediaPath)
				if verr != nil {
					return mcpError(verr.Error())
				}
				mediaPath = resolved
			}
			refPaths := make([]string, 0, len(in.ReferencePaths))
			for _, rp := range in.ReferencePaths {
				trimmed := strings.TrimSpace(rp)
				if trimmed == "" {
					continue
				}
				if s.policy != nil {
					resolved, verr := s.policy.ValidateRead(trimmed)
					if verr != nil {
						return mcpError(verr.Error())
					}
					trimmed = resolved
				}
				refPaths = append(refPaths, trimmed)
			}
			p := contracts.StudioQCReviewPayload{
				ProjectID:      strings.TrimSpace(in.ProjectID),
				EpisodeID:      strings.TrimSpace(in.EpisodeID),
				TargetKind:     domainstudio.MediaTargetKind(strings.TrimSpace(in.TargetKind)),
				TargetID:       strings.TrimSpace(in.TargetID),
				MediaVersionID: strings.TrimSpace(in.MediaVersionID),
				MediaPath:      mediaPath,
				ReferencePaths: refPaths,
				PromptExpected: strings.TrimSpace(in.PromptExpected),
				VisionModelRef: strings.TrimSpace(in.VisionModelRef),
				SampleCount:    in.SampleCount,
				SkipVision:     in.SkipVision,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			if !p.SkipVision {
				projDetail, err := s.studio.GetProject(guardedCtx, p.ProjectID)
				if err != nil {
					return mcpError(err.Error())
				}
				allFiles := append([]string{p.MediaPath}, p.ReferencePaths...)
				prep := s.studioProjectPrep(projDetail.Project, allFiles...)
				if !projDetail.Project.AutoApproveBatches || !allFileTargetsTrusted(s.policy, prep) {
					return s.needsApproval(ctx, "studio_qc_review", "", p, "Run visual QC review",
						fmt.Sprintf("Run multimodal Vision QC review on %s", filepath.Base(p.MediaPath)),
						func(execCtx context.Context) (any, error) {
							p.Approved = true
							return p2.RunQCReview(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
						})
				}
			}
			res, err := p2.RunQCReview(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_project_update",
		"Update AI film studio project metadata, visual style bible prompt, or default model selections. Enabling auto_approve_batches on an existing project requires user approval.",
		func(ctx context.Context, in studioProjectUpdateToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio project updates unavailable")
			}
			var genrePtr *domainstudio.Genre
			if in.Genre != nil {
				g := domainstudio.Genre(strings.TrimSpace(*in.Genre))
				genrePtr = &g
			}
			var modePtr *domainstudio.ProductionMode
			if in.ProductionMode != nil {
				m := domainstudio.ProductionMode(strings.TrimSpace(*in.ProductionMode))
				modePtr = &m
			}
			p := contracts.StudioProjectUpdatePayload{
				ID:                 strings.TrimSpace(in.ID),
				Title:              in.Title,
				Genre:              genrePtr,
				ProductionMode:     modePtr,
				AspectRatio:        in.AspectRatio,
				StylePrompt:        in.StylePrompt,
				AutoApproveBatches: in.AutoApproveBatches,
				DefaultTextModel:   in.DefaultTextModel,
				DefaultImageModel:  in.DefaultImageModel,
				DefaultVideoModel:  in.DefaultVideoModel,
				DefaultTTSModel:    in.DefaultTTSModel,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			curProj, err := s.studio.GetProject(guardedCtx, p.ID)
			if err != nil {
				return mcpError(err.Error())
			}
			p.ID = curProj.Project.ID
			if p.AutoApproveBatches != nil && *p.AutoApproveBatches && !curProj.Project.AutoApproveBatches {
				return s.needsApproval(ctx, "studio_project_update", "", p, "Enable studio batch auto-approval",
					fmt.Sprintf("Enable auto_approve_batches on studio project %q", curProj.Project.Title),
					func(execCtx context.Context) (any, error) {
						return ext.UpdateProject(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
					})
			}
			res, err := ext.UpdateProject(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_shot_reorder",
		"Reorder storyboard shots in an episode and persist storyboard/shots.json.",
		func(ctx context.Context, in studioShotReorderToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio shot reorder unavailable")
			}
			res, err := ext.ReorderShots(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioShotReorderPayload{
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				ShotIDs:   in.ShotIDs,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_pricing_upsert",
		"Upsert a provider/model unit rate rule in studio_pricing_catalog for cost estimates (requires user approval).",
		func(ctx context.Context, in studioPricingUpsertToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio pricing catalog updates unavailable")
			}
			p := contracts.StudioPricingUpsertPayload{
				ProviderID:   strings.TrimSpace(in.ProviderID),
				ModelID:      strings.TrimSpace(in.ModelID),
				Capability:   strings.TrimSpace(in.Capability),
				Unit:         strings.TrimSpace(in.Unit),
				UnitPriceUSD: in.UnitPriceUSD,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			return s.needsApproval(ctx, "studio_pricing_upsert", "", p, "Update studio pricing catalog",
				fmt.Sprintf("Set %s/%s (%s) pricing rate to $%.4f per %s", p.ProviderID, p.ModelID, p.Capability, p.UnitPriceUSD, p.Unit),
				func(execCtx context.Context) (any, error) {
					return ext.UpsertPricing(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
				})
		})

	addMCPTool(server, "studio_grid_split",
		"Split a multi-panel grid image into individual frame files and record them in media version history.",
		func(ctx context.Context, in studioGridSplitToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio grid split unavailable")
			}
			imgPath := strings.TrimSpace(in.ImagePath)
			if imgPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(imgPath)
				if err != nil {
					return mcpError(err.Error())
				}
				imgPath = resolved
			}
			res, err := ext.SplitGrid(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioGridSplitPayload{
				ProjectID:  strings.TrimSpace(in.ProjectID),
				EpisodeID:  strings.TrimSpace(in.EpisodeID),
				TargetKind: domainstudio.MediaTargetKind(strings.TrimSpace(in.TargetKind)),
				TargetID:   strings.TrimSpace(in.TargetID),
				ImagePath:  imgPath,
				Rows:       in.Rows,
				Cols:       in.Cols,
				MarginPx:   in.MarginPx,
				GutterPx:   in.GutterPx,
				RowWeights: in.RowWeights,
				ColWeights: in.ColWeights,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_mask_edit",
		"Apply targeted mask inpainting to a keyframe or asset reference sheet and record the repaired output version.",
		func(ctx context.Context, in studioMaskEditToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio mask edit unavailable")
			}
			imgPath := strings.TrimSpace(in.ImagePath)
			if imgPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(imgPath)
				if err != nil {
					return mcpError(err.Error())
				}
				imgPath = resolved
			}
			maskPath := strings.TrimSpace(in.MaskPath)
			if maskPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(maskPath)
				if err != nil {
					return mcpError(err.Error())
				}
				maskPath = resolved
			}
			p := contracts.StudioMaskEditPayload{
				ProjectID:      strings.TrimSpace(in.ProjectID),
				EpisodeID:      strings.TrimSpace(in.EpisodeID),
				TargetKind:     domainstudio.MediaTargetKind(strings.TrimSpace(in.TargetKind)),
				TargetID:       strings.TrimSpace(in.TargetID),
				ImagePath:      imgPath,
				MaskPath:       maskPath,
				MaskDataBase64: strings.TrimSpace(in.MaskDataBase64),
				Prompt:         strings.TrimSpace(in.Prompt),
				ModelRef:       strings.TrimSpace(in.ModelRef),
				AspectRatio:    strings.TrimSpace(in.AspectRatio),
				Resolution:     strings.TrimSpace(in.Resolution),
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			projDetail, err := s.studio.GetProject(guardedCtx, p.ProjectID)
			if err != nil {
				return mcpError(err.Error())
			}
			prep := s.studioProjectPrep(projDetail.Project, p.ImagePath, p.MaskPath)
			if projDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				res, rerr := ext.ApplyMaskEdit(guardedCtx, p)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				return mcpJSON(res)
			}
			return s.needsApproval(ctx, "studio_mask_edit", "", p, "Run studio mask edit", fmt.Sprintf("Inpaint masked area of %s", filepath.Base(imgPath)), func(execCtx context.Context) (any, error) {
				p.Approved = true
				return ext.ApplyMaskEdit(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
			})
		})

	addMCPTool(server, "studio_extract_last_frame",
		"Extract the final frame of a shot's video for shot-to-shot visual continuity and optionally bind it to the next shot's keyframe.",
		func(ctx context.Context, in studioExtractLastFrameToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio extract last frame unavailable")
			}
			res, err := ext.ExtractLastFrame(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioExtractLastFramePayload{
				EpisodeID:    strings.TrimSpace(in.EpisodeID),
				SourceShotID: strings.TrimSpace(in.SourceShotID),
				TargetShotID: strings.TrimSpace(in.TargetShotID),
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})
}
