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

type studioProjectGetToolInput struct {
	ID string `json:"id" jsonschema:"Studio project ID or slug"`
}

type studioProjectDeleteToolInput struct {
	ID          string `json:"id" jsonschema:"Studio project ID"`
	DeleteFiles bool   `json:"delete_files,omitempty" jsonschema:"Also delete the project workspace folder inside ProjectsDir"`
}

type studioProjectCreateToolInput struct {
	Title               string `json:"title" jsonschema:"Project title"`
	Slug                string `json:"slug,omitempty" jsonschema:"Optional filesystem slug (derived from title if omitted)"`
	Genre               string `json:"genre,omitempty" jsonschema:"drama (character dialogues) or narration (documentary/voiceover story)"`
	ProductionMode      string `json:"production_mode,omitempty" jsonschema:"storyboard_i2v (keyframe then I2V) or r2v (reference-to-video)"`
	AspectRatio         string `json:"aspect_ratio,omitempty" jsonschema:"Target aspect ratio e.g. 16:9 or 9:16"`
	StylePrompt         string `json:"style_prompt,omitempty" jsonschema:"Global visual style bible prompt applied across shots and sheets"`
	AutoApproveBatches  bool   `json:"auto_approve_batches,omitempty" jsonschema:"If true, trusted workspace studio batches skip interactive confirmation"`
	DefaultTextModel    string `json:"default_text_model,omitempty" jsonschema:"Optional default LLM text model"`
	DefaultImageModel   string `json:"default_image_model,omitempty" jsonschema:"Optional default image generation model"`
	DefaultVideoModel   string `json:"default_video_model,omitempty" jsonschema:"Optional default video generation model"`
	DefaultTTSModel     string `json:"default_tts_model,omitempty" jsonschema:"Optional default TTS voice model"`
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
	DurationSec     *int                           `json:"duration_sec,omitempty" jsonschema:"Target clip duration in seconds"`
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
	ErrorText       *string                        `json:"error_text,omitempty" jsonschema:"Optional shot error detail when status=failed"`
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

func (s *Service) registerStudioPhase1Tools(server *mcpToolset) {
	addMCPTool(server, "studio_project_list",
		"List all AI film studio projects and the active workspace projects directory.",
		func(ctx context.Context, _ studioProjectListToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.ListProjects(engine.WithFileGuard(ctx, s.CheckTrustedFile))
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_project_get",
		"Get studio project metadata, episodes list, active episode details, and asset bible by ID or slug.",
		func(ctx context.Context, in studioProjectGetToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.GetProject(engine.WithFileGuard(ctx, s.CheckTrustedFile), strings.TrimSpace(in.ID))
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
			p := contracts.StudioProjectCreatePayload{
				Title:               in.Title,
				Slug:                in.Slug,
				Genre:               domainstudio.Genre(strings.TrimSpace(in.Genre)),
				ProductionMode:      domainstudio.ProductionMode(strings.TrimSpace(in.ProductionMode)),
				AspectRatio:         in.AspectRatio,
				StylePrompt:         in.StylePrompt,
				AutoApproveBatches:  in.AutoApproveBatches,
				DefaultTextModel:    strings.TrimSpace(in.DefaultTextModel),
				DefaultImageModel:   strings.TrimSpace(in.DefaultImageModel),
				DefaultVideoModel:   strings.TrimSpace(in.DefaultVideoModel),
				DefaultTTSModel:     strings.TrimSpace(in.DefaultTTSModel),
				InitialEpisodeTitle: in.InitialEpisodeTitle,
			}
			if in.AutoApproveBatches {
				return s.needsApproval(ctx, "studio_project_create", "", p,
					"Create auto-approved studio project",
					fmt.Sprintf("Create studio project %q with AutoApproveBatches=true", strings.TrimSpace(in.Title)),
					func(execCtx context.Context) (any, error) {
						return s.studio.CreateProject(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
					})
			}
			res, err := s.studio.CreateProject(engine.WithFileGuard(ctx, s.CheckTrustedFile), p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_project_delete",
		"Delete an AI film studio project from DezuxkStudio (and optionally its workspace folder inside ProjectsDir; always requires user approval).",
		func(ctx context.Context, in studioProjectDeleteToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio project delete unavailable")
			}
			p := contracts.StudioProjectDeletePayload{
				ID:          strings.TrimSpace(in.ID),
				DeleteFiles: in.DeleteFiles,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			projDetail, err := s.studio.GetProject(guardedCtx, p.ID)
			if err != nil {
				return mcpError(err.Error())
			}
			p.ID = projDetail.Project.ID
			summary := fmt.Sprintf("Delete studio project %q (%s)", projDetail.Project.Title, projDetail.Project.ID)
			if p.DeleteFiles {
				summary += " including workspace files"
			}
			return s.needsApproval(ctx, "studio_project_delete", "", p, "Delete studio project", summary, func(execCtx context.Context) (any, error) {
				if err := ext.DeleteProject(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "deleted_id": p.ID}, nil
			})
		})

	addMCPTool(server, "studio_episode_get",
		"Get complete film episode details: project settings, asset bible, storyboard shots, raw/normalized script, and latest export.",
		func(ctx context.Context, in studioEpisodeGetToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.GetEpisode(engine.WithFileGuard(ctx, s.CheckTrustedFile), in.EpisodeID)
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
			res, err := s.studio.SaveScript(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioEpisodeSaveScriptPayload{
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
			res, err := s.studio.CreateEpisode(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioEpisodeCreatePayload{
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
		"Create, update, or delete an Asset Bible entry (character, scene, prop with @Tag) and sync asset_bible.json. Deleting an asset requires user approval.",
		func(ctx context.Context, in studioAssetUpsertToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			primaryPath := strings.TrimSpace(in.PrimaryPath)
			if !in.Delete && primaryPath != "" && s.policy != nil {
				resolved, verr := s.policy.ValidateRead(primaryPath)
				if verr != nil {
					return mcpError(verr.Error())
				}
				primaryPath = resolved
			}
			p := contracts.StudioAssetUpsertPayload{
				ID:             strings.TrimSpace(in.ID),
				ProjectID:      strings.TrimSpace(in.ProjectID),
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
			}
			if p.Delete {
				if err := p.Validate(); err != nil {
					return mcpError(err.Error())
				}
				return s.needsApproval(ctx, "studio_asset_upsert", "", p, "Delete studio asset",
					fmt.Sprintf("Delete Asset Bible entry %s from project %s", p.ID, p.ProjectID),
					func(execCtx context.Context) (any, error) {
						return s.studio.UpsertAsset(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
					})
			}
			res, err := s.studio.UpsertAsset(engine.WithFileGuard(ctx, s.CheckTrustedFile), p)
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
				if s.policy == nil {
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
				ErrorText:       in.ErrorText,
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
			detail, err := s.studio.SyncWorkspace(engine.WithFileGuard(ctx, s.CheckTrustedFile), in.ProjectID)
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

			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}

			// Validate any explicit external audio path with the agent file policy.
			if p.NarrationAudioPath != "" && s.policy != nil {
				resolvedAudio, rerr := s.policy.ValidateRead(p.NarrationAudioPath)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				p.NarrationAudioPath = resolvedAudio
			}

			if p.Stage == contracts.StudioStageProbeShots {
				res, rerr := s.studio.RunStage(guardedCtx, p)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				return mcpJSON(res)
			}

			prep := s.studioStageTargets(epDetail, p)
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
}

func (s *Service) studioProjectsDir() string {
	if s.studio != nil {
		if ext, ok := s.studio.(interface{ ProjectsDir() string }); ok {
			if dir := strings.TrimSpace(ext.ProjectsDir()); dir != "" {
				return dir
			}
		}
	}
	if s.policy != nil {
		if dir := strings.TrimSpace(s.policy.ProjectsDir()); dir != "" {
			return dir
		}
	}
	return ""
}

func (s *Service) studioProjectPrep(proj contracts.StudioProject, files ...string) preparedNodeInput {
	var prep preparedNodeInput
	if root := strings.TrimSpace(proj.RootDir); root != "" {
		prep.OutputDirs = append(prep.OutputDirs, root)
	} else if pDir := s.studioProjectsDir(); pDir != "" {
		prep.OutputDirs = append(prep.OutputDirs, pDir)
	}
	for _, f := range files {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			prep.Files = append(prep.Files, trimmed)
		}
	}
	return prep
}

func (s *Service) studioStageTargets(ep contracts.StudioEpisodeDetailResult, p contracts.StudioEpisodeRunStagePayload) preparedNodeInput {
	prep := s.studioProjectPrep(ep.Project, p.NarrationAudioPath)
	wantAssets := make(map[string]bool, len(p.AssetIDs))
	for _, id := range p.AssetIDs {
		wantAssets[id] = true
	}
	for _, a := range ep.Assets {
		if len(wantAssets) > 0 && !wantAssets[a.ID] {
			continue
		}
		if path := strings.TrimSpace(a.PrimaryPath); path != "" {
			prep.Files = append(prep.Files, path)
		}
	}
	wantShots := make(map[string]bool, len(p.ShotIDs))
	for _, id := range p.ShotIDs {
		wantShots[id] = true
	}
	for _, sh := range ep.Shots {
		if len(wantShots) > 0 && !wantShots[sh.ID] {
			continue
		}
		for _, fp := range []string{sh.KeyframePath, sh.VideoPath, sh.AudioPath} {
			if path := strings.TrimSpace(fp); path != "" {
				prep.Files = append(prep.Files, path)
			}
		}
	}
	return prep
}
