# Test Suite Readiness Report (`TEST_READY.md`)

**Date**: 2026-11-01  
**Status**: **ALL TESTS PASSING (100% GREEN)**  
**Milestone**: M-E2E / M4 Automated Verification Track  

---

## 1. Executive Summary

A comprehensive, requirement-driven, opaque-box automated test suite has been designed and implemented across both the Go backend and React/TypeScript frontend.

| Test Track | Test Files | Total Tests | Passed | Failed | Execution Time |
|:---|:---:|:---:|:---:|:---:|:---:|
| **Go Backend E2E Suite** | 2 | 14 | 14 | 0 | ~1.0 s |
| **Go Backend Full Repo** (`./...`) | 38 packages | 38 | 38 | 0 | ~7.5 s |
| **Frontend Vitest Suite** | 5 | 44 | 44 | 0 | ~0.8 s |
| **Frontend Typecheck & Lint** | — | 0 errors | Clean | 0 | ~7.0 s |
| **TOTAL E2E & Unit Tests** | **7 files** | **58 tests** | **58** | **0** | **Clean** |

---

## 2. Test Files Inventory

### Backend (Go)
1. **`test/e2e_refactor_test.go`** (NEW — Exclusive Write Ownership):
   - `TestE2E_Tier1_R2_BackendEnglishStandardization_ToolMetadata`: Zero Vietnamese diacritics in node metadata, labels, ports, parameters.
   - `TestE2E_Tier1_R2_BackendEnglishStandardization_GraphErrors`: Engine-level cycle, duplicate node ID, and unknown node type error English standardization; realtime empty graph error validation.
   - `TestE2E_Tier1_R3_CentralizedBinariesManifest`: Validates `binaries.Manifest`, HTTPS URL scheme, platform specs (`windows_amd64`, `windows_arm64`), and version regex extraction.
   - `TestE2E_Tier1_R4_CoreLifecycleAndEndpoints`: HTTP `/health` check and authenticated WebSocket handshake.
   - `TestE2E_Tier2_BoundaryAndCornerCases`: Empty binary IDs, non-existent tool lookups, version extraction with empty/garbage strings, system path traversal protection.
   - `TestE2E_Tier3_CrossFeature_ErrorContractsAndCodes`: Realtime structured error body formatting across multiple actions.
   - `TestE2E_Tier4_Scenario_EndToEndWorkflow`: Multi-client workflow with language preference switching, manifest listing, custom binary path configuration, and English validation error persistence.
2. **`test/e2e_test.go`** (Existing core tests):
   - 7 end-to-end tests covering action routing, preferences roundtrip, diagnostics progress, open folder, HTTP endpoints, broadcast endpoints, and binary probe/set_path actions.

### Frontend (TypeScript / Vitest)
1. **`frontend/src/lib/i18n/localization_audit.test.ts`** (NEW — Exclusive Write Ownership):
   - 14 tests covering 100% key parity between `en.ts` and `vi.ts`, non-empty translations, placeholder symmetry, zero Vietnamese diacritics in English dictionary, boundary cases (omitted vars, malicious tokens), error code integration (`describeError`), and real-world multi-module language switching.
2. **`frontend/src/features/binaries/useBinariesStore.test.ts`** (NEW — Exclusive Write Ownership):
   - 9 tests covering `fetchBinaries`, `probe`, `setCustomPath`, `download` with progress tracking, network timeout handling, non-existent binary ID probing, custom path reset, and full lifecycle simulation.
3. **`frontend/src/lib/i18n/i18n.test.ts`** (Existing): 3 tests.
4. **`frontend/src/lib/rtClient/rtClient.test.ts`** (Existing): 15 tests.
5. **`frontend/src/features/preferences/usePreferencesStore.test.ts`** (Existing): 3 tests.

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
- [x] `go test -v ./test/...` passes (14/14 tests pass).
- [x] `go test ./...` passes across all 38 Go packages.
- [x] `npm.cmd --prefix frontend test` passes (44/44 tests pass).
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
