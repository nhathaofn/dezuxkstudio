# DezuxkStudio

A desktop application base built with **Go + Wails v2 + React 19** (currently **Windows-only**; Linux/macOS unsupported) paired with a shared-codebase **headless core server**.

| Component | Entry Point | Build Command | Output |
| :--- | :--- | :--- | :--- |
| **Desktop Application** | `main.go` | `task build` | `build/bin/DezuxkStudio-<version>.exe` |
| **Windows Installer** (NSIS) | `main.go` | `task build:installer` | `build/bin/DezuxkStudio-setup-<version>.exe` |
| **Headless Core Server** | `cmd/server` | `task build:server` | `bin/DezuxkStudio-server-<version>.exe` |

Supported execution modes (`--mode`):
- `desktop` (GUI + embedded loopback core)
- `client` (GUI connecting to a remote core server)
- `server` (Headless core server)

---

## 1. Quick Start

**Prerequisites**: Go (toolchain managed in `go.mod`), Node.js 22, [Task](https://taskfile.dev) `v3.53.1`, Wails CLI `v2.17.0`.

```bash
go install github.com/go-task/task/v3/cmd/task@v3.53.1
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
task setup      # Installs frontend dependencies (npm ci)
task dev        # Launches Wails dev mode with hot reload
task check      # Runs lint + tests + dependency audits (CI parity)
```

On Windows without global Task:
```powershell
powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 <task>
```

View all available targets: `task --list`.

---

## 2. Architecture Overview

For the detailed, authoritative blueprint including Mermaid diagrams, lifecycle graphs, and extension recipes, read **[docs/architecture.md](docs/architecture.md)**.

```
main.go / cmd/server            Entry points: load config → paths → logger → crash handler
  └─ internal/desktop           Wails shell: window lifecycle, Bridge bindings, dynamic CSP
  └─ internal/core              Composition root: storage → services → modules → HTTP server
       ├─ internal/realtime     WebSocket hub: auth, typed registry, dispatcher, cancellation
       │    └─ modules/         Thin adapters: payload → service → DTO (zero business logic)
       ├─ internal/service/*    Use cases (preferences, system, update, diagnostics, binaries,
       │                        provider, mediagen, workflow, agent, agentcli)
       ├─ internal/domain/*     Models, validation invariants, and repository interfaces
       ├─ internal/engine       Node/DAG graph runtime, MediaRef/ResolveMediaPath/FileGuard
       │    └─ internal/tools/* Implementation nodes (watermark, mediagen, provider)
       └─ internal/storage/*    Repository implementations (SQLite, versioned migrations)

Shared Foundation Packages:
  internal/progress             Streaming progress & ctx heartbeat, idle timeouts
  internal/proc                 Process spawning & termination (tree kill, hidden console)
  internal/runner               CLI invocation, streaming output parser
  internal/lookup               Executable resolution precedence
  internal/httpclient           Shared outbound HTTP client (proxy, pool, SSRF guard, WithRedirectGuard)
  internal/docparse             Bounded plain-text extraction (.docx, .odt, .rtf, .pdf)
  internal/fsutil, safego, paths, apperr, secrets, logger

Contracts & Protocol:
  internal/contracts            SINGLE SOURCE OF TRUTH for Go ↔ TS boundary (actions, DTOs, bridge)
  cmd/contractgen               Generates frontend/src/contracts/generated.ts from contracts
frontend/src
  ├─ contracts/generated.ts     (Auto-generated – DO NOT edit manually)
  ├─ lib/rtClient               Typed WebSocket client: request/cancel/progress/reconnect
  ├─ lib/bridge.ts, lib/i18n    Desktop bindings & 100% parity internationalization (en, vi)
  ├─ features/<name>/           Stores & views (agent state split into useAgentStore, useAgentDrawerStore, useAgentCustomizationsStore)
  └─ app/                       Bootstrap, routing, layout, and fatal error displays
```

**One-Way Dependency Rule**: `modules → service → domain ← storage`. `internal/contracts` depends only on domain/engine primitives and never imports implementation packages (`service`, `tools`).

---

## 3. Configuration & Zero-Hardcode Guiding Rules

Every configurable parameter is declared **exactly once** in `internal/config/fields.go`. Environment variables and CLI flags are automatically derived:

| Key | `config.json` | Environment Variable | CLI Flag |
| :--- | :--- | :--- | :--- |
| `server.port` | `{"server":{"port":0}}` | `DEZUXKSTUDIO_SERVER_PORT` | `--server-port` |
| `server.allow_host_admin` | `{"server":{"allow_host_admin":false}}` | `DEZUXKSTUDIO_SERVER_ALLOW_HOST_ADMIN` | `--server-allow-host-admin` |
| `server.max_media_upload_bytes` | `{"server":{"max_media_upload_bytes":67108864}}` | `DEZUXKSTUDIO_SERVER_MAX_MEDIA_UPLOAD_BYTES` | `--server-max-media-upload-bytes` |
| `agent.max_concurrent_subagents` | `{"agent":{"max_concurrent_subagents":4}}` | `DEZUXKSTUDIO_AGENT_MAX_CONCURRENT_SUBAGENTS` | `--agent-max-concurrent-subagents` |

- **Resolution Precedence**: Default values $\rightarrow$ `config.json` $\rightarrow$ Environment variables $\rightarrow$ CLI flags.
- Default values reside **strictly** in `config.Default()`; services read limits through typed config adapters (`agent.LimitsFrom(cfg)`).
- OS directory standards ([internal/paths](internal/paths/paths.go)): Configuration in `%APPDATA%`, Data & Logs in `%LOCALAPPDATA%` (Windows). Never write into installation or system temp roots.

---

## 4. Security & Server Isolation Boundaries

- **Desktop Mode**: Ephemeral 256-bit token generated per session, compared in constant-time, bound exclusively to loopback (`127.0.0.1`).
- **Server Mode (`--mode server`)**:
  - **Host Administration Guard**: `server.allow_host_admin` defaults to `false`. Remote clients are blocked with `ERR_UNAVAILABLE` from mutating host executable paths (`binaries:set_path`, `agent:set_path`), triggering OS folder openers (`system:open_folder`), or editing skills/subagents (`agent:save_skill`, `agent:delete_skill`).
  - **Trusted Folders**: `agent:add_allowed_dir` and `agent:remove_allowed_dir` are permanently disabled in server mode. All node file operations are confined within validated roots via `engine.CheckFile`.
  - **AI Generation**: Authorized remote clients with bearer tokens retain full access to AI model providers (`providers:*`) and prompt execution; credentials stay securely hosted on the server.
- **Outbound HTTP Security**: Link-local, private, and multicast addresses are rejected to protect against SSRF; `WithRedirectGuard` restricts redirect chains to a maximum of 10 hops.

---

## 5. Local Data Storage & Migrations

- **SQLite Engine**: Running in WAL mode, single-connection, with `PRAGMA foreign_keys = ON` enforced.
- **Append-Only Migrations**: Located in `internal/storage/sqlite/migrations/NNN_name.sql`, applied in discrete transactions. Migration `014_agent_foreign_keys.sql` guarantees `FOREIGN KEY ... ON DELETE CASCADE` on conversation messages and sessions, rescuing legacy orphan rows before rebuilding tables.
- **Automated Backups**: Backups are created via `VACUUM INTO` prior to executing migrations on existing databases.
- **Atomic File Operations**: All file modifications stream to disk atomically using `internal/fsutil`.

---

## 6. Extending Features

Refer to these dedicated guides when extending the codebase:
- **[docs/architecture.md](docs/architecture.md)** – System blueprints, execution sequence diagrams, prompt assembly tiers, and developer recipes.
- **[docs/agent-feature-integration.md](docs/agent-feature-integration.md)** – How to create `engine.Spec` nodes so the Assistant can use them immediately without agent-specific code.
- **[docs/provider-integration.md](docs/provider-integration.md)** – Connecting LLM, Image, Video, and TTS API providers and configuring model defaults.
- **[docs/agent-cli.md](docs/agent-cli.md)** – Management, isolation, and authentication for external agent CLIs (Codex, Claude, Antigravity).
- **[docs/feature-prompt-template.md](docs/feature-prompt-template.md)** – Prompt templates for directing AI assistants to implement new features cleanly.

---

## 7. Verification & CI Commands

Always verify changes using the standardized test runner:

```powershell
# Set local temporary build directory on Windows (satisfies Smart App Control):
$env:GOTMPDIR = "$PWD\build\.gotmp"

# 1. Full CI suite (Lint + Test + Audit):
task check

# 2. Race detector (Requires GCC / MinGW-w64 on Windows):
task test:race

# 3. Frontend checks:
npm.cmd --prefix frontend run typecheck
npm.cmd --prefix frontend run lint
npm.cmd --prefix frontend run format:check
npm.cmd --prefix frontend run test:run
```
