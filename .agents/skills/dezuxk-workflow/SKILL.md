---
name: dezuxk-workflow
description: >-
  Build, test, lint and feature-development workflow for DezuxkStudio
  (Go + Wails desktop app with a shared headless core server).
---

# DezuxkStudio workflow

`Taskfile.yml` is the single source of truth for commands (`task --list`).
On Windows without Task: `powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 <task>`.

| Goal | Command |
| :--- | :--- |
| Install frontend deps | `task setup` |
| Desktop dev mode (hot reload) | `task dev` |
| Everything CI runs | `task check` (= `lint` + `test` + `audit`) |
| Go + frontend tests | `task test` (race: `task test:race`, needs cgo) |
| Regenerate TS contracts | `task generate` |
| Desktop build | `task build` (version from git tag or `VERSION=x.y.z`) |
| Windows installer | `task build:installer` |
| Headless server | `task build:server` |
| Format | `task format` |

## Adding a realtime action (end to end)

1. Domain model / rules / repository interface in `internal/domain/<feature>`.
2. Storage: new migration `internal/storage/sqlite/migrations/NNN_<name>.sql` + repository.
3. Service in `internal/service/<feature>` (depends on interfaces only; unit-test with an in-memory fake).
4. Contracts in `internal/contracts`: `Action<Name>` constant, payload/result DTOs (payload may implement `Validate() error`), entry in `Actions()`; events in `Events()`.
5. Module in `internal/realtime/modules/<feature>.go`:
   ```go
   realtime.Handle(r, contracts.ActionProjectsList,
       func(ctx context.Context, req *realtime.Request, p contracts.ProjectsListPayload) (contracts.ProjectList, error) {
           return m.Service.List(ctx, p.Filter)
       })
   ```
6. One line in `modules()` in `internal/core/core.go`.
7. `task generate`, then in the frontend:
   ```ts
   const list = await rtClient.request(Actions.PROJECTS_LIST, { filter: "" }, { signal, onProgress });
   ```
8. UI strings in `frontend/src/lib/i18n/en.ts` + `vi.ts`; route (if any) in `frontend/src/app/navigation.ts`.
9. Tests next to the code; end-to-end cases in `test/e2e_test.go`. Run `task check`.

Startup fails if an action is undeclared, registered twice, or declared without a handler;
CI fails if `generated.ts` is stale.
