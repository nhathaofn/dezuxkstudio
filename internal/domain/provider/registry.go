package provider

import "fmt"

// Built-in provider ids. Never rename one after release: credentials are stored under it.
const (
	IDGeminiAIStudio = "gemini-aistudio"
	IDGeminiVertex   = "gemini-vertex"
	IDOpenAI         = "openai"
	IDAnthropic      = "anthropic"
	IDArk            = "ark"
	IDArkAgentPlan   = "ark-agent-plan"
	IDGrok           = "grok"
	IDAgnes          = "agnes"
	IDDashScope      = "dashscope"
	IDMiniMax        = "minimax"
	IDKling          = "kling"
	IDVidu           = "vidu"
)

// Field keys used by specific providers.
const (
	FieldAccessKey       = "access_key"
	FieldSecretKey       = "secret_key"
	FieldCredentialsFile = "credentials_file"
	FieldGCSBucket       = "gcs_bucket"
	FieldWan3BaseURL     = "wan3_base_url"
)

func apiKey() Field { return Field{Key: FieldAPIKey, Kind: FieldText, Secret: true, Required: true} }

func baseURL(def string) Field {
	return Field{Key: FieldBaseURL, Kind: FieldURL, Placeholder: def}
}

var (
	llm   = CapabilityLLM
	image = CapabilityImage
	video = CapabilityVideo
	tts   = CapabilityTTS
)

// registry mirrors ArcReel's PROVIDER_REGISTRY (lib/config/registry.py) and media support
// (lib/backends/backend_assembly/specs.py). Anthropic comes from ArcReel's agent credential.
var registry = []Meta{
	{
		ID: IDGeminiAIStudio, DisplayName: "Google AI Studio",
		DefaultBaseURL: "https://generativelanguage.googleapis.com",
		Capabilities:   []Capability{llm, image, video, tts},
		Fields:         []Field{apiKey(), baseURL("https://generativelanguage.googleapis.com")},
		DocsURL:        "https://aistudio.google.com/apikey",
		Probe:          Probe{Kind: ProbeGeminiModels, ModelFilter: `veo|imagen|image`},
	},
	{
		ID: IDGeminiVertex, DisplayName: "Google Vertex AI",
		Capabilities: []Capability{llm, image, video, tts},
		Fields: []Field{
			{Key: FieldCredentialsFile, Kind: FieldFile, Secret: true, Required: true},
			{Key: FieldGCSBucket, Kind: FieldText},
		},
		DocsURL: "https://console.cloud.google.com/iam-admin/serviceaccounts",
		Probe:   Probe{Kind: ProbeVertexToken},
	},
	{
		ID: IDOpenAI, DisplayName: "OpenAI",
		Capabilities:   []Capability{llm, image, video},
		Fields:         []Field{apiKey(), baseURL("https://api.openai.com/v1")},
		DefaultBaseURL: "https://api.openai.com/v1",
		DocsURL:        "https://platform.openai.com/api-keys",
		Probe:          Probe{Kind: ProbeOpenAIModels, ModelFilter: `gpt|sora|dall|o1|o3|o4`},
	},
	{
		ID: IDAnthropic, DisplayName: "Anthropic",
		Capabilities:   []Capability{llm},
		Fields:         []Field{apiKey(), baseURL("https://api.anthropic.com")},
		DefaultBaseURL: "https://api.anthropic.com",
		DocsURL:        "https://console.anthropic.com/settings/keys",
		Probe:          Probe{Kind: ProbeAnthropicModels},
	},
	{
		ID: IDArk, DisplayName: "Volcengine Ark",
		Capabilities:   []Capability{llm, image, video},
		Fields:         []Field{apiKey(), baseURL("https://ark.cn-beijing.volces.com/api/v3")},
		DefaultBaseURL: "https://ark.cn-beijing.volces.com/api/v3",
		DocsURL:        "https://console.volcengine.com/ark",
		Probe:          Probe{Kind: ProbeArkTasks},
	},
	{
		ID: IDArkAgentPlan, DisplayName: "Volcengine Ark Agent Plan",
		Capabilities:   []Capability{llm, image, video},
		Fields:         []Field{apiKey()},
		DefaultBaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3",
		DocsURL:        "https://console.volcengine.com/ark",
		Probe:          Probe{Kind: ProbeArkTasks},
	},
	{
		ID: IDGrok, DisplayName: "xAI Grok",
		Capabilities:   []Capability{llm, image, video},
		Fields:         []Field{apiKey()},
		DefaultBaseURL: "https://api.x.ai/v1",
		DocsURL:        "https://console.x.ai",
		Probe:          Probe{Kind: ProbeOpenAIModels},
	},
	{
		ID: IDAgnes, DisplayName: "Agnes",
		Capabilities:   []Capability{llm, image, video},
		Fields:         []Field{apiKey(), baseURL("https://apihub.agnes-ai.com/v1")},
		DefaultBaseURL: "https://apihub.agnes-ai.com/v1",
		Probe:          Probe{Kind: ProbeOpenAIModels},
	},
	{
		ID: IDDashScope, DisplayName: "Alibaba DashScope",
		Capabilities: []Capability{llm, image, video, tts},
		Fields: []Field{apiKey(), baseURL("https://dashscope.aliyuncs.com"),
			{Key: FieldWan3BaseURL, Kind: FieldURL}},
		DefaultBaseURL: "https://dashscope.aliyuncs.com",
		DocsURL:        "https://bailian.console.aliyun.com/?apiKey=1",
		Probe:          Probe{Kind: ProbeOpenAIModels, StripSuffixes: []string{"/compatible-mode/v1", "/api/v1"}, Path: "/compatible-mode/v1", ModelFilter: `qwen|wan`},
		Hosts:          []string{"dashscope-intl.aliyuncs.com"},
	},
	{
		ID: IDMiniMax, DisplayName: "MiniMax",
		Capabilities:   []Capability{llm, image, video, tts},
		Fields:         []Field{apiKey(), baseURL("https://api.minimaxi.com/v1 · https://api.minimax.io/v1")},
		DefaultBaseURL: "https://api.minimaxi.com/v1",
		DocsURL:        "https://platform.minimax.io",
		Probe:          Probe{Kind: ProbeOpenAIModels, StripSuffixes: []string{"/v1"}, Path: "/v1", ModelFilter: `minimax|abab`},
		Hosts:          []string{"api.minimax.io", "api.minimaxi.chat"},
	},
	{
		ID: IDKling, DisplayName: "Kling",
		Capabilities: []Capability{image, video},
		Fields: []Field{
			apiKey(),
			{Key: FieldAccessKey, Kind: FieldText, Secret: true, Required: true},
			{Key: FieldSecretKey, Kind: FieldText, Secret: true, Required: true},
			baseURL("https://api-beijing.klingai.com/v1"),
		},
		CredentialGroups: [][]string{{FieldAPIKey}, {FieldAccessKey, FieldSecretKey}},
		DefaultBaseURL:   "https://api-beijing.klingai.com/v1",
		DocsURL:          "https://app.klingai.com/global/dev",
		Probe:            Probe{Kind: ProbeKlingCosts, StripSuffixes: []string{"/v1"}},
		Hosts:            []string{"api-singapore.klingai.com", "api.klingai.com"},
	},
	{
		ID: IDVidu, DisplayName: "Vidu",
		Capabilities:   []Capability{image, video},
		Fields:         []Field{apiKey(), baseURL("https://api.vidu.cn/ent/v2")},
		DefaultBaseURL: "https://api.vidu.cn/ent/v2",
		DocsURL:        "https://platform.vidu.cn",
		Probe:          Probe{Kind: ProbeViduTasks},
		Hosts:          []string{"api.vidu.com"},
	},
}

// Registry returns the built-in providers in display order.
func Registry() []Meta { return append([]Meta(nil), registry...) }

// Lookup returns a built-in provider by id.
func Lookup(id string) (Meta, bool) {
	for _, m := range registry {
		if m.ID == id {
			return m, true
		}
	}
	return Meta{}, false
}

// ValidateRegistry checks every entry and id uniqueness; called at start-up.
func ValidateRegistry() error {
	seen := map[string]bool{}
	for _, m := range registry {
		if err := m.Validate(); err != nil {
			return err
		}
		if seen[m.ID] {
			return fmt.Errorf("provider: duplicate id %s", m.ID)
		}
		seen[m.ID] = true
	}
	return nil
}
