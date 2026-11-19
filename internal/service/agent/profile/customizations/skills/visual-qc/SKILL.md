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
2. **Batch Contact Sheet + Signal Inspection first, then deep QC**:
   - Run `studio_inspect_shots` (`mode: "both"`, `include_signals: true`) to render composite JPEG contact sheets (timestamped tiles + `BLACK` / `FREEZE` / `CUT` FFmpeg signal badges) returned directly as visual `ImageContent` + structured `signals` metadata for multi-shot triage.
   - Drill into flagged shots with `studio_qc_review` (`tool.qc_review` = `ffprobe` + sampled frames + multimodal Vision critique) passing canonical `@Character`/`@Scene` reference paths and canonical `target_kind` (`asset`, `keyframe`, `video`, `dub_audio`, `lipsync_video`, `grid_panel`, `clean_video`).
   - Check for broken aspect ratios, missing audio tracks, excessive dead air (`silent_ratio` above the configured `qc_silence_warn_ratio` inspectable via `workflow_list_specs`), deformed hands/faces, or wardrobe drift.
3. **Repair surgically — don't discard good framing**:
   - If a keyframe or character turnaround only has a local flaw (wrong prop, outfit defect, text artifact), use `studio_mask_edit` (or `tool.mask_edit` with `mask_path` over the problem area) instead of regenerating the entire frame from scratch.
   - If a sheet generated as a 2x2 or 3x3 grid, split individual panels via `studio_grid_split` (or check `studio.grid_to_timeline` in `workflow_list_templates`) and select the best candidate via `studio_version_select`.
   - For burned-in corner logos on `.mp4` clips, run `tool.video_watermark_remove` (`delogo` or `crop` mode) or follow the `studio.clean_and_inspect` template rather than re-generating clean video.
4. **Enforce continuity & poll-free queue waiting**:
   - For sequential camera shots, prefer extracting the previous shot's final frame with `studio_extract_last_frame` (or `tool.extract_frames` with `mode="last"`) as the next keyframe.
   - Wait on active async generation jobs with `studio_queue_wait` instead of tight polling loops against `studio_queue_list` (which burns tokens). Never cancel active remote video jobs unnecessarily so `provider_job_id` recovery works across restarts.
