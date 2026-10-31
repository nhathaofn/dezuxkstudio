-- 003_dynamic_providers.sql
-- Dynamic Self-Healing Reverse Provider Architecture Schema
-- Providers, accounts, encrypted secrets, features, endpoints, payload templates, and cookie partition rules.

-- 1. Bảng định nghĩa Provider (Zero-hardcode manifest)
CREATE TABLE IF NOT EXISTS dynamic_providers (
    id TEXT PRIMARY KEY,                       -- 'flow', 'muse', 'gemini'
    name TEXT NOT NULL,                        -- 'Google Flow', 'Muse.ai', 'Google Gemini'
    base_url TEXT NOT NULL,                    -- 'https://flow.google.com'
    auth_domain TEXT NOT NULL,                 -- '.google.com'
    allowed_domains_json TEXT NOT NULL,        -- JSON array: ['.google.com', 'flow.google.com']
    probe_rules_json TEXT NOT NULL,            -- Quy tắc trích xuất identity, tokens, build info
    security_rules_json TEXT NOT NULL DEFAULT '{}', -- Quy tắc bot/captcha
    status TEXT NOT NULL DEFAULT 'active',     -- 'active', 'disabled'
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 2. Bảng tài khoản người dùng
CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,                       -- UUID
    provider_id TEXT NOT NULL,
    label TEXT NOT NULL,                       -- Tên người dùng đặt
    identity TEXT NOT NULL DEFAULT '',         -- Email/User ID thực tế trích xuất được
    status TEXT NOT NULL DEFAULT 'unverified', -- 'unverified', 'ready', 'signed_out', 'rate_limited', 'error'
    profile_dir TEXT NOT NULL,                 -- %LOCALAPPDATA%\DezuxkStudio\data\profiles\<uuid>
    proxy_id TEXT NOT NULL DEFAULT '',         -- Proxy riêng biệt nếu có
    paused_until TIMESTAMP,                    -- Tự động nghỉ khi gặp 429
    usage_json TEXT NOT NULL DEFAULT '[]',     -- JSON chứa credit/quota
    metadata_json TEXT NOT NULL DEFAULT '{}',  -- Metadata mở rộng
    last_checked_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (provider_id) REFERENCES dynamic_providers(id) ON DELETE CASCADE
);

-- 3. Bảng bảo mật (Mã hóa DPAPI)
CREATE TABLE IF NOT EXISTS account_secrets (
    account_id TEXT PRIMARY KEY,
    encrypted_cookies_blob BLOB NOT NULL,      -- DPAPI(JSON array DynamicCookie)
    encrypted_tokens_blob BLOB NOT NULL,       -- DPAPI(JSON map[string]string)
    cookie_hash TEXT NOT NULL DEFAULT '',      -- Hash kiểm tra biến động
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

-- 4. Bảng tính năng của Provider (Ánh xạ sang DAG Node Types)
CREATE TABLE IF NOT EXISTS provider_features (
    id TEXT PRIMARY KEY,                       -- 'flow.image.generate', 'muse.video.generate'
    provider_id TEXT NOT NULL,
    feature_key TEXT NOT NULL,                 -- 'image_gen', 'video_gen', 'chat'
    name TEXT NOT NULL,
    zone_type TEXT NOT NULL,                   -- 'auth', 'billing', 'execution'
    dag_node_type TEXT NOT NULL UNIQUE,        -- Node type dùng trong DAG graph: 'flow.image.generate'
    execution_channel TEXT NOT NULL,           -- 'direct_http', 'in_page_fetch', 'cdp_evaluate'
    inputs_schema_json TEXT NOT NULL,          -- Đặc tả Port Input của DAG Node
    outputs_schema_json TEXT NOT NULL,         -- Đặc tả Port Output của DAG Node
    params_schema_json TEXT NOT NULL,          -- Đặc tả Parameters điều khiển trên UI
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (provider_id) REFERENCES dynamic_providers(id) ON DELETE CASCADE
);

-- 5. Bảng Endpoints động (Được cập nhật tự động bởi Discovery Engine)
CREATE TABLE IF NOT EXISTS dynamic_endpoints (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    feature_key TEXT NOT NULL,
    http_method TEXT NOT NULL DEFAULT 'POST',
    url_template TEXT NOT NULL,
    headers_template_json TEXT NOT NULL,
    rpc_method_name TEXT NOT NULL DEFAULT '',  -- Tên method gốc: '/FlowService.BatchGenerateImages'
    resolved_rpc_id TEXT NOT NULL DEFAULT '',  -- Hash động: 'ogiZ0b'
    resolution_strategy TEXT NOT NULL,         -- 'script_ast', 'traffic_sniff', 'static_url'
    expires_at TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (provider_id) REFERENCES dynamic_providers(id) ON DELETE CASCADE
);

-- 6. Bảng Template Payload (Chịu lỗi & Biến đổi tự động)
CREATE TABLE IF NOT EXISTS payload_templates (
    id TEXT PRIMARY KEY,
    feature_key TEXT NOT NULL,
    schema_type TEXT NOT NULL,                 -- 'jspb_positional_tuple', 'json_object_map'
    template_body TEXT NOT NULL,
    field_mappings_json TEXT NOT NULL,
    baseline_sample_json TEXT NOT NULL DEFAULT '{}',
    version INTEGER NOT NULL DEFAULT 1,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 7. Bảng quy tắc phân vùng Cookie
CREATE TABLE IF NOT EXISTS cookie_partition_rules (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    zone_type TEXT NOT NULL,                   -- 'auth', 'billing', 'execution', 'security'
    domain_pattern TEXT NOT NULL,              -- '*.google.com', 'flow.google.com'
    name_pattern TEXT NOT NULL,                -- Regex: '^(SID|SSID|HSID|__Secure-.*)$'
    is_required INTEGER NOT NULL DEFAULT 1,
    FOREIGN KEY (provider_id) REFERENCES dynamic_providers(id) ON DELETE CASCADE
);

-- Create helpful indexes for lookups
CREATE INDEX IF NOT EXISTS idx_accounts_provider_status ON accounts(provider_id, status);
CREATE INDEX IF NOT EXISTS idx_provider_features_provider ON provider_features(provider_id);
CREATE INDEX IF NOT EXISTS idx_dynamic_endpoints_provider_feature ON dynamic_endpoints(provider_id, feature_key);
CREATE INDEX IF NOT EXISTS idx_cookie_partition_rules_provider ON cookie_partition_rules(provider_id);
