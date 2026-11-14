# Test Suite Readiness Report (`TEST_READY.md`)

**Date**: 2026-11-14 (synced across Phases 1–5 on `refactor/phase-5-db-cleanup-docs`; final post-merge Go counts refreshed by `@eng-lead` after Wave 5A `T5.1` lands)  
**Status**: **PASSING (GREEN)**  
**Milestone**: Phases 1–5 Hardening, Boundary Decoupling & Cleanup

---

## 1. Executive Summary

| Test Track | Scope | Passed | Failed | Skipped |
|:---|:---|:---:|:---:|:---:|
| **Go — full repo** (`task test` / `go test ./...`) | 50 packages (49 under `internal/`, `pkg/`, `test/`, `cmd/` + root `app` package; exact post-`T5.1` count refreshed after merge) | — | 0 | Live CLI tests skip unless opt-in env vars / signed-in profiles are present |
| **Go — cross-package suite** (`go test -count=1 ./test/...`) | 7 `*_test.go` files, 31 top-level tests | 31 | 0 | 0 |
| **Frontend Vitest** (`npm.cmd --prefix frontend run test:run`) | 27 test files | 227 | 0 | 0 |
| **Frontend typecheck, lint & build** | `tsc --noEmit`, `tsc -p tsconfig.node.json`, `eslint`, `check-styles.mjs`, `prettier --check`, `vite build` | clean | 0 | — |

Numbers change with every feature; regenerate them with the commands in §4 rather than editing by hand.

---

## 2. Test Files Inventory

Test files are named after the domain/behaviour they cover (never after the process that produced them).

### Backend (Go) — `test/`
- `e2e_test.go`, `e2e_refactor_test.go` — headless core end to end: routing, preferences, diagnostics, HTTP/WS endpoints, binaries, English error contracts.
- `server_authz_test.go` — `--mode server` authorization boundary: `server.allow_host_admin` default `false` (locks `system:open_folder`, `binaries:set_path`, `agent:set_path`, and all 6 skill/subagent mutators), `AllowHostAdmin=true` unlocks host-admin/customizations while keeping `agent:add_allowed_dir` / `agent:remove_allowed_dir` locked and `providers:*` accessible.
- `resilience_migrations_binaries_watermark_test.go` — migration resilience, binary SHA-256 tampering, watermark MIME/transcode integrity.
- `edgecases_manifest_engine_secrets_download_test.go` — binaries manifest regexes, engine cycle detection and errors, DPAPI multi-byte data, crash-safe downloads.
- `edgecases_sqlite_preferences_test.go` — clean database migration, preferences persistence.
- `migration_version_test.go` — `latestMigrationVersion` helper dynamically tracking `NNN_*.sql` migrations.

Package tests live next to the code (`internal/**/_test.go`, `pkg/**/_test.go`). Notable:
- `internal/service/system/service_test.go` — `New`, `Info()` (`Mode`, `Build`, `Runtime`, `Paths.Diagnostics`), and `OpenFolder` (all 5 `FolderTarget`s, invalid target validation, opener errors).
- `internal/tools/watermark/node_guard_test.go`, `node_collision_test.go` — file guard for remote clients, streaming order, per-image progress, and collision-safe output paths.
- `internal/service/agent/access_test.go`, `code_guard_test.go`, `hardening_test.go`, `limits_test.go` — access policy incl. `CheckTrustedFile`, `code_guard`, prompt rules (`TestPolicyIsRulesOnly`), and subagent/concurrency limits.
- `internal/docparse/docparse_test.go` — bounded plain-text extraction for `.docx`, `.odt`, `.rtf`, `.pdf`.
- `internal/httpclient/httpclient_test.go` — link-local/unspecified/multicast SSRF dial blocking and `WithRedirectGuard` hop validation/cap.
- `internal/engine/media_input_test.go`, `media_path_test.go`, `spec_validate_test.go` — shared `ImageInputRef`, `ResolveMediaPath`, and startup `ValidateSpec` invariants.
- `internal/server/handlers/media_test.go` — media upload body cap (`server.max_media_upload_bytes`) and expired staging pruning (`server.media_staging_ttl`).
- `internal/realtime/hub_test.go` — auth, protocol version negotiation (`SupportedProtocolVersions`).
- `internal/realtime/modules/agent_test.go`, `binaries_test.go`, `system_test.go` — trusted folders, host-admin, and customization locks in server mode.
- `internal/storage/sqlite/migrations_test.go`, `sqlite_test.go`, `agent_redact_test.go` — versioned schema upgrades (incl. `014_agent_foreign_keys.sql` `ON DELETE CASCADE` & pre-014 orphan rescue) and secret scrubbing.

### Frontend (TypeScript / Vitest)
- `lib/i18n/` — `i18n.test.ts`, `localization_audit.test.ts`, `serverMessages.test.ts`, `i18n_robustness.test.ts`.
- `lib/rtClient/` — `rtClient.test.ts`, `connection.test.ts` (half-open detection), `media.test.ts`; `lib/bridge.test.ts`; `lib/hooks/flash.test.ts`.
- `features/agent/` — `useAgentStore.test.ts`, `useAgentDrawerStore.test.ts`, `useAgentCustomizationsStore.test.ts`, `AgentDrawer.folders.test.ts`, `markdown.test.ts`.
- `features/mediagen/` — `mediagen.test.ts`.
- `features/providers/` — `health.test.ts`, `useProvidersStore.test.ts`, `layout.test.ts`, `customModels.test.ts`, `credentialPayload.test.ts`, `modelPresentation.test.ts`, `modelGroups.test.ts`, `settings.test.ts`.
- `features/binaries/` — `useBinariesStore.test.ts`; `features/preferences/` — `usePreferencesStore.test.ts`; `features/watermark/` — `watermark.test.ts`; `features/workflow/` — `useNodeSpecs.test.ts`.

---

## 3. Requirement Verification Checklist

### R1. Frontend JSX Localization
- [x] **Dictionary Parity**: `en.ts` and `vi.ts` have 100% key parity (zero missing keys in either locale).
- [x] **Placeholder Symmetry**: All `{variable}` interpolation tokens match exactly between English and Vietnamese.
- [x] **Diacritic Cleanliness**: English source catalog contains zero Vietnamese diacritics (excluding language endonym `"Tiếng Việt"`).
- [x] **Error Code Localization**: All backend error codes (`apperr.Code`) and frontend `ClientErrorCodes` map to localized strings in both locales.
- [x] **Fallback Protection**: Uncataloged language codes safely fall back to the English dictionary.

### R2. Backend Go English Standardization
- [x] **Tool Metadata Cleanliness**: Tool description, input port labels, output port labels, and parameter labels in `internal/tools/watermark/node.go` (with shared node DTOs in `internal/contracts/nodes_dto.go`) return clean English with zero Vietnamese diacritics.
- [x] **Graph Error Cleanliness**: Engine cycle detection, duplicate node IDs, unknown node types, and input port validations return clean English error messages.
- [x] **Contract Independence**: Switching client display language to Vietnamese does not regress backend error messages into Vietnamese, preserving contract stability.
- [x] **UTF-8 Test Fixture Safety**: Multi-byte Unicode test fixtures in `dpapi_windows_test.go` are preserved and continue to pass (provider storage tests transferred to standalone Gateway `dezuxk`).

### R3. Centralized Binaries Configuration
- [x] **Centralized Manifest**: Centralized manifest in `internal/domain/binaries/manifest.go` houses constants, download specs, and precompiled regexes.
- [x] **HTTPS Protocol Enforced**: All binary download URLs use secure `https://` endpoints.
- [x] **Target Platform Architecture**: Windows AMD64 and ARM64 platform specs are fully defined.
- [x] **Regex Extraction**: Precompiled version regexes extract canonical version strings (`7.1-essentials_build`, `2024.10.22`) accurately.
- [x] **Store Integration**: Frontend Zustand `useBinariesStore` actions (`fetchBinaries`, `probe`, `setCustomPath`, `download`) properly interface with the manifest actions.

### R4. Automated Verification & Project Health
- [x] `go test -count=1 ./test/...` passes (`31/31` top-level tests).
- [x] `npm.cmd --prefix frontend run test:run` passes (`27` files, `227/227` tests).
- [x] `npm.cmd --prefix frontend run typecheck` passes with `0` errors.
- [x] `npm.cmd --prefix frontend run lint` and `format:check` pass with `0` warnings/errors.
- [x] Full `task check` (Go vet/gofmt/tidy/contractgen/full 50-package `go test ./...`/audit) & `task test:race` refreshed on integration branch after Wave 5A merge.

---

## 4. Test Runner Invocations & Verification Commands

```powershell
# On Windows, set GOTMPDIR inside the repo so Smart App Control does not block temp test binaries:
$env:GOTMPDIR = "$PWD\build\.gotmp"

# 1. Full CI check (lint + test + audit)
task check

# 2. Backend E2E & Server Authz Tests
go test -v -count=1 ./test/...

# 3. Race Detector (requires CGO / gcc)
task test:race

# 4. Frontend Vitest, Typecheck, Lint & Build
npm.cmd --prefix frontend run test:run
npm.cmd --prefix frontend run typecheck
npm.cmd --prefix frontend run lint
npm.cmd --prefix frontend run build
```
