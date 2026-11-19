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
3. **Tag recurring entities consistently**: use clean ASCII `@Tags` in beat tags matching Asset Bible entries created with `studio_asset_upsert`. If duplicate or inconsistently spelled tags appear across episodes, unify them cleanly with `studio_asset_rename` and `studio_asset_merge` (run with `dry_run: true` first to inspect cascading script/shot rewrites).
4. **Use Studio MCP persistence & no-text-API fallback**: save raw script text via `studio_save_script`. Check `studio_workflow_plan`: if `text_provider_ready: true`, run `studio_episode_run_stage` (`script_normalize`); if `text_provider_ready: false`, decompose scenes/beats directly in your reasoning (or invoke `script-analyst`) and persist structured assets (`studio_asset_upsert`) + storyboard beats/shots (`studio_shots_batch_upsert`) directly without requiring an external LLM text key.
