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

## Node (agent và workflow dùng chung)
Quản lý provider được đóng gói thành **node** giống `tool.watermark_remover` (`internal/tools/provider`), đăng ký trong
`workflow.New`. Agent không có tool riêng: nó đọc `workflow_list_specs` và chạy bằng `workflow_run_node`.

| Node | Việc làm | Ghi chú |
|---|---|---|
| `tool.provider_list` | Xem provider, các trường cần nhập, key đã lưu (đã che), trạng thái sức khỏe | chỉ đọc |
| `tool.provider_setup` | Lưu key, kích hoạt, rồi test | `provider=auto` để tự nhận diện; các tham số được suy ra từ registry |
| `tool.provider_test` | Test key theo id | id lấy từ option source `provider.saved` |
| `tool.provider_activate` | Đổi key đang dùng | option source `provider.credentials` |
| `tool.provider_delete` | Xoá key | `confirm` là toggle Destructive, nên phải chờ người dùng **Approve** |

- **Không hardcode:** danh sách provider, các trường secret, giao thức, cách test (`Meta.Probe`), bộ lọc model và host nhận diện
  (`Meta.Hosts`) đều khai báo **một lần** trong `internal/domain/provider/registry.go`. Thêm provider chỉ cần sửa registry:
  node, form UI, cách test và cách nhận diện sẽ tự cập nhật theo.
- **Secret:** tham số có `engine.Param.Secret` không hiện trong summary hay thẻ Approve. Sau khi chạy (cả một node lẫn
  cả graph), agent xoá giá trị đó khỏi lịch sử chat trong DB. Cơ chế này nằm ở engine, nên **mọi** node sau này đều được hưởng.
- **Nhận diện** (`domain.PlanSetup`):
  1. Có `provider` thì dùng luôn.
  2. Nếu không, so host của base URL với registry.
  3. Không có base URL thì dựa vào tiền tố key: `sk-ant-`, `AIza`, `xai-`. Có AK+SK là Kling, có file JSON là Vertex.
  4. Host lạ thì tạo custom provider. Phải discover model thành công thì mới lưu.
- CLI agent (Claude, Codex, Gemini) vẫn có thể giữ transcript riêng trong profile của nó. Muốn an toàn tuyệt đối thì nhập key ở Settings → Providers.

## Model, giới hạn và model mặc định
- **Model**: bảng `provider_models` dùng chung cho provider có sẵn và custom (`owner_id`). Mỗi model gồm loại
  (llm/image/video/tts), `display_name`, bật/tắt, mặc định theo loại (mỗi loại chỉ một model), `user_added`, và `facts`
  (`durations`, `resolutions`, `audio`, `max_output_tokens`) do người dùng sửa. Catalog **không lấy từ ArcReel**
  (AGPL), mà xây dựng từ:
  1. API của chính provider: `tool.provider_discover_models` hoặc nút "Refresh from API". Model không dùng để sinh
     nội dung (embedding, whisper…) bị bỏ qua. Khi discover lại, lựa chọn cũ của người dùng được giữ nguyên.
  2. Model người dùng tự thêm theo ID. Đây là cách duy nhất cho Kling, Vidu, Ark, Vertex vì API của họ không có
     danh sách model.
- **Giới hạn** (`provider_settings`, khai báo trong `domain.SettingSpecs`): `image/video/tts_max_workers`, `image/video_rpm`,
  `request_gap_ms`. Thứ tự ưu tiên khi lấy giá trị: người dùng đặt > `Meta.DefaultConcurrency` (ví dụ Agnes video = 1) > config
  (`providers.default_max_workers`, `providers.default_rpm`, `providers.default_request_gap_ms`).
- **Model mặc định theo mục đích** (`provider_defaults`, khai báo trong `domain.PurposeSpecs`: text, image, video, tts):
  - Giá trị lưu dạng `provider/model`. Để trống nghĩa là **auto**: lấy model mặc định của provider sẵn sàng đầu tiên,
    provider có sẵn theo thứ tự registry, sau đó tới custom.
  - Provider bị coi là không sẵn sàng khi không có key active hoặc key active đã chết.
  - Nếu model đã chọn không còn dùng được thì hiện cảnh báo và tạm dùng auto.
- **Dùng trong tính năng mới**:
  ```go
  m, err := providers.ResolveModel(ctx, provider.PurposeImage, "") // "" = default/auto, hoặc "openai/gpt-image-1"
  // m.Model (id, facts), m.Limits (max workers, rpm, gap), m.Builtin (key, base URL) hoặc m.Custom (endpoint, key)
  ```
  Chưa có model dùng được thì trả `ERR_VALIDATION` với `details.not_configured`.

## Tự động kiểm tra khi nhập key
Không cần bấm "Kiểm tra" hay "Làm mới": khi bạn **lưu, đổi hoặc chọn dùng** một key (và khi lưu hoặc sửa custom provider),
app tự chạy nền (`internal/service/provider/autocheck.go`):
1. Test key: sống hay chết, kết quả lưu vào `health`.
2. Nếu key sống, là key **đang dùng** của provider, và API của provider có danh sách model
   (`Meta.Discoverable()`), app lấy danh sách model và **thêm model mới** vào mục "Mô hình". Lựa chọn cũ của bạn
   (bật/tắt, mặc định, thông số, tên hiển thị) được giữ nguyên, model không dùng để sinh nội dung (embedding, whisper…) bị bỏ.
3. Nếu key sống nhưng không lấy được danh sách model, trạng thái là **models_failed** ("Khóa hoạt động nhưng không lấy được
   danh sách model", không bị coi là key chết) thay vì im lặng để danh sách rỗng.

- Trong lúc kiểm tra, giao diện hiện **"Đang kiểm tra…"** (cờ `checking`) ở dòng key, tiêu đề provider và chấm ở danh sách bên
  trái. Kết quả được đẩy qua sự kiện `providers:changed`, không cần tải lại.
- Lưu lại khi đang kiểm tra thì kiểm tra thêm đúng một lần nữa sau đó, với dữ liệu mới nhất.
- Khi mở app, `providers.check_on_startup` cũng làm mới danh sách model cho mọi key đang dùng (cùng đường đi).
- Tắt bằng `providers.auto_check = false` (mặc định bật): lúc đó chỉ còn hai nút Test và Làm mới.
- **Provider mới trong tương lai** tự có tính năng này chỉ cần khai báo `Probe` trong registry. Không có mã riêng cho từng provider.
- Bước kiểm tra key và bước lấy danh sách model dùng **cùng một địa chỉ**: địa chỉ gốc bạn nhập được giữ nguyên.

## Kiểm tra sức khỏe key
- Khi app mở (`providers.check_on_startup`, mặc định bật), mọi credential và custom provider được test ở chế độ nền,
  tối đa `providers.check_concurrency` (mặc định 4) test chạy cùng lúc. Việc test không làm chậm lúc mở app, và dừng lại khi tắt app.
- Kết quả mới nhất được lưu vào DB (`last_outcome`, `last_detail`, `last_tested_at`, migration `010`). Bấm Test thủ công cũng cập nhật kết quả này.
  Khi đổi key, base URL hoặc giao thức, kết quả cũ bị xoá.
- Nhóm **dead** (hiện đỏ): `auth_failed`, `invalid_credentials`, `secret_missing`.
  Lỗi mạng, timeout, rate limit hoặc HTTP khác hiện **vàng** ("không xác minh được") vì key có thể vẫn tốt.
- App **không** tự chuyển sang key khác khi key chết. Người dùng chọn thủ công, giống ArcReel.

## Khác biệt còn lại so với ArcReel
- Vertex: test bằng cách đổi service account lấy OAuth token. ArcReel gọi `models.list`, nên bắt được thêm trường hợp Vertex API bị tắt.
- Agnes: ArcReel không có test, ở đây gọi `GET {base}/models`. Nếu trả 404 thì hiện vàng, không bị coi là key chết.
- Đoán endpoint cho custom provider chỉ dựa trên ID model, đơn giản hơn `infer_endpoint` của ArcReel. Người dùng sửa được trong bảng model.
- Không có catalog model dựng sẵn (giá, vòng đời model, đồng bộ chính thức). Đây là lựa chọn có chủ ý để không chép dữ liệu AGPL.
- Không làm: tính tiền và hệ số giá, ComfyUI, endpoint khai báo bằng JSON hoặc do người dùng tự định nghĩa, `path_overrides`, credential agent Anthropic theo tier.
