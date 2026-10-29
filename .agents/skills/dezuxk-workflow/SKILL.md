---
name: dezuxk-workflow
description: >-
  Workflows, build procedures, test execution, linting, and development commands
  for DezuxkStudio desktop and headless server architecture.
---

# DezuxkStudio Development & Operations Skill

Use this skill whenever you need to build, test, lint, format, or run DezuxkStudio components.

## Build Commands

Always use `Taskfile.yml` as the single source of truth for build operations:

- **Desktop Dev Mode (HMR + Go Engine):**
  ```powershell
  task dev
  # hoặc: wails dev
  ```

- **Compile Frontend:**
  ```powershell
  task build:frontend
  # tương đương: cd frontend; npm run build
  ```

- **Compile Headless Core Server:**
  ```powershell
  task build:server
  # binary xuất ra tại: bin/dezuxk-server.exe
  ```

- **Production Desktop App Build:**
  ```powershell
  task build
  # binary xuất ra tại: build/bin/DezuxkStudio-<version>.exe
  ```

## Quality Assurance & Testing

- **Run Go Unit Tests (với Race Detector):**
  ```powershell
  task test
  # Chạy toàn bộ test trong thư mục cách ly: go test -v -race ./test/...
  ```

- **Linting:**
  ```powershell
  task lint
  # Kiểm tra code quality: cd frontend && npm run lint; go vet ./...
  ```

- **Formatting:**
  ```powershell
  task format
  # Định dạng chuẩn: cd frontend && npm run format; go fmt ./...
  ```

- **Clean Artifacts:**
  ```powershell
  task clean
  ```

## Creating New Realtime Actions

Khi thêm một API/Action realtime mới giữa React Frontend và Go Server:

1. **Go Hub Registration (`internal/realtime/`):**
   Đăng ký action handler trong Action Registry hoặc dispatcher:
   ```go
   registry.Register("domain:action_name", func(ctx context.Context, client *Client, payload json.RawMessage) (interface{}, error) {
       // logic xử lý non-blocking
       return result, nil
   })
   ```

2. **Frontend Schemas & Dispatch (`frontend/src/`):**
   - Khai báo kiểu request/response trong `frontend/src/schemas/`.
   - Dispatch bằng:
     ```ts
     const res = await useRealtimeStore.getState().sendAction("domain:action_name", payload);
     ```

## Adding Unit Tests

- Luôn tạo file test mới bên trong thư mục [test/](file:///d:/gehihi/test) (ví dụ: `test/my_feature_test.go`).
- Package name bắt buộc là `package test`.
- Import các package từ `app/internal/...` hoặc `app/...`.
