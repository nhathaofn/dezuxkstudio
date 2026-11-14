# Mẫu prompt: thêm tính năng mà agent trong app điều khiển được

Quy tắc kỹ thuật nằm trong `.agents/rules/architecture.md` (luôn được nạp), nên prompt chỉ cần nói rõ:
**tính năng làm gì, dạng nào, agent được tự làm gì, cái gì phải chờ Approve.**
Chi tiết kỹ thuật: [agent-feature-integration.md](agent-feature-integration.md).

## 1. Mẫu ngắn

```
Thêm tính năng <tên>: <mô tả 1–2 câu tính năng làm gì>.
Tính năng phải để agent trong app điều khiển được, theo quy tắc §11 (architecture.md).
- Input: <loại file / dữ liệu vào>
- Output: <kết quả mong muốn>
- Thiết lập người dùng chỉnh được: <danh sách, kèm giá trị mặc định>
- Thao tác cần Approve: <ví dụ: ghi đè file gốc / xoá file>
Làm trên branch mới, có test thật, chạy task check.
```

## 2. Tính năng xử lý file (dạng engine node)

```
Thêm tính năng "Nén ảnh" dạng engine node (input → output), agent dùng được qua workflow_* tool.

Mục đích (để viết Description cho agent): giảm dung lượng ảnh JPG/PNG/WebP, giữ nguyên kích thước.
Khi nào agent nên dùng: người dùng muốn ảnh nhẹ hơn, gửi qua mạng, tiết kiệm ổ đĩa.

Input:
- images: nhiều ảnh (Multiple), nhận cả thư mục

Thiết lập (Param):
- quality: integer 1–100, mặc định 80
- format: select [giữ nguyên, jpg, webp], mặc định "giữ nguyên"
- output_dir: thư mục kết quả (Role output_dir)
- inplace: ghi đè file gốc, toggle, mặc định false, Destructive → phải chờ Approve

Output: số ảnh đã xử lý, dung lượng trước/sau từng file, lỗi rõ ràng nếu file hỏng.

Yêu cầu:
- Báo tiến độ theo từng ảnh (progress.Report), xử lý dạng stream (không load hết vào RAM)
- Kiểm tra mọi đường dẫn đọc/ghi cục bộ qua engine.CheckFile(ctx, path, write) (và dùng engine.ImageInputRef / engine.ResolveMediaPath nếu nhận media ref)
- DTO/hằng số node chia sẻ với contract đặt trong internal/contracts/nodes_dto.go (tách khỏi internal/tools/*)
- UI đọc Default từ spec, có đủ i18n en/vi
- Test: chạy node thật với ảnh mẫu; test agent gọi workflow_run_node chạy được; inplace=true phải sinh approval
```

## 3. Tính năng không xử lý file (dạng action + MCP tool)

```
Thêm tính năng "Quản lý project": tạo, đổi tên, xoá, liệt kê project.
Đây không phải dạng input → output, nên làm service + realtime action + MCP tool cho agent (§11).

Agent được tự làm: liệt kê, tạo mới, đổi tên.
Agent phải chờ Approve: xoá project, di chuyển file ra ngoài project.
Thư mục project được thêm vào thư mục tin cậy của AccessPolicy.
Thêm project vào studio_list_capabilities.
Test: tool gọi đúng service, thao tác xoá sinh approval, đường dẫn đi qua policy.
```

## 4. Vì sao các mục trong prompt quan trọng

| Mục bạn ghi | Trở thành | Agent trong app nhờ đó biết |
|---|---|---|
| "Mục đích" và "khi nào dùng" | `Description` của spec | Khi nào nên chọn tính năng này |
| Thiết lập kèm mặc định | `Param` có `Default`, `Options`, `Min`/`Max` | Gọi đúng như UI, không đoán tham số |
| Thao tác cần Approve | `Destructive: true` hoặc `needsApproval` | Phải dừng lại chờ người dùng bấm Approve |
| Output có số liệu và lỗi rõ | Kết quả có cấu trúc | Báo cáo đúng sự thật cho người dùng |

`engine.ValidateSpec` chặn spec khai báo thiếu, nhưng không kiểm tra được nội dung có hợp lý không.
Vì vậy phần "mục đích" và "khi nào dùng" là phần nên viết kỹ nhất.

## 5. Kiểm tra sau khi làm xong

1. Node có trong danh sách `nodes` của `workflow.New` (dạng node), hoặc có MCP tool trong
   `internal/service/agent/mcp_tools.go` (dạng khác).
2. Có test chứng minh thao tác Destructive sinh approval (và test `engine.CheckFile` nếu node chạm vào file cục bộ).
3. Mở chat agent trong app, hỏi "bạn làm được những gì?": tính năng mới phải có trong danh sách.
4. Chạy `task check` (hoặc đặt `$env:GOTMPDIR="$PWD\build\.gotmp"` khi chạy `go test` trực tiếp trên Windows).
