---
description: "Architecture boundaries, zero-hardcode rules and conventions for DezuxkStudio"
globs: "**/*"
always_on: true
---

# DezuxkStudio – Architecture & Coding Rules

## 1. Layers & dependency direction
- `internal/domain/<feature>`: models, invariants (`Validate`), `Repository` interfaces, typed errors (`apperr`). No I/O, no transport types.
- `internal/service/<feature>`: use cases. Depend only on domain interfaces and injected funcs (no `realtime`, no `sqlite`, no `wails`).
- `internal/storage/sqlite`: repository implementations + migrations `migrations/NNN_name.sql` (append-only, never edit a released file).
- `internal/contracts`: the ONLY definition of what crosses Go ↔ TS (action/event names, DTOs, enums, bridge methods). Regenerate TS with `task generate`.
- `internal/realtime/modules`: thin adapters `realtime.Handle(r, contracts.ActionX, fn)`; decode → one service call → DTO. No business logic.
- `internal/core`: the composition root. A new feature = one entry in `modules()`.
- `internal/desktop`: Wails shell only. `Bridge` exported methods must equal `contracts.Bridge()` (enforced by test).
- Direction: `modules → service → domain ← storage`. Nothing imports `core` or `desktop` except entry points.

## 2. Naming
- Actions: `<domain>:<verb>[_<noun>]` (`preferences:update`). Events: `<domain>:<past_tense>` (`preferences:updated`). Native (Wails) events: `app:<state>`.
- Go constants `ActionXxx` / `EventXxx` in `internal/contracts`; TS uses the generated `Actions.XXX` / `Events.XXX`. String literals for action/event names are forbidden on both sides.

## 3. Errors
- Return `*apperr.Error` (or wrap with `%w`) for user-meaningful failures; codes are the `apperr.Code` constants.
- Never put secrets, paths of user files, SQL or raw causes into `Message`; unknown errors become `ERR_INTERNAL` automatically and are logged with the request id.
- Frontend branches only on `ActionError.code` and translates via `errors.<CODE>` i18n keys.

## 4. Configuration (zero hardcode)
- Every tunable lives in `config.Config` and is declared once in `internal/config/fields.go` (file key, env var and flag are derived).
- Validate all invariants in `Config.Validate()`; never silently fall back.
- Product identity and build metadata only in `internal/buildinfo` (ldflags). Frontend client-only knobs only in `frontend/src/config/appConfig.ts`.
- Paths only via `internal/paths`. Never write next to the executable or into temp dirs.

## 5. Concurrency & lifecycle
- Start goroutines with `safego.Go` (panic → log + crash dump). Every blocking call takes a `context.Context`.
- Long-running actions call `req.Progress(...)` and return promptly when `ctx` is cancelled.
- Shutdown order: hub (drain sessions + actions) → HTTP server → database.

## 6. Security
- Desktop core: loopback only, per-session random token (subprotocol `auth.<token>`), constant-time compare.
- Secrets: OS credential store via `internal/secrets`; never in config files, DB or logs.
- Webview CSP is sent from `internal/desktop/csp.go`; no inline scripts in production.

## 7. Frontend
- Feature folders `src/features/<name>/` (store + components). Shared UI in `src/components/ui`.
- Zustand with atomic selectors; subscriptions are created once in `app/bootstrap.ts` or in effects that return their unsubscribe.
- All server frames are validated with Zod (`lib/rtClient/protocol.ts`); static types come from `contracts/generated.ts`.
- All UI strings go through `useT()`; add keys to `en.ts` and every other locale.
- Routes: add to `app/navigation.ts` (HashRouter). Icons: `lucide-react`.

## 8. Tests
- Unit tests live next to the package (`*_test.go`, white-box allowed). `test/` holds end-to-end tests that run the real core.
- Frontend unit tests: `src/**/*.test.ts` (Vitest).
- `task check` must pass before committing.
