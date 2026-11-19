// Package mediagen provides unified image, video and text-to-speech generation
// routed across all configured built-in and custom API providers.
package mediagen

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/httpclient"
	"app/internal/progress"
	"app/internal/service/media"
)

const (
	// maxErrorSnippetBytes bounds sanitized upstream provider error snippets included in user-facing messages.
	maxErrorSnippetBytes = 512
	// defaultPollStartPercent and defaultPollPercentCap bound intermediate progress reporting while polling async tasks.
	defaultPollStartPercent = 20.0
	defaultPollPercentCap   = 90.0
	// maxCachedAssetIDs bounds the in-memory path->assetID lookup map over long server uptimes.
	maxCachedAssetIDs = 2048
)

var (
	errStageTooLarge   = errors.New("staged media payload exceeded size limit")
	secretTokenPattern = regexp.MustCompile(`(?i)(bearer\s+[A-Za-z0-9._\-~+/=]+|sk-[A-Za-z0-9_\-]{8,}|(?:api[_-]?key|token|ticket|secret|signature|sig)=[^&\s"']+)`)
	embeddedURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

// MediaRegistrar registers newly produced media files into the media library.
type MediaRegistrar interface {
	RegisterAsset(ctx context.Context, params media.RegisterAssetParams) (*contracts.MediaAssetItem, error)
}

// ProviderResolver resolves configured models/credentials (satisfied by service/provider.Service).
type ProviderResolver interface {
	ResolveModel(ctx context.Context, p domainprovider.Purpose, ref string) (domainprovider.ResolvedModel, error)
	VertexToken(ctx context.Context, raw string) (accessToken, projectID string, expiresIn time.Duration, err error)
}

// Options configures Service construction.
type Options struct {
	Providers               ProviderResolver
	HTTPClient              *http.Client
	MediaDir                string
	StagingDir              string
	Registrar               MediaRegistrar
	MediaRepo               MediaRegistrar
	Config                  config.MediaGenConfig
	MaxVideoDurationSec     int
	DefaultVideoDurationSec int
	Logger                  *slog.Logger
	Leases                  *LeaseRegistry
	StagingTTL              time.Duration
	StagingGCInterval       time.Duration
	OnUsage                 func(context.Context, domainmedia.UsageEvent)
}

type cachedVertexToken struct {
	accessToken string
	projectID   string
	expiresAt   time.Time
}

// Service unifies media generation across built-in providers and custom provider endpoints.
type Service struct {
	providers               ProviderResolver
	httpClient              *http.Client
	mediaMu                 sync.RWMutex
	mediaDir                string
	stagingDir              string
	registrar               MediaRegistrar
	cfg                     config.MediaGenConfig
	maxVideoDurationSec     int
	defaultVideoDurationSec int
	log                     *slog.Logger
	limiter                 *rateLimiter
	leases                  *LeaseRegistry
	stagingTTL              time.Duration
	stagingGCInterval       time.Duration

	vertexMu    sync.Mutex
	vertexCache map[string]cachedVertexToken

	assetsMu    sync.RWMutex
	assets      map[string]string // map[path]assetID
	assetsOrder []string

	usageMu sync.RWMutex
	onUsage func(context.Context, domainmedia.UsageEvent)
}

// New constructs a unified media generation service.
func New(o Options) *Service {
	def := config.Default(config.ModeDesktop)
	client := o.HTTPClient
	if client == nil {
		client = httpclient.New(def.Network)
	}
	clientCopy := httpclient.WithRedirectGuard(client, domainprovider.ValidateOutboundURL)

	cfg := config.ResolveMediaGenConfig(o.Config)

	log := o.Logger
	if log == nil {
		log = slog.Default()
	}

	reg := o.Registrar
	if reg == nil {
		reg = o.MediaRepo
	}

	leases := o.Leases
	if leases == nil {
		leases = defaultLeaseRegistry
	}
	stagingTTL := o.StagingTTL
	if stagingTTL <= 0 {
		stagingTTL = def.Media.StagingTTL
	}
	stagingGCInterval := o.StagingGCInterval
	if stagingGCInterval <= 0 {
		stagingGCInterval = def.Media.StagingGCInterval
	}
	maxVidDur := o.MaxVideoDurationSec
	if maxVidDur <= 0 {
		maxVidDur = def.Studio.MaxShotDurationSec
	}
	defVidDur := o.DefaultVideoDurationSec
	if defVidDur <= 0 {
		defVidDur = def.Studio.DefaultShotDurationSec
	}
	if defVidDur > maxVidDur {
		defVidDur = maxVidDur
	}

	return &Service{
		providers:               o.Providers,
		httpClient:              clientCopy,
		mediaDir:                o.MediaDir,
		stagingDir:              strings.TrimSpace(o.StagingDir),
		registrar:               reg,
		cfg:                     cfg,
		maxVideoDurationSec:     maxVidDur,
		defaultVideoDurationSec: defVidDur,
		log:                     log,
		limiter:                 newRateLimiter(cfg.LimiterPollInterval, def.Providers.DefaultMaxWorkers),
		leases:                  leases,
		stagingTTL:              stagingTTL,
		stagingGCInterval:       stagingGCInterval,
		vertexCache:             make(map[string]cachedVertexToken),
		assets:                  make(map[string]string),
		onUsage:                 o.OnUsage,
	}
}

// SetUsageHook registers or clears an asynchronous usage reporting callback.

func withMediaGenDefaults(cfg config.MediaGenConfig) config.MediaGenConfig {
	return config.ResolveMediaGenConfig(cfg)
}

type targetSpec struct {
	ownerID      string
	providerName string
	resolvedRef  string
	protocol     string
	baseURL      string
	modelID      string
	modelFacts   domainprovider.Model
	apiKey       string
	accessKey    string
	secretKey    string
	credentialID string
	vertexJSON   string
}

func (s *Service) resolveTarget(ctx context.Context, purpose domainprovider.Purpose, modelRef string) (targetSpec, domainprovider.Limits, error) {
	if s.providers == nil {
		return targetSpec{}, domainprovider.Limits{}, apperr.Unavailable("Provider service is unavailable").
			WithMsg("errors.mediagen.provider_unavailable", nil)
	}
	res, err := s.providers.ResolveModel(ctx, purpose, modelRef)
	if err != nil {
		return targetSpec{}, domainprovider.Limits{}, err
	}
	resolvedRef := strings.TrimSpace(res.Ref.String())
	if resolvedRef == "" {
		resolvedRef = strings.TrimSpace(modelRef)
	}
	t := targetSpec{
		resolvedRef: resolvedRef,
		modelID:     res.Model.ModelID,
		modelFacts:  res.Model,
	}

	if res.Custom != nil {
		t.ownerID = "custom:" + res.Custom.Provider.ID
		t.providerName = res.Custom.Provider.Name
		if t.providerName == "" {
			t.providerName = res.Custom.Provider.ID
		}
		t.baseURL = strings.TrimRight(res.Custom.Provider.BaseURL, "/")
		t.apiKey = res.Custom.APIKey
		proto, mapErr := customProtocolFor(res.Custom.Provider.DiscoveryFormat, res.Model.Endpoint, purpose)
		if mapErr != nil {
			return targetSpec{}, domainprovider.Limits{}, mapErr
		}
		t.protocol = proto
		return t, res.Limits, nil
	}

	if res.Builtin != nil {
		b := res.Builtin
		t.ownerID = b.ProviderID
		t.providerName = b.ProviderID
		if meta, ok := domainprovider.Lookup(b.ProviderID); ok && meta.DisplayName != "" {
			t.providerName = meta.DisplayName
		}
		t.credentialID = b.CredentialID
		t.apiKey = b.Secrets[domainprovider.FieldAPIKey]
		t.accessKey = b.Secrets[domainprovider.FieldAccessKey]
		t.secretKey = b.Secrets[domainprovider.FieldSecretKey]
		t.vertexJSON = b.Secrets[domainprovider.FieldCredentialsFile]
		t.baseURL = strings.TrimRight(b.BaseURL, "/")
		if t.baseURL == "" {
			if meta, ok := domainprovider.Lookup(b.ProviderID); ok {
				t.baseURL = strings.TrimRight(meta.DefaultBaseURL, "/")
			}
		}
		proto, mapErr := builtinProtocolFor(b.ProviderID, purpose)
		if mapErr != nil {
			return targetSpec{}, domainprovider.Limits{}, mapErr
		}
		t.protocol = proto
		return t, res.Limits, nil
	}

	return targetSpec{}, domainprovider.Limits{}, apperr.Unavailable("Resolved model has no active provider configuration").
		WithMsg("errors.mediagen.provider_unavailable", nil)
}

func builtinProtocolFor(providerID string, purpose domainprovider.Purpose) (string, error) {
	switch providerID {
	case domainprovider.IDOpenAI:
		switch purpose {
		case domainprovider.PurposeText:
			return "openai-chat", nil
		case domainprovider.PurposeImage:
			return "openai-images", nil
		case domainprovider.PurposeVideo:
			return "openai-video", nil
		case domainprovider.PurposeTTS:
			return "openai-tts", nil
		}
	case domainprovider.IDGeminiAIStudio:
		switch purpose {
		case domainprovider.PurposeText:
			return "gemini-aistudio-text", nil
		case domainprovider.PurposeImage:
			return "gemini-aistudio-image", nil
		case domainprovider.PurposeVideo:
			return "gemini-aistudio-video", nil
		case domainprovider.PurposeTTS:
			return "gemini-aistudio-tts", nil
		}
	case domainprovider.IDGeminiVertex:
		switch purpose {
		case domainprovider.PurposeText:
			return "gemini-vertex-text", nil
		case domainprovider.PurposeImage:
			return "gemini-vertex-image", nil
		case domainprovider.PurposeVideo:
			return "gemini-vertex-video", nil
		case domainprovider.PurposeTTS:
			return "gemini-vertex-tts", nil
		}
	case domainprovider.IDAnthropic:
		if purpose == domainprovider.PurposeText {
			return "anthropic-messages", nil
		}
	case domainprovider.IDArk, domainprovider.IDArkAgentPlan:
		switch purpose {
		case domainprovider.PurposeText:
			return "ark-chat", nil
		case domainprovider.PurposeImage:
			return "ark-image", nil
		case domainprovider.PurposeVideo:
			return "ark-video", nil
		}
	case domainprovider.IDDashScope:
		switch purpose {
		case domainprovider.PurposeText:
			return "dashscope-chat", nil
		case domainprovider.PurposeImage:
			return "dashscope-image", nil
		case domainprovider.PurposeVideo:
			return "dashscope-video", nil
		case domainprovider.PurposeTTS:
			return "dashscope-tts", nil
		}
	case domainprovider.IDMiniMax:
		switch purpose {
		case domainprovider.PurposeText:
			return "minimax-text", nil
		case domainprovider.PurposeImage:
			return "minimax-image", nil
		case domainprovider.PurposeVideo:
			return "minimax-video", nil
		case domainprovider.PurposeTTS:
			return "minimax-tts", nil
		case domainprovider.PurposeLipSync:
			return "minimax-lipsync", nil
		}
	case domainprovider.IDKling:
		switch purpose {
		case domainprovider.PurposeImage:
			return "kling-image", nil
		case domainprovider.PurposeVideo:
			return "kling-video", nil
		case domainprovider.PurposeLipSync:
			return "kling-lipsync", nil
		}
	case domainprovider.IDVidu:
		switch purpose {
		case domainprovider.PurposeImage:
			return "vidu-image", nil
		case domainprovider.PurposeVideo:
			return "vidu-video", nil
		}
	case domainprovider.IDGrok:
		switch purpose {
		case domainprovider.PurposeText:
			return "openai-chat", nil
		case domainprovider.PurposeImage:
			return "grok-image", nil
		case domainprovider.PurposeVideo:
			return "grok-video", nil
		}
	case domainprovider.IDAgnes:
		switch purpose {
		case domainprovider.PurposeText:
			return "openai-chat", nil
		case domainprovider.PurposeImage:
			return "agnes-image", nil
		case domainprovider.PurposeVideo:
			return "agnes-video", nil
		}
	}
	return "", apperr.Validation("Provider "+providerID+" does not support "+string(purpose)).
		WithMsg("errors.mediagen.unsupported_provider_capability", map[string]any{"provider": providerID, "purpose": string(purpose)})
}

func customProtocolFor(format domainprovider.DiscoveryFormat, ep domainprovider.Endpoint, purpose domainprovider.Purpose) (string, error) {
	switch ep {
	case domainprovider.EndpointOpenAIChat:
		return "openai-chat", nil
	case domainprovider.EndpointOpenAIResponses:
		return "openai-responses", nil
	case domainprovider.EndpointGeminiGenerate:
		return "gemini-aistudio-text", nil
	case domainprovider.EndpointAnthropicMessages:
		return "anthropic-messages", nil
	case domainprovider.EndpointOpenAIImages:
		return "openai-images", nil
	case domainprovider.EndpointOpenAIImagesGenerations:
		return "openai-images-generations", nil
	case domainprovider.EndpointOpenAIImagesEdits:
		return "openai-images-edits", nil
	case domainprovider.EndpointOpenAIChatImage:
		return "openai-chat-image", nil
	case domainprovider.EndpointOpenRouterImages:
		return "openrouter-images", nil
	case domainprovider.EndpointGeminiImage:
		return "gemini-aistudio-image", nil
	case domainprovider.EndpointOpenAIVideo:
		return "openai-video", nil
	case domainprovider.EndpointGeminiVideo:
		return "gemini-aistudio-video", nil
	case domainprovider.EndpointOpenAITTS:
		return "openai-tts", nil
	case domainprovider.EndpointGeminiTTS:
		return "gemini-aistudio-tts", nil
	case domainprovider.EndpointLipSync:
		return "openai-lipsync", nil
	}

	// Fallback when custom model endpoint wasn't specified explicitly
	if format == domainprovider.DiscoveryGemini {
		switch purpose {
		case domainprovider.PurposeText:
			return "gemini-aistudio-text", nil
		case domainprovider.PurposeImage:
			return "gemini-aistudio-image", nil
		case domainprovider.PurposeVideo:
			return "gemini-aistudio-video", nil
		case domainprovider.PurposeTTS:
			return "gemini-aistudio-tts", nil
		}
	}
	if format == domainprovider.DiscoveryAnthropic && purpose == domainprovider.PurposeText {
		return "anthropic-messages", nil
	}
	if format == domainprovider.DiscoveryOpenAI || format == "" {
		switch purpose {
		case domainprovider.PurposeText:
			return "openai-chat", nil
		case domainprovider.PurposeImage:
			return "openai-images", nil
		case domainprovider.PurposeVideo:
			return "openai-video", nil
		case domainprovider.PurposeTTS:
			return "openai-tts", nil
		case domainprovider.PurposeLipSync:
			return "openai-lipsync", nil
		}
	}
	return "", apperr.Validation("Unsupported custom endpoint "+string(ep)+" for "+string(purpose)).
		WithMsg("errors.mediagen.unsupported_provider_capability", map[string]any{"provider": string(ep), "purpose": string(purpose)})
}

func (s *Service) getVertexToken(ctx context.Context, t targetSpec) (accessToken, projectID string, err error) {
	cacheKey := t.credentialID
	if cacheKey == "" {
		cacheKey = t.vertexJSON
	}
	s.vertexMu.Lock()
	if cached, ok := s.vertexCache[cacheKey]; ok && time.Until(cached.expiresAt) > s.cfg.OAuthRefreshSkew {
		s.vertexMu.Unlock()
		return cached.accessToken, cached.projectID, nil
	}
	s.vertexMu.Unlock()

	tok, proj, expiresIn, err := s.providers.VertexToken(ctx, t.vertexJSON)
	if err != nil {
		return "", "", apperr.Wrap(apperr.CodeUnauthorized, "Failed to obtain Vertex AI access token", err).
			WithMsg("errors.mediagen.auth_failed", map[string]any{"provider": t.providerName})
	}
	if expiresIn <= 0 {
		expiresIn = s.cfg.OAuthFallbackTTL
	}
	s.vertexMu.Lock()
	s.vertexCache[cacheKey] = cachedVertexToken{
		accessToken: tok,
		projectID:   proj,
		expiresAt:   time.Now().Add(expiresIn),
	}
	s.vertexMu.Unlock()
	return tok, proj, nil
}

func (s *Service) klingBearer(t targetSpec) (string, error) {
	if t.apiKey != "" {
		return t.apiKey, nil
	}
	if t.accessKey != "" && t.secretKey != "" {
		tok, err := domainprovider.KlingJWT(t.accessKey, t.secretKey, time.Now())
		if err != nil {
			return "", apperr.Wrap(apperr.CodeUnauthorized, "Failed to sign Kling JWT", err).
				WithMsg("errors.mediagen.auth_failed", map[string]any{"provider": t.providerName})
		}
		return tok, nil
	}
	return "", apperr.New(apperr.CodeUnauthorized, "Missing Kling credentials").
		WithMsg("errors.mediagen.auth_failed", map[string]any{"provider": t.providerName})
}

func ensureRootStrippingSuffixes(raw string, suffixes ...string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}
	for _, suf := range suffixes {
		if strings.HasSuffix(trimmed, suf) {
			return strings.TrimSuffix(trimmed, suf)
		}
	}
	return trimmed
}

func geminiModelName(modelID string) string {
	return strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
}

func ensureArkBase(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return domainprovider.DefaultBaseURLFor(domainprovider.IDArk)
	}
	if strings.HasSuffix(trimmed, "/api/v3") || strings.HasSuffix(trimmed, "/v3") {
		return trimmed
	}
	return trimmed + "/api/v3"
}

func (s *Service) SetRegistrar(r MediaRegistrar) {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	s.registrar = r
}

func (s *Service) getRegistrar() MediaRegistrar {
	s.mediaMu.RLock()
	defer s.mediaMu.RUnlock()
	return s.registrar
}

func (s *Service) SetMediaDir(dir string) {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	s.mediaDir = dir
}

// MediaDir returns the currently configured media directory.
func (s *Service) MediaDir() string {
	return s.getMediaDir()
}

func (s *Service) getMediaDir() string {
	s.mediaMu.RLock()
	defer s.mediaMu.RUnlock()
	return s.mediaDir
}

// StagingDir returns the active staging directory path, or empty if neither StagingDir nor MediaDir was configured.

func (s *Service) pollUntil(ctx context.Context, msgKey string, fallbackMsg string, extraParams map[string]string, fn func() (done bool, err error)) error {
	start := time.Now()
	pollNum := 0
	rampSec := s.cfg.VideoTimeout.Seconds()
	if rampSec <= 0 {
		rampSec = config.Default(config.ModeDesktop).MediaGen.VideoTimeout.Seconds()
	}
	for {
		pollNum++
		done, err := fn()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		elapsed := time.Since(start)
		pct := defaultPollStartPercent + (elapsed.Seconds()/rampSec)*(defaultPollPercentCap-defaultPollStartPercent)
		if pct > defaultPollPercentCap {
			pct = defaultPollPercentCap
		}
		params := make(map[string]string, len(extraParams)+1)
		for k, v := range extraParams {
			if v != "" {
				params[k] = v
			}
		}
		params["attempt"] = strconv.Itoa(pollNum)
		progress.Report(ctx, progress.Update{
			Percent: pct,
			Message: fallbackMsg,
			Key:     msgKey,
			Params:  params,
		})

		t := time.NewTimer(s.cfg.VideoPollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return apperr.From(ctx.Err())
		case <-t.C:
		}
	}
}
