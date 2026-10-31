// Package providers defines domain models and repository contracts for dynamic reverse providers.
package providers

import (
	"encoding/json"
	"time"
)

// ProviderStatus represents whether a provider is enabled.
type ProviderStatus string

const (
	ProviderStatusActive   ProviderStatus = "active"
	ProviderStatusDisabled ProviderStatus = "disabled"
)

// AccountStatus represents the operational readiness of an account.
type AccountStatus string

const (
	AccountStatusUnverified  AccountStatus = "unverified"
	AccountStatusReady       AccountStatus = "ready"
	AccountStatusSignedOut   AccountStatus = "signed_out"
	AccountStatusRateLimited AccountStatus = "rate_limited"
	AccountStatusError       AccountStatus = "error"
)

// CookieZone categorizes cookies into functional groups.
type CookieZone string

const (
	ZoneAuth      CookieZone = "auth"
	ZoneSecurity  CookieZone = "security"
	ZoneBilling   CookieZone = "billing"
	ZoneExecution CookieZone = "execution"
)

// ExecutionChannel specifies how the provider API is triggered.
type ExecutionChannel string

const (
	ChannelDirectHTTP  ExecutionChannel = "direct_http"
	ChannelInPageFetch ExecutionChannel = "in_page_fetch"
	ChannelCDPEvaluate ExecutionChannel = "cdp_evaluate"
)

// Cookie represents a single browser cookie.
type Cookie struct {
	Name     string     `json:"name"`
	Value    string     `json:"value"`
	Domain   string     `json:"domain"`
	Path     string     `json:"path"`
	Expires  float64    `json:"expires,omitempty"`
	HTTPOnly bool       `json:"httpOnly,omitempty"`
	Secure   bool       `json:"secure,omitempty"`
	SameSite string     `json:"sameSite,omitempty"`
	Zone     CookieZone `json:"zone,omitempty"`
}

// AccountSecrets stores sensitive session material (cookies, tokens).
type AccountSecrets struct {
	AccountID  string            `json:"account_id"`
	Cookies    []Cookie          `json:"cookies"`
	Tokens     map[string]string `json:"tokens"`
	CookieHash string            `json:"cookie_hash"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// ProbeRules defines how to extract identity and tokens from the provider web DOM.
type ProbeRules struct {
	IdentityExpressions []string          `json:"identityExpressions,omitempty"`
	TokenExtractors     map[string]string `json:"tokenExtractors,omitempty"`
	ScriptFilterURL     string            `json:"scriptFilterUrl,omitempty"`
	DiscoveryKeywords   []string          `json:"discoveryKeywords,omitempty"`
}

// Provider represents a reverse provider blueprint.
type Provider struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	BaseURL        string         `json:"base_url"`
	AuthDomain     string         `json:"auth_domain"`
	AllowedDomains []string       `json:"allowed_domains"`
	ProbeRules     ProbeRules     `json:"probe_rules"`
	SecurityRules  map[string]any `json:"security_rules"`
	Status         ProviderStatus `json:"status"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// QuotaUsage tracks credit/quota information.
type QuotaUsage struct {
	Feature   string    `json:"feature"`
	Remaining int64     `json:"remaining"`
	Total     int64     `json:"total"`
	ResetAt   time.Time `json:"reset_at,omitempty"`
}

// Account represents a user session for a provider.
type Account struct {
	ID            string         `json:"id"`
	ProviderID    string         `json:"provider_id"`
	Label         string         `json:"label"`
	Identity      string         `json:"identity"`
	Status        AccountStatus  `json:"status"`
	ProfileDir    string         `json:"profile_dir"`
	ProxyID       string         `json:"proxy_id,omitempty"`
	PausedUntil   *time.Time     `json:"paused_until,omitempty"`
	Usage         []QuotaUsage   `json:"usage,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	LastCheckedAt *time.Time     `json:"last_checked_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// ProviderFeature maps an upstream capability to an engine DAG node type.
type ProviderFeature struct {
	ID               string           `json:"id"`
	ProviderID       string           `json:"provider_id"`
	FeatureKey       string           `json:"feature_key"`
	Name             string           `json:"name"`
	ZoneType         CookieZone       `json:"zone_type"`
	DAGNodeType      string           `json:"dag_node_type"`
	ExecutionChannel ExecutionChannel `json:"execution_channel"`
	InputsSchema     json.RawMessage  `json:"inputs_schema"`
	OutputsSchema    json.RawMessage  `json:"outputs_schema"`
	ParamsSchema     json.RawMessage  `json:"params_schema"`
	CreatedAt        time.Time        `json:"created_at"`
}

// DynamicEndpoint stores discovered or static endpoints and RPC hashes.
type DynamicEndpoint struct {
	ID                 string            `json:"id"`
	ProviderID         string            `json:"provider_id"`
	FeatureKey         string            `json:"feature_key"`
	HTTPMethod         string            `json:"http_method"`
	URLTemplate        string            `json:"url_template"`
	HeadersTemplate    map[string]string `json:"headers_template"`
	RPCMethodName      string            `json:"rpc_method_name,omitempty"`
	ResolvedRPCID      string            `json:"resolved_rpc_id,omitempty"`
	ResolutionStrategy string            `json:"resolution_strategy"`
	ExpiresAt          *time.Time        `json:"expires_at,omitempty"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// CookiePartitionRule defines regex matching for assigning cookies to zones.
type CookiePartitionRule struct {
	ID            string     `json:"id"`
	ProviderID    string     `json:"provider_id"`
	ZoneType      CookieZone `json:"zone_type"`
	DomainPattern string     `json:"domain_pattern"`
	NamePattern   string     `json:"name_pattern"`
	IsRequired    bool       `json:"is_required"`
}
