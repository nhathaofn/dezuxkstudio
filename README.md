# DezuxkStudio

Base ứng dụng desktop **Go + Wails v2 + React 18** (Windows / macOS / Linux) kèm một **core server headless** dùng chung mã nguồn.

| Thành phần | Entry point | Lệnh build | Kết quả |
| :--- | :--- | :--- | :--- |
| Desktop (all-in-one) | `main.go` | `task build` | `build/bin/DezuxkStudio-<version>[.exe/.app]` |
| Bộ cài Windows (NSIS, cần cài [NSIS](https://nsis.sourceforge.io)) | `main.go` | `task build:installer` | `build/bin/DezuxkStudio-setup-<version>.exe` |
| Core server headless | `cmd/server` | `task build:server` | `bin/DezuxkStudio-server-<version>[.exe]` |

Ba chế độ chạy (`--mode`): `desktop` (GUI + core loopback nhúng), `client` (GUI nối tới core từ xa), `server` (core headless).

---

## 1. Bắt đầu

Yêu cầu: Go (bản theo `toolchain` trong `go.mod`, tự tải), Node 22, [Task](https://taskfile.dev) `v3.53.1`, Wails CLI `v2.17.0`. Linux cần thêm `libgtk-3-dev libwebkit2gtk-4.1-dev`.

```bash
go install github.com/go-task/task/v3/cmd/task@v3.53.1
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
task setup      # npm ci
task dev        # wails dev -skipbindings -m (hot reload, build tag "dev"); nhanh hơn gọi `wails dev` trực tiếp
task check      # lint + test + audit: đúng những gì CI chạy
```

Windows không cài Task: `powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 <task>` (script tự `go run` bản Task đã ghim).

Toàn bộ lệnh: `task --list`.

---

## 2. Kiến trúc

```
main.go / cmd/server            entry points: nạp config → paths → logger → crash handler
  └─ internal/desktop           Wails shell: cửa sổ, Bridge (binding), CSP động, vòng đời
  └─ internal/core              composition root: storage → services → modules → HTTP server
       ├─ internal/realtime     hub WebSocket: auth, registry typed, dispatcher, cancel, progress
       │    └─ modules/         adapter mỏng: payload → service → DTO (không có logic nghiệp vụ)
       ├─ internal/service/*    use case (preferences, system, update, diagnostics)
       ├─ internal/domain/*     model + quy tắc nghiệp vụ + interface repository
       └─ internal/storage/*    triển khai repository (SQLite, migration có version)
internal/contracts              NGUỒN DUY NHẤT của hợp đồng Go ↔ TS (action, event, DTO, bridge)
cmd/contractgen                 sinh frontend/src/contracts/generated.ts từ internal/contracts
frontend/src
  ├─ contracts/generated.ts     (sinh tự động – không sửa tay)
  ├─ lib/rtClient               client WebSocket typed: request/cancel/progress/timeout/reconnect
  ├─ lib/bridge.ts, lib/i18n    binding desktop, đa ngôn ngữ (en, vi)
  ├─ features/<tên>/            store + component của từng tính năng
  └─ app/                       bootstrap, router, layout, màn hình lỗi fatal
```

Hướng phụ thuộc một chiều: `modules → service → domain ← storage`; `contracts` chỉ phụ thuộc `domain`/`apperr`/`config`. Không package nào import ngược lên `core`/`desktop`.

### Luồng khởi động desktop

1. `main.go` nạp config. Nếu config lỗi, cửa sổ vẫn mở và hiện màn hình lỗi fatal với lý do cụ thể.
2. `desktop.Run` mở cửa sổ. `OnStartup` khởi tạo core trong goroutine riêng, không chặn UI.
3. Core sinh **token ngẫu nhiên cho từng phiên**, mở SQLite (backup → migrate), mount module, kiểm tra mọi action khai báo đều có handler, rồi lắng nghe trên `127.0.0.1:<port do OS cấp>`.
4. Frontend gọi `Bridge.GetConnectionInfo()` để lấy URL + token, rồi kết nối WebSocket. Token đi qua subprotocol `auth.<token>`, không bao giờ nằm trong URL.
5. Mọi lỗi khởi động được phát qua event `app:fatal` để frontend hiện màn hình fatal.

### Giao thức realtime (`dezuxk.v1`)

| Chiều | Frame |
| :--- | :--- |
| client → server | `{type:"action", id, action, payload}` · `{type:"cancel", id}` |
| server → client | `{type:"response", id, success, data?, error?:{code,message,details?}}` · `{type:"event", event, ref_id?, data, timestamp}` |

- Mỗi action có timeout (`realtime.action_timeout`). Số action chạy đồng thời bị giới hạn (`realtime.max_concurrent_actions`). Có panic recovery, và lỗi nội bộ không bao giờ lộ chi tiết ra frontend.
- Tác vụ dài gọi `req.Progress(%, msg)` để phát `action:progress` (với `ref_id` = id của action). Client hủy bằng `AbortSignal`; server hủy `ctx` tương ứng. Xem `diagnostics:export` làm mẫu.
- Mã lỗi (`ERR_*`) sinh từ `internal/apperr`. Frontend dịch theo mã (`lib/i18n`).

---

## 3. Cấu hình (zero hardcode)

Mỗi tùy chọn được khai báo **một lần** trong [internal/config/fields.go](internal/config/fields.go). Tên khóa file, biến môi trường và cờ CLI đều được suy ra từ khai báo đó:

| Khóa | `config.json` | Biến môi trường | Cờ CLI |
| :--- | :--- | :--- | :--- |
| `server.port` | `{"server":{"port":0}}` | `DEZUXKSTUDIO_SERVER_PORT` | `--server-port` |

Thứ tự ưu tiên: mặc định → `config.json` → môi trường → cờ CLI. Chạy `--help` để xem toàn bộ tùy chọn, giá trị mặc định và tên biến.

- File config được tìm theo thứ tự: `--config` / `DEZUXKSTUDIO_CONFIG` (bắt buộc phải tồn tại), rồi thư mục config của người dùng, rồi thư mục chứa exe (bản portable).
- Khóa lạ, JSON sai hoặc giá trị sai kiểu đều làm app **dừng kèm lỗi rõ ràng**, không bị bỏ qua lặng lẽ.
- `Validate()` kiểm tra toàn bộ ràng buộc theo từng chế độ, ví dụ: desktop chỉ được bind loopback; server bind ra ngoài phải có token; client phải dùng `ws(s)://`.
- Thư mục lưu trữ theo chuẩn từng OS ([internal/paths](internal/paths/paths.go)): config ở `%APPDATA%`, data và log ở `%LOCALAPPDATA%` (Windows); `~/Library/...` (macOS); XDG (Linux). Không bao giờ ghi vào thư mục cài đặt hay thư mục temp.

---

## 4. Bảo mật

- Desktop: token 256-bit sinh mới mỗi phiên, so sánh constant-time, chỉ bind loopback. Allowed origins mặc định chỉ gồm origin của Wails.
- Server: token lấy từ `server.auth_token_file` hoặc biến môi trường (nếu đặt token trong file config hoặc qua cờ CLI sẽ có cảnh báo).
- Client: token của server từ xa được lưu trong **keychain của OS** (`internal/secrets`, qua `Bridge.SetRemoteToken`).
- `POST /api/events/broadcast` tắt mặc định. Khi bật, bắt buộc có Bearer token, `Content-Type: application/json`, và event phải được khai báo trong contracts.
- CSP được gửi qua HTTP header, `connect-src` tùy theo chế độ ([internal/desktop/csp.go](internal/desktop/csp.go)). Bản production không có `unsafe-inline` cho script.
- Log tự che các khóa nhạy cảm (`token`, `password`, `authorization`…). Gói chẩn đoán chỉ chứa config đã được che.
- `task audit` chạy `govulncheck` và `npm audit`.

---

## 5. Dữ liệu cục bộ

- SQLite (WAL, một connection) đặt sau các interface repository trong `internal/domain`.
- Migration nằm ở `internal/storage/sqlite/migrations/NNN_name.sql`: được nhúng vào binary, áp dụng theo version, mỗi migration trong một transaction. **Không sửa file đã phát hành**; thay đổi schema thì thêm file mới.
- Trước khi migrate một DB đã có dữ liệu, app tự backup bằng `VACUUM INTO` vào `data/backups` (giữ `storage.backups_to_keep` bản). App **từ chối mở** DB có schema mới hơn chính nó (trường hợp người dùng hạ cấp app).
- Ghi file luôn dùng atomic write (`internal/fsutil`).

---

## 6. Log và chẩn đoán

- Log dùng `slog` (dạng `text` hoặc `json`), xoay vòng theo kích thước (`log.max_size_mb`, `log.max_backups`). Panic ở bất kỳ goroutine nào (qua `safego.Go`) đều được ghi crash dump `crash_*.log`.
- Màn hình **Overview → Diagnostics bundle** xuất một file zip gồm log, crash dump, thông tin build và config đã che. Người dùng gửi file này kèm báo lỗi. Có thanh tiến độ và nút hủy.

---

## 7. Build và phát hành

- Version lấy từ git tag `vX.Y.Z`. `task version:sync` chép version sang `wails.json` và `frontend/package.json`. Metadata (version, commit, ngày build, URL kiểm tra cập nhật) được nhúng qua `-ldflags` vào `internal/buildinfo`.
- Build dev và prod tách nhau bằng build tag `dev` (`wails dev`). Tag `dev` nới CSP cho Vite HMR và cho phép origin localhost.
- CI ([ci.yml](.github/workflows/ci.yml)) chạy trên 3 OS: lint, test, race detector, build desktop và server, cộng một job audit dependency.
- Release ([release.yml](.github/workflows/release.yml)) chạy khi có tag `v*`: build, ký mã Windows/macOS (bước ký tự bỏ qua nếu chưa có secret), notarize macOS, tính SHA-256, rồi đăng GitHub Release. Bản build tự trỏ tới feed `releases/latest` của repo; app chỉ **thông báo** có bản mới, không tự cài đè.

Secret tùy chọn để ký mã: `WINDOWS_CERT_PFX_BASE64`, `WINDOWS_CERT_PASSWORD`, `MACOS_CERT_P12_BASE64`, `MACOS_CERT_PASSWORD`, `MACOS_SIGN_IDENTITY`, `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD`.

---

## 8. Thêm một tính năng mới

Ví dụ thêm tính năng `projects` có action `projects:list`:

1. **Domain**: `internal/domain/projects/` chứa model, `Validate()`, lỗi kiểu `apperr` và `Repository` interface.
2. **Storage**: thêm migration `internal/storage/sqlite/migrations/002_projects.sql` và `projects_repo.go` (cài đặt interface).
3. **Service**: `internal/service/projects/` chứa use case. Service chỉ phụ thuộc interface, nên test được bằng repo giả lập trong bộ nhớ.
4. **Contracts**: trong `internal/contracts`, thêm hằng `ActionProjectsList`, DTO payload/result, và dòng tương ứng trong `Actions()`. Nếu có event mới thì thêm vào `Events()`.
5. **Module**: `internal/realtime/modules/projects.go` dùng `realtime.Handle(r, contracts.ActionProjectsList, ...)`.
6. **Wiring**: thêm **một dòng** vào `modules()` trong [internal/core/core.go](internal/core/core.go).
7. **Sinh hợp đồng TS**: `task generate`.
8. **Frontend**: tạo `features/projects/` (store gọi `rtClient.request(Actions.PROJECTS_LIST, {...})`, kiểu dữ liệu lấy hoàn toàn từ file sinh tự động), thêm chuỗi i18n vào `en.ts` và `vi.ts`, và nếu cần màn hình riêng thì thêm 1 dòng vào `app/navigation.ts`.
9. **Test**: unit test cạnh package; nếu cần thì thêm case end-to-end vào `test/e2e_test.go`.

Các rào chắn tự động:

- Handler cho action chưa khai báo, handler bị đăng ký trùng, hoặc action đã khai báo mà thiếu handler đều làm **app không khởi động được**.
- CI báo lỗi nếu `generated.ts` lệch với Go.
- TypeScript báo lỗi nếu một ngôn ngữ thiếu chuỗi dịch.

---

## 9. Ghi chú

- Windows Smart App Control có thể chặn file test `.exe` vừa build trong `%TEMP%`. Taskfile đặt `GOTMPDIR=build/.gotmp` để giảm tình trạng này; nếu vẫn bị chặn, chạy lại lệnh.
- Race detector cần CGO và trình biên dịch C (`task test:race`). CI chạy race trên Linux và macOS.
