// Package mediagen provides unified image, video and text-to-speech generation
// routed across all configured built-in and custom API providers.
package mediagen

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/httpclient"
	"app/internal/logger"
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
func (s *Service) SetUsageHook(fn func(context.Context, domainmedia.UsageEvent)) {
	s.usageMu.Lock()
	s.onUsage = fn
	s.usageMu.Unlock()
}

func (s *Service) emitUsage(ctx context.Context, ev domainmedia.UsageEvent) {
	s.usageMu.RLock()
	fn := s.onUsage
	s.usageMu.RUnlock()
	if fn == nil {
		return
	}
	meta := domainmedia.UsageMetaFromContext(ctx)
	if ev.ProjectID == "" {
		ev.ProjectID = meta.ProjectID
	}
	if ev.EpisodeID == "" {
		ev.EpisodeID = meta.EpisodeID
	}
	if ev.ShotID == "" {
		ev.ShotID = meta.ShotID
	}
	if ev.JobID == "" {
		ev.JobID = meta.JobID
	}
	if ev.Operation == "" {
		ev.Operation = meta.Operation
	}
	if ev.EstimatedUSD == 0 {
		ev.EstimatedUSD = meta.EstimatedUSD
	}
	fn(ctx, ev)
}

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

func (s *Service) requestTimeoutCtx(ctx context.Context, isVideo bool) (context.Context, context.CancelFunc) {
	d := s.cfg.RequestTimeout
	if isVideo {
		d = s.cfg.VideoTimeout
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= d {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

func (s *Service) doJSON(ctx context.Context, method, targetURL string, headers map[string]string, reqBody any, out any) error {
	if err := domainprovider.ValidateOutboundURL(targetURL); err != nil {
		return apperr.Wrap(apperr.CodeValidation, "Invalid provider endpoint URL", err)
	}
	var bodyReader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return apperr.Internal(err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return apperr.Internal(err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	raw, _, status, err := s.execHTTP(req, s.cfg.MaxJSONResponseBytes)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return mapAPIError(status, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return apperr.Wrap(apperr.CodeUnavailable, "Unexpected non-JSON response from provider", err)
		}
	}
	return nil
}

func (s *Service) doMultipart(ctx context.Context, targetURL string, headers map[string]string, writeFields func(*multipart.Writer) error, out any) error {
	if err := domainprovider.ValidateOutboundURL(targetURL); err != nil {
		return apperr.Wrap(apperr.CodeValidation, "Invalid provider endpoint URL", err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := writeFields(mw); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return apperr.Internal(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return apperr.Internal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	raw, _, status, err := s.execHTTP(req, s.cfg.MaxJSONResponseBytes)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return mapAPIError(status, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return apperr.Wrap(apperr.CodeUnavailable, "Unexpected response from provider", err)
		}
	}
	return nil
}

type mediaOutput struct {
	data     []byte
	filePath string
	mimeType string
}

func (m *mediaOutput) cleanup() {
	if m != nil && m.filePath != "" {
		_ = os.Remove(m.filePath)
		m.filePath = ""
	}
}

func filterTrustedHeaders(rawURL string, headers map[string]string, trustedURLs ...string) map[string]string {
	if len(headers) == 0 || len(trustedURLs) == 0 {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return nil
	}
	for _, tuRaw := range trustedURLs {
		tu, err := url.Parse(strings.TrimSpace(tuRaw))
		if err != nil || tu.Host == "" {
			continue
		}
		if strings.EqualFold(u.Scheme, tu.Scheme) && strings.EqualFold(u.Host, tu.Host) {
			return headers
		}
	}
	return nil
}

func newBinaryRequest(ctx context.Context, method, targetURL string, headers map[string]string, reqBody any) (*http.Request, error) {
	if err := domainprovider.ValidateOutboundURL(targetURL); err != nil {
		return nil, apperr.Wrap(apperr.CodeValidation, "Invalid provider URL", err)
	}
	var bodyReader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

func (s *Service) doBinary(ctx context.Context, method, targetURL string, headers map[string]string, reqBody any) ([]byte, string, error) {
	req, err := newBinaryRequest(ctx, method, targetURL, headers, reqBody)
	if err != nil {
		return nil, "", err
	}
	raw, ct, status, err := s.execHTTP(req, s.cfg.MaxOutputBytes)
	if err != nil {
		return nil, "", err
	}
	if status < 200 || status >= 300 {
		return nil, "", mapAPIError(status, raw)
	}
	if len(raw) == 0 {
		return nil, "", apperr.Unavailable("Provider returned empty media body")
	}
	return raw, ct, nil
}

func (s *Service) doBinaryToFile(ctx context.Context, method, targetURL string, headers map[string]string, reqBody any) (mediaOutput, error) {
	req, err := newBinaryRequest(ctx, method, targetURL, headers, reqBody)
	if err != nil {
		return mediaOutput{}, err
	}
	tmpPath, ct, status, size, errBody, err := s.execHTTPToFile(req, s.cfg.MaxOutputBytes)
	if err != nil {
		return mediaOutput{}, err
	}
	if status < 200 || status >= 300 {
		return mediaOutput{}, mapAPIError(status, errBody)
	}
	if size == 0 {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
		return mediaOutput{}, apperr.Unavailable("Provider returned empty media body")
	}
	return mediaOutput{filePath: tmpPath, mimeType: ct}, nil
}

func newDownloadRequest(ctx context.Context, rawURL string, headers map[string]string, trustedURLs ...string) (*http.Request, error) {
	if err := domainprovider.ValidateOutboundURL(rawURL); err != nil {
		return nil, apperr.Wrap(apperr.CodeUnavailable, "Blocked unsafe remote media URL", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	for k, v := range filterTrustedHeaders(rawURL, headers, trustedURLs...) {
		req.Header.Set(k, v)
	}
	return req, nil
}

func (s *Service) downloadURL(ctx context.Context, rawURL string, headers map[string]string, trustedURLs ...string) ([]byte, string, error) {
	req, err := newDownloadRequest(ctx, rawURL, headers, trustedURLs...)
	if err != nil {
		return nil, "", err
	}
	raw, ct, status, err := s.execHTTP(req, s.cfg.MaxOutputBytes)
	if err != nil {
		return nil, "", err
	}
	if status < 200 || status >= 300 {
		return nil, "", mapAPIError(status, raw)
	}
	if len(raw) == 0 {
		return nil, "", apperr.Unavailable("Downloaded media file was empty")
	}
	return raw, ct, nil
}

func (s *Service) downloadURLToFile(ctx context.Context, rawURL string, headers map[string]string, trustedURLs ...string) (mediaOutput, error) {
	req, err := newDownloadRequest(ctx, rawURL, headers, trustedURLs...)
	if err != nil {
		return mediaOutput{}, err
	}
	tmpPath, ct, status, size, errBody, err := s.execHTTPToFile(req, s.cfg.MaxOutputBytes)
	if err != nil {
		return mediaOutput{}, err
	}
	if status < 200 || status >= 300 {
		return mediaOutput{}, mapAPIError(status, errBody)
	}
	if size == 0 {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
		return mediaOutput{}, apperr.Unavailable("Downloaded media file was empty")
	}
	return mediaOutput{filePath: tmpPath, mimeType: ct}, nil
}

// RetryConfig tunes transient HTTP retry behavior.
type RetryConfig struct {
	MaxRetries    int           // maximum retry attempts (default 3)
	InitialDelay  time.Duration // initial backoff delay (default 100ms)
	MaxDelay      time.Duration // maximum backoff delay cap (default 2s)
	BackoffFactor float64       // exponential factor (default 2.0)
}

// DefaultRetryConfig returns standard production defaults for mediagen requests from config.Default.
func DefaultRetryConfig() RetryConfig {
	def := config.Default(config.ModeDesktop).MediaGen
	return RetryConfig{
		MaxRetries:    def.RetryMax,
		InitialDelay:  def.RetryInitialDelay,
		MaxDelay:      def.RetryMaxDelay,
		BackoffFactor: def.RetryBackoffFactor,
	}
}

// IsRetryableStatus reports whether an HTTP status code represents a transient, retryable error.
func IsRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, // 429
		http.StatusServiceUnavailable, // 503
		http.StatusBadGateway,         // 502
		http.StatusGatewayTimeout:     // 504
		return true
	default:
		return false
	}
}

// isSyscallReset checks if an error represents a connection reset or peer disconnect.
func isSyscallReset(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.EPIPE:
			return true
		default:
			const (
				wsaeconnreset   syscall.Errno = 10054
				wsaeconnrefused syscall.Errno = 10061
			)
			if errno == wsaeconnreset || errno == wsaeconnrefused {
				return true
			}
		}
	}
	return false
}

// IsTransientNetErr reports whether err represents a transient network or socket error.
func IsTransientNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return true
		}
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			if isSyscallReset(sysErr.Err) {
				return true
			}
		}
		if isSyscallReset(opErr.Err) {
			return true
		}
	}

	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"wsarecv",
		"wsasend",
		"wsaeconnreset",
		"wsaeconnrefused",
		"unexpected eof",
		"tls handshake timeout",
		"i/o timeout",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func parseRetryAfter(header string) (time.Duration, bool) {
	if header == "" {
		return 0, false
	}
	if sec, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && sec >= 0 {
		return time.Duration(sec) * time.Second, true
	}
	if t, err := http.ParseTime(strings.TrimSpace(header)); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

func computeDelay(attempt int, cfg RetryConfig, retryAfter string) time.Duration {
	if d, ok := parseRetryAfter(retryAfter); ok {
		if d > cfg.MaxDelay {
			return cfg.MaxDelay
		}
		return d
	}
	delay := cfg.InitialDelay
	for i := 0; i < attempt; i++ {
		delay = time.Duration(float64(delay) * cfg.BackoffFactor)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
			break
		}
	}
	return delay
}

// ExecHTTPWithRetry executes an HTTP request with bounded body streaming and transient retry with backoff.
// It retries on HTTP 503, 429, and transient network errors (such as connection reset).
func ExecHTTPWithRetry(ctx context.Context, client *http.Client, req *http.Request, maxBytes int64, cfg RetryConfig) ([]byte, string, int, error) {
	return execHTTPWithRetryObserver(ctx, client, req, maxBytes, cfg, nil)
}

func normalizeHTTPClientAndRetry(client *http.Client, cfg RetryConfig) (*http.Client, RetryConfig) {
	if client == nil {
		client = httpclient.WithRedirectGuard(
			httpclient.New(config.Default(config.ModeDesktop).Network),
			domainprovider.ValidateOutboundURL,
		)
	}
	defCfg := DefaultRetryConfig()
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = defCfg.InitialDelay
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = defCfg.MaxDelay
	}
	if cfg.BackoffFactor <= 0 {
		cfg.BackoffFactor = defCfg.BackoffFactor
	}
	return client, cfg
}

func waitRetryDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return apperr.From(ctx.Err())
	case <-timer.C:
		return nil
	}
}

func execHTTPAttemptLoop(
	ctx context.Context,
	client *http.Client,
	req *http.Request,
	cfg RetryConfig,
	onRateLimit func(time.Duration),
	consume func(resp *http.Response, canRetry bool) (retryAfter string, retry bool, err error),
) error {
	client, cfg = normalizeHTTPClientAndRetry(client, cfg)
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 && req.GetBody != nil {
			rc, err := req.GetBody()
			if err != nil {
				return apperr.Internal(err)
			}
			req.Body = rc
		}
		if ctx.Err() != nil {
			return apperr.From(ctx.Err())
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return apperr.From(ctx.Err())
			}
			if attempt < cfg.MaxRetries && IsTransientNetErr(err) {
				if wErr := waitRetryDelay(ctx, computeDelay(attempt, cfg, "")); wErr != nil {
					return wErr
				}
				continue
			}
			return apperr.Wrap(apperr.CodeUnavailable, "Provider HTTP request failed", err).
				WithMsg("errors.mediagen.request_failed", nil)
		}

		if resp.StatusCode == http.StatusTooManyRequests && onRateLimit != nil {
			bo, ok := parseRetryAfter(resp.Header.Get("Retry-After"))
			if !ok || bo <= 0 {
				bo = computeDelay(attempt, cfg, "")
			}
			onRateLimit(bo)
		}

		retryAfter, retry, cErr := consume(resp, attempt < cfg.MaxRetries)
		_ = resp.Body.Close()
		if !retry {
			return cErr
		}
		if cErr != nil {
			lastErr = cErr
		}
		if wErr := waitRetryDelay(ctx, computeDelay(attempt, cfg, retryAfter)); wErr != nil {
			return wErr
		}
	}
	if lastErr != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Provider HTTP request failed after retries", lastErr).
			WithMsg("errors.mediagen.request_failed", nil)
	}
	return nil
}

func execHTTPWithRetryObserver(ctx context.Context, client *http.Client, req *http.Request, maxBytes int64, cfg RetryConfig, onRateLimit func(time.Duration)) ([]byte, string, int, error) {
	var lastStatus int
	var lastRaw []byte
	var lastCT string

	err := execHTTPAttemptLoop(ctx, client, req, cfg, onRateLimit, func(resp *http.Response, canRetry bool) (string, bool, error) {
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		lastStatus = resp.StatusCode
		lastRaw = raw
		lastCT = resp.Header.Get("Content-Type")
		retryAfterHeader := resp.Header.Get("Retry-After")

		if readErr != nil {
			if canRetry && (IsRetryableStatus(resp.StatusCode) || IsTransientNetErr(readErr)) {
				return retryAfterHeader, true, readErr
			}
			return "", false, apperr.Wrap(apperr.CodeUnavailable, "Failed reading provider response", readErr)
		}
		if int64(len(raw)) > maxBytes {
			return "", false, apperr.New(apperr.CodeUnavailable, "Provider response exceeded size limit").
				WithMsg("errors.mediagen.output_too_large", nil)
		}
		if canRetry && IsRetryableStatus(resp.StatusCode) {
			return retryAfterHeader, true, nil
		}
		return "", false, nil
	})
	if err != nil {
		return nil, "", lastStatus, err
	}
	return lastRaw, lastCT, lastStatus, nil
}

func execHTTPToFileWithRetryObserver(ctx context.Context, client *http.Client, req *http.Request, maxBytes int64, cfg RetryConfig, onRateLimit func(time.Duration)) (string, string, int, int64, []byte, error) {
	const maxErrBodyBytes = 64 * 1024
	var lastStatus int
	var lastCT string
	var lastTmpPath string
	var lastSize int64
	var lastErrBody []byte

	err := execHTTPAttemptLoop(ctx, client, req, cfg, onRateLimit, func(resp *http.Response, canRetry bool) (string, bool, error) {
		lastStatus = resp.StatusCode
		lastCT = resp.Header.Get("Content-Type")
		retryAfterHeader := resp.Header.Get("Retry-After")

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errCap := maxErrBodyBytes
			if maxBytes > 0 && maxBytes < int64(errCap) {
				errCap = int(maxBytes)
			}
			raw, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(errCap)+1))
			lastErrBody = raw
			if readErr != nil {
				if canRetry && (IsRetryableStatus(resp.StatusCode) || IsTransientNetErr(readErr)) {
					return retryAfterHeader, true, readErr
				}
				return "", false, apperr.Wrap(apperr.CodeUnavailable, "Failed reading provider response", readErr)
			}
			if canRetry && IsRetryableStatus(resp.StatusCode) {
				return retryAfterHeader, true, nil
			}
			return "", false, nil
		}

		f, err := os.CreateTemp("", "mediagen-dl-*")
		if err != nil {
			return "", false, apperr.Wrap(apperr.CodeUnavailable, "Failed creating temporary download file", err)
		}
		tmpName := f.Name()
		n, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxBytes+1))
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(tmpName)
			opErr := copyErr
			if opErr == nil {
				opErr = closeErr
			}
			if canRetry && IsTransientNetErr(opErr) {
				return retryAfterHeader, true, opErr
			}
			return "", false, apperr.Wrap(apperr.CodeUnavailable, "Failed downloading provider response", opErr)
		}
		if n > maxBytes {
			_ = os.Remove(tmpName)
			return "", false, apperr.New(apperr.CodeUnavailable, "Provider response exceeded size limit").
				WithMsg("errors.mediagen.output_too_large", nil)
		}
		lastTmpPath = tmpName
		lastSize = n
		lastErrBody = nil
		return "", false, nil
	})
	if err != nil {
		if lastTmpPath != "" {
			_ = os.Remove(lastTmpPath)
			lastTmpPath = ""
		}
		return "", "", lastStatus, 0, lastErrBody, err
	}
	return lastTmpPath, lastCT, lastStatus, lastSize, lastErrBody, nil
}

func (s *Service) retryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    s.cfg.RetryMax,
		InitialDelay:  s.cfg.RetryInitialDelay,
		MaxDelay:      s.cfg.RetryMaxDelay,
		BackoffFactor: s.cfg.RetryBackoffFactor,
	}
}

func (s *Service) rateLimitObserver(ctx context.Context) func(time.Duration) {
	scope, ok := rateLimitScopeFromContext(ctx)
	if !ok || s.limiter == nil {
		return nil
	}
	return func(backoff time.Duration) {
		s.limiter.recordBackoff(scope.owner, scope.cap, backoff)
	}
}

func (s *Service) execHTTP(req *http.Request, maxBytes int64) ([]byte, string, int, error) {
	return s.execHTTPWithRetry(req, maxBytes, s.retryConfig())
}

func (s *Service) execHTTPWithRetry(req *http.Request, maxBytes int64, cfg RetryConfig) ([]byte, string, int, error) {
	return execHTTPWithRetryObserver(req.Context(), s.httpClient, req, maxBytes, cfg, s.rateLimitObserver(req.Context()))
}

func (s *Service) execHTTPToFile(req *http.Request, maxBytes int64) (string, string, int, int64, []byte, error) {
	return execHTTPToFileWithRetryObserver(req.Context(), s.httpClient, req, maxBytes, s.retryConfig(), s.rateLimitObserver(req.Context()))
}

func mapAPIError(status int, body []byte) error {
	msg := extractAPIErrorMessage(body)
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d from provider", status)
	} else {
		msg = fmt.Sprintf("HTTP %d: %s", status, msg)
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return apperr.New(apperr.CodeUnauthorized, msg).WithMsg("errors.mediagen.provider_auth_failed", map[string]any{"detail": msg})
	case status == http.StatusTooManyRequests:
		return apperr.New(apperr.CodeUnavailable, msg).WithMsg("errors.mediagen.rate_limited", map[string]any{"detail": msg})
	case status >= 500:
		return apperr.New(apperr.CodeUnavailable, msg).WithMsg("errors.mediagen.provider_error", map[string]any{"detail": msg})
	default:
		return apperr.New(apperr.CodeBadRequest, msg).WithMsg("errors.mediagen.provider_error", map[string]any{"detail": msg})
	}
}

func extractAPIErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err == nil {
		if errVal, ok := obj["error"].(map[string]any); ok {
			if m, _ := errVal["message"].(string); m != "" {
				return sanitizeProviderSnippet(m)
			}
		}
		if baseResp, ok := obj["base_resp"].(map[string]any); ok {
			if m, _ := baseResp["status_msg"].(string); m != "" {
				return sanitizeProviderSnippet(m)
			}
		}
		for _, k := range []string{"message", "detail", "msg", "error"} {
			if m, _ := obj[k].(string); m != "" {
				return sanitizeProviderSnippet(m)
			}
		}
	}
	return sanitizeProviderSnippet(trimmed)
}

func sanitizeProviderSnippet(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	cleaned := embeddedURLPattern.ReplaceAllStringFunc(raw, func(match string) string {
		if safe := domainmedia.SanitizeRemoteURL(match); safe != "" {
			return safe
		}
		return "[redacted-url]"
	})
	cleaned = logger.RedactString(cleaned)
	cleaned = secretTokenPattern.ReplaceAllString(cleaned, "[redacted]")
	return clipText(cleaned, maxErrorSnippetBytes)
}

func providerUnavailableErr(prefix, rawDetail string) *apperr.Error {
	detail := sanitizeProviderSnippet(rawDetail)
	msg := prefix
	if detail != "" {
		msg = prefix + ": " + detail
	}
	return apperr.Unavailable(msg).WithMsg("errors.mediagen.provider_error", map[string]any{"detail": msg})
}

func writeMultipartField(mw *multipart.Writer, name, value string) error {
	if err := mw.WriteField(name, value); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func clipText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

type localImageData struct {
	Raw      []byte
	Base64   string
	MimeType string
	DataURI  string
	Filename string
}

func (s *Service) readReferenceImage(path string) (localImageData, error) {
	baseName := filepath.Base(path)
	info, err := os.Stat(path)
	if err != nil {
		code := apperr.CodeUnavailable
		if os.IsNotExist(err) {
			code = apperr.CodeNotFound
		}
		return localImageData{}, apperr.Wrap(code, "Cannot access reference image", err).
			WithMsg("errors.mediagen.reference_unreadable", map[string]any{"name": baseName})
	}
	if info.IsDir() {
		return localImageData{}, apperr.Validation("Reference path must be an image file")
	}
	if info.Size() > s.cfg.MaxReferenceBytes {
		return localImageData{}, apperr.Validation("Reference image exceeds maximum allowed size").
			WithMsg("errors.mediagen.reference_too_large", map[string]any{"name": baseName})
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return localImageData{}, apperr.Wrap(apperr.CodeUnavailable, "Cannot read reference image", err).
			WithMsg("errors.mediagen.reference_unreadable", map[string]any{"name": baseName})
	}
	mimeType := strings.Split(http.DetectContentType(raw), ";")[0]
	if mimeType == "application/octet-stream" || !strings.HasPrefix(mimeType, "image/") {
		mimeType = domainmedia.MimeForExt(filepath.Ext(path))
	}
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/png"
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	return localImageData{
		Raw:      raw,
		Base64:   b64,
		MimeType: mimeType,
		DataURI:  "data:" + mimeType + ";base64," + b64,
		Filename: baseName,
	}, nil
}

func (s *Service) decodeDataOrDownload(ctx context.Context, rawDataURIOrB64, remoteURL, fallbackExt string, headers map[string]string, trustedURLs ...string) ([]byte, string, error) {
	if rawDataURIOrB64 != "" {
		if strings.HasPrefix(rawDataURIOrB64, "data:") {
			return parseDataURI(rawDataURIOrB64)
		}
		if strings.HasPrefix(rawDataURIOrB64, "http://") || strings.HasPrefix(rawDataURIOrB64, "https://") {
			return s.downloadURL(ctx, rawDataURIOrB64, headers, trustedURLs...)
		}
		dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawDataURIOrB64))
		if err != nil {
			return nil, "", apperr.Wrap(apperr.CodeUnavailable, "Invalid base64 payload from provider", err)
		}
		return dec, domainmedia.MimeForExt(fallbackExt), nil
	}
	if remoteURL != "" {
		if strings.HasPrefix(remoteURL, "data:") {
			return parseDataURI(remoteURL)
		}
		return s.downloadURL(ctx, remoteURL, headers, trustedURLs...)
	}
	return nil, "", apperr.Unavailable("Provider returned neither media data nor download URL")
}

func (s *Service) decodeMediaOrDownload(ctx context.Context, rawDataURIOrB64, remoteURL, fallbackExt string, headers map[string]string, trustedURLs ...string) (mediaOutput, error) {
	if rawDataURIOrB64 != "" {
		if strings.HasPrefix(rawDataURIOrB64, "data:") {
			raw, ct, err := parseDataURI(rawDataURIOrB64)
			return mediaOutput{data: raw, mimeType: ct}, err
		}
		if strings.HasPrefix(rawDataURIOrB64, "http://") || strings.HasPrefix(rawDataURIOrB64, "https://") {
			return s.downloadURLToFile(ctx, rawDataURIOrB64, headers, trustedURLs...)
		}
		dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawDataURIOrB64))
		if err != nil {
			return mediaOutput{}, apperr.Wrap(apperr.CodeUnavailable, "Invalid base64 payload from provider", err)
		}
		return mediaOutput{data: dec, mimeType: domainmedia.MimeForExt(fallbackExt)}, nil
	}
	if remoteURL != "" {
		if strings.HasPrefix(remoteURL, "data:") {
			raw, ct, err := parseDataURI(remoteURL)
			return mediaOutput{data: raw, mimeType: ct}, err
		}
		return s.downloadURLToFile(ctx, remoteURL, headers, trustedURLs...)
	}
	return mediaOutput{}, apperr.Unavailable("Provider returned neither media data nor download URL")
}

func parseDataURI(uri string) ([]byte, string, error) {
	comma := strings.IndexByte(uri, ',')
	if comma < 0 {
		return nil, "", apperr.Unavailable("Malformed data URI from provider")
	}
	meta := uri[len("data:"):comma]
	payload := uri[comma+1:]
	mimeType := strings.TrimSpace(strings.Split(meta, ";")[0])
	var raw []byte
	var err error
	if strings.Contains(meta, ";base64") {
		raw, err = base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	} else {
		var unesc string
		unesc, err = url.PathUnescape(payload)
		raw = []byte(unesc)
	}
	if err != nil {
		return nil, "", apperr.Wrap(apperr.CodeUnavailable, "Failed decoding data URI", err)
	}
	return raw, mimeType, nil
}

func readHeadBytes(out mediaOutput, max int) []byte {
	if out.filePath == "" {
		if len(out.data) <= max {
			return out.data
		}
		return out.data[:max]
	}
	f, err := os.Open(out.filePath)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, max)
	n, _ := io.ReadFull(f, buf)
	return buf[:n]
}

func (s *Service) saveOutput(ctx context.Context, kind domainmedia.Kind, outputDir, prefix, mimeType, fallbackExt string, data []byte, t targetSpec) (domainmedia.Result, error) {
	return s.saveMediaOutput(ctx, kind, outputDir, prefix, fallbackExt, mediaOutput{data: data, mimeType: mimeType}, t)
}

func (s *Service) saveMediaOutput(ctx context.Context, kind domainmedia.Kind, outputDir, prefix, fallbackExt string, out mediaOutput, t targetSpec) (domainmedia.Result, error) {
	defer out.cleanup()

	mimeType := out.mimeType
	if mimeType == "" || mimeType == "application/octet-stream" {
		if kind == domainmedia.KindImage {
			sniff := strings.Split(http.DetectContentType(readHeadBytes(out, 512)), ";")[0]
			if strings.HasPrefix(sniff, "image/") {
				mimeType = sniff
			}
		}
		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = domainmedia.MimeForExt(fallbackExt)
		}
	}
	ext := domainmedia.ExtForMime(mimeType, fallbackExt)
	if ext == "" {
		ext = fallbackExt
	}

	mediaDir := s.getMediaDir()
	dir := strings.TrimSpace(outputDir)
	if dir == "" {
		sub := "images"
		switch kind {
		case domainmedia.KindVideo:
			sub = "videos"
		case domainmedia.KindTTS:
			sub = "tts"
		}
		dir = filepath.Join(mediaDir, sub)
	}
	if !filepath.IsAbs(dir) && mediaDir != "" {
		dir = filepath.Join(mediaDir, dir)
	}

	name := uniqueFilename(prefix, ext)
	fullPath := filepath.Join(dir, name)
	if err := engine.CheckFile(ctx, fullPath, true); err != nil {
		return domainmedia.Result{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return domainmedia.Result{}, apperr.Wrap(apperr.CodeUnavailable, "Failed creating media output directory", err)
	}

	var sizeBytes int64
	if out.filePath != "" {
		info, err := os.Stat(out.filePath)
		if err != nil {
			return domainmedia.Result{}, apperr.Wrap(apperr.CodeUnavailable, "Failed inspecting downloaded media output", err)
		}
		sizeBytes = info.Size()
		if err := fsutil.WriteAtomic(fullPath, 0o600, func(w io.Writer) error {
			src, err := os.Open(out.filePath)
			if err != nil {
				return err
			}
			defer src.Close()
			_, err = io.Copy(w, src)
			return err
		}); err != nil {
			return domainmedia.Result{}, apperr.Wrap(apperr.CodeUnavailable, "Failed writing generated media output", err)
		}
	} else {
		sizeBytes = int64(len(out.data))
		if err := fsutil.WriteFileAtomic(fullPath, out.data, 0o600); err != nil {
			return domainmedia.Result{}, apperr.Wrap(apperr.CodeUnavailable, "Failed writing generated media output", err)
		}
	}

	if reg := s.getRegistrar(); reg != nil {
		sourceNode := "mediagen"
		switch kind {
		case domainmedia.KindImage:
			sourceNode = "tool.generate_image"
		case domainmedia.KindVideo:
			sourceNode = "tool.generate_video"
		case domainmedia.KindTTS:
			sourceNode = "tool.generate_tts"
		}
		regRoot := mediaDir
		if strings.TrimSpace(outputDir) != "" {
			regRoot = outputDir
		}
		if regRoot == "" {
			regRoot = dir
		}
		asset, err := reg.RegisterAsset(ctx, media.RegisterAssetParams{
			Kind:           string(kind),
			SourceNode:     sourceNode,
			FilePath:       fullPath,
			RegisteredRoot: regRoot,
			MIMEType:       mimeType,
			SizeBytes:      sizeBytes,
		})
		if err != nil {
			s.log.WarnContext(ctx, "mediagen: failed to register generated media asset",
				"kind", string(kind),
				"source_node", sourceNode,
				"error", err,
			)
		} else if asset != nil {
			s.setAssetID(fullPath, asset.ID)
		}
	}

	return domainmedia.Result{
		Kind:      kind,
		Path:      fullPath,
		LocalURL:  fullPath,
		MimeType:  mimeType,
		Provider:  t.providerName,
		Model:     t.modelID,
		SizeBytes: sizeBytes,
	}, nil
}

func (s *Service) setAssetID(path, assetID string) {
	s.assetsMu.Lock()
	defer s.assetsMu.Unlock()
	if s.assets == nil {
		s.assets = make(map[string]string)
	}
	if _, exists := s.assets[path]; !exists {
		if len(s.assets) >= maxCachedAssetIDs && len(s.assetsOrder) > 0 {
			evict := s.assetsOrder[0]
			delete(s.assets, evict)
			s.assetsOrder = s.assetsOrder[1:]
			if cap(s.assetsOrder) > maxCachedAssetIDs*2 {
				compacted := make([]string, len(s.assetsOrder), maxCachedAssetIDs)
				copy(compacted, s.assetsOrder)
				s.assetsOrder = compacted
			}
		}
		s.assetsOrder = append(s.assetsOrder, path)
	}
	s.assets[path] = assetID
}

// AssetID returns the registered asset ID for a generated media file path, or empty string if not found.
func (s *Service) AssetID(path string) string {
	s.assetsMu.RLock()
	defer s.assetsMu.RUnlock()
	if s.assets == nil {
		return ""
	}
	return s.assets[path]
}

// SetRegistrar sets or updates the media asset registrar thread-safely.
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

func uniqueFilename(prefix, ext string) string {
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	ts := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s-%s-%s%s", prefix, ts, hex.EncodeToString(rnd[:]), ext)
}

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

// SetMediaDir updates the root media directory thread-safely.
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
func (s *Service) StagingDir() string {
	s.mediaMu.RLock()
	defer s.mediaMu.RUnlock()
	if strings.TrimSpace(s.stagingDir) != "" {
		return s.stagingDir
	}
	if strings.TrimSpace(s.mediaDir) != "" {
		return filepath.Join(s.mediaDir, "staging")
	}
	return ""
}

func (s *Service) resolveStageTarget(subDir, name string) (string, string, error) {
	baseStagingDir := s.StagingDir()
	if baseStagingDir == "" {
		return "", "", apperr.Validation("Staging directory is not configured")
	}
	baseAbs, err := filepath.Abs(baseStagingDir)
	if err != nil {
		return "", "", apperr.Wrap(apperr.CodeValidation, "Invalid staging directory", err)
	}
	dir := baseAbs
	if cleanSub := strings.TrimSpace(subDir); cleanSub != "" {
		if filepath.IsAbs(cleanSub) || filepath.VolumeName(cleanSub) != "" || strings.HasPrefix(cleanSub, "/") || strings.HasPrefix(cleanSub, `\`) {
			return "", "", apperr.Validation("Staging subdirectory must be relative")
		}
		dir = filepath.Clean(filepath.Join(baseAbs, cleanSub))
		if !fsutil.IsSubpath(baseAbs, dir, true) {
			return "", "", apperr.Validation("Staging subdirectory escapes staging directory")
		}
	}
	fileName := strings.TrimSpace(name)
	if fileName == "" {
		fileName = uniqueFilename("staged", ".bin")
	} else {
		if filepath.IsAbs(fileName) || strings.ContainsAny(fileName, `/\`) || fileName == "." || fileName == ".." || filepath.Base(fileName) != fileName {
			return "", "", apperr.Validation("Staging filename must be a plain basename")
		}
	}
	stagedPath := filepath.Clean(filepath.Join(dir, fileName))
	if !fsutil.IsSubpath(baseAbs, stagedPath, false) {
		return "", "", apperr.Validation("Staged path escapes staging directory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", apperr.Wrap(apperr.CodeUnavailable, "Failed creating staging directory", err)
	}
	return dir, stagedPath, nil
}

// StageFile writes data to a unique file in the staging directory and acquires an active lease on it.
// The caller must invoke the returned release function when the job completes.
func (s *Service) StageFile(subDir, name string, data []byte) (string, func(), error) {
	if s.cfg.MaxOutputBytes > 0 && int64(len(data)) > s.cfg.MaxOutputBytes {
		return "", nil, apperr.New(apperr.CodeUnavailable, "Staged media payload exceeded size limit").
			WithMsg("errors.mediagen.output_too_large", nil)
	}
	_, stagedPath, err := s.resolveStageTarget(subDir, name)
	if err != nil {
		return "", nil, err
	}
	if err := fsutil.WriteFileAtomic(stagedPath, data, 0o600); err != nil {
		return "", nil, apperr.Wrap(apperr.CodeUnavailable, "Failed writing staged media file", err)
	}
	release := s.LeaseFile(stagedPath)
	return stagedPath, release, nil
}

// StageStream streams bounded content from reader r into a staging file atomically and acquires an active lease on it.
func (s *Service) StageStream(subDir, name string, r io.Reader, maxBytes int64) (string, func(), error) {
	if maxBytes <= 0 {
		maxBytes = s.cfg.MaxOutputBytes
	}
	_, stagedPath, err := s.resolveStageTarget(subDir, name)
	if err != nil {
		return "", nil, err
	}

	err = fsutil.WriteAtomic(stagedPath, 0o600, func(w io.Writer) error {
		n, copyErr := io.Copy(w, io.LimitReader(r, maxBytes+1))
		if copyErr != nil {
			return copyErr
		}
		if n > maxBytes {
			return errStageTooLarge
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errStageTooLarge) {
			return "", nil, apperr.New(apperr.CodeUnavailable, "Staged media payload exceeded size limit").
				WithMsg("errors.mediagen.output_too_large", nil)
		}
		return "", nil, apperr.Wrap(apperr.CodeUnavailable, "Failed streaming staged data", err)
	}

	release := s.LeaseFile(stagedPath)
	return stagedPath, release, nil
}

// leaseInputs acquires active leases on all provided file paths and returns a combined release function.
func (s *Service) leaseInputs(paths ...string) func() {
	var releases []func()
	for _, p := range paths {
		if strings.TrimSpace(p) != "" {
			releases = append(releases, s.LeaseFile(p))
		}
	}
	return func() {
		for _, rel := range releases {
			if rel != nil {
				rel()
			}
		}
	}
}

// prepareJobContext sets up a bounded request timeout context.
// Single-shot requests (isVideo == false) attach progress.KeepAlive, whereas long async video/lip-sync
// polling flows report real progress ticks via pollUntil so a hung socket is never masked (§6).
func (s *Service) prepareJobContext(ctx context.Context, isVideo bool) (context.Context, func()) {
	callCtx, cancel := s.requestTimeoutCtx(ctx, isVideo)
	if isVideo {
		return callCtx, cancel
	}
	stopKA := progress.KeepAlive(callCtx)
	cleanup := func() {
		stopKA()
		cancel()
	}
	return callCtx, cleanup
}

// readReferenceImages reads and encodes multiple local reference images.
func (s *Service) readReferenceImages(paths []string) ([]localImageData, error) {
	refs := make([]localImageData, 0, len(paths))
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		img, err := s.readReferenceImage(p)
		if err != nil {
			return nil, err
		}
		refs = append(refs, img)
	}
	return refs, nil
}

// LeaseRegistry tracks active job leases on files/directories to protect them from background GC.
type LeaseRegistry struct {
	mu     sync.RWMutex
	leases map[string]int // normalized absolute path -> active lease refcount
}

// NewLeaseRegistry creates a new active lease registry.
func NewLeaseRegistry() *LeaseRegistry {
	return &LeaseRegistry{
		leases: make(map[string]int),
	}
}

var defaultLeaseRegistry = NewLeaseRegistry()

func normalizeLeasePath(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	return strings.ToLower(p)
}

// Acquire registers an active lease on path and returns a release callback.
// Releasing is idempotent.
func (r *LeaseRegistry) Acquire(path string) func() {
	key := normalizeLeasePath(path)
	if key == "" {
		return func() {}
	}
	r.mu.Lock()
	r.leases[key]++
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			r.leases[key]--
			if r.leases[key] <= 0 {
				delete(r.leases, key)
			}
			r.mu.Unlock()
		})
	}
}

// Release drops one lease reference on path.
func (r *LeaseRegistry) Release(path string) {
	key := normalizeLeasePath(path)
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leases[key]--
	if r.leases[key] <= 0 {
		delete(r.leases, key)
	}
}

// IsLeased reports whether path or any file within/enclosing path has an active lease.
func (r *LeaseRegistry) IsLeased(path string) bool {
	key := normalizeLeasePath(path)
	if key == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Direct match
	if r.leases[key] > 0 {
		return true
	}

	// Check if any leased file is inside this directory: key + separator prefix
	prefix := key + string(filepath.Separator)
	for leasedPath := range r.leases {
		if strings.HasPrefix(leasedPath, prefix) {
			return true
		}
	}

	// Check if this path is inside a leased directory: leasedPath + separator prefix
	for leasedPath := range r.leases {
		if strings.HasPrefix(key, leasedPath+string(filepath.Separator)) {
			return true
		}
	}

	return false
}

// ActiveLeases returns all normalized paths currently holding an active lease.
func (r *LeaseRegistry) ActiveLeases() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.leases))
	for k := range r.leases {
		out = append(out, k)
	}
	return out
}

// Count returns the number of unique active lease paths.
func (r *LeaseRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.leases)
}

// Prune scans dir and removes unleased files whose modTime is older than ttl.
// Expired files WITH an active lease are strictly preserved.
func (r *LeaseRegistry) Prune(dir string, ttl time.Duration, now ...time.Time) (int, error) {
	if strings.TrimSpace(dir) == "" {
		return 0, nil
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if ttl <= 0 {
		ttl = config.Default(config.ModeDesktop).Media.StagingTTL
	}
	cur := time.Now()
	if len(now) > 0 && !now[0].IsZero() {
		cur = now[0]
	}
	cutoff := cur.Add(-ttl)

	type item struct {
		path    string
		isDir   bool
		modTime time.Time
	}
	var files []item
	var dirs []item

	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if p == dir {
			return nil
		}
		if info.IsDir() {
			dirs = append(dirs, item{path: p, isDir: true, modTime: info.ModTime()})
		} else {
			files = append(files, item{path: p, isDir: false, modTime: info.ModTime()})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	pruned := 0
	// 1. Prune expired unleased files
	for _, f := range files {
		if r.IsLeased(f.path) {
			continue // strictly protected
		}
		if f.modTime.Before(cutoff) {
			if err := os.Remove(f.path); err == nil {
				pruned++
			}
		}
	}

	// 2. Prune empty unleased directories (deepest first)
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i].path) > len(dirs[j].path)
	})
	for _, d := range dirs {
		if r.IsLeased(d.path) {
			continue
		}
		entries, err := os.ReadDir(d.path)
		if err == nil && len(entries) == 0 {
			_ = os.Remove(d.path)
		}
	}

	return pruned, nil
}

// Package-level lease functions delegating to defaultLeaseRegistry:

// LeaseFile registers an active lease on path and returns a release callback.
func LeaseFile(path string) func() {
	return defaultLeaseRegistry.Acquire(path)
}

// AcquireLease registers an active lease on path and returns a release callback.
func AcquireLease(path string) func() {
	return defaultLeaseRegistry.Acquire(path)
}

// ReleaseLease drops one lease reference for path.
func ReleaseLease(path string) {
	defaultLeaseRegistry.Release(path)
}

// IsLeased reports whether path is protected by an active lease.
func IsLeased(path string) bool {
	return defaultLeaseRegistry.IsLeased(path)
}

// PruneStaging scans dir and deletes unleased files older than ttl.
// Any file with an active lease is strictly protected.
func PruneStaging(dir string, ttl time.Duration, now ...time.Time) (int, error) {
	return defaultLeaseRegistry.Prune(dir, ttl, now...)
}

// StartStagingGC runs periodic background staging garbage collection until ctx is canceled.
// It executes an initial scan immediately, then ticks at the given interval.
func StartStagingGC(ctx context.Context, dir string, ttl, interval time.Duration) {
	defMedia := config.Default(config.ModeDesktop).Media
	if ttl <= 0 {
		ttl = defMedia.StagingTTL
	}
	if interval <= 0 {
		interval = defMedia.StagingGCInterval
	}
	if _, err := PruneStaging(dir, ttl); err != nil {
		slog.Default().WarnContext(ctx, "mediagen: staging GC prune failed", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := PruneStaging(dir, ttl); err != nil {
				slog.Default().WarnContext(ctx, "mediagen: staging GC prune failed", "error", err)
			}
		}
	}
}

// Service methods delegating to the service's LeaseRegistry:

// LeaseFile registers an active lease on path for this service instance.
func (s *Service) LeaseFile(path string) func() {
	if s.leases == nil {
		return defaultLeaseRegistry.Acquire(path)
	}
	return s.leases.Acquire(path)
}

// AcquireLease registers an active lease on path for this service instance.
func (s *Service) AcquireLease(path string) func() {
	return s.LeaseFile(path)
}

// ReleaseLease drops one lease reference for path.
func (s *Service) ReleaseLease(path string) {
	if s.leases == nil {
		defaultLeaseRegistry.Release(path)
		return
	}
	s.leases.Release(path)
}

// IsLeased reports whether path is protected by an active lease.
func (s *Service) IsLeased(path string) bool {
	if s.leases == nil {
		return defaultLeaseRegistry.IsLeased(path)
	}
	return s.leases.IsLeased(path)
}

// ActiveLeases returns currently held active leases for this service instance.
func (s *Service) ActiveLeases() []string {
	if s.leases == nil {
		return defaultLeaseRegistry.ActiveLeases()
	}
	return s.leases.ActiveLeases()
}

// PruneStaging scans dir (or s.StagingDir() if empty) and deletes unleased expired files.
func (s *Service) PruneStaging(dir string, ttl time.Duration, now ...time.Time) (int, error) {
	if strings.TrimSpace(dir) == "" {
		dir = s.StagingDir()
	}
	if ttl <= 0 {
		ttl = s.stagingTTL
	}
	reg := s.leases
	if reg == nil {
		reg = defaultLeaseRegistry
	}
	return reg.Prune(dir, ttl, now...)
}

// StartStagingGC runs periodic background staging garbage collection until ctx is canceled.
func (s *Service) StartStagingGC(ctx context.Context, dir string, ttl, interval time.Duration) {
	if strings.TrimSpace(dir) == "" {
		dir = s.StagingDir()
	}
	if ttl <= 0 {
		ttl = s.stagingTTL
	}
	if interval <= 0 {
		interval = s.stagingGCInterval
	}
	if _, err := s.PruneStaging(dir, ttl); err != nil {
		s.log.WarnContext(ctx, "mediagen: staging GC prune failed", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.PruneStaging(dir, ttl); err != nil {
				s.log.WarnContext(ctx, "mediagen: staging GC prune failed", "error", err)
			}
		}
	}
}

// StartStagingGCAuto runs staging GC with configured defaults on the service.
func (s *Service) StartStagingGCAuto(ctx context.Context) {
	s.StartStagingGC(ctx, "", s.stagingTTL, s.stagingGCInterval)
}
