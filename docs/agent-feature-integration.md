# Cho agent dùng được tính năng mới (không cần viết code riêng cho agent)

DezuxkStudio Assistant (agent) **không có danh sách tính năng viết cứng**. Ở mỗi lượt, agent đọc các tính năng
đã đăng ký trong workflow engine qua tool `workflow_list_specs`, rồi chạy chúng qua `workflow_run_node` /
`workflow_run_graph`. Các tool này đi **đúng luồng mà UI dùng** (`workflow.Service.RunNode` → `engine.Registry`).

Vì vậy: **một tính năng mới được viết dưới dạng node và khai báo `engine.Spec` đầy đủ thì agent dùng được ngay.**

---

## 1. Checklist khi thêm một node mới

1. Viết node trong `internal/tools/<ten_tinh_nang>/node.go` bằng `engine.Typed(spec, func(ctx, In) (Out, error))`.
2. Đăng ký node trong `internal/service/workflow/service.go` (`reg.Register(...)`).
3. Khai báo `engine.Spec` đầy đủ (mục 2 bên dưới). Đây là "hợp đồng" duy nhất mà agent đọc.
4. Chạy `go run ./cmd/contractgen` nếu đổi struct trong `internal/engine` / `internal/contracts`.
5. UI của tính năng nên dùng đúng `Default` / `Options` trong spec. Tốt nhất là đọc từ spec, để UI và agent không bị lệch nhau.
6. Thêm test như `internal/service/agent/access_test.go` (`TestWorkflowRunNode…`), dùng `fakeWorkflow{specs: ...}` với spec thật của node.

Không cần sửa `internal/service/agent` cho từng node.

## 2. Khai báo `engine.Spec` để agent hiểu đúng

| Trường | Agent dùng để làm gì | Lưu ý |
|---|---|---|
| `Type` | Định danh duy nhất, ví dụ `tool.watermark_remover` | Không đổi sau khi phát hành |
| `Label` | Tên hiển thị, dùng trong bảng Approve | Ngắn, rõ ràng |
| `Description` | Agent đọc để biết **khi nào** dùng node này | Viết rõ tính năng làm gì và nhận loại file nào |
| `Inputs[]` (`Port`) | Agent truyền đường dẫn file/thư mục vào đây | Xem mục 3 |
| `Params[]` (`Param`) | Các thiết lập | Xem mục 4 |
| `Outputs[]` | Mô tả kết quả | |

## 3. Cổng input (`engine.Port`)

- `Type`: `image` / `video` / `media` (ảnh hoặc video) / `text`.
  - Với cổng media, agent có thể truyền **đường dẫn file, đường dẫn thư mục, hoặc danh sách**. Server tự mở thư mục thành
    danh sách file đúng loại (sắp theo tên; bỏ qua file code, file bí mật, thư mục `.git`…). Có tham số `recursive` của tool
    để lấy cả thư mục con. Node chỉ nhận các đường dẫn file tuyệt đối đã được kiểm tra.
  - Đường dẫn tương đối được hiểu là nằm trong thư mục media của app (giống quy ước của node).
  - Cổng `text` được chuyển nguyên giá trị.
- `Multiple: true`: nhận nhiều file (batch). Nếu node có cả cổng đơn lẫn cổng nhiều, agent truyền nhiều file vào cổng đơn
  sẽ nhận lỗi gợi ý dùng cổng nhiều.
- `Optional: false`: bắt buộc. Thiếu thì agent nhận lỗi rõ ràng.

Giới hạn: tối đa `maxNodeInputFiles` (10.000) file cho mỗi lần chạy.

## 4. Thiết lập (`engine.Param`)

| Trường | Ý nghĩa với agent |
|---|---|
| `Kind` | `toggle` → bool, `integer` → số nguyên, `select` → một trong `Options`, `text` → chuỗi |
| `Options` | **Bắt buộc với `select`.** Giá trị ngoài danh sách bị từ chối (không phân biệt hoa/thường) |
| `Default` | **Giá trị mặc định giống UI**, dạng chuỗi (`"false"`, `"0"`, `"bottom-right"`). Agent không truyền thì server tự điền |
| `Min` / `Max` | Giới hạn của `integer`. Ngoài khoảng sẽ bị từ chối |
| `Role: engine.ParamRoleOutputDir` | Tham số là **thư mục ghi kết quả**: được kiểm tra qua `AccessPolicy` và không được là file |
| `Destructive: true` | Toggle này khi bật sẽ **sửa/ghi đè file gốc của người dùng** → luôn chờ người dùng bấm **Approve** |

Mọi key không có trong `Inputs` / `Params` đều bị từ chối. Vì vậy, các tham số tinh chỉnh nội bộ mà UI không hiển thị
(ví dụ `min_score` của watermark) agent sẽ không dùng được. Đó là chủ ý, để agent đi đúng như UI.

Ví dụ (watermark):

```go
Params: []engine.Param{
    {Name: "inplace", Label: "Overwrite original file", Kind: engine.ParamToggle, Default: "false", Destructive: true},
    {Name: "output_dir", Label: "Output directory", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
    {Name: "workers", Label: "CPU worker threads (0 = Auto)", Kind: engine.ParamInteger, Min: 0, Max: 64, Default: "0"},
    {Name: "corner", Label: "Scan corner", Kind: engine.ParamSelect,
        Options: []string{"bottom-right", "bottom-left", "top-right", "top-left", "any"}, Default: "bottom-right"},
},
```

## 5. Kết quả trả về của node

Agent **chỉ được báo cáo dựa trên kết quả tool trả về**. Vì vậy output của node nên có:
- tổng số mục, số mục đã thay đổi;
- danh sách theo từng file: đường dẫn vào, đường dẫn ra, trạng thái, lỗi (nếu có);
- thông báo lỗi có thể hành động được (nói rõ sai gì và sửa thế nào), không chỉ "failed".

Node nên tôn trọng `ctx` (huỷ/timeout), ghi file bằng `fsutil.WriteFileAtomic`, và không tự ghi đè file gốc trừ khi
có tham số `Destructive` được bật.

## 6. Tính năng không phải dạng node (project, điều hướng UI, …)

Những tính năng không phải "xử lý input → output" (quản lý project, mở màn hình, …) thì thêm **MCP tool** trong
`internal/service/agent/mcp_tools.go`:

- Gọi vào service domain có sẵn (giống realtime module). Không viết lại logic trong agent.
- Hành động phá huỷ hoặc thay đổi dữ liệu người dùng → `s.needsApproval(tool, title, summary, exec)`.
- Đường dẫn → luôn qua `s.policy` (`Resolve`, `ValidateRead`, `ValidateWrite`, …).
- Trả kết quả có cấu trúc bằng `mcpJSON(...)`, lỗi bằng `mcpError(...)`.
- Cập nhật `buildCapabilitiesSnapshot` nếu muốn agent thấy tính năng đó trong `studio_list_capabilities`.

**Project (dự kiến):** khi có project, đưa thư mục các project vào `AccessPolicy.SetAllowedDirs(...)`, hoặc mở rộng
nguồn "trusted roots". Agent sẽ tự làm việc tự do trong project mà không cần thay đổi gì khác.

**Điều hướng UI (dự kiến):** agent hiện **không** có tool để chuyển màn hình. Nếu cần, thêm tool `ui_navigate(route)`
phát một event realtime cho frontend. `route` phải lấy từ danh sách route có thật (`frontend/src/app/navigation.ts`),
không cho agent truyền tự do.

## 7. Quyền truy cập file của agent (`AccessPolicy`)

| Vùng | Quyền |
|---|---|
| Mọi nơi trên máy | Liệt kê, đọc (text / ảnh / metadata video-audio), tạo file text mới, di chuyển (giữ nguyên loại file), chạy tính năng |
| Ghi đè / xoá ngoài thư mục tin cậy | Cần **Approve** |
| Thư mục tin cậy (workspace, media, thư mục người dùng thêm, sau này là project) | Tự do |
| Luôn chặn | Mã nguồn/script/file thực thi, thông tin đăng nhập (`.ssh`, `.env`, khoá, kho mật khẩu), `AppData`/profile trình duyệt, thư mục hệ điều hành, dữ liệu nội bộ của DezuxkStudio |

Danh sách nằm trong `internal/service/agent/access_policy.go` (`forbiddenExtensions`, `forbiddenFileNames`,
`forbiddenSegmentNames`, `buildProtectedDirs`).

## 8. Giới hạn vai trò (không cần làm gì thêm)

Mọi đầu ra của agent đi qua `code_guard.go`: code và lệnh terminal bị chặn trước khi tới UI, lịch sử chat hoặc file.
System prompt nằm ở `internal/service/agent/profile/agent.md`. Nếu một tính năng mới cần agent hiểu thêm ngữ cảnh,
hãy viết vào `Description` của node trước, chỉ sửa prompt khi thật cần thiết.

## 9. Kiểm thử với model thật

```powershell
# Antigravity đã đăng nhập trong app (dùng profile isolated của app)
$env:DEZUXK_LIVE_AGY="1"; go test -run TestLiveAgyFeatures -v -timeout 40m ./internal/service/agent/

# Bộ red-team (cần API key test của provider)
$env:DEZUXK_REDTEAM="1"; $env:DEZUXK_TEST_GEMINI_API_KEY="..."; go test -run TestLiveRedTeam -v ./internal/service/agent/
```
