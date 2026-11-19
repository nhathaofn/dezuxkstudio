---
name: film-director
description: End-to-end AI film production director orchestrating screenplay normalization, asset bible consistency, storyboard prompts, keyframes, video shots, narration TTS, and MP4 assembly in /studio.
user-invocable: true
enabled: true
---

# Film Director

Coordinate end-to-end AI film production in DezuxkStudio's Film Studio using shared engine nodes and Studio MCP tools:

1. **Discover active state & readiness first**: call `studio_workflow_plan` (and `studio_project_list` / `studio_project_get` when `current_screen.settings.episode_id` is missing) to inspect missing artifacts, speech/timeline issues, configured AI providers, and recommended next actions.
2. **Author directly when no PurposeText API is configured**: if `studio_workflow_plan` reports `text_provider_ready: false`, do NOT fail on `script_normalize` / `extract_assets` / `author_shots`; act as the Writer/Director directly using `screenplay-normalize` + `prompt-authoring`, saving scripts (`studio_save_script`), assets (`studio_asset_upsert`), and bulk shots (`studio_shots_batch_upsert`), or delegate long prose to `script-analyst` via `subagent_run`.
3. **Choose the right mode**:
   - `genre: drama` for character dialogues and dramatic scenes (`audio_speaker_id` / spoken `audio_script` cues).
   - `genre: narration` for documentary/storyteller voiceover (`audio_script` narration synthesized via `generate_tts`).
   - `production_mode: storyboard_i2v` to generate keyframes first and drive video with `start_image`.
   - `production_mode: r2v` to pass character/scene `@Tag` reference sheets directly to video generation.
4. **Follow the 5 ArcReel stages sequentially**:
   - Stage 1 (`script`): save raw script with `studio_save_script`, then normalize scenes/beats (`script_normalize` when text provider is ready, or author directly).
   - Stage 2 (`assets`): extract recurring `@Character`, `@Scene`, `@Prop` entries (`extract_assets` or direct `studio_asset_upsert`), deduplicate/tidy tags with `studio_asset_rename` & `studio_asset_merge` (`dry_run: true` first), check cost estimates via `studio_batch_preflight`, and generate reference sheets via `generate_assets` or `studio_queue_enqueue`.
   - Stage 3 (`storyboard`): author detailed `image_prompt` + `video_prompt` shots (`author_shots` or bulk `studio_shots_batch_upsert`) and generate keyframes (`generate_keyframes` or `studio_queue_enqueue`).
   - Stage 4 (`video`): preflight spend with `studio_batch_preflight`, enqueue durable generation via `studio_queue_enqueue`, await completion without polling loops via `studio_queue_wait` (or monitor `studio_queue_list`), and visually audit shots in batch with `studio_inspect_shots` (or delegate to `visual-reviewer`) plus targeted `studio_qc_review`. Check `workflow_list_templates` for multi-node pipelines (`studio.clean_and_inspect`, `studio.grid_to_timeline`).
   - Stage 5 (`export`): assemble multi-track timeline via `studio_timeline_auto_assemble` (choosing `overrun_policy`: `freeze_last_frame`, `speed_fit`, or `warn`), review `issues` (`narration_overrun`, `narration_source_collision`), refine with `studio_timeline_apply_ops`, and master via `studio_timeline_render` or `studio_timeline_export_nle` (`studio.dub_and_master` template for dub + loudness mastering).
5. **Respect user control & approvals**: paid generation, destructive deletions, `replace_all: true` batch shot rewrites, non-dry-run `studio_asset_merge`, pricing updates, and timeline renders pass through DezuxkStudio's approval gate unless `auto_approve_batches` is enabled on the project inside trusted folders. If JSON files are edited on disk, run `studio_sync_workspace` to reconcile SQLite before proceeding.
