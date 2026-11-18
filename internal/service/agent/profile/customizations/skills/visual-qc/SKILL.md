---
name: visual-qc
description: Multimodal visual continuity, technical FFprobe validation, cost preflighting, and targeted repair workflow for Film Studio assets, keyframes, and shot videos.
user-invocable: true
enabled: true
---

# Visual QC & Production Budget Gatekeeper

When evaluating or repairing Film Studio outputs on `/studio`:

1. **Preflight before spending**:
   - Call `studio_batch_preflight` (or `workflow_run_node` with `tool.batch_preflight`) before launching paid image, video, TTS, or lip-sync batches.
   - Check `studio_cost_summary` when continuing an episode to monitor actual vs estimated spend.
2. **Inspect technically AND visually**:
   - Call `studio_qc_review` (which uses `tool.qc_review` = `ffprobe` stream/silence checks + sampled FFmpeg frames + multimodal Vision critique) with canonical `@Character`/`@Scene` reference paths and canonical `target_kind` (`asset`, `keyframe`, `video`, `dub_audio`, `lipsync_video`, `grid_panel`, `clean_video`).
   - Check for broken aspect ratios, missing audio tracks, excessive dead air (`silent_ratio` above the configured `qc_silence_warn_ratio`, default `0.90` — inspectable via `workflow_list_specs`), deformed hands/faces, or wardrobe drift.
3. **Repair surgically — don't discard good framing**:
   - If a keyframe or character turnaround only has a local flaw (wrong prop, outfit defect, text artifact), use `studio_mask_edit` (or `tool.mask_edit` with `mask_path` over the problem area) instead of regenerating the entire frame from scratch.
   - If a sheet generated as a 2x2 or 3x3 grid, split individual panels via `studio_grid_split` (or `tool.grid_split`) and select the best candidate via `studio_version_select`.
   - For burned-in corner logos on `.mp4` clips, run `tool.video_watermark_remove` (`delogo` or `crop` mode) rather than image remover.
4. **Enforce continuity & safe queue control**:
   - For sequential camera shots, prefer extracting the previous shot's final frame with `studio_extract_last_frame` (or `tool.extract_frames` with `mode="last"`) as the next keyframe.
   - Never cancel or retry active remote video jobs unnecessarily; inspect `studio_queue_list` first so remote jobs resume cleanly via `provider_job_id`.
