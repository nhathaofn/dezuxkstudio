# Test Suite Readiness Report (`TEST_READY.md`)

**Date**: 2026-11-13 (counts refreshed on branch `refactor/agent-turn-lifecycle`)  
**Status**: **ALL TESTS PASSING (100% GREEN)**  
**Milestone**: M6 Hardening (see `PROJECT.md`)  

---

## 1. Executive Summary

| Test Track | Scope | Passed | Failed | Skipped |
|:---|:---|:---:|:---:|:---:|
| **Go — full repo** (`go test -count=1 ./...`) | 39 packages, 452 top-level tests | 452 | 0 | 8 (live CLI tests, need a signed-in agent) |
| **Go — cross-package suite** (`./test`) | 6 files, 28 top-level tests | 28 | 0 | 0 |
| **Frontend Vitest** (`npm --prefix frontend test`) | 23 files | 205 | 0 | 0 |
| **Frontend typecheck & lint** | `tsc --noEmit`, ESLint | clean | 0 | — |

Numbers change with every feature; regenerate them with the commands in §4 rather than editing by hand.

---

## 2. Test Files Inventory

Test files are named after the domain/behaviour they cover (never after the process that produced them).

### Backend (Go) — `test/`
- `e2e_test.go`, `e2e_refactor_test.go` — headless core end to end: routing, preferences, diagnostics, HTTP/WS endpoints, binaries, English error contracts.
- `resilience_migrations_binaries_watermark_test.go` — migration resilience, binary SHA-256 tampering, watermark MIME/transcode integrity.
- `edgecases_manifest_engine_secrets_download_test.go` — binaries manifest regexes, engine cycle detection and errors, DPAPI multi-byte data, crash-safe downloads.
- `edgecases_sqlite_preferences_test.go` — clean database migration, preferences persistence.
- `migration_version_test.go` — migration numbering.

Package tests live next to the code (`internal/**/_test.go`, `pkg/**/_test.go`). Notable:
- `internal/tools/watermark/node_guard_test.go` — file guard for remote clients, streaming order and per-image progress.
- `internal/service/agent/access_test.go` — access policy incl. `CheckTrustedFile`.
- `internal/realtime/hub_test.go` — auth, protocol version negotiation (`SupportedProtocolVersions`).
- `internal/realtime/modules/agent_test.go` — trusted folders locked in server mode.

### Frontend (TypeScript / Vitest)
- `lib/i18n/` — `i18n.test.ts`, `localization_audit.test.ts`, `serverMessages.test.ts`, `i18n_robustness.test.ts`.
- `lib/rtClient/` — `rtClient.test.ts`, `connection.test.ts` (half-open detection).
- `features/watermark/watermark.test.ts` — rendering, spec-driven settings, live progress, store payloads.
- `features/workflow/useNodeSpecs.test.ts` — spec loading (reload on connect, latest load wins).
- `features/agent/AgentDrawer.folders.test.ts` — folder add/remove only on desktop with a local core.
- `features/agent/`, `features/binaries/`, `features/preferences/`, `features/providers/` — store and UI tests.

---

## 3. Requirement Verification Checklist

### R1. Frontend JSX Localization
- [x] **Dictionary Parity**: `en.ts` and `vi.ts` have 100% key parity (zero missing keys in either locale).
- [x] **Placeholder Symmetry**: All `{variable}` interpolation tokens match exactly between English and Vietnamese.
- [x] **Diacritic Cleanliness**: English source catalog contains zero Vietnamese diacritics (excluding language endonym `"Tiếng Việt"`).
- [x] **Error Code Localization**: All backend error codes (`apperr.Code`) and frontend `ClientErrorCodes` map to localized strings in both locales.
- [x] **Fallback Protection**: Uncataloged language codes safely fall back to the English dictionary.

### R2. Backend Go English Standardization
- [x] **Tool Metadata Cleanliness**: Tool description, input port labels, output port labels, and parameter labels in `internal/tools/watermark/node.go` return clean English with zero Vietnamese diacritics.
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
- [x] `go test -v ./test/...` passes (28/28 top-level tests).
- [x] `go test ./...` passes across all 39 Go packages.
- [x] `npm.cmd --prefix frontend test` passes (205/205 tests).
- [x] `npm.cmd --prefix frontend run typecheck` passes with 0 errors.
- [x] `npm.cmd --prefix frontend run lint` passes with 0 warnings/errors.

---

## 4. Test Runner Invocations & Verification Commands

```powershell
# 1. Backend E2E Tests
go test -v ./test/...

# 2. Frontend Vitest Suite
npm.cmd --prefix frontend test

# 3. TypeScript Typecheck
npm.cmd --prefix frontend run typecheck

# 4. ESLint Check
npm.cmd --prefix frontend run lint

# 5. Full Project Check
go test ./...
```
