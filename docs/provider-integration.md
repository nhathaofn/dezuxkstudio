# AI Providers & Model Credential Management

The Provider subsystem manages API keys, custom endpoints, model catalogs, rate limits, and authentication handshakes for LLM, Image, Video, and Text-to-Speech (TTS) services. Media generation and AI workflow features **do not manage or parse API keys directly**; instead, they call `provider.Service.ResolveFor(...)` or `provider.Service.ResolveModel(...)`.

---

## 1. Architectural Layout

| Layer | Files & Packages |
|---|---|
| **Domain** (Registry, Validation, Custom Providers, Model Resolution) | `internal/domain/provider/`<br/>- `resolve.go` (`Resolved`, `ResolvedModel`, `KlingJWT`, `SignJWT`, `EnsureOpenAIBase`)<br/>- `custom.go` (`ValidateOutboundURL`, SSRF checks)<br/>- `registry.go` (Builtin provider specs, capabilities, probes) |
| **Metadata Storage** (No plain secrets) | `internal/storage/sqlite/provider_repo.go`, migrations `009_providers.sql` through `013_provider_models_facts.sql` |
| **Secret Storage** (Keyring / AES-GCM) | `internal/secrets` (`DurableSecretStorage`: OS Keyring $\rightarrow$ AES-GCM encrypted file $\rightarrow$ in-memory fallback) |
| **Outbound HTTP Security** | `internal/httpclient` (`WithRedirectGuard` + `domain.ValidateOutboundURL`) |
| **Service Layer** (Use-cases, probes, autocheck) | `internal/service/provider/` (`service.go`, `probe.go`, `models.go`, `autocheck.go`) |
| **Downstream Consumers** | `internal/service/mediagen/`, `internal/tools/mediagen/` |
| **Realtime Transport** | `internal/realtime/modules/providers.go`, actions `providers:*`, event `providers:changed` |
| **Frontend UI** | `frontend/src/features/providers/`, located at **Settings → Providers** |

---

## 2. Core Security & Credential Rules

1. **Active Credential Invariant**:
   - Each provider can store multiple credentials, but **exactly one credential is active** at any given time.
   - Users switch active credentials manually. The system does not silently rotate keys.
   - Deleting the active credential automatically designates the oldest remaining credential as active. A unique partial index in SQLite guarantees this invariant.
2. **Zero-Secret Leakage Guarantee**:
   - Plain API keys **never** exist in the SQLite database, application logs, frontend DTOs, or error payloads.
   - The database stores only masked hints (`sk-...abcd`).
   - Secrets are saved in the OS Keyring via `internal/secrets` using dedicated slots: `provider_cred_<credID>_<field>` or `provider_custom_<id>_api_key`. Large JSON service account payloads are chunked to accommodate Windows Credential Manager payload limits.
   - In form submissions, leaving a secret field blank retains the existing secret. The explicit `clear_secrets` flag is required to delete it.
3. **Server Mode Accessibility (`--mode server`)**:
   - Authorized clients with valid authentication tokens retain full access to `providers:*` actions (listing, testing, saving keys, setting models) even when `server.allow_host_admin` is `false`. Secrets remain securely hosted on the core server and are never streamed to remote clients.
4. **Secret Storage Backends**:
   - Reported as `secret_backend` in `providers:list`:
     - `keyring`: Normal operation using Windows Credential Manager.
     - `encrypted_file`: Fallback mode using AES-GCM encrypted file storage (`secrets/secrets.enc`). Set `DEZUXK_MASTER_KEY` to separate the key from disk.
     - `memory`: In-memory only fallback. Secrets are lost on restart.

---

## 3. Resolving Credentials in Downstream Features

Downstream services (such as Image/Video/TTS generators or tools) resolve provider credentials through domain models:

```go
// 1. Resolve raw capability & endpoint credentials:
resolved, err := providerSvc.ResolveFor(ctx, provider.CapabilityImage, provider.IDOpenAI)
if err != nil {
    // If not configured, err is *apperr.Error with CodeValidation and details["not_configured"] = "true"
}
// Access resolved.BaseURL, resolved.Secrets["api_key"], resolved.Values[...]

// 2. Resolve default or specific model with rate limits:
m, err := providerSvc.ResolveModel(ctx, provider.PurposeImage, "") // "" = Auto / Default
// m.Model (id, facts, user overrides)
// m.Limits (max_workers, rpm, request_gap_ms)
// m.Builtin (key, base URL) or m.Custom (endpoint, key)
```

> [!IMPORTANT]
> The `domainprovider.Resolved` and `domainprovider.ResolvedModel` objects must never be exposed over WebSocket contracts, passed to agent prompt contexts, or injected into child process environments.

---

## 4. Provider Connectivity Probes

The provider service verifies connectivity using lightweight probes defined in `probeBuiltin` (`internal/service/provider/probe.go`):

| Provider | Probe Endpoint | Auth Header / Mechanism |
|---|---|---|
| `openai`, `grok`, `agnes` | `GET {base}/models` | `Bearer <token>` |
| `dashscope` | `GET {host}/compatible-mode/v1/models` | `Bearer <token>` |
| `minimax` | `GET {host}/v1/models` | `Bearer <token>` |
| `ark`, `ark-agent-plan` | `GET {base}/contents/generations/tasks?page_size=1` | `Bearer <token>` |
| `anthropic` | `GET {base}/v1/models` (retried with Bearer on 401) | `x-api-key: <key>`, `anthropic-version: 2023-06-01` |
| `gemini-aistudio` | `GET {base}/v1beta/models` | `x-goog-api-key: <key>` |
| `gemini-vertex` | Exchanges service account credentials for OAuth token | RS256 JWT assertion $\rightarrow$ Google OAuth |
| `kling` | `GET {root}/account/costs?start_time&end_time` | Bearer key or HS256 JWT generated via `domainprovider.KlingJWT` |
| `vidu` | `GET {base}/tasks/0/creations` (404 confirms valid auth) | `Token <key>` |

Probes return standardized `HealthResult` records with outcomes: `ok`, `auth_failed`, `rate_limited`, `not_found`, `http_error`, `timeout`, `network`, `invalid_credentials`. Probe failures never return raw Go errors; error summaries are scrubbed of sensitive data and capped at 200 characters.

---

## 5. Unified Nodes (Shared with UI and Agent)

Provider management operations are packaged as engine nodes in `internal/tools/provider/` and registered into `workflow.New`:

| Node Type | Responsibility | Access / Safety |
|---|---|---|
| `tool.provider_list` | Lists providers, input requirements, masked credentials, and health states | Read-only. |
| `tool.provider_setup` | Stores credentials, activates them, and triggers background health probe | `provider=auto` infers provider from base URL or key prefix. |
| `tool.provider_test` | Tests connectivity for a designated credential | Non-destructive. |
| `tool.provider_activate` | Sets active credential ID | Configuration change. |
| `tool.provider_delete` | Permanently deletes a credential | Destructive toggle; requires user **Approval** when triggered by Agent. |

---

## 6. Model Catalogs & Concurrency Limits

- **`provider_models`**: Stores model definitions for both builtin and custom providers. Tracks `display_name`, enable toggles, user overrides, and hardware facts (`durations`, `resolutions`, `audio`, `max_output_tokens`).
- **Discovery**: Models are populated via:
  1. Automated discovery via `Meta.Discoverable()` (`tool.provider_discover_models` or UI Refresh button). Non-generative models (embeddings, audio moderation) are filtered out.
  2. Manual ID entry by users for providers without catalog listing APIs (Kling, Vidu, Ark, Vertex).
- **Concurrency & Rate Limits**: Configured in `provider_settings` with hierarchical precedence:
  $$\text{User Setting} \longrightarrow \text{Provider Meta Default} \longrightarrow \text{Application Config Defaults}$$
  Application defaults in `internal/config`: `providers.default_max_workers` (2), `providers.default_rpm` (0 = unlimited), `providers.default_request_gap_ms` (0).
