# Feature Specification Prompt Templates (Agent-Accessible Capabilities)

Technical design rules are strictly enforced in `.agents/rules/architecture.md` and [docs/architecture.md](architecture.md). When prompting an AI agent to implement a new capability, you only need to specify:
**What the feature does, its execution shape, autonomous agent permissions, and actions requiring explicit user Approval.**

For comprehensive technical requirements, refer to [docs/agent-feature-integration.md](agent-feature-integration.md).

---

## 1. Concise Prompt Template

```text
Add feature <name>: <1-2 sentence summary of what the feature achieves>.
The feature must be accessible to both UI workflows and the assistant agent, adhering to clean architecture rules.
- Input: <file types / input data shape>
- Output: <expected result structure & output artifacts>
- User-configurable parameters: <list with UI default values and bounds>
- Actions requiring Approval: <e.g. in-place file overwrite / permanent deletion>
Implement on a new branch with automated test suites, and ensure `task check` passes.
```

---

## 2. File-Processing Feature Template (Engine Node Model)

```text
Add an "Image Compressor" feature as an engine node (input → output), accessible to the agent via workflow_* tools.

Purpose (for engine.Spec Description): Compresses JPG/PNG/WebP images to reduce file size while preserving resolution.
When the agent should invoke it: When the user asks to reduce image size, optimize for web, or free up disk space.

Input Ports:
- images: Multiple image inputs (engine.Port with Multiple: true), accepts both file paths and directories.

Parameters (engine.Param):
- quality: integer between 1 and 100, default 80.
- format: select [original, jpg, webp], default "original".
- output_dir: directory path (engine.ParamRoleOutputDir).
- inplace: overwrite original files in place, toggle, default false, Destructive: true → requires user Approval.

Output:
- Total items processed, per-file before/after byte sizes, and actionable error messages for corrupt files.

Technical Requirements:
- Report streaming progress per item using progress.Report; process as a stream (do not load all files into RAM simultaneously).
- Validate all local read/write paths via engine.CheckFile(ctx, path, write) and resolve paths using engine.ResolveMediaPath / engine.ImageInputRef.
- Declare shared node DTOs in internal/contracts/nodes_dto.go (decoupled from internal/tools/*).
- UI must read Default values directly from spec, with 100% en/vi i18n parity.
- Unit tests: execute node against sample images; test agent MCP workflow_run_node invocation; verify inplace=true creates a pending approval item.
```

---

## 3. Non-File Feature Template (Realtime Action + MCP Tool Model)

```text
Add a "Project Workspace Manager" feature: create, rename, delete, and list project directories.
This is not an input → output media transform; implement it as a domain service + realtime module + agent MCP tool.

Agent Permissions:
- Autonomous: list projects, create new project, rename project.
- Requires Approval: delete project, move files outside project boundary.

Requirements:
- Register project paths into AccessPolicy trusted roots.
- Include project management in studio_list_capabilities.
- Tests: verify MCP tool dispatches to service; verify delete operations pause for user Approval; verify path boundaries pass through AccessPolicy.
```

---

## 4. Why Prompt Sections Matter

| Specified Field | Becomes | Purpose for the Agent |
|---|---|---|
| "Purpose" & "When to use" | `engine.Spec.Description` | Guides the LLM's semantic routing to pick this tool. |
| Settings & Defaults | `engine.Param` (`Default`, `Options`, `Min`/`Max`) | Allows the agent to use identical defaults to the UI without guessing. |
| Destructive actions | `Destructive: true` or `needsApproval` | Triggers the safety gate to pause turn and request user confirmation. |
| Structured error shapes | Machine-readable outputs | Enables the agent to accurately report status and diagnose failures. |

---

## 5. Verification Checklist After Implementation

1. **Node Registered**: Verified in `workflow.New(...)` (for engine nodes), or declared in `internal/service/agent/mcp_tools.go` (for direct MCP tools).
2. **Approval Verification**: Automated tests verify that `Destructive` operations generate approval requests.
3. **Capability Discovery**: Asking the Assistant "What capabilities do you have?" in chat includes the new feature.
4. **CI Gates Pass**: `$env:GOTMPDIR="$PWD\build\.gotmp"; task check` and `task test:race` pass with 0 errors.
