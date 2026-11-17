package agent

import (
	"context"
	"fmt"
	"strings"

	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type studioProjectListToolInput struct{}

type studioProjectCreateToolInput struct {
	Title               string `json:"title" jsonschema:"Project title"`
	Slug                string `json:"slug,omitempty" jsonschema:"Optional filesystem slug (derived from title if omitted)"`
	Genre               string `json:"genre,omitempty" jsonschema:"drama (character dialogues) or narration (documentary/voiceover story)"`
	ProductionMode      string `json:"production_mode,omitempty" jsonschema:"storyboard_i2v (keyframe then I2V) or r2v (reference-to-video)"`
	AspectRatio         string `json:"aspect_ratio,omitempty" jsonschema:"Target aspect ratio e.g. 16:9 or 9:16"`
	StylePrompt         string `json:"style_prompt,omitempty" jsonschema:"Global visual style bible prompt applied across shots and sheets"`
	AutoApproveBatches  bool   `json:"auto_approve_batches,omitempty" jsonschema:"If true, trusted workspace studio batches skip interactive confirmation"`
	InitialEpisodeTitle string `json:"initial_episode_title,omitempty" jsonschema:"Optional starter episode title (default: Episode 1)"`
}

type studioEpisodeCreateToolInput struct {
	ProjectID string `json:"project_id" jsonschema:"Studio project ID"`
	Code      string `json:"code,omitempty" jsonschema:"Optional episode code (e.g. ep02)"`
	Title     string `json:"title,omitempty" jsonschema:"Episode title"`
}

type studioEpisodeGetToolInput struct {
	EpisodeID string `json:"episode_id" jsonschema:"Studio episode ID"`
}

type studioSaveScriptToolInput struct {
	EpisodeID        string                            `json:"episode_id" jsonschema:"Studio episode ID"`
	RawScript        *string                           `json:"raw_script,omitempty" jsonschema:"Full screenplay or narration text (replaces raw_screenplay.txt when provided)"`
	NormalizedScript *contracts.StudioNormalizedScript `json:"normalized_script,omitempty" jsonschema:"Optional edited structured NormalizedScript JSON object"`
	Stage            *string                           `json:"stage,omitempty" jsonschema:"Optional episode stage: script|assets|storyboard|video|export"`
}

type studioAssetUpsertToolInput struct {
	ID             string `json:"id,omitempty" jsonschema:"Existing asset ID to update or delete; omit to create"`
	ProjectID      string `json:"project_id" jsonschema:"Studio project ID"`
	Kind           string `json:"kind,omitempty" jsonschema:"character|scene|prop"`
	Name           string `json:"name,omitempty" jsonschema:"Display name of character, location, or prop"`
	CodeTag        string `json:"code_tag,omitempty" jsonschema:"Consistent @Tag identifier (e.g. @Minh, @OldCafe)"`
	Description    string `json:"description,omitempty" jsonschema:"Brief role or visual summary"`
	Prompt         string `json:"prompt,omitempty" jsonschema:"Reference-sheet image generation prompt"`
	PrimaryAssetID string `json:"primary_asset_id,omitempty" jsonschema:"Optional media asset ID for existing reference sheet"`
	PrimaryPath    string `json:"primary_path,omitempty" jsonschema:"Optional reference sheet image file path inside workspace"`
	VoiceProvider  string `json:"voice_provider,omitempty" jsonschema:"Optional voice provider note"`
	VoiceID        string `json:"voice_id,omitempty" jsonschema:"Optional voice ID note"`
	VoiceStyle     string `json:"voice_style,omitempty" jsonschema:"Optional voice tone note"`
	Delete         bool   `json:"delete,omitempty" jsonschema:"Set true to delete asset by id"`
}

type studioShotUpsertToolInput struct {
	ID              string                         `json:"id,omitempty" jsonschema:"Existing shot ID to update; omit to append a new shot"`
	EpisodeID       string                         `json:"episode_id" jsonschema:"Studio episode ID"`
	Seq             *int                           `json:"seq,omitempty" jsonschema:"1-based shot sequence number"`
	SceneCode       string                         `json:"scene_code,omitempty" jsonschema:"Scene identifier e.g. S01"`
	Title           string                         `json:"title,omitempty" jsonschema:"Short shot title"`
	ShotMode        string                         `json:"shot_mode,omitempty" jsonschema:"single|grid_2x2|grid_3x3|r2v"`
	DurationSec     *int                           `json:"duration_sec,omitempty" jsonschema:"Target clip duration in seconds (1-60)"`
	ImagePrompt     *string                        `json:"image_prompt,omitempty" jsonschema:"Keyframe image prompt"`
	VideoPrompt     *string                        `json:"video_prompt,omitempty" jsonschema:"Video motion/camera/dialogue prompt"`
	NarrationText   *string                        `json:"narration_text,omitempty" jsonschema:"Voiceover narration text for this shot"`
	Dialogues       []contracts.StudioDialogueLine `json:"dialogues,omitempty" jsonschema:"Character dialogue lines for Drama mode"`
	RefAssetTags    []string                       `json:"ref_asset_tags,omitempty" jsonschema:"@Tag references used for visual consistency"`
	KeyframeAssetID *string                        `json:"keyframe_asset_id,omitempty"`
	KeyframePath    *string                        `json:"keyframe_path,omitempty"`
	VideoAssetID    *string                        `json:"video_asset_id,omitempty"`
	VideoPath       *string                        `json:"video_path,omitempty"`
	AudioAssetID    *string                        `json:"audio_asset_id,omitempty"`
	AudioPath       *string                        `json:"audio_path,omitempty"`
	Status          *string                        `json:"status,omitempty" jsonschema:"pending|keyframe_ready|video_ready|tts_ready|qc_passed|failed"`
}

type studioSyncWorkspaceToolInput struct {
	ProjectID string `json:"project_id,omitempty" jsonschema:"Optional studio project ID or slug; empty scans and syncs all workspace projects"`
}

type studioEpisodeRunStageToolInput struct {
	EpisodeID          string   `json:"episode_id" jsonschema:"Studio episode ID"`
	Stage              string   `json:"stage" jsonschema:"script_normalize|extract_assets|author_shots|generate_assets|generate_keyframes|generate_videos|generate_tts|probe_shots|concat_video"`
	AssetIDs           []string `json:"asset_ids,omitempty" jsonschema:"Optional asset IDs filter for generate_assets"`
	ShotIDs            []string `json:"shot_ids,omitempty" jsonschema:"Optional shot IDs filter for shot-level stages"`
	Model              string   `json:"model,omitempty" jsonschema:"Optional provider/model override (empty uses project/app default)"`
	Voice              string   `json:"voice,omitempty" jsonschema:"Optional TTS voice override for generate_tts"`
	Resolution         string   `json:"resolution,omitempty" jsonschema:"Optional output resolution e.g. 1920x1080 or 720p"`
	FPS                int      `json:"fps,omitempty" jsonschema:"Optional output FPS for concat_video"`
	NarrationAudioPath string   `json:"narration_audio_path,omitempty" jsonschema:"Optional narration audio path to mix in concat_video"`
	Force              bool     `json:"force,omitempty" jsonschema:"Re-run even for targets that already have generated outputs"`
}

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
}

type studioVersionsListToolInput struct {
	TargetKind string `json:"target_kind" jsonschema:"asset|keyframe|video|dub_audio|lipsync_video|grid_panel"`
	TargetID   string `json:"target_id" jsonschema:"Asset ID or Shot ID"`
}

type studioVersionSelectToolInput struct {
	VersionID string `json:"version_id" jsonschema:"Media version ID to activate"`
}

type studioQCReviewToolInput struct {
	ProjectID      string   `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID      string   `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	TargetKind     string   `json:"target_kind,omitempty" jsonschema:"keyframe|video|asset|dub_audio|lipsync_video"`
	TargetID       string   `json:"target_id,omitempty" jsonschema:"Target asset or shot ID"`
	MediaVersionID string   `json:"media_version_id,omitempty" jsonschema:"Optional linked media version ID"`
	MediaPath      string   `json:"media_path" jsonschema:"Path to the image or video file to review"`
	ReferencePaths []string `json:"reference_paths,omitempty" jsonschema:"Optional canonical reference images for continuity checking"`
	PromptExpected string   `json:"prompt_expected,omitempty" jsonschema:"Expected visual content or prompt description"`
	SkipVision     bool     `json:"skip_vision,omitempty" jsonschema:"Run FFprobe technical inspection only without Vision LLM"`
}

type studioBatchPreflightToolInput struct {
	ProjectID string `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID string `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	Stage     string `json:"stage" jsonschema:"generate_assets|generate_keyframes|generate_videos|generate_tts|lipsync"`
	Model     string `json:"model,omitempty" jsonschema:"Optional provider/model override"`
	ItemCount int    `json:"item_count,omitempty" jsonschema:"Optional explicit item count"`
}

func (s *Service) registerStudioTools(server *mcpToolset) {
	addMCPTool(server, "studio_project_list",
		"List all AI film studio projects and the active workspace projects directory.",
		func(ctx context.Context, _ studioProjectListToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.ListProjects(ctx)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_project_create",
		"Create a new AI film studio project with its workspace folder structure and starter Episode 1.",
		func(ctx context.Context, in studioProjectCreateToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.CreateProject(ctx, contracts.StudioProjectCreatePayload{
				Title:               in.Title,
				Slug:                in.Slug,
				Genre:               domainstudio.Genre(strings.TrimSpace(in.Genre)),
				ProductionMode:      domainstudio.ProductionMode(strings.TrimSpace(in.ProductionMode)),
				AspectRatio:         in.AspectRatio,
				StylePrompt:         in.StylePrompt,
				AutoApproveBatches:  in.AutoApproveBatches,
				InitialEpisodeTitle: in.InitialEpisodeTitle,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_episode_get",
		"Get complete film episode details: project settings, asset bible, storyboard shots, raw/normalized script, and latest export.",
		func(ctx context.Context, in studioEpisodeGetToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.GetEpisode(ctx, in.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_save_script",
		"Save raw screenplay text and/or edited normalized script JSON for an episode, keeping SQLite and disk JSON synchronized.",
		func(ctx context.Context, in studioSaveScriptToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			var stagePtr *domainstudio.EpisodeStage
			if in.Stage != nil && strings.TrimSpace(*in.Stage) != "" {
				st := domainstudio.EpisodeStage(strings.TrimSpace(*in.Stage))
				stagePtr = &st
			}
			res, err := s.studio.SaveScript(ctx, contracts.StudioEpisodeSaveScriptPayload{
				EpisodeID:        in.EpisodeID,
				RawScript:        in.RawScript,
				NormalizedScript: in.NormalizedScript,
				Stage:            stagePtr,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_episode_create",
		"Create a new episode inside an existing studio project.",
		func(ctx context.Context, in studioEpisodeCreateToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.CreateEpisode(ctx, contracts.StudioEpisodeCreatePayload{
				ProjectID: in.ProjectID,
				Code:      in.Code,
				Title:     in.Title,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_asset_upsert",
		"Create, update, or delete an Asset Bible entry (character, scene, prop with @Tag) and sync asset_bible.json.",
		func(ctx context.Context, in studioAssetUpsertToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			primaryPath := strings.TrimSpace(in.PrimaryPath)
			if !in.Delete && primaryPath != "" {
				resolved, verr := s.policy.ValidateRead(primaryPath)
				if verr != nil {
					return mcpError(verr.Error())
				}
				primaryPath = resolved
			}
			res, err := s.studio.UpsertAsset(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioAssetUpsertPayload{
				ID:             in.ID,
				ProjectID:      in.ProjectID,
				Kind:           domainstudio.AssetKind(strings.TrimSpace(in.Kind)),
				Name:           in.Name,
				CodeTag:        in.CodeTag,
				Description:    in.Description,
				Prompt:         in.Prompt,
				PrimaryAssetID: in.PrimaryAssetID,
				PrimaryPath:    primaryPath,
				VoiceProvider:  in.VoiceProvider,
				VoiceID:        in.VoiceID,
				VoiceStyle:     in.VoiceStyle,
				Delete:         in.Delete,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_shot_upsert",
		"Create or update a storyboard shot (prompts, @Tag refs, duration, dialogue, narration, or media paths) and sync shots.json.",
		func(ctx context.Context, in studioShotUpsertToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			validateOptPath := func(ptr *string) (*string, error) {
				if ptr == nil || strings.TrimSpace(*ptr) == "" {
					return ptr, nil
				}
				resolved, verr := s.policy.ValidateRead(*ptr)
				if verr != nil {
					return nil, verr
				}
				return &resolved, nil
			}
			keyframePath, err := validateOptPath(in.KeyframePath)
			if err != nil {
				return mcpError(err.Error())
			}
			videoPath, err := validateOptPath(in.VideoPath)
			if err != nil {
				return mcpError(err.Error())
			}
			audioPath, err := validateOptPath(in.AudioPath)
			if err != nil {
				return mcpError(err.Error())
			}
			var statusPtr *domainstudio.ShotStatus
			if in.Status != nil && strings.TrimSpace(*in.Status) != "" {
				st := domainstudio.ShotStatus(strings.TrimSpace(*in.Status))
				statusPtr = &st
			}
			res, err := s.studio.UpsertShot(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioShotUpsertPayload{
				ID:              in.ID,
				EpisodeID:       in.EpisodeID,
				Seq:             in.Seq,
				SceneCode:       in.SceneCode,
				Title:           in.Title,
				ShotMode:        domainstudio.ShotMode(strings.TrimSpace(in.ShotMode)),
				DurationSec:     in.DurationSec,
				ImagePrompt:     in.ImagePrompt,
				VideoPrompt:     in.VideoPrompt,
				NarrationText:   in.NarrationText,
				Dialogues:       in.Dialogues,
				RefAssetTags:    in.RefAssetTags,
				KeyframeAssetID: in.KeyframeAssetID,
				KeyframePath:    keyframePath,
				VideoAssetID:    in.VideoAssetID,
				VideoPath:       videoPath,
				AudioAssetID:    in.AudioAssetID,
				AudioPath:       audioPath,
				Status:          statusPtr,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_sync_workspace",
		"Synchronize studio project.json, asset_bible.json, and shots.json edited on disk back into DezuxkStudio SQLite state.",
		func(ctx context.Context, in studioSyncWorkspaceToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			detail, err := s.studio.SyncWorkspace(ctx, in.ProjectID)
			if err != nil {
				return mcpError(err.Error())
			}
			if detail == nil {
				return mcpJSON(map[string]any{"ok": true, "synced": 0})
			}
			return mcpJSON(detail)
		})

	addMCPTool(server, "studio_episode_run_stage",
		"Execute a film studio production stage (script_normalize, extract_assets, author_shots, generate_assets, generate_keyframes, generate_videos, generate_tts, probe_shots, concat_video) through shared engine nodes. Batch/generation stages require user approval unless the project enables auto_approve_batches inside trusted folders.",
		func(ctx context.Context, in studioEpisodeRunStageToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			p := contracts.StudioEpisodeRunStagePayload{
				EpisodeID:          strings.TrimSpace(in.EpisodeID),
				Stage:              contracts.StudioRunStageAction(strings.TrimSpace(in.Stage)),
				AssetIDs:           in.AssetIDs,
				ShotIDs:            in.ShotIDs,
				Model:              strings.TrimSpace(in.Model),
				Voice:              strings.TrimSpace(in.Voice),
				Resolution:         strings.TrimSpace(in.Resolution),
				FPS:                in.FPS,
				NarrationAudioPath: strings.TrimSpace(in.NarrationAudioPath),
				Force:              in.Force,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}

			epDetail, err := s.studio.GetEpisode(ctx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}

			// Validate any explicit external audio path with the agent file policy.
			if p.NarrationAudioPath != "" {
				resolvedAudio, rerr := s.policy.ValidateRead(p.NarrationAudioPath)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				p.NarrationAudioPath = resolvedAudio
			}

			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			if p.Stage == contracts.StudioStageProbeShots {
				res, rerr := s.studio.RunStage(guardedCtx, p)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				return mcpJSON(res)
			}

			prep := studioStageTargets(epDetail, p)
			if epDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				p.Approved = true
				res, rerr := s.studio.RunStage(guardedCtx, p)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				return mcpJSON(res)
			}

			title := fmt.Sprintf("Run studio stage %s", p.Stage)
			summary := fmt.Sprintf("Run studio stage %q on %s / %s", p.Stage, epDetail.Project.Title, epDetail.Episode.Code)
			if len(p.ShotIDs) > 0 {
				summary = fmt.Sprintf("%s (%d shot(s))", summary, len(p.ShotIDs))
			} else if len(p.AssetIDs) > 0 {
				summary = fmt.Sprintf("%s (%d asset(s))", summary, len(p.AssetIDs))
			}
			return s.needsApproval(ctx, "studio_episode_run_stage", "", p, title, summary, func(execCtx context.Context) (any, error) {
				p.Approved = true
				return s.studio.RunStage(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
			})
		})

	addMCPTool(server, "studio_batch_preflight",
		"Verify active provider credentials, pricing, and USD cost estimate before running a batch stage or enqueuing jobs.",
		func(ctx context.Context, in studioBatchPreflightToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 preflight is unavailable")
			}
			out, err := p2.RunBatchPreflight(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioBatchPreflightPayload{
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				Stage:     strings.TrimSpace(in.Stage),
				Model:     strings.TrimSpace(in.Model),
				ItemCount: in.ItemCount,
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
			epDetail, err := s.studio.GetEpisode(ctx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			prep := studioStageTargets(epDetail, contracts.StudioEpisodeRunStagePayload{EpisodeID: p.EpisodeID})
			if epDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				p.Approved = true
				res, qerr := p2.EnqueueBatch(engine.WithFileGuard(ctx, s.CheckTrustedFile), p)
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
			res, err := p2.ListJobs(ctx, contracts.StudioQueueListPayload{
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				Status:    strings.TrimSpace(in.Status),
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_queue_control",
		"Pause, resume, cancel, or retry durable studio queue jobs without duplicating already-submitted remote video tasks.",
		func(ctx context.Context, in studioQueueControlToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 queue is unavailable")
			}
			res, err := p2.ControlQueue(ctx, contracts.StudioQueueControlPayload{
				Action:    strings.TrimSpace(in.Action),
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				JobID:     strings.TrimSpace(in.JobID),
			})
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
			res, err := p2.GetCostSummary(ctx, contracts.StudioCostSummaryPayload{
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
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
			res, err := p2.ListVersions(ctx, contracts.StudioVersionsListPayload{
				TargetKind: strings.TrimSpace(in.TargetKind),
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
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio QC review unavailable")
			}
			res, err := p2.RunQCReview(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioQCReviewPayload{
				ProjectID:      strings.TrimSpace(in.ProjectID),
				EpisodeID:      strings.TrimSpace(in.EpisodeID),
				TargetKind:     strings.TrimSpace(in.TargetKind),
				TargetID:       strings.TrimSpace(in.TargetID),
				MediaVersionID: strings.TrimSpace(in.MediaVersionID),
				MediaPath:      strings.TrimSpace(in.MediaPath),
				ReferencePaths: in.ReferencePaths,
				PromptExpected: strings.TrimSpace(in.PromptExpected),
				SkipVision:     in.SkipVision,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})
}

func studioStageTargets(ep contracts.StudioEpisodeDetailResult, p contracts.StudioEpisodeRunStagePayload) preparedNodeInput {
	var prep preparedNodeInput
	if root := strings.TrimSpace(ep.Project.RootDir); root != "" {
		prep.OutputDirs = append(prep.OutputDirs, root)
	}
	if p.NarrationAudioPath != "" {
		prep.Files = append(prep.Files, p.NarrationAudioPath)
	}
	return prep
}
