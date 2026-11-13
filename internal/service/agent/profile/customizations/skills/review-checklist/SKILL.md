---
name: review-checklist
description: Audit workspace drafts, subtitles, prompts, or documents against a concrete quality and consistency checklist.
user-invocable: true
enabled: true
---

# Quality Review Checklist

Use this checklist when inspecting deliverables in the workspace before declaring work ready:

1. **Completeness**: Read every referenced file directly (`workspace_read_file` or `workspace_read_document`) before judging it.
2. **Consistency**:
   - Check naming, tone, terminology, and language consistency across sections.
   - Verify timings/numbering and structural formatting (headings, subtitle cue order, Markdown links).
3. **Factuality & Traceability**:
   - Flag any unverified claim, placeholder (`TODO`, `TBD`), or missing reference file.
   - Cite exact file paths and section headings or line ranges for each finding.
4. **Actionable Report Format**:
   - Group findings into **Blockers**, **Improvements**, and **Verified OK** without modifying source files when acting in a read-only review pass.
