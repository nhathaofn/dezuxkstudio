# Project: Watermark Module Refactoring & Atomic File Overwrite

## Architecture
DezuxkStudio includes a high-performance Watermark Removal subsystem consisting of:
1. **Core Algorithm Library (`pkg/watermark/`)**: High-throughput multi-scale watermark detection and reconstruction algorithms in Go, operating in-memory on raw image buffers with concurrency worker pools.
2. **Engine Tool Node (`internal/tools/watermark/`)**: Realtime workflow execution node that parses user parameters (`InPlace`, `OutputDir`, `Workers`, `Corner`), interacts with the filesystem, and executes atomic writes.
3. **Frontend UI (`frontend/src/features/watermark/`)**: React + TypeScript view providing drag-and-drop file queueing, configuration options, progress indication, and live batch results.
4. **Crash-Safe Filesystem Layer (`internal/fsutil/`)**: Atomic file writing primitives ensuring readers never observe partial files and interrupted writes never corrupt original files.

---

## Feature Inventory
| # | Feature | Description | Milestone | Source | Status |
|---|---------|-------------|-----------|--------|--------|
| F1 | Backend Types & Lifecycle Modularization | Extract models, enums, config, templates, and engine lifecycle into `pkg/watermark/types.go` | M1 | Survey Explorer 2 | DONE |
| F2 | Watermark Detection Modularization | Extract normalized cross-correlation and gain fitting algorithms into `pkg/watermark/detect.go` | M1 | Survey Explorer 2 | DONE |
| F3 | Watermark Reconstruction Modularization | Extract inpainting, contour smoothing, unblending, and image removal into `pkg/watermark/reconstruct.go` | M1 | Survey Explorer 2 | DONE |
| F4 | Batch & Concurrency Pipeline Modularization | Extract worker pool and streaming channel pipeline into `pkg/watermark/batch.go` | M1 | Survey Explorer 2 | DONE |
| F5 | Atomic In-Place File Overwrite | Replace `os.WriteFile` with `fsutil.WriteAtomic` / `fsutil.WriteFileAtomic` in `internal/tools/watermark/node.go`, preserving original file permissions on `InPlace` | M1 | Survey Explorer 3 | DONE |
| F6 | Watermark Package Atomic Write Export | Export `WriteAtomic(path string, data []byte, perm os.FileMode) error` in `pkg/watermark` | M1 | Survey Explorer 3 | DONE |
| F7 | Atomic Write & Rollback Unit Verification | Add tests in `pkg/watermark` and `internal/tools/watermark` verifying in-place atomic writes and failure rollback | M1 | Survey Explorer 3 | DONE |
| F8 | Frontend Config Card Extraction | Extract dropzone, corner, workers, save mode, and directory picker into `frontend/src/features/watermark/WatermarkConfigCard.tsx` | M2 | Survey Explorer 1 | DONE |
| F9 | Frontend Results Card Extraction | Extract empty queue, queued list, processing spinner, KPI stats, and batch results list into `frontend/src/features/watermark/WatermarkResultsCard.tsx` | M2 | Survey Explorer 1 | DONE |
| F10 | WatermarkView Composition Refactoring | Refactor `frontend/src/features/watermark/WatermarkView.tsx` to strictly compose `WatermarkConfigCard` and `WatermarkResultsCard` without extra subcomponents or hooks | M2 | Survey Explorer 1 | DONE |
| F11 | Automated Project Check Suite | Verify `go test ./pkg/watermark/...`, `go test ./internal/tools/watermark/...`, and `task check` with zero errors | M3 | Survey Explorer 3 | DONE |
| F12 | Adversarial Hardening & Forensic Integrity Audit | Multi-agent challenge, review, and zero-tolerance forensic integrity audit | M4 | Survey Explorers 1, 2, 3 | DONE |

---

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|------|-------|-------------|--------|
| M1 | Backend Package Modularization & Atomic Overwrite | `pkg/watermark/` decomposition into `types.go`, `detect.go`, `reconstruct.go`, `batch.go`, delete `watermark.go`, apply atomic write in `internal/tools/watermark/node.go` & `pkg/watermark`, add unit tests | none | DONE |
| M2 | Frontend Component Modularization | Split `WatermarkView.tsx` strictly into `WatermarkConfigCard.tsx` and `WatermarkResultsCard.tsx`, maintaining all state/controls/i18n/render | none | DONE |
| M3 | Comprehensive Verification Track | Run package tests, workspace regression, TypeScript typecheck, frontend tests, and `task check` | M1, M2 | DONE |
| M4 | Final Milestone: Review, Challenge & Forensic Audit | Dual Reviewers + Dual Challengers + Forensic Auditor Gate | M3 | DONE |

---

## Interface Contracts

### 1. `pkg/watermark` Package Contract
- All exported types (`Config`, `Corner`, `Info`, `Template`, `Engine`, `BatchItem`, `BatchResult`) and functions (`New`, `Default`, `Detect`, `Remove`, `RemoveImage`, `RemoveBatch`, `NewTemplate`, `NewTemplateFromBytes`, `DefaultTemplates`) remain 100% backward compatible.
- Exposes `WriteAtomic(path string, data []byte, perm os.FileMode) error` using `internal/fsutil`.
- Package namespace `watermark` is shared across `types.go`, `detect.go`, `reconstruct.go`, and `batch.go`.
- `watermark.go` is deleted.

### 2. File Writing Contract (`internal/tools/watermark/node.go`)
- `in.InPlace == true` sets `outPath = resPath`.
- File writes use `fsutil.WriteAtomic(outPath, perm, ...)` or `fsutil.WriteFileAtomic(outPath, data, perm)`.
- If `in.InPlace == true`, `perm` is read from `os.Stat(resPath).Mode().Perm()`; otherwise `0o644`.
- On write error, the original image file at `resPath` remains intact and unmodified.

### 3. Frontend Component Contract (`frontend/src/features/watermark/`)
- `WatermarkConfigCard`: React.FC rendering the left configuration card and dropzone, consuming `useWatermarkStore`.
- `WatermarkResultsCard`: React.FC rendering the right live output card, KPI metrics, and results list, consuming `useWatermarkStore`.
- `WatermarkView`: React.FC rendering the page hero header, global reset/start buttons, error text, and composing `<WatermarkConfigCard />` and `<WatermarkResultsCard />`. Exports named `WatermarkView` and default `WatermarkView`.
- No extra subcomponents or custom hooks added.

---

## Code Layout

### Backend Code Layout
- `pkg/watermark/types.go` — Data models, enums, config, templates, engine lifecycle (F1)
- `pkg/watermark/detect.go` — Search region calculation, NCC cross-correlation, gain fitting (F2)
- `pkg/watermark/reconstruct.go` — Unblending, inpainting, contour smoothing, bilateral denoising (F3)
- `pkg/watermark/batch.go` — Concurrent worker pool, streaming channel pipeline, WriteAtomic helper (F4, F6)
- `pkg/watermark/watermark_test.go` — Unit tests for detection, removal, batch, and atomic write (F7)
- `internal/tools/watermark/node.go` — Atomic file save for InPlace overwrites with permission preservation (F5)
- `internal/tools/watermark/node_test.go` — Node tests verifying InPlace atomic overwrite and error handling (F7)

### Frontend Code Layout
- `frontend/src/features/watermark/WatermarkConfigCard.tsx` — Left config card and dropzone (F8)
- `frontend/src/features/watermark/WatermarkResultsCard.tsx` — Right results card, queue list, metrics (F9)
- `frontend/src/features/watermark/WatermarkView.tsx` — Page wrapper and action bar (F10)
- `frontend/src/features/watermark/useWatermarkStore.ts` — Zustand store; types come from `NodeMap` in `contracts/generated.ts`, settings from `workflow:specs` (M6)

---

# Milestone M5: Timeout Model & Shared Building Blocks (branch `fix/timeouts-hardcode`)

## Goal
Stop the UI from reporting timeouts while the backend is still working, remove hard-coded timings,
and merge duplicated mechanisms into shared packages. Rules are in `.agents/rules/architecture.md` (§4–§6).

## Feature Inventory
| # | Feature | Where | Status |
|---|---------|-------|--------|
| F13 | Idle-timeout model for every action (download heartbeat generalised) | `internal/progress`, `internal/realtime/session.go`, `frontend/src/lib/rtClient/pending.ts` | DONE |
| F14 | Single long-action list, generated to TS | `contracts.LongActions()`, `cmd/contractgen` | DONE |
| F15 | Keep-alive for bounded waits (agent login/connect/probe/set_model) | `internal/service/agent` | DONE |
| F16 | Idle-based agent chat turns (output, MCP calls, tool progress re-arm) | `progress.NewIdleContext`, `agent/mcp_server.go` | DONE |
| F17 | CLI MCP tool timeout = long action timeout (Codex/Claude) | `agent/codex_runner.go`, `agent/claude_runner.go` | DONE |
| F18 | Retryable slow desktop start | `desktop/bridge.go`, `rtClient/endpoint.ts` | DONE |
| F19 | Browser clients use server timeouts from `/runtime-config.json` | `server/handlers`, `rtClient/endpoint.ts` | DONE |
| F20 | Hard-coded timings to config (`binaries.probe_timeout`, `realtime.keepalive_interval`, `agent.interrupt_wait`, `network.*`) | `internal/config` | DONE |
| F21 | Download errors as ERR_UNAVAILABLE/ERR_INTEGRITY; cancellable queue; translated progress | `service/binaries` | DONE |
| F22 | Shared packages replacing duplicates: `proc`, `lookup`, `httpclient`, `fsutil.IsFile`, single progress path, `verifyChecksum`, `LimitsFrom` | `internal/*` | DONE |
| F23 | Frontend: chat unlock after reconnect, no heartbeat logs, `useFlash`, `fakeBridge` | `frontend/src` | DONE |
| F24 | Agent-usable features enforced: `engine.ValidateSpec` at startup, node list in `workflow.New` | `internal/engine`, `service/workflow` | DONE |
| F25 | Rules for logging, protocol versioning, performance limits, git workflow; log key `error` standardised | `.agents/rules/architecture.md` §11–15 | DONE |
| F26 | Race detector job in CI (Windows, blocking); fixed the WaitGroup race it found in `runner` | `.github/workflows/ci.yml`, `internal/runner` | DONE |

## Known gaps
- Portable `tools/` next to the executable is still searched (violates the paths rule; kept for portable builds).
- Startup update check reports an error when the feed has no release.
- `go test -race` runs only in CI (no cgo toolchain on the dev machine); the watermark streaming node relies on it.
- Gaps closed in M6: swallowed agent bootstrap errors, half-open WebSocket detection, watermark whole-batch memory, protocol rule §13.
- Gaps closed in M7: `--mode server` admin actions locked under `server.allow_host_admin` (F43), SSRF redirect hopping prevented (F44), database foreign keys and cascade cleanup enforced (F51).

---

# Milestone M6a: Providers & Agent CLIs (branch `feat/providers`, PR #5, #6)

## Goal
Manage API providers (built-in and custom) as workflow nodes, and let the app own self-updating copies of the agent CLIs.

## Feature Inventory
| # | Feature | Where | Status |
|---|---------|-------|--------|
| F27 | Providers managed through workflow nodes; registry-driven probes; key health checked at start-up and on save | `internal/service/provider`, `internal/tools/provider` | DONE |
| F28 | Shared model list, per-provider limits, default model per purpose; translated provider errors (`msg_key`) | `domain/provider`, `features/providers` | DONE |
| F29 | Master-detail Providers screen | `frontend/src/features/providers` | DONE |
| F30 | `providers_*` MCP tools so the agent can set up keys from chat (pasted keys redacted) | `internal/service/agent` | DONE |
| F31 | App-owned, self-updating copies of claude / codex / agy; in-app sign-in; connection restored after restart | `internal/service/agentcli`, `internal/service/agent` | DONE |

---

# Milestone M6b: Agent Turn Lifecycle & Hardening (branch `refactor/agent-turn-lifecycle`)

## Goal
Make the agent package maintainable (drivers, one turn object, no global state) and close the known gaps
and rule violations found in the 2026-11 audit.

## Feature Inventory
| # | Feature | Where | Status |
|---|---------|-------|--------|
| F32 | One `*turn` with a single end path; turn/session drivers; no provider-ID branches; no package-level state | `internal/service/agent` (`turn.go`, `drivers.go`) | DONE |
| F33 | `service.go` split by responsibility (1 656 → ~330 lines): `provider_status.go`, `connection.go`, `conversation.go`, `turn_run.go`, `access.go` | `internal/service/agent` | DONE |
| F34 | Watermark node streams the batch (`ProcessStream`): one reader, `workers` encoders, each result written as it arrives; per-image progress | `internal/tools/watermark/node.go` | DONE |
| F35 | `engine.FileGuard`: nodes ask before reading/writing a path; headless `--mode server` attaches `AccessPolicy.CheckTrustedFile` to WebSocket node runs (media, workspace, trusted folders only) | `internal/engine/fileguard.go`, `core.go`, `realtime/modules/workflow.go` | DONE |
| F36 | Node catalogue generated to TS (`NodeTypes`, `NodeMap`); watermark UI reads options/limits/defaults from `workflow:specs` and shows live progress | `contracts/nodes.go`, `cmd/contractgen`, `features/workflow`, `features/watermark` | DONE |
| F37 | Agent store surfaces history/approvals/allowed-dir load errors instead of swallowing them | `features/agent/useAgentStore.ts` | DONE |
| F38 | Client detects a half-open WebSocket (ping after `liveness.idleMs` of silence, reconnect after `liveness.timeoutMs`) | `lib/rtClient/connection.ts`, `appConfig.liveness` | DONE |
| F39 | Hub accepts every version in `contracts.SupportedProtocolVersions()` (rule §13) | `internal/contracts/protocol.go`, `internal/realtime/hub.go` | DONE |
| F40 | Test files renamed by domain; test docs refreshed | `test/`, `TEST_READY.md`, `TEST_INFRA.md` | DONE |
| F41 | Review fixes: node specs reload on every connect (latest load wins); `output_dir` anchored to media before guard + write; trusted folders locked in server mode; agent error banner dismissible; liveness ping send guarded | `bootstrap.ts`, `useNodeSpecs.ts`, `tools/watermark/node.go`, `modules/agent.go`, `AgentDrawer.tsx`, `connection.ts` | DONE |
| F42 | Agent drawer shows trusted folders read-only unless the desktop app runs on its own (non-server) core | `features/agent/AgentDrawer.tsx` | DONE |

---

# Milestone M7: Architectural Hardening, Domain Boundaries & Subsystem Decoupling (Phases 1–5)

## Goal
Harden security boundaries, eliminate cross-package duplication, establish clean domain/service layer abstractions, decouple frontend Zustand stores, enforce relational database integrity, and establish unified English architectural documentation.

## Feature Inventory
| # | Feature | Where | Status |
|---|---------|-------|--------|
| F43 | Server mode admin lock via `server.allow_host_admin` (locks `system:open_folder`, `binaries:set_path`, `agent:set_path`, and 6 skill/subagent mutators) | `internal/config`, `internal/realtime/modules`, `internal/contracts` | DONE |
| F44 | HTTP client SSRF redirect protection with `WithRedirectGuard` & dial guard blocking private/link-local/multicast hops | `internal/httpclient` | DONE |
| F45 | Document parsing bounds & caps for Office/PDF formats (`maxDocBytes`, `maxTextBytes`, decompression zip bomb defense) | `internal/docparse` | DONE |
| F46 | Media staging upload size caps (`server.max_media_upload_bytes`) and automatic background TTL pruning (`server.media_staging_ttl`) | `internal/server/handlers/media.go` | DONE |
| F47 | Deduplication of shared runtime paths into `internal/paths`, subprocess execution into `internal/proc`, and executable discovery into `internal/lookup` | `internal/paths`, `internal/proc`, `internal/lookup` | DONE |
| F48 | Domain layer modularization separating core domain types and persistence from business orchestration | `internal/domain/provider`, `internal/domain/agent`, `internal/domain/mediagen`, `internal/domain/preferences` | DONE |
| F49 | Service layer extraction isolating orchestration logic (`service/provider`, `service/agent`, `service/system`, `service/diagnostics`) | `internal/service/*` | DONE |
| F50 | Frontend monolithic store decomposition into dedicated Zustand slices (`useProvidersStore`, `useAgentCustomizationsStore`, `useAgentDrawerStore`) | `frontend/src/features/providers`, `frontend/src/features/agent` | DONE |
| F51 | SQLite relational schema integrity migration (`014_agent_foreign_keys.sql`) with foreign key constraints, `ON DELETE CASCADE`, and pre-014 orphan cleanup | `internal/storage/sqlite` | DONE |
| F52 | Comprehensive English architecture blueprint & unified English documentation (`docs/architecture.md`, `README.md`, `docs/*.md`) | `docs/architecture.md`, `docs/*` | DONE |

