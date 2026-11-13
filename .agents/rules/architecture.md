---
description: "Architecture boundaries, zero-hardcode rules and conventions for DezuxkStudio"
globs: "**/*"
always_on: true
---

# DezuxkStudio – Architecture & Coding Rules

## 1. Layers & dependency direction
- `internal/domain/<feature>`: models, invariants (`Validate`), `Repository` interfaces, typed errors (`apperr`). No I/O, no transport types.
- `internal/service/<feature>`: use cases. Depend only on domain interfaces, injected funcs and the shared utility packages (§4). No `realtime`, no `sqlite`, no `wails`.
- `internal/storage/sqlite`: repository implementations + migrations `migrations/NNN_name.sql` (append-only, never edit a released file).
- `internal/contracts`: the ONLY definition of what crosses Go ↔ TS (action/event names, DTOs, enums, bridge methods, the long-action list). Regenerate TS with `task generate`.
- `internal/realtime/modules`: thin adapters `realtime.Handle(r, contracts.ActionX, fn)`; decode → one service call → DTO. No business logic, no progress plumbing.
- `internal/core`: the composition root. A new feature = one entry in `modules()`.
- `internal/desktop`: Wails shell only. `Bridge` exported methods must equal `contracts.Bridge()` (enforced by test).
- Direction: `modules → service → domain ← storage`. Nothing imports `core` or `desktop` except entry points.

## 2. Naming
- Actions: `<domain>:<verb>[_<noun>]` (`preferences:update`). Events: `<domain>:<past_tense>` (`preferences:updated`). Native (Wails) events: `app:<state>`.
- Go constants `ActionXxx` / `EventXxx` in `internal/contracts`; TS uses the generated `Actions.XXX` / `Events.XXX`. String literals for action/event names are forbidden on both sides.

## 3. Errors
- Return `*apperr.Error` (or wrap with `%w`) for user-meaningful failures; codes are the `apperr.Code` constants. Network/IO failures the user can act on are `ERR_UNAVAILABLE`, damaged data `ERR_INTEGRITY` — never let them fall through to `ERR_INTERNAL`.
- Never put secrets, paths of user files, SQL or raw causes into `Message`; unknown errors become `ERR_INTERNAL` automatically and are logged with the request id.
- Frontend branches only on `ActionError.code` (and documented `details` flags such as `starting`) and translates via `errors.<CODE>` i18n keys.

## 4. Shared building blocks (use these, do not re-implement)
| Need | Package | API |
| :--- | :--- | :--- |
| Report progress / keep an action alive | `internal/progress` | `Report(ctx, Update)`, `Func(ctx)`, `KeepAlive(ctx)`, `NewIdleContext` |
| Start / stop an external process | `internal/proc` | `Command(ctx, exe, args...)`, `KillTree(cmd)` |
| Run a CLI with streamed output + parsed progress | `internal/runner` | `RunWithOptions` |
| Find an executable (custom path → tools → PATH → known paths) | `internal/lookup` | `First(Candidate...)`, `File`, `Files`, `OnPath` |
| Outbound HTTP | `internal/httpclient` | `New(cfg.Network)`, `WithTimeout(base, d)` — the core receives one shared client |
| Crash-safe file writes / file checks | `internal/fsutil` | `WriteFileAtomic`, `WriteAtomic`, `IsFile` |
| Goroutines | `internal/safego` | `Go(name, fn)` |
| Paths | `internal/paths` | never write next to the executable or into temp dirs |
A second copy of any of these is a review blocker; extend the shared package instead.

## 5. Configuration (zero hardcode)
- Every tunable lives in `config.Config` and is declared once in `internal/config/fields.go` (file key, env var and flag are derived).
- Default values exist ONLY in `config.Default()`. Services that need standalone defaults derive them (`agent.LimitsFrom(cfg)`, `DefaultLimits()`, `binaries.DefaultProbeTimeout()`) — never repeat a literal.
- Validate all invariants in `Config.Validate()` (e.g. `realtime.keepalive_interval < realtime.action_timeout`); never silently fall back.
- Product identity and build metadata only in `internal/buildinfo` (ldflags). Frontend client-only knobs only in `frontend/src/config/appConfig.ts` (including UI durations under `appConfig.ui`).
- Paths only via `internal/paths`.

## 6. Timeouts & progress (idle-timeout model)
- An action's timeout measures **silence, not total duration**. Every progress report re-arms it on the server (idle context per action) and on the client (pending request timer). The binaries download is the reference implementation.
- Report progress with `progress.Report(ctx, progress.Update{Percent, Message, Key, Params})`. `Key`/`Params` name an i18n key so the UI can translate; `Message` is the English fallback. Do not add progress callback parameters to service APIs.
- A wait that is already bounded by its own deadline (CLI sign-in, ACP handshake, a probe with its own timeout) wraps itself in `defer progress.KeepAlive(ctx)()`. NEVER keep alive an unbounded wait — report real progress instead, so a hang still times out.
- `contracts.LongActions()` lists actions where a single step can be silent for long (download, workflow, diagnostics, agent approvals); they get `realtime.long_action_timeout`. Bounded waits are NOT long actions — they use `KeepAlive`. The TS list is generated; never hard-code it on either side.
- Agent chat turns use `progress.NewIdleContext` with `agent.turn_timeout`: streamed output, MCP tool calls and progress reported inside a tool re-arm it.
- Desktop startup: `desktop.ready_timeout` is a polling window. A slow start returns a retryable error (`details.starting`); only a real startup failure is fatal.
- Limits that must agree are derived, not duplicated (e.g. MCP tool timeout of agent CLIs = `realtime.long_action_timeout`).

## 7. Concurrency & lifecycle
- Start goroutines with `safego.Go` (panic → log + crash dump). Every blocking call takes a `context.Context`; waiting for a lock/slot must also select on `ctx.Done()`.
- Long-running work returns promptly when `ctx` is cancelled.
- Work that must not hold up an action response (e.g. reading a CLI model catalog) runs in the background and pushes its result as an event.
- Shutdown order: hub (drain sessions + actions) → HTTP server → database.

## 8. Security
- Desktop core: loopback only, per-session random token (subprotocol `auth.<token>`), constant-time compare.
- Secrets: OS credential store via `internal/secrets`; never in config files, DB or logs.
- Webview CSP is sent from `internal/desktop/csp.go`; no inline scripts in production.
- Agent CLIs run with isolated profiles; the local MCP server is loopback-only with a per-process bearer token.

## 9. Frontend
- Feature folders `src/features/<name>/` (store + components). Shared UI in `src/components/ui`, shared hooks in `src/lib/hooks` (e.g. `useFlash` for self-resetting UI state).
- Zustand with atomic selectors; subscriptions are created once in `app/bootstrap.ts` or in effects that return their unsubscribe. Timers started by a component are cleared on unmount.
- All server frames are validated with Zod (`lib/rtClient/protocol.ts`); static types come from `contracts/generated.ts`.
- `action:progress` frames with `heartbeat: true` only keep a request alive: never render or log them. Translate progress with `translateServerMessage(t, msg_key, msg_params)`, falling back to `message`.
- State that depends on server events (e.g. "sending") is re-synchronised from server state after every reconnect.
- All UI strings go through `useT()`; add keys to `en.ts` and every other locale (including server-side message/progress keys).
- Routes: add to `app/navigation.ts` (HashRouter). Icons: `lucide-react`.

## 10. Tests
- Unit tests live next to the package (`*_test.go`, white-box allowed). `test/` holds end-to-end tests that run the real core.
- Timing behaviour is tested for real (fake slow CLIs/tools as scripts, real WebSocket/HTTP servers), not only with mocks.
- Frontend unit tests: `src/**/*.test.ts` (Vitest). Logic that needs testing is kept framework-free (e.g. `Flash` behind `useFlash`).
- `task check` must pass before committing.

## 11. Features must be agent-usable by construction
The agent has no hard-coded feature list. Choose the feature's shape so the agent uses it with **zero agent code**:

| The feature… | Build it as | The agent uses it through |
| :--- | :--- | :--- |
| processes input → output (files, media, text) | an **engine node** in `internal/tools/<name>/`, listed in `workflow.New` | `workflow_list_specs` / `workflow_run_node` / `workflow_run_graph` (same path as the UI) |
| is not input → output (settings, projects, navigation…) | a service + realtime action, **plus** one MCP tool in `agent/mcp_tools.go` that calls the same service | that MCP tool |

- A realtime action alone is invisible to the agent. Never put feature logic in the agent package or in an MCP tool; both call the service.
- The node's `engine.Spec` is the agent's only contract. `engine.ValidateSpec` runs at startup (the app refuses to start on an incomplete spec): non-empty `Label`/`Description`; every param labelled; `select` has `Options`/`OptionsFrom` and a valid `Default`; integer `Default` within `Min..Max`; `Destructive` only on toggles and defaulting to `false`; `ParamRoleOutputDir` only on text params; known port types; unique names.
- `Default` values are the UI's defaults; the UI should read them from the spec, not repeat them.
- Node results carry counts, per-file in/out paths, status and actionable errors (the agent may only report what the result says). Nodes honour `ctx`, report with `progress.Report`, write with `fsutil`.
- Nodes call `engine.CheckFile(ctx, path, write)` before reading or writing any local file. Remote clients of a headless server get a guard (`AccessPolicy.CheckTrustedFile`) through the ctx; local callers get none. A path a node writes to (e.g. `output_dir`) is anchored exactly like its inputs before it is checked, never left relative to the process working directory.
- Trust model of `--mode server`: an authenticated WebSocket client is NOT trusted with the server's disk. It may only touch media, workspace and the trusted folders, and cannot change that list (`agent:add_allowed_dir` / `agent:remove_allowed_dir` answer `ERR_UNAVAILABLE`); the owner edits trusted folders from the desktop app.
- A screen that runs one node uses its generated types (`NodeTypes` / `NodeMap`, listed in `contracts.Nodes()`) and reads options, limits and defaults from `workflow:specs` (`useNodeSpec`); it never re-declares them.
- MCP tools:
  - Anything that modifies or deletes the user's data outside trusted dirs, or turns on a `Destructive` param → `s.needsApproval(tool, title, summary, exec)`. Read-only and trusted-dir work runs directly.
  - Every path goes through `s.policy` (`Resolve`, `ValidateRead`, `ValidateWrite`).
  - Return `mcpJSON(...)` / `mcpError(...)`; usage guidance goes in the tool description, never in `profile/policy.md`.
  - Add the feature to `buildCapabilitiesSnapshot` if it should appear in `studio_list_capabilities`.
- Details and examples: `docs/agent-feature-integration.md`.

### Agent package structure
- Providers are drivers (`agent/drivers.go`): a `turnDriver` runs each chat turn as its own CLI invocation (Codex, Claude), a `sessionDriver` keeps one ACP connection (Antigravity). Lifecycle code (probe, login, connect, turns) never branches on a provider ID; a new provider is a `domain/agent` spec in `providerSpecs` plus a driver in `newDrivers`. CLI quirks (resume errors, login args) are spec data.
- `Service` methods are grouped by responsibility: `service.go` (construction, shutdown), `provider_status.go` (probe, executables, sign-in), `connection.go`, `conversation.go`, `turn_run.go`, `access.go`, `approvals.go`, `ui_context.go`. New code goes into the matching file, not `service.go`.
- A chat turn is one `*turn` (`agent/turn.go`). Every way it ends goes through `Service.endTurn` exactly once, which stores the guard-sanitized reply and the error. Runners only stream events through `emitChat`; they never write chat history.
- No package-level mutable state: the MCP tool list, the profile secret store and the driver table belong to the `Service`. Lists derived from the registered MCP tools (capabilities snapshot, ACP permission allow-list, `<tools>` prompt layer) are generated, never written by hand.

## 12. Logging
- `slog` only; use the `...Context(ctx, ...)` variants when a ctx exists.
- Levels: `Debug` per-action/per-step trace; `Info` lifecycle (startup, connect/disconnect, migration, shutdown); `Warn` degraded but handled (fallback taken, probe failed, optional step skipped); `Error` needs attention (internal error, panic, data-loss risk). A user mistake (validation, not signed in) is not an error log.
- Attribute keys (snake_case, reuse these): `error` (never `err`), `request_id`, `action`, `client_id`, `provider`, `id` (feature item), `path` (app-owned paths only), `duration`.
- Never log secrets, tokens, API keys, prompt/chat content or file contents. The logger redacts known secret keys; do not rely on it.
- `ERR_INTERNAL` is logged once, where `realtime` classifies it (with `request_id`); services do not log an error they also return.

## 13. Protocol & contract versioning
- `contracts.ProtocolVersion` (`dezuxk.v1`) changes only for a **breaking** change: a frame shape/semantics change, or removing/renaming an action, event or DTO field.
- Additive changes keep the version: new actions/events, new optional DTO fields (`omitempty` in Go, `.optional()` in Zod, a fallback in the client).
- While a new version rolls out, the hub accepts the previous subprotocol too and the client sends the newest it supports (client/server mode can run mixed versions; desktop ships both halves together).
- Never reuse a removed action/event name for a different meaning. SQLite migrations follow the same rule: append-only.

## 14. Performance & resource limits
- Every limit is a config key (`realtime.max_concurrent_actions` 64, `realtime.max_message_bytes` 1 MiB, `realtime.client_buffer_size` 256, `agent.history_limit` 200, `agent.max_pending_approvals` 20, `agent.context_messages` 10 x `agent.context_message_chars` 1200, `log.max_size_mb` 10 x `log.max_backups` 3) or a named constant with a comment saying why (`maxNodeInputFiles` 10 000 per agent run, MCP request body 4 MiB (`mcpMaxRequestBytes`), Codex JSON-RPC line 16 MiB, CLI stream-json line 4 MiB (`cliStdoutLineMax`)).
- CPU-parallel work uses a bounded worker pool; `0` workers = automatic (CPU count); the user-settable maximum is declared in the spec (watermark: 64).
- Batches must not need memory proportional to the whole batch: stream items to workers (`ProcessStream`) instead of loading every file first.
- Hot paths (sending a prompt, finishing a turn, status pushes) never spawn processes: use cached state (`statusInfo`) and refresh in the background.
- Downloads and file processing stream to disk (`fsutil.WriteAtomic`); never read a whole archive into memory.

## 15. Git workflow
- Never commit to `main`. Branches: `feat/<topic>`, `fix/<topic>`, `refactor/<topic>`, `docs/<topic>`, `ci/<topic>` (kebab-case).
- Commits follow Conventional Commits: `type(scope): summary`, English, imperative, <= 72 chars; types `feat`, `fix`, `refactor`, `docs`, `test`, `ci`, `chore`; scope = package/feature (`agent`, `binaries`, `realtime`...). The body explains why. One logical change per commit.
- Generated files (`frontend/src/contracts/generated.ts`) are committed with the Go change that produced them.
- Merge into `main` only through a PR with: CI green (lint, test, build, race), `task check` run locally, tests for new behaviour, docs/rules updated when a convention changes, and a description of user-visible changes and migrations. Merge commits; no force-push to `main`.
- Releases are tags `vX.Y.Z` on `main` (README §7).
