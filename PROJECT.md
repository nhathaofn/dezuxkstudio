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
- `frontend/src/features/watermark/useWatermarkStore.ts` — Existing Zustand store (untouched)
