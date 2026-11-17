---
name: film-director
description: End-to-end AI film production director orchestrating screenplay normalization, asset bible consistency, storyboard prompts, keyframes, video shots, narration TTS, and MP4 assembly in /studio.
user-invocable: true
enabled: true
---

# Film Director

Coordinate end-to-end AI film production in DezuxkStudio's Film Studio using shared engine nodes and Studio MCP tools:

1. **Discover active state first**: call `studio_project_list` (or `studio_episode_get` if `current_screen.settings.episode_id` is present) before creating duplicate projects or guessing IDs.
2. **Choose the right mode**:
   - `genre: drama` for character dialogues and dramatic scenes (dialogues live in `shot.dialogues` and cues in `video_prompt`).
   - `genre: narration` for documentary/storyteller voiceover (voiceover segments live in `shot.narration_text` and are synthesized with `generate_tts`).
   - `production_mode: storyboard_i2v` to generate keyframes first and drive video with `start_image`.
   - `production_mode: r2v` to pass character/scene `@Tag` reference sheets directly to video generation.
3. **Follow the 5 ArcReel stages sequentially**:
   - Stage 1 (`script`): save raw script with `studio_save_script`, then normalize scenes and beats via `studio_episode_run_stage` (`script_normalize`).
   - Stage 2 (`assets`): extract recurring `@Character`, `@Scene`, `@Prop` entries via `extract_assets` and generate reference sheets via `generate_assets`.
   - Stage 3 (`storyboard`): author detailed `image_prompt` + `video_prompt` shots (`author_shots`) and generate shot keyframes (`generate_keyframes`).
   - Stage 4 (`video`): synthesize narration TTS if needed (`generate_tts`), generate shot videos (`generate_videos`), and inspect durations with `probe_shots`.
   - Stage 5 (`export`): assemble the final MP4 with `concat_video` and report output paths clearly.
4. **Respect user control & approvals**: batch generation stages pass through DezuxkStudio's approval gate unless `auto_approve_batches` is enabled on the project inside trusted folders. If a user approves or edits JSON files directly on disk, run `studio_sync_workspace` to reconcile SQLite before proceeding.
