---
name: screenplay-normalize
description: Parse raw story ideas or messy screenplays into faithful, clean NormalizedScript scenes, beats, dialogue lines, narration segments, and consistent @Tag references.
user-invocable: true
enabled: true
---

# Screenplay Normalization

When structuring a raw screenplay, prose story, or voiceover draft for Film Studio:

1. **Preserve original language**: keep every spoken dialogue (`dialogue.line`) and voiceover (`narration.text`) in the exact language of the user's source script (e.g. Vietnamese or English).
2. **Split into visual beats (4-8s each)**: break long paragraphs into distinct continuous camera actions so downstream video models can render each beat cleanly in one clip.
3. **Tag recurring entities consistently**: use clean ASCII `@Tags` in `beat.asset_tags` (for example `@Minh`, `@OldCafe`, `@Lantern`) matching the Asset Bible entries created with `studio_asset_upsert`.
4. **Use Studio MCP persistence**: save verified raw script text via `studio_save_script`, run `studio_episode_run_stage` with `script_normalize` (or invoke `script-analyst` on long multi-scene inputs), and confirm scene/beat counts back to the user.
