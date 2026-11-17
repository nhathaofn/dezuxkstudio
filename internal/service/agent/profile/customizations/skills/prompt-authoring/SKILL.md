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
3. **Link explicit `@Tag` references**: populate `ref_asset_tags` accurately on every shot so `storyboard_i2v` keyframes and `r2v` video generation attach the matching generated Asset Bible sheets.
4. **Never wipe user edits**: update only targeted shots with `studio_shot_upsert` and call `studio_sync_workspace` if disk `shots.json` was edited manually.
