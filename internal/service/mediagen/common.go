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
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/fsutil"
	"app/internal/progress"
	providersvc "app/internal/service/provider"
)

const (
	maxJSONResponseBytes  = 16 << 20 // 16 MiB handles inline base64 images/audio inside JSON payloads
	maxErrorSnippetBytes  = 512
	defaultPollPercentCap = 90.0
)

// ProviderResolver resolves configured models/credentials (satisfied by *providersvc.Service).
type ProviderResolver interface {
	ResolveModel(ctx context.Context, p domainprovider.Purpose, ref string) (providersvc.ResolvedModel, error)
	VertexToken(ctx context.Context, raw string) (accessToken, projectID string, expiresIn time.Duration, err error)
}

// Options configures Service construction.
type Options struct {
	Providers  ProviderResolver
	HTTPClient *http.Client
	MediaDir   string
	Config     config.MediaGenConfig
	Logger     *slog.Logger
}

type cachedVertexToken struct {
	accessToken string
	projectID   string
	expiresAt   time.Time
}

// Service unifies media generation across built-in providers and custom provider endpoints.
type Service struct {
	providers  ProviderResolver
	httpClient *http.Client
	mediaDir   string
	cfg        config.MediaGenConfig
	log        *slog.Logger
	limiter    *rateLimiter

	vertexMu    sync.Mutex
	vertexCache map[string]cachedVertexToken
}

// New constructs a unified media generation service.
func New(o Options) *Service {
	client := o.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute}
	}
	clientCopy := withRedirectGuard(client)

	cfg := o.Config
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 2 * time.Minute
	}
	if cfg.VideoTimeout <= 0 {
		cfg.VideoTimeout = 15 * time.Minute
	}
	if cfg.VideoPollInterval <= 0 {
		cfg.VideoPollInterval = 4 * time.Second
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 256 << 20
	}
	if cfg.MaxReferenceBytes <= 0 {
		cfg.MaxReferenceBytes = 20 << 20
	}

	log := o.Logger
	if log == nil {
		log = slog.Default()
	}

	return &Service{
		providers:   o.Providers,
		httpClient:  clientCopy,
		mediaDir:    o.MediaDir,
		cfg:         cfg,
		log:         log,
		limiter:     newRateLimiter(),
		vertexCache: make(map[string]cachedVertexToken),
	}
}

func withRedirectGuard(base *http.Client) *http.Client {
	c := *base
	prev := base.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := domainprovider.ValidateOutboundURL(req.URL.String()); err != nil {
			return fmt.Errorf("blocked redirect target: %w", err)
		}
		if prev != nil {
			return prev(req, via)
		}
		return nil
	}
	return &c
}

type targetSpec struct {
	ownerID      string
	providerName string
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
	t := targetSpec{
		modelID:    res.Model.ModelID,
		modelFacts: res.Model,
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
		case domainprovider.PurposeImage:
			return "openai-images", nil
		case domainprovider.PurposeVideo:
			return "openai-video", nil
		case domainprovider.PurposeTTS:
			return "openai-tts", nil
		}
	case domainprovider.IDGeminiAIStudio:
		switch purpose {
		case domainprovider.PurposeImage:
			return "gemini-aistudio-image", nil
		case domainprovider.PurposeVideo:
			return "gemini-aistudio-video", nil
		case domainprovider.PurposeTTS:
			return "gemini-aistudio-tts", nil
		}
	case domainprovider.IDGeminiVertex:
		switch purpose {
		case domainprovider.PurposeImage:
			return "gemini-vertex-image", nil
		case domainprovider.PurposeVideo:
			return "gemini-vertex-video", nil
		case domainprovider.PurposeTTS:
			return "gemini-vertex-tts", nil
		}
	case domainprovider.IDArk, domainprovider.IDArkAgentPlan:
		switch purpose {
		case domainprovider.PurposeImage:
			return "ark-image", nil
		case domainprovider.PurposeVideo:
			return "ark-video", nil
		}
	case domainprovider.IDDashScope:
		switch purpose {
		case domainprovider.PurposeImage:
			return "dashscope-image", nil
		case domainprovider.PurposeVideo:
			return "dashscope-video", nil
		case domainprovider.PurposeTTS:
			return "dashscope-tts", nil
		}
	case domainprovider.IDMiniMax:
		switch purpose {
		case domainprovider.PurposeImage:
			return "minimax-image", nil
		case domainprovider.PurposeVideo:
			return "minimax-video", nil
		case domainprovider.PurposeTTS:
			return "minimax-tts", nil
		}
	case domainprovider.IDKling:
		switch purpose {
		case domainprovider.PurposeImage:
			return "kling-image", nil
		case domainprovider.PurposeVideo:
			return "kling-video", nil
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
		case domainprovider.PurposeImage:
			return "grok-image", nil
		case domainprovider.PurposeVideo:
			return "grok-video", nil
		}
	case domainprovider.IDAgnes:
		switch purpose {
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
	}

	// Fallback when custom model endpoint wasn't specified explicitly
	if format == domainprovider.DiscoveryGemini {
		switch purpose {
		case domainprovider.PurposeImage:
			return "gemini-aistudio-image", nil
		case domainprovider.PurposeVideo:
			return "gemini-aistudio-video", nil
		case domainprovider.PurposeTTS:
			return "gemini-aistudio-tts", nil
		}
	}
	if format == domainprovider.DiscoveryOpenAI || format == "" {
		switch purpose {
		case domainprovider.PurposeImage:
			return "openai-images", nil
		case domainprovider.PurposeVideo:
			return "openai-video", nil
		case domainprovider.PurposeTTS:
			return "openai-tts", nil
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
	if cached, ok := s.vertexCache[cacheKey]; ok && time.Until(cached.expiresAt) > 2*time.Minute {
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
		expiresIn = 30 * time.Minute
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
		tok, err := providersvc.KlingJWT(t.accessKey, t.secretKey, time.Now())
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
		return "https://ark.cn-beijing.volces.com/api/v3"
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

	raw, _, status, err := s.execHTTP(req, maxJSONResponseBytes)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, &buf)
	if err != nil {
		return apperr.Internal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	raw, _, status, err := s.execHTTP(req, maxJSONResponseBytes)
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

func (s *Service) doBinary(ctx context.Context, method, targetURL string, headers map[string]string, reqBody any) ([]byte, string, error) {
	if err := domainprovider.ValidateOutboundURL(targetURL); err != nil {
		return nil, "", apperr.Wrap(apperr.CodeValidation, "Invalid provider URL", err)
	}
	var bodyReader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return nil, "", apperr.Internal(err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return nil, "", apperr.Internal(err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
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

func (s *Service) downloadURL(ctx context.Context, rawURL string, headers map[string]string) ([]byte, string, error) {
	if err := domainprovider.ValidateOutboundURL(rawURL); err != nil {
		return nil, "", apperr.Wrap(apperr.CodeUnavailable, "Blocked unsafe remote media URL", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", apperr.Internal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
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

func (s *Service) execHTTP(req *http.Request, maxBytes int64) ([]byte, string, int, error) {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, "", 0, apperr.From(req.Context().Err())
		}
		return nil, "", 0, apperr.Wrap(apperr.CodeUnavailable, "Provider HTTP request failed", err).
			WithMsg("errors.mediagen.request_failed", nil)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", resp.StatusCode, apperr.Wrap(apperr.CodeUnavailable, "Failed reading provider response", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, "", resp.StatusCode, apperr.New(apperr.CodeUnavailable, "Provider response exceeded size limit").
			WithMsg("errors.mediagen.output_too_large", nil)
	}
	return raw, resp.Header.Get("Content-Type"), resp.StatusCode, nil
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
				return clipText(m, maxErrorSnippetBytes)
			}
		}
		if baseResp, ok := obj["base_resp"].(map[string]any); ok {
			if m, _ := baseResp["status_msg"].(string); m != "" {
				return clipText(m, maxErrorSnippetBytes)
			}
		}
		for _, k := range []string{"message", "detail", "msg", "error"} {
			if m, _ := obj[k].(string); m != "" {
				return clipText(m, maxErrorSnippetBytes)
			}
		}
	}
	return clipText(trimmed, maxErrorSnippetBytes)
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
	info, err := os.Stat(path)
	if err != nil {
		return localImageData{}, apperr.Wrap(apperr.CodeValidation, "Cannot access reference image: "+filepath.Base(path), err).
			WithMsg("errors.mediagen.reference_unreadable", map[string]any{"name": filepath.Base(path)})
	}
	if info.IsDir() {
		return localImageData{}, apperr.Validation("Reference path is a directory: " + filepath.Base(path))
	}
	if info.Size() > s.cfg.MaxReferenceBytes {
		return localImageData{}, apperr.Validation("Reference image exceeds maximum allowed size").
			WithMsg("errors.mediagen.reference_too_large", map[string]any{"name": filepath.Base(path)})
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return localImageData{}, apperr.Wrap(apperr.CodeValidation, "Cannot read reference image", err)
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
		Filename: filepath.Base(path),
	}, nil
}

func (s *Service) decodeDataOrDownload(ctx context.Context, rawDataURIOrB64, remoteURL, fallbackExt string, headers map[string]string) ([]byte, string, error) {
	if rawDataURIOrB64 != "" {
		if strings.HasPrefix(rawDataURIOrB64, "data:") {
			return parseDataURI(rawDataURIOrB64)
		}
		if strings.HasPrefix(rawDataURIOrB64, "http://") || strings.HasPrefix(rawDataURIOrB64, "https://") {
			return s.downloadURL(ctx, rawDataURIOrB64, headers)
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
		return s.downloadURL(ctx, remoteURL, headers)
	}
	return nil, "", apperr.Unavailable("Provider returned neither media data nor download URL")
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

func (s *Service) saveOutput(kind domainmedia.Kind, outputDir, prefix, mimeType, fallbackExt string, data []byte, t targetSpec) (domainmedia.Result, error) {
	if mimeType == "" || mimeType == "application/octet-stream" {
		if kind == domainmedia.KindImage {
			sniff := strings.Split(http.DetectContentType(data), ";")[0]
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

	dir := strings.TrimSpace(outputDir)
	if dir == "" {
		sub := "images"
		switch kind {
		case domainmedia.KindVideo:
			sub = "videos"
		case domainmedia.KindTTS:
			sub = "tts"
		}
		dir = filepath.Join(s.mediaDir, "generated", sub)
	}
	if !filepath.IsAbs(dir) && s.mediaDir != "" {
		dir = filepath.Join(s.mediaDir, dir)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return domainmedia.Result{}, apperr.Internal(err)
	}

	name := uniqueFilename(prefix, ext)
	fullPath := filepath.Join(dir, name)
	if err := fsutil.WriteFileAtomic(fullPath, data, 0o600); err != nil {
		return domainmedia.Result{}, apperr.Internal(err)
	}

	return domainmedia.Result{
		Kind:      kind,
		Path:      fullPath,
		LocalURL:  fullPath,
		MimeType:  mimeType,
		Provider:  t.providerName,
		Model:     t.modelID,
		SizeBytes: int64(len(data)),
	}, nil
}

func uniqueFilename(prefix, ext string) string {
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	ts := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s-%s-%s%s", prefix, ts, hex.EncodeToString(rnd[:]), ext)
}

func (s *Service) pollUntil(ctx context.Context, msgKey string, fallbackMsg string, fn func() (done bool, err error)) error {
	start := time.Now()
	pollNum := 0
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
		pct := 20.0 + (elapsed.Seconds()/180.0)*(defaultPollPercentCap-20.0)
		if pct > defaultPollPercentCap {
			pct = defaultPollPercentCap
		}
		progress.Report(ctx, progress.Update{
			Percent: pct,
			Message: fallbackMsg,
			Key:     msgKey,
			Params:  map[string]string{"attempt": fmt.Sprintf("%d", pollNum)},
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
