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
2. **Keep cuts musical, paced, and artifact-free**:
   - Use `tool.timeline_auto_assemble` (via `workflow_run_node`) if the episode has shots and TTS audio ready but the timeline is still empty.
   - Use `tool.media_peaks_thumbs` to inspect audio RMS peaks (`peaks`) and thumbnail sprites before trimming dead-air heads/tails or placing BGM ducking cues.
   - Prefer crisp atomic ops (`split_clip`, `move_clip`, `update_clip` for `transition_in`/`transition_ms`/`speed`/`volume`/`ken_burns_preset`, `update_track` for `muted`/`solo`/`volume`, `update_subtitle` for Vietnamese UTF-8 diacritics & timing).
3. **Final Render & External NLE Handoff**:
   - Final MP4 mastering uses `tool.render_timeline` (`xfade` transitions, EBU R128 `-14 LUFS` loudness normalization, and UTF-8 `.srt`/`.ass` Vietnamese subtitle burn-in).
   - For external NLE handoff, export via `tool.export_nle_draft` prioritizing `format="capcut"` / `"jianying"` (draft `.zip` bundle with `draft_content.json` + `draft_meta_info.json` + local media copies under ZIP limits) or `"fcpxml"` / `"otio"`.
