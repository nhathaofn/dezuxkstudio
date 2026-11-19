---
name: edit-timeline
description: Multi-track EditTimeline post-production, optimistic-revision atomic edits, waveform/sprite inspection, FFmpeg loudness/subtitle rendering, and CapCut/FCPXML draft export workflow.
user-invocable: true
enabled: true
---

# Timeline Post-Production & NLE Export Editor

When editing or rendering an episode's multi-track timeline on `/studio`:

1. **Always read current timeline & revision first**:
   - Call `studio_timeline_get` before making any timeline mutation so you have the latest `revision` (`expected_revision`) and exact `track_id`, `clip_id`, and `subtitle` cue IDs.
   - Pass `expected_revision` cleanly to `studio_timeline_apply_ops`, `studio_timeline_undo`, or `studio_timeline_redo`; if you receive a revision conflict error, re-fetch with `studio_timeline_get` and rebase your ops rather than overwriting blindly.
2. **Keep cuts musical, paced, and overrun-safe**:
   - Use `studio_timeline_auto_assemble` (or `tool.timeline_auto_assemble`) when assembling from shots + TTS audio. Configure `overrun_policy` intentionally (`warn` to report issues, `freeze_last_frame` to insert a held tail frame `-hold.png` matching narration duration, or `speed_fit` bounded by `max_speed_fit_ratio`), and inspect `issues` (`narration_overrun`, `narration_source_collision`) plus `studio_workflow_plan.timeline_issues`.
   - Use `studio_media_peaks_thumbs` (or `tool.media_peaks_thumbs`) to inspect audio RMS peaks (`peaks`) and thumbnail sprites before trimming dead-air heads/tails or placing BGM ducking cues.
   - Prefer crisp atomic ops (`split_clip`, `move_clip`, `update_clip` for `transition_in`/`transition_ms`/`speed`/`volume`/`mute`/`ken_burns_preset`, `update_track` for `muted`/`solo`/`volume`, `update_subtitle` for Vietnamese UTF-8 diacritics & timing).
3. **Final Render, Workflow Templates & External NLE Handoff**:
   - Check `workflow_list_templates` for multi-node pipelines such as `studio.dub_and_master` (TTS dub + BGM mix + render) and `studio.grid_to_timeline` (contact grid split + frame assembly).
   - Final MP4 mastering uses `studio_timeline_render` (or `tool.render_timeline` — `xfade` transitions, EBU R128 loudness normalization using the configured `export_loudnorm_lufs` inspectable via `workflow_list_specs`, and UTF-8 `.srt`/`.ass` Vietnamese subtitle burn-in).
   - For external NLE handoff, export via `studio_timeline_export_nle` (or `tool.export_nle_draft`) prioritizing `format="capcut"` / `"jianying"` (draft `.zip` bundle with `draft_content.json` + `draft_meta_info.json` + local media copies under ZIP limits) or `"fcpxml"` / `"otio"`.
