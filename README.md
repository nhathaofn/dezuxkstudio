# DezuxkStudio

Kiến trúc Desktop Siêu Tinh Gọn (Zero Python, Pure Native): **DezuxkStudio Desktop Client (React 18 + Zustand + Zod + TanStack Virtual + Vite) + dezuxk-server (Go WebSocket Hub & Action Registry)**.

---

## 1. Định danh thành phần & Binary Output

| Thành phần | Tên cấu hình | Output Binary khi Build |
| :--- | :--- | :--- |
| **Desktop App** | `DezuxkStudio` | `build/bin/DezuxkStudio.exe` |
| **Go Core Server** | `dezuxk-server` | `bin/dezuxk-server.exe` |

---

## 2. Luồng giao tiếp hệ thống

```
[ Desktop Client: DezuxkStudio ]
         │ ▲
         │ │ Full-duplex WebSocket (/ws, coder/websocket)
         ▼ │
[ Go Core Server: dezuxk-server ]
  ├── Realtime Hub & Action Registry (`hub.RegisterAction`)
  ├── Dynamic Port Allocator (netutil)
  ├── Structured Logger & Health Prober (/health)
  └── Dynamic Runtime Asset Server (/runtime-config.json)
```

1. **Client (`DezuxkStudio`)**: Giao tiếp 2 chiều với Go Core qua WebSocket. Tự động nhận diện WebSocket URL qua Wails IPC (`GetActiveWS()`) trong cả môi trường Dev và Production.
2. **Go Core Server (`dezuxk-server`)**: 
   - Quản lý Action Registry nội bộ (`hub.RegisterAction("tên_action", handler)`).
   - Tiếp nhận và xử lý lệnh phi đồng bộ không khóa luồng giao diện.
   - Phát sự kiện trực tiếp tới từng client (`hub.Emit`) hoặc toàn bộ client (`hub.Broadcast`).

---

## 3. Cấu hình hoàn toàn động (Zero Hardcoded)

Hệ thống tải cấu hình theo thứ tự ưu tiên đa tầng:
**CLI Flags** > **Environment Variables** > **File `config.json`** > **Defaults**.

- **File cấu hình**: Tự động nhận diện qua `--config=<path>`, biến môi trường `CONFIG_FILE`, hoặc file `config.json` tại thư mục chạy/thư mục file `.exe`.
- **Cổng mạng**: Mặc định cổng `0` (hệ điều hành tự cấp phát cổng trống an toàn, chống va chạm tiến trình).
- **Kích thước cửa sổ & Tiêu đề**: Tùy biến linh hoạt qua `--width`, `--height`, `--title`, `--frameless`, `--hidden`.
- **Thông tin phiên bản**: Tự động inject qua Git tag/commit lúc build bằng `-ldflags`.

---

## 4. Quản lý dự án & Lệnh điều khiển

### Sử dụng PowerShell script (Windows Native)

```powershell
# 1. Khởi động môi trường phát triển (Wails Dev + Vite HMR)
powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 dev

# 2. Chạy toàn bộ bộ test Go
powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 test

# 3. Build bản Desktop Client: build/bin/DezuxkStudio.exe
powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 build

# 4. Build Go Server độc lập: bin/dezuxk-server.exe
powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 build:server

# 5. Dọn dẹp artifacts
powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 clean
```

### Sử dụng Taskfile (nếu cài đặt `go-task`)

```bash
task dev            # Chạy wails dev
task test           # Chạy unit tests với race detector (-race)
task build          # Build Desktop Client
task build:server   # Build dezuxk-server binary
task clean          # Dọn dẹp binary tạm
```

---

## 5. CI/CD & Tự động hóa

- **`.github/workflows/ci.yml`**: Tự động chạy trên mọi Pull Request và Push (Windows, Ubuntu, macOS), kiểm tra TypeScript compile, Go Unit Tests và Race Detector.
- **`.github/workflows/release.yml`**: Tự động kích hoạt khi gắn tag phiên bản (`v*`), tự compile đa nền tảng và phát hành GitHub Release kèm mã băm SHA-256.
