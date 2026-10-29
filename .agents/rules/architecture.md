---
description: "Core architectural boundaries, zero hardcode rules, and code conventions for DezuxkStudio"
globs: ["**/*"]
always_on: true
---

# DezuxkStudio Architecture & Coding Rules

## 1. Directory Structure Standards
- `cmd/server/`: Entry point cho headless standalone server binary (`dezuxk-server.exe`).
- `internal/`: Toàn bộ logic ứng dụng nội bộ (config, logger, netutil, realtime, server).
- `test/`: **BẮT BUỘC** chứa toàn bộ các file kiểm thử (`*_test.go`). Tuyệt đối KHÔNG đặt file test lẫn lộn trong `internal/` hay `cmd/`.
- `frontend/`: Ứng dụng Thin Client React 18+ (Zustand, Zod, Lucide-React, TanStack Virtual).
- `build/`: NSIS installer, app manifests, icons.

## 2. Zero Hardcode Principles
- **Mạng (Network):** Không bao giờ hardcode IP hoặc Port (`8080`, `3000`, `5173`). Port mặc định là `0` để HĐH tự phân bổ.
- **WebSocket URL:** Luôn giải quyết động qua 5 tầng: Query Param -> Wails Native IPC -> LocalStorage -> `/runtime-config.json` -> `window.location`.
- **Cấu hình (Config):** Luôn tuân theo 4 cấp bậc ưu tiên: Defaults -> Config File (`config.json`) -> Environment Variables -> CLI Flags.

## 3. Realtime Hub & Concurrency Safety
- WebSocket Client sử dụng `sync.RWMutex` và `sync.Once` bảo vệ kênh `sendChan`. Không bao giờ ghi vào channel đã đóng.
- Action Dispatcher giới hạn tối đa 64 goroutines đồng thời bằng semaphore channel. Mọi action bắt buộc có panic recovery (`defer func() { recover() }`).
- Endpoint nhận sự kiện ngoại vi `POST /api/events/broadcast` bắt buộc giới hạn body bằng `http.MaxBytesReader(w, r.Body, 1024*1024)`.

## 4. Frontend Conventions
- **Biểu tượng (Icons):** Bắt buộc dùng `lucide-react`. Không viết SVG inline tĩnh.
- **Quản lý State:** Sử dụng Zustand với atomic selectors (ví dụ: `useRealtimeStore(s => s.status)`).
- **Validation:** Bắt buộc dùng Zod schema để validate toàn bộ payload nhận từ WebSocket.
- **Formatting & Linting:** Tuân thủ `eslint.config.js` và `.prettierrc`.
