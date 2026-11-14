# Exposing Features to the Agent (No Agent-Specific Code Required)

The DezuxkStudio Assistant (agent) **does not maintain a hardcoded list of features**. On every conversation turn, the agent dynamically discovers registered capabilities in the workflow engine via `workflow_list_specs`, and executes them via `workflow_run_node` or `workflow_run_graph`. These tools traverse the **exact same code path used by the UI** (`workflow.Service.RunNode` → `engine.Registry`).

Therefore: **Any new feature implemented as an engine node with an `engine.Spec` is immediately available to the agent.**

To prompt an AI assistant to implement a new feature, refer to the template in [feature-prompt-template.md](feature-prompt-template.md).

---

## 1. Checklist for Adding a New Node

1. **Implement Node**: Create your node in `internal/tools/<feature_name>/node.go` using `engine.Typed(spec, func(ctx context.Context, in In) (out Out, err error))`. If DTOs or `NodeType*` constants are shared across boundaries (e.g. for frontend or runtime contracts), place them in `internal/contracts/nodes_dto.go` and alias them in your tool package (keeping `internal/contracts` decoupled from `internal/tools/*`).
2. **Register Node**: Register the node within `workflow.New(...)` in `internal/service/workflow/service.go`.
3. **Declare Complete `engine.Spec`**: Provide complete specifications (see Section 2). This is the sole contract parsed by the agent. `engine.ValidateSpec` validates all specs on application startup: missing `Description`, `select` without `Options`, incorrect `Default`, or non-toggle `Destructive` flags will **cause startup validation to fail**.
4. **Regenerate Contracts**: Run `task generate` (or `go run ./cmd/contractgen`) if you modify structs in `internal/engine` or `internal/contracts`.
5. **UI Alignment**: The frontend UI for the feature must use the exact `Default` and `Options` values specified in the spec. Deriving UI defaults directly from the spec guarantees zero divergence between manual UI workflows and autonomous agent operations.
6. **Add Unit & Agent Tests**: Add test cases following `internal/service/agent/access_test.go` (`TestWorkflowRunNode...`), using `fakeWorkflow{specs: ...}` with the node's real spec.

No modifications to `internal/service/agent` are required when adding new processing nodes.

---

## 2. Declaring `engine.Spec` for Clear Agent Comprehension

| Field | Purpose for the Agent | Guidelines |
|---|---|---|
| `Type` | Unique identifier (e.g. `tool.watermark_remover`) | Immutable once published. |
| `Label` | Display name shown in UI and Approval dialogs | Keep concise and self-explanatory. |
| `Description` | Semantic trigger condition read by the LLM | Clearly state what the node does and which file types it accepts. |
| `Inputs[]` (`engine.Port`) | File, directory, or media inputs | See Section 3. |
| `Params[]` (`engine.Param`) | Execution configuration parameters | See Section 4. |
| `Outputs[]` | Output descriptors | Document result shapes accurately. |

---

## 3. Input Ports (`engine.Port`)

- **`Type`**: `image`, `video`, `media` (either image or video), or `text`.
  - For media ports, the agent can supply **a single file path, a directory path, or an array of paths**. The server automatically expands directory paths into matching media files (sorted alphabetically, filtering out code files, secrets, `.git`, etc.). The agent tool accepts an optional `recursive: true` parameter to traverse subdirectories. The node receives only sanitized, validated absolute file paths.
  - Relative paths or `/media/...` URLs are resolved deterministically against the application media directory using `engine.ResolveMediaPath`. Image inputs and collections use `engine.ImageInputRef` and `engine.ImageInputRefs`.
  - `text` ports pass strings unmodified.
- **`Multiple: true`**: Accepts batch files. If a node defines both single-item and multi-item ports, providing multiple files to a single-item port produces an actionable error guiding the agent to use the multi-item port.
- **`Optional: false`**: Mandatory input. If omitted, the agent receives an immediate validation error.

Limit: A maximum of `maxNodeInputFiles` (10,000 files) can be processed per execution.

---

## 4. Settings & Parameters (`engine.Param`)

| Field | Meaning to the Agent |
|---|---|
| `Kind` | `engine.ParamToggle` (bool), `engine.ParamInteger` (int), `engine.ParamSelect` (enum), `engine.ParamText` (string). |
| `Options` | **Mandatory for `select`**. Values not in this list are rejected (case-insensitive). |
| `Default` | **Matches UI default values**, serialized as a string (`"false"`, `"0"`, `"bottom-right"`). If omitted by the agent, the server automatically populates it. |
| `Min` / `Max` | Bounds for `integer`. Values outside this range are rejected. |
| `Role: engine.ParamRoleOutputDir` | Indicates an **output destination directory**: validated against `AccessPolicy` and cannot point to an existing file. |
| `Destructive: true` | Setting this toggle to true will **modify or overwrite the user's original files** $\rightarrow$ Always pauses execution to await user **Approval**. |
| `Secret: true` | Parameter contains sensitive credentials (e.g. API keys). Redacted from logs, summaries, and approval cards, and purged from chat history upon turn completion. Must never define a `Default`. |

Any parameter not declared in `Inputs` or `Params` is rejected. Internal tuning knobs not exposed in the UI (e.g. internal algorithm thresholds) are deliberately omitted from the spec to ensure the agent adheres strictly to UI behavior.

Example (Watermark Remover):
```go
Params: []engine.Param{
    {Name: "inplace", Label: "Overwrite original file", Kind: engine.ParamToggle, Default: "false", Destructive: true},
    {Name: "output_dir", Label: "Output directory", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
    {Name: "workers", Label: "CPU worker threads (0 = Auto)", Kind: engine.ParamInteger, Min: 0, Max: 64, Default: "0"},
    {Name: "corner", Label: "Scan corner", Kind: engine.ParamSelect,
        Options: []string{"bottom-right", "bottom-left", "top-right", "top-left", "any"}, Default: "bottom-right"},
},
```

---

## 5. Node Output & Result Contracts

The agent **reasons and reports strictly based on structured tool outputs**. Node outputs should include:
- Total items processed and count of modified items.
- Per-file breakdown: input path, output path, status, and error details (if any).
- Actionable error messages clearly stating the root cause and remediation, rather than generic `"failed"` messages.

Nodes must respect `ctx.Done()` (cancellation/timeouts), check all local read/write paths via `engine.CheckFile(ctx, path, write)` (ensuring `--mode server` enforces `AccessPolicy.CheckTrustedFile` for remote clients), report streaming progress with `progress.Report(ctx, ...)`, write files atomically using `fsutil.WriteFileAtomic`, and never overwrite original files unless a `Destructive` flag is explicitly enabled.

---

## 6. Non-Node Capabilities (Navigation, Project Management, System)

Capabilities that do not follow an `input → output` processing model (such as project management or window navigation) are exposed as **MCP Tools** in `internal/service/agent/mcp_tools.go`:
- Delegate directly to existing domain services. Never re-implement business logic inside the agent package.
- Destructive actions or user data modifications must trigger `s.needsApproval(tool, title, summary, exec)`.
- All paths must pass through `s.policy` (`Resolve`, `ValidateRead`, `ValidateWrite`, etc.).
- Return structured data using `mcpJSON(...)` and errors via `mcpError(...)`.
- Register the tool in `buildCapabilitiesSnapshot` if it should appear in `mcp__dezuxk__studio_list_capabilities`.

---

## 7. Filesystem Access Policy (`AccessPolicy`)

| Zone | Permissions |
|---|---|
| **Outside Trusted Directories** | **Completely Denied** (Reading, writing, listing, or deleting). |
| **Trusted Directories** (Workspace, media, user-approved folders) | Listing, reading, writing, moving, and deleting within authorized roots. Node operations marked `Destructive` still require explicit user approval. |
| **Always Blocked Inside Trusted Directories** | Source code, scripts, binaries (unless in `DeveloperMode`), credentials (`.ssh`, `.env`, keyrings), browser profiles, OS directories, and DezuxkStudio internal application storage. |

Security rules are enforced by `internal/service/agent/access_policy.go` (`forbiddenExtensions`, `forbiddenFileNames`, `forbiddenSegmentNames`, `buildProtectedDirs`). In `--mode server`, `agent:add_allowed_dir` and `agent:remove_allowed_dir` are strictly locked (`ERR_UNAVAILABLE`), and editing shared customizations (`<data>/agent-customizations/`) is denied unless `server.allow_host_admin` is enabled. Document reading in workspace directories (.docx, .odt, .rtf, .pdf) uses `internal/docparse`, and parallel subagent execution is bounded by `agent.max_concurrent_subagents` (default 4).

---

## 8. Safety & Policy Guardrails

All raw text output generated by external agent CLIs passes through `code_guard.go`. Prohibited code blocks or unauthorized shell commands are intercepted before reaching the UI, conversation history, or filesystem.

### Layered System Prompt Structure (`internal/service/agent/profile.go`)

System prompts are assembled in stable layers to maximize prefix prompt caching across model providers:

| Layer | Source | Contents |
|---|---|---|
| `<policy>` | `profile/policy.md` | **Application invariant rules**: scope, hard boundaries, truthfulness. Tool names and features are forbidden. `{{app}}` expands to the product name. |
| `<application>` | Runtime | Name, version, OS platform, active UI language. |
| `<features>` | Registered `engine.Spec`s | Dynamic single-line summary per capability: type, name, description, inputs, settings. |
| Per Turn | `UIContext` | Screen context, sent **only when changed** within a session. |

### Editing Principles:
- Tool usage guidelines belong in tool descriptions in `mcp_tools.go`, never in `policy.md`.
- Capability descriptions belong in `engine.Spec.Description`; they automatically populate the `<features>` block.
- User-visible text must use frontend i18n keys (`agent.notice.*`, `agent.err.*`).
- `policy.md` is strictly reserved for core system invariants. The `TestPolicyIsRulesOnly` test guarantees that tool names, feature specifics, and non-English text never pollute `policy.md`.
