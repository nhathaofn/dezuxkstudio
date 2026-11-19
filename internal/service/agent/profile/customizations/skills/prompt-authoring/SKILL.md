---
name: prompt-authoring
description: Craft visually consistent English image_prompt keyframes, motion-rich video_prompt instructions, and @Tag reference mappings across every storyboard shot.
user-invocable: true
enabled: true
---

# Cinematic Prompt Authoring

When writing or refining Asset Bible entries (`studio_asset_upsert`) and Storyboard Shots (`studio_shot_upsert`):

1. **Maintain visual bible consistency**: write English visual prompts in `asset.prompt` specifying lighting, palette, clothing details, proportions, and neutral turnaround framing for character sheets.
2. **Separate still composition from motion**:
   - `image_prompt`: concrete composition, subject pose, facial expression, wardrobe, environment, camera lens, and lighting (avoid temporal words like "then" or "walks across").
   - `video_prompt`: camera movement (`slow dolly in`, `pan left`, `tracking shot`), micro-actions over the shot duration, facial performance, and spoken dialogue cues when `drama`.
3. **Link explicit `@Tag` & Asset IDs**: populate `asset_ids` (plus `@Tags` / `audio_speaker_id`) accurately on every shot so `storyboard_i2v` keyframes and `r2v` video generation attach matching Asset Bible sheets and TTS speakers. Use `studio_asset_rename` / `studio_asset_merge` when unifying asset names instead of manually hunting every shot.
4. **Author cleanly in batches without wiping edits**:
   - Prefer `studio_shots_batch_upsert` (`replace_all: false`) to create or patch multiple shots by `id` or `seq` in one atomic transaction (`replace_all: true` is reserved for intentional full storyboard replacement and invokes approval if shots exist).
   - For single-shot tweaks use `studio_shot_upsert`, and call `studio_sync_workspace` if disk `shots.json` was edited manually.
