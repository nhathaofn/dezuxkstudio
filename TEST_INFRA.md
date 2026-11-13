# Test Infrastructure & Automated Verification Architecture

DezuxkStudio employs a unified, multi-tiered, opaque-box automated test infrastructure that covers both the Headless Go Core and the React 18 / TypeScript Frontend.

---

## 1. Architecture & Framework Overview

### 1.1 Backend Test Infrastructure (Go)
- **Framework**: Go standard `testing` library with table-driven test cases and subtests.
- **E2E Core Harness** (`test/e2e_test.go`, `test/e2e_refactor_test.go`):
  - Spawns an isolated, headless DezuxkStudio core (`core.New`) within a temporary directory (`t.TempDir()`).
  - Generates cryptographic auth tokens (`security.NewToken()`).
  - Binds HTTP endpoints and full-duplex WebSocket transports (`github.com/coder/websocket`).
  - Simulates genuine client connections, sending protocol frames (`contracts.ClientMessage`), testing realtime actions (`contracts.Actions()`), and asserting broadcast events (`contracts.Events()`).
- **Domain & Unit Verification**:
  - Direct opaque-box verification of `internal/domain/binaries` manifest, precompiled regexes, URL format validity, and version extraction.
  - Verification of `internal/engine` DAG validation, cycle detection, duplicate node IDs, and English error formatting.

### 1.2 Frontend Test Infrastructure (TypeScript / Vitest)
- **Framework**: Vitest v5 with JSDOM / Node environment and `@testing-library` / Zustand stores.
- **Audit & Store Harness** (`frontend/src/lib/i18n/`, `frontend/src/features/binaries/`):
  - `localization_audit.test.ts`: Automated dictionary parity auditing, placeholder symmetry analysis, Vietnamese diacritic integrity check on English catalogs, and `describeError` integration.
  - `useBinariesStore.test.ts`: Zustand state store verification for binary actions (`fetchBinaries`, `probe`, `setCustomPath`, `download`), signal abortion, and progress callbacks.
  - Existing suite: `i18n.test.ts`, `rtClient.test.ts`, `connection.test.ts`, `usePreferencesStore.test.ts`, `features/watermark/watermark.test.ts`.

---

## 2. 4-Tier Testing Methodology

The test suite is structured into 4 hierarchical verification tiers derived from `ORIGINAL_REQUEST.md`:

```
┌─────────────────────────────────────────────────────────────┐
│  Tier 4: Real-World Scenarios (End-to-end user workflows)   │
├─────────────────────────────────────────────────────────────┤
│  Tier 3: Cross-Feature Combinations (Pairwise integration)  │
├─────────────────────────────────────────────────────────────┤
│  Tier 2: Boundary & Corner Cases (Stress, malformed inputs) │
├─────────────────────────────────────────────────────────────┤
│  Tier 1: Feature Coverage (R1, R2, R3, R4 primary behavior) │
└─────────────────────────────────────────────────────────────┘
```

### Tier 1 — Feature Coverage
- **R1 (Frontend JSX Localization)**:
  - 100% dictionary key parity between `en.ts` and `vi.ts`.
  - Non-empty, non-whitespace translation values for every key in all supported languages.
  - Symmetrical interpolation placeholder variables (`{variable}`) between locales.
  - Zero Vietnamese diacritics in English source catalog `en.ts` (excluding language endonyms).
- **R2 (Backend Go English Standardization)**:
  - Realtime `workflow:specs` metadata: node labels, descriptions, input ports, output ports, and parameters contain zero Vietnamese diacritics.
  - Realtime error responses and `engine` DAG cycle, duplicate ID, and missing port errors are standardized in English with zero Vietnamese diacritics.
- **R3 (Centralized Binaries Configuration)**:
  - All registered binary tools (FFmpeg, FFprobe, yt-dlp) are present in `binaries.Manifest`.
  - All download URLs are valid HTTPS URLs (`https://...`).
  - Supported platform specs exist for `windows_amd64` and `windows_arm64`.
  - Precompiled version regexes extract canonical version strings accurately.
  - Precompiled progress regexes match time, speed, and percentage output lines.
- **R4 (Automated Verification & Lifecycle)**:
  - HTTP `/health` and WebSocket handshakes operate cleanly without regressions.

### Tier 2 — Boundary & Corner Cases
- Empty binary ID strings in probe or set-path requests rejected with `ERR_VALIDATION`.
- Non-existent binary IDs in manifest lookup safely return `ok == false`.
- Version regex extraction robust against empty strings, non-matching text, and large garbage inputs (zero crashes or panics).
- Uncataloged languages in frontend safely fall back to English.
- Missing interpolation variables preserve literal `{variable}` tokens without throwing errors.
- System directory traversal attempts (`../../../windows/system32`) rejected with `ERR_VALIDATION`.

### Tier 3 — Cross-Feature Combinations
- Backend `apperr.Code` (`ERR_VALIDATION`, `ERR_INTERNAL`, `ERR_NOT_FOUND`) correctly formats across WebSocket frames and flows into frontend `describeError()`.
- Localized error rendering with distinct categories in English and Vietnamese.
- Simulated network failure and abort signal during binary downloads verified in store error states.

### Tier 4 — Real-World Application Scenarios
- End-to-end multi-client workflow:
  1. Client A updates language preference to `vi`.
  2. Client B receives real-time broadcast event `preferences:updated`.
  3. Client A queries binary manifest list via `binaries:list`.
  4. Client A sets custom path for FFmpeg in a temporary directory.
  5. Client A triggers workflow execution in Vietnamese UI state; backend returns English validation error, confirming language decoupling across contract boundaries.

---

## 3. Feature Inventory Coverage Mapping

| Feature | Description | Primary Test Location | Tier |
|:---|:---|:---|:---:|
| **F1** | Centralized Binary Manifest Constants | `test/e2e_refactor_test.go:TestE2E_Tier1_R3_CentralizedBinariesManifest` | Tier 1 |
| **F2** | Centralized Binary Manifest Registry | `test/e2e_refactor_test.go:TestE2E_Tier1_R3_CentralizedBinariesManifest` | Tier 1 |
| **F3** | Refactored Binary Resolution & Downloader | `frontend/src/features/binaries/useBinariesStore.test.ts` | Tier 1, 4 |
| **F4** | Binaries Automated Verification Tests | `test/e2e_refactor_test.go`, `useBinariesStore.test.ts` | Tier 1, 2 |
| **F5** | Engine Core Error String Standardization | `test/e2e_refactor_test.go:TestE2E_Tier1_R2_BackendEnglishStandardization_GraphErrors` | Tier 1 |
| **F6** | Engine Unit Test Synchronization | `internal/engine/graph_test.go:TestCycleDetection` | Tier 1 |
| **F7** | Tool Watermark Remover String Standardization | `test/e2e_refactor_test.go:TestE2E_Tier1_R2_BackendEnglishStandardization_ToolMetadata` | Tier 1 |
| **F8** | Database Migration Comments Standardization | `internal/storage/sqlite/sqlite_test.go` | Tier 1 |
| **F9** | UTF-8 Test Fixture Preservation | `internal/security/dpapi_windows_test.go` (provider storage transferred to Gateway `dezuxk`) | Tier 1 |
| **F10**| i18n Catalog Dictionary Expansion | `frontend/src/lib/i18n/localization_audit.test.ts` | Tier 1 |
| **F11**| WatermarkView JSX Localization | `frontend/src/lib/i18n/localization_audit.test.ts` | Tier 1, 4 |
| **F12**| SettingsView & BinariesView Localization | `frontend/src/lib/i18n/localization_audit.test.ts` | Tier 1, 3 |
| **F13**| Uptime & System Card Formatting | `frontend/src/lib/i18n/localization_audit.test.ts` | Tier 1, 2 |
| **F14**| Opaque-box E2E Verification Suite | `test/e2e_refactor_test.go`, `localization_audit.test.ts` | Tiers 1–4 |

---

## 4. Test Execution & Runner Invocation

### 4.1 Running Go Backend Tests
```powershell
# Run E2E test suite (including refactoring verification)
go test -v ./test/...

# Run all backend unit and integration tests
go test ./...
```

### 4.2 Running Frontend Vitest Tests
```powershell
# Run all frontend tests
npm.cmd --prefix frontend test

# Run frontend TypeScript type checking
npm.cmd --prefix frontend run typecheck

# Run frontend ESLint linting
npm.cmd --prefix frontend run lint
```

### 4.3 Unified Project Check
```powershell
# Run full suite via project management script
powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 test
```
