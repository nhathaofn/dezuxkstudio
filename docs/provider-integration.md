# Nhà cung cấp API (Providers)

Tính năng này quản lý API key của các nhà cung cấp LLM / Image / Video / TTS. Các tính năng tạo ảnh/video
sau này **không tự đọc key**, mà gọi `provider.Service.ResolveFor(...)`.

Nguồn tham chiếu: ArcReel (`lib/config/registry.py`, `server/routers/providers.py`,
`lib/custom_provider/*`, ADR 0008 / 0016 / 0027 / 0037).

## Kiến trúc

| Lớp | File |
|---|---|
| Domain (registry, validate, custom provider, endpoint catalog) | `internal/domain/provider/` |
| Lưu metadata (không có secret) | `internal/storage/sqlite/provider_repo.go`, migration `009_providers.sql` |
| Lưu secret | `internal/secrets` (`Durable`: OS keyring → file mã hóa AES-GCM → bộ nhớ) |
| Use case, test kết nối, discover | `internal/service/provider/` |
| Realtime | `internal/realtime/modules/providers.go`, action `providers:*`, event `providers:changed` |
| UI | `frontend/src/features/providers/`, tab **Settings → Providers** |

### Quy tắc
- Mỗi provider có nhiều credential nhưng **chỉ một credential active**. Người dùng chuyển thủ công, app không tự xoay vòng.
  Xóa credential đang active thì credential cũ nhất còn lại được chọn thay. Index unique trong DB đảm bảo quy tắc này.
- Secret **không bao giờ** nằm trong DB, log, DTO hay lỗi. DB chỉ có `secret_hints` dạng che (`sk-…abcd`).
  Slot keyring: `provider_cred_<credID>_<field>`, `provider_custom_<id>_api_key`. Giá trị lớn (file JSON
  service account) được chia thành nhiều slot nhỏ (giới hạn của Windows Credential Manager).
- Khi cập nhật, ô secret để trống nghĩa là giữ giá trị cũ. `clear_secrets` dùng để xóa.
- `secret_backend` trong `providers:list`:
  - `keyring`: bình thường.
  - `encrypted_file`: cảnh báo vàng. Có thể đặt `DEZUXK_MASTER_KEY` để khóa mã hóa không nằm cạnh file `secrets/secrets.enc`.
  - `memory`: cảnh báo đỏ, key mất khi khởi động lại. `Resolve` sẽ trả `ERR_INTEGRITY` kèm `details.secret_missing`.
  Lần khởi động sau, nếu keyring hoạt động lại, secret trong file được tự chuyển vào keyring.
- Timeout test/discover: `providers.test_timeout` (mặc định 15s, giống ArcReel).

## Thêm một provider có sẵn
1. Thêm ID và một entry `Meta` vào `internal/domain/provider/registry.go`: `Capabilities`, `Fields`,
   `DefaultBaseURL`, `CredentialGroups` (nếu có kiểu "chọn một trong hai" như Kling) và `DocsURL`. Lúc khởi động `ValidateRegistry` sẽ kiểm tra.
2. Thêm cách test vào `probeBuiltin` (`internal/service/provider/probe.go`) và test bằng `httptest`.
3. Thêm nhãn cho field mới (`providers.field.<key>`) vào `en.ts` và `vi.ts`.
4. Chạy `task generate` nếu đổi enum hoặc DTO.

## Dùng credential trong tính năng mới (ví dụ node tạo ảnh)
```go
r, err := providers.ResolveFor(ctx, provider.CapabilityImage, provider.IDOpenAI)
// r.BaseURL (đã có mặc định), r.Secrets["api_key"], r.Values[...]
```
Chưa cấu hình thì trả về `ERR_VALIDATION` với `details.not_configured = true`. UI dùng cờ này để hướng người dùng sang Settings → Providers.
Custom provider: `ResolveCustom(ctx, id)` trả về provider (model và endpoint) cùng API key.
**Không** đưa `Resolved` ra contracts, cho agent, hay vào biến môi trường của tiến trình con (ADR 0027 của ArcReel).

## Test kết nối (theo ArcReel)
| Provider | Request | Auth |
|---|---|---|
| openai, grok, agnes | `GET {base}/models` | Bearer |
| dashscope | `GET {host}/compatible-mode/v1/models` | Bearer |
| minimax | `GET {host}/v1/models` | Bearer |
| ark, ark-agent-plan | `GET {base}/contents/generations/tasks?page_size=1` | Bearer |
| anthropic | `GET {base}/v1/models` (401 thì thử lại bằng Bearer) | `x-api-key` + `anthropic-version` |
| gemini-aistudio | `GET {base}/v1beta/models` | `x-goog-api-key` |
| gemini-vertex | Đổi service account lấy OAuth token (JWT RS256) | service account |
| kling | `GET {root}/account/costs?start_time&end_time` | Bearer `api_key` hoặc JWT HS256 (iss=AK, exp=+1800s, nbf=-5s) |
| vidu | `GET {base}/tasks/0/creations`, trả 404 nghĩa là key hợp lệ | `Token <key>` |

Kết quả (`outcome`): `ok`, `auth_failed`, `rate_limited`, `not_found`, `http_error`, `timeout`, `network`, `invalid_credentials`.
Test thất bại vẫn trả về kết quả, không phải lỗi. `detail` tối đa 200 ký tự và đã xóa secret.

> Phần test với API thật **chưa được chạy** vì chưa có key. Hành vi được kiểm bằng `httptest` dựa trên
> request/response mà ArcReel dùng. Khi có key thật, hãy bấm **Test** ở từng provider để xác nhận.
