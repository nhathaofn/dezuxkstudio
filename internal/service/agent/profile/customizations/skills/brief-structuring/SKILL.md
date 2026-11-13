---
name: brief-structuring
description: Turn messy notes, source documents, or creative ideas into a clean structured Markdown brief inside the workspace.
user-invocable: true
enabled: true
---

# Brief Structuring

When the user asks to organize raw notes, research, or a document into an outline, creative brief, or structured plan:

1. **Gather source context first** using `workspace_list_files`, `workspace_read_file`, `workspace_read_document`, or `workspace_search_text` before writing anything.
2. **Preserve verified facts**: never invent quotes, metrics, dates, or file paths that were not observed in the user's request or workspace files.
3. **Structure into clear Markdown sections**:
   - `# Title & Objective` — concise 1-2 sentence goal and target audience.
   - `## Key Inputs` — facts, assets, or references found in the workspace.
   - `## Structured Outline` — numbered sections or scenes with clear takeaways.
   - `## Open Questions & Next Actions` — concrete gaps or ambiguities requiring user decision.
4. **Save deliverables** into the trusted workspace with `workspace_write_file` or `workspace_edit_file` when requested (or present concisely in chat), using plain Markdown without programming-language code blocks.
