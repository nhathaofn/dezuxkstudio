package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type studioWorkflowPlanToolInput struct {
	EpisodeID string `json:"episode_id" jsonschema:"Studio episode ID"`
}

type studioShotBatchDialogueInput struct {
	Speaker string `json:"speaker" jsonschema:"Speaker name or asset tag"`
	Line    string `json:"line" jsonschema:"Spoken dialogue line"`
}

type studioShotBatchItemToolInput struct {
	ID            string                         `json:"id,omitempty" jsonschema:"Optional existing shot ID to update"`
	Seq           *int                           `json:"seq,omitempty" jsonschema:"Optional 1-based sequence number"`
	SceneCode     string                         `json:"scene_code,omitempty" jsonschema:"Scene code e.g. S01"`
	Title         string                         `json:"title,omitempty" jsonschema:"Concise shot title or beat summary"`
	ShotMode      string                         `json:"shot_mode,omitempty" jsonschema:"storyboard_i2v|r2v|tts_only"`
	DurationSec   *int                           `json:"duration_sec,omitempty" jsonschema:"Shot duration in integer seconds"`
	ImagePrompt   *string                        `json:"image_prompt,omitempty" jsonschema:"Keyframe image generation prompt"`
	VideoPrompt   *string                        `json:"video_prompt,omitempty" jsonschema:"Video motion/camera prompt"`
	NarrationText *string                        `json:"narration_text,omitempty" jsonschema:"Narration line for this shot"`
	Dialogues     []studioShotBatchDialogueInput `json:"dialogues,omitempty" jsonschema:"Optional speaker dialogue lines"`
	RefAssetTags  []string                       `json:"ref_asset_tags,omitempty" jsonschema:"Referenced @AssetTags e.g. @Minh"`
}

type studioShotsBatchUpsertToolInput struct {
	EpisodeID  string                         `json:"episode_id" jsonschema:"Studio episode ID"`
	Shots      []studioShotBatchItemToolInput `json:"shots" jsonschema:"Ordered batch of shots to create, update, or replace"`
	ReplaceAll bool                           `json:"replace_all,omitempty" jsonschema:"If true, replaces all existing shots in the episode (requires approval if shots exist)"`
}

type studioAssetRenameToolInput struct {
	ProjectID  string `json:"project_id" jsonschema:"Studio project ID"`
	AssetID    string `json:"asset_id" jsonschema:"Studio asset ID"`
	NewName    string `json:"new_name,omitempty" jsonschema:"Optional new asset display name"`
	NewCodeTag string `json:"new_code_tag" jsonschema:"New script @tag (e.g. @MinhHero) to rewrite across scripts and shots"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"Preview impacted episodes/shots without modifying state"`
}

type studioAssetMergeToolInput struct {
	ProjectID     string `json:"project_id" jsonschema:"Studio project ID"`
	SourceAssetID string `json:"source_asset_id" jsonschema:"Duplicate/source asset ID to merge and remove"`
	TargetAssetID string `json:"target_asset_id" jsonschema:"Surviving target asset ID"`
	DryRun        bool   `json:"dry_run,omitempty" jsonschema:"Preview impacted episodes/shots without deleting or modifying state"`
}

type studioInspectShotsToolInput struct {
	EpisodeID      string   `json:"episode_id" jsonschema:"Studio episode ID"`
	ShotIDs        []string `json:"shot_ids,omitempty" jsonschema:"Optional shot IDs filter (defaults to all video-ready shots)"`
	MaxFrames      int      `json:"max_frames,omitempty" jsonschema:"Optional max frames to sample per shot (bounded by StudioConfig)"`
	FramesPerSheet int      `json:"frames_per_sheet,omitempty" jsonschema:"Optional tiles per composite JPEG sheet (bounded by StudioConfig)"`
	IncludeSignals *bool    `json:"include_signals,omitempty" jsonschema:"Extract blackdetect/freezedetect/scenedetect signals (default true)"`
}

type studioQueueWaitToolInput struct {
	ProjectID  string   `json:"project_id,omitempty" jsonschema:"Studio project ID (required unless episode_id or job_ids provided)"`
	EpisodeID  string   `json:"episode_id,omitempty" jsonschema:"Studio episode ID"`
	JobIDs     []string `json:"job_ids,omitempty" jsonschema:"Optional subset of job IDs to await"`
	TimeoutSec int      `json:"timeout_sec,omitempty" jsonschema:"Max wait time in seconds (bounded by StudioConfig queue_wait_max_duration)"`
}

func (s *Service) registerStudioTier123Tools(server *mcpToolset) {
	addMCPTool(server, "studio_workflow_plan",
		"Inspect episode readiness, missing artifacts, speech/timeline issues, configured text/image/video/TTS providers, and recommended next steps.",
		func(ctx context.Context, in studioWorkflowPlanToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			t123, ok := s.studio.(StudioTier123Manager)
			if !ok {
				return mcpError("studio workflow plan unavailable")
			}
			epID := strings.TrimSpace(in.EpisodeID)
			if epID == "" {
				return mcpError("episode_id is required")
			}
			res, err := t123.GetWorkflowPlan(engine.WithFileGuard(ctx, s.CheckTrustedFile), epID)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_shots_batch_upsert",
		"Batch create, update, or atomically replace episode storyboard shots (validates ordering and limits against StudioConfig).",
		func(ctx context.Context, in studioShotsBatchUpsertToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			t123, ok := s.studio.(StudioTier123Manager)
			if !ok {
				return mcpError("studio batch shot upsert unavailable")
			}
			items := make([]contracts.StudioShotBatchItem, 0, len(in.Shots))
			for _, item := range in.Shots {
				var dialogues []contracts.StudioDialogueLine
				if len(item.Dialogues) > 0 {
					dialogues = make([]contracts.StudioDialogueLine, 0, len(item.Dialogues))
					for _, d := range item.Dialogues {
						dialogues = append(dialogues, contracts.StudioDialogueLine{
							Speaker: strings.TrimSpace(d.Speaker),
							Line:    d.Line,
						})
					}
				}
				items = append(items, contracts.StudioShotBatchItem{
					ID:            strings.TrimSpace(item.ID),
					Seq:           item.Seq,
					SceneCode:     strings.TrimSpace(item.SceneCode),
					Title:         strings.TrimSpace(item.Title),
					ShotMode:      domainstudio.ShotMode(strings.TrimSpace(item.ShotMode)),
					DurationSec:   item.DurationSec,
					ImagePrompt:   item.ImagePrompt,
					VideoPrompt:   item.VideoPrompt,
					NarrationText: item.NarrationText,
					Dialogues:     dialogues,
					RefAssetTags:  item.RefAssetTags,
				})
			}
			p := contracts.StudioShotsBatchUpsertPayload{
				EpisodeID:  strings.TrimSpace(in.EpisodeID),
				Shots:      items,
				ReplaceAll: in.ReplaceAll,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			if p.ReplaceAll {
				epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
				if err != nil {
					return mcpError(err.Error())
				}
				if len(epDetail.Shots) > 0 {
					prep := s.studioProjectPrep(epDetail.Project)
					if !epDetail.Project.AutoApproveBatches || !allFileTargetsTrusted(s.policy, prep) {
						return s.needsApproval(ctx, "studio_shots_batch_upsert", "", p, "Replace episode shots",
							fmt.Sprintf("Replace all %d existing shots in %s / %s with %d shots",
								len(epDetail.Shots), epDetail.Project.Title, epDetail.Episode.Code, len(p.Shots)),
							func(execCtx context.Context) (any, error) {
								return t123.BatchUpsertShots(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
							})
					}
				}
			}
			res, err := t123.BatchUpsertShots(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_asset_rename",
		"Rename a studio asset and cascade its @tag and speaker references across scripts and shots (supports dry_run).",
		func(ctx context.Context, in studioAssetRenameToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			t123, ok := s.studio.(StudioTier123Manager)
			if !ok {
				return mcpError("studio asset rename unavailable")
			}
			p := contracts.StudioAssetRenamePayload{
				ProjectID:  strings.TrimSpace(in.ProjectID),
				AssetID:    strings.TrimSpace(in.AssetID),
				NewName:    strings.TrimSpace(in.NewName),
				NewCodeTag: strings.TrimSpace(in.NewCodeTag),
				DryRun:     in.DryRun,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			res, err := t123.RenameAsset(engine.WithFileGuard(ctx, s.CheckTrustedFile), p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_asset_merge",
		"Merge a duplicate studio asset into a surviving asset, rewriting all script/shot/asset references (supports dry_run).",
		func(ctx context.Context, in studioAssetMergeToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			t123, ok := s.studio.(StudioTier123Manager)
			if !ok {
				return mcpError("studio asset merge unavailable")
			}
			p := contracts.StudioAssetMergePayload{
				ProjectID:     strings.TrimSpace(in.ProjectID),
				SourceAssetID: strings.TrimSpace(in.SourceAssetID),
				TargetAssetID: strings.TrimSpace(in.TargetAssetID),
				DryRun:        in.DryRun,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			if !p.DryRun {
				projDetail, err := s.studio.GetProject(guardedCtx, p.ProjectID)
				if err != nil {
					return mcpError(err.Error())
				}
				prep := s.studioProjectPrep(projDetail.Project)
				if !projDetail.Project.AutoApproveBatches || !allFileTargetsTrusted(s.policy, prep) {
					return s.needsApproval(ctx, "studio_asset_merge", "", p, "Merge and delete duplicate studio asset",
						fmt.Sprintf("Merge asset %s into %s and rewrite project %s", p.SourceAssetID, p.TargetAssetID, projDetail.Project.Title),
						func(execCtx context.Context) (any, error) {
							return t123.MergeAssets(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
						})
				}
			}
			res, err := t123.MergeAssets(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_inspect_shots",
		"Build visual contact sheets + FFmpeg signal anomalies for episode video shots and return both structured metadata and ImageContent frames for vision inspection.",
		func(ctx context.Context, in studioInspectShotsToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			t123, ok := s.studio.(StudioTier123Manager)
			if !ok {
				return mcpError("studio inspect shots unavailable")
			}
			p := contracts.StudioInspectShotsPayload{
				EpisodeID:      strings.TrimSpace(in.EpisodeID),
				ShotIDs:        in.ShotIDs,
				MaxFrames:      in.MaxFrames,
				FramesPerSheet: in.FramesPerSheet,
				IncludeSignals: in.IncludeSignals,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			res, err := t123.InspectShots(engine.WithFileGuard(ctx, s.CheckTrustedFile), p)
			if err != nil {
				return mcpError(err.Error())
			}
			return s.buildInspectShotsMCPResult(res)
		})

	addMCPTool(server, "studio_queue_wait",
		"Block until active queued Studio generation jobs finish, fail, or reach timeout (pushes progress heartbeats without token polling).",
		func(ctx context.Context, in studioQueueWaitToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			t123, ok := s.studio.(StudioTier123Manager)
			if !ok {
				return mcpError("studio queue wait unavailable")
			}
			p := contracts.StudioQueueWaitPayload{
				ProjectID:  strings.TrimSpace(in.ProjectID),
				EpisodeID:  strings.TrimSpace(in.EpisodeID),
				JobIDs:     in.JobIDs,
				TimeoutSec: in.TimeoutSec,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			res, err := t123.WaitForQueue(engine.WithFileGuard(ctx, s.CheckTrustedFile), p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})
}

func (s *Service) buildInspectShotsMCPResult(res contracts.StudioInspectShotsResult) (*mcp.CallToolResult, any, error) {
	content := make([]mcp.Content, 0, 1+len(res.Shots))
	metaBytes, err := json.Marshal(res)
	if err != nil {
		return mcpError(fmt.Sprintf("marshal inspect shots result: %v", err))
	}
	content = append(content, &mcp.TextContent{Text: string(metaBytes)})

	for _, shot := range res.Shots {
		for _, rawPath := range shot.SheetPaths {
			p := strings.TrimSpace(rawPath)
			if p == "" {
				continue
			}
			target := p
			if s.policy != nil {
				resolved, err := s.policy.ValidateRead(p)
				if err != nil {
					continue
				}
				target = resolved
			}
			info, err := os.Stat(target)
			if err != nil || info.Size() > maxImageReadBytes {
				continue
			}
			imgBytes, err := os.ReadFile(target)
			if err != nil {
				continue
			}
			mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(target)))
			if mimeType == "" {
				mimeType = "image/jpeg"
			}
			content = append(content, &mcp.ImageContent{
				Data:     imgBytes,
				MIMEType: mimeType,
			})
		}
	}
	return &mcp.CallToolResult{Content: content}, nil, nil
}
