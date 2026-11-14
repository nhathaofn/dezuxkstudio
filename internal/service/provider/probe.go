package provider

import (
	"cmp"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"crypto"
	"crypto/rand"

	"app/internal/apperr"
	domain "app/internal/domain/provider"
	"app/internal/progress"
)

// Outcome re-exports the domain outcome so callers need one import.
type Outcome = domain.Outcome

const (
	OutcomeOK            = domain.OutcomeOK
	OutcomeAuthFailed    = domain.OutcomeAuthFailed
	OutcomeRateLimited   = domain.OutcomeRateLimited
	OutcomeNotFound      = domain.OutcomeNotFound
	OutcomeHTTPError     = domain.OutcomeHTTPError
	OutcomeTimeout       = domain.OutcomeTimeout
	OutcomeNetwork       = domain.OutcomeNetwork
	OutcomeInvalid       = domain.OutcomeInvalid
	OutcomeSecretMissing = domain.OutcomeSecretMissing
	OutcomeModelsFailed  = domain.OutcomeModelsFailed
)

// TestResult is the outcome of a connectivity test. Detail never contains a secret.
type TestResult struct {
	Outcome   Outcome
	Status    int
	LatencyMs int64
	Detail    string
	Models    []string
}

// OK reports success.
func (r TestResult) OK() bool { return r.Outcome == OutcomeOK }

// probeError carries a classified failure out of a probe.
type probeError struct {
	outcome Outcome
	status  int
	detail  string
}

func (e *probeError) Error() string { return string(e.outcome) + ": " + e.detail }

func fail(o Outcome, status int, detail string) error {
	return &probeError{outcome: o, status: status, detail: detail}
}

// TestCredential checks a saved credential against the provider's API.
func (s *Service) TestCredential(ctx context.Context, id string) (TestResult, error) {
	c, err := s.repo.GetCredential(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	m, err := lookup(c.ProviderID)
	if err != nil {
		return TestResult{}, err
	}
	r, err := s.resolveCredential(m, c)
	var res TestResult
	switch {
	case isSecretMissing(err):
		res = TestResult{Outcome: domain.OutcomeSecretMissing, Detail: "the stored key is gone; enter it again"}
	case err != nil:
		return TestResult{}, err
	default:
		res, err = s.run(ctx, allSecrets(r), func(ctx context.Context) ([]string, error) { return s.probeBuiltin(ctx, m, r) })
		if err != nil {
			return TestResult{}, err
		}
	}
	s.recordHealth(ctx, func(h domain.Health) error { return s.repo.SetCredentialHealth(ctx, id, h) }, res)
	return res, nil
}

func isSecretMissing(err error) bool {
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeIntegrity {
		return false
	}
	v, _ := ae.Details["secret_missing"].(bool)
	return v
}

// recordHealth stores a test result and notifies listeners (best effort: a failed write only loses the badge).
func (s *Service) recordHealth(ctx context.Context, set func(domain.Health) error, r TestResult) {
	if set(domain.Health{Outcome: r.Outcome, Detail: r.Detail, TestedAt: time.Now()}) == nil {
		s.changed()
	}
	_ = ctx
}

// TestCustom checks a saved custom provider by listing its models.
func (s *Service) TestCustom(ctx context.Context, id string) (TestResult, error) {
	rc, err := s.ResolveCustom(ctx, id)
	var res TestResult
	switch {
	case isSecretMissing(err):
		res = TestResult{Outcome: domain.OutcomeSecretMissing, Detail: "the stored key is gone; enter it again"}
	case err != nil:
		return TestResult{}, err
	default:
		res, err = s.run(ctx, []string{rc.APIKey}, func(ctx context.Context) ([]string, error) {
			models, err := s.discover(ctx, rc.Provider.DiscoveryFormat, rc.Provider.BaseURL, rc.APIKey)
			return discoveredIDs(models), err
		})
		if err != nil {
			return TestResult{}, err
		}
	}
	s.recordHealth(ctx, func(h domain.Health) error { return s.repo.SetCustomHealth(ctx, id, h) }, res)
	return res, nil
}

// DiscoverRequest lists the models of a custom endpoint. When CustomID is set and APIKey is empty the
// stored key is used, so the edit form can re-discover without asking for the key again.
type DiscoverRequest struct {
	CustomID        string
	DiscoveryFormat domain.DiscoveryFormat
	BaseURL         string
	APIKey          string
}

// DiscoveredModel is a model found on a custom endpoint with its inferred endpoint.
type DiscoveredModel struct {
	ModelID    string
	Endpoint   domain.Endpoint
	Capability domain.Capability
}

func discovered(id string, e domain.Endpoint) DiscoveredModel {
	spec, _ := domain.EndpointByKey(e)
	return DiscoveredModel{ModelID: id, Endpoint: e, Capability: spec.Capability}
}

// AsModel turns a discovered model into a (not yet enabled) provider model.
func (d DiscoveredModel) AsModel() domain.Model {
	return domain.Model{ModelID: d.ModelID, Endpoint: d.Endpoint, Capability: d.Capability}
}

// DiscoverModels lists models of a (possibly unsaved) custom provider.
func (s *Service) DiscoverModels(ctx context.Context, req DiscoverRequest) ([]DiscoveredModel, error) {
	in := domain.CustomInput{Name: "x", DiscoveryFormat: req.DiscoveryFormat, BaseURL: req.BaseURL, APIKey: req.APIKey}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	key := in.APIKey
	if key == "" && req.CustomID != "" {
		p, err := s.repo.GetCustom(ctx, req.CustomID)
		if err != nil {
			return nil, err
		}
		if key, err = s.customKey(p); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.testTimeout)
	defer cancel()
	defer progress.KeepAlive(ctx)()
	models, err := s.discover(ctx, in.DiscoveryFormat, in.BaseURL, key)
	if err != nil {
		return nil, discoveryError(err, key)
	}
	return models, nil
}

// discoveryError turns a failed model listing into an ERR_UNAVAILABLE carrying the classified outcome.
func discoveryError(err error, key string) error {
	var pe *probeError
	if errors.As(err, &pe) {
		return apperr.New(apperr.CodeUnavailable, "Model discovery failed: "+redact(pe.detail, key)).
			WithDetails(map[string]any{"outcome": string(pe.outcome), "status": pe.status}).WithMsg("errors.provider.discovery_failed", map[string]any{"detail": redact(pe.detail, key)})
	}
	return apperr.Wrap(apperr.CodeUnavailable, "Model discovery failed", err).WithMsg("errors.provider.discovery_unavailable", nil)
}

func allSecrets(r Resolved) []string {
	out := make([]string, 0, len(r.Secrets))
	for _, v := range r.Secrets {
		out = append(out, v)
	}
	return out
}

// run executes a bounded probe and turns failures into a TestResult (a failed test is not an error).
func (s *Service) run(ctx context.Context, secretVals []string, probe func(context.Context) ([]string, error)) (TestResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.testTimeout)
	defer cancel()
	defer progress.KeepAlive(ctx)()
	start := time.Now()
	models, err := probe(ctx)
	res := TestResult{LatencyMs: time.Since(start).Milliseconds(), Models: models}
	if err == nil {
		res.Outcome = OutcomeOK
		return res, nil
	}
	var pe *probeError
	switch {
	case errors.As(err, &pe):
		res.Outcome, res.Status, res.Detail = pe.outcome, pe.status, pe.detail
	case errors.Is(err, context.DeadlineExceeded):
		res.Outcome, res.Detail = OutcomeTimeout, "no response within the test timeout"
	case errors.Is(err, context.Canceled):
		return TestResult{}, apperr.Wrap(apperr.CodeCancelled, "Cancelled", err).WithMsg("errors.provider.cancelled", nil)
	default:
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			res.Outcome = OutcomeTimeout
		} else {
			res.Outcome = OutcomeNetwork
		}
		res.Detail = err.Error()
	}
	for _, v := range secretVals {
		res.Detail = redact(res.Detail, v)
	}
	return res, nil
}

var authPattern = regexp.MustCompile(`(?i)(bearer|token|basic)\s+[A-Za-z0-9._~+/=-]{8,}|((?:api[_-]?key|access[_-]?token|key|secret)["']?\s*[:=]\s*["']?)[^\s"'&,}]{6,}`)

// redact removes the secret (also URL-encoded) and anything that looks like a credential, then trims to
// 200 runes without splitting a character.
func redact(text, secret string) string {
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "[redacted]")
		if esc := url.QueryEscape(secret); esc != secret {
			text = strings.ReplaceAll(text, esc, "[redacted]")
		}
	}
	text = authPattern.ReplaceAllStringFunc(text, func(m string) string {
		if sm := authPattern.FindStringSubmatch(m); sm[2] != "" {
			return sm[2] + "[redacted]"
		}
		return strings.Fields(m)[0] + " [redacted]"
	})
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200]) + "..."
	}
	return text
}

// ---- HTTP helpers ----

type request struct {
	method  string
	url     string
	headers map[string]string
	body    any
}

// do sends req and returns the body of a 2xx response; other statuses become classified failures.
func (s *Service) do(ctx context.Context, req request) ([]byte, int, error) {
	var body io.Reader
	if req.body != nil {
		raw, err := json.Marshal(req.body)
		if err != nil {
			return nil, 0, err
		}
		body = strings.NewReader(string(raw))
	}
	hr, err := http.NewRequestWithContext(ctx, req.method, req.url, body)
	if err != nil {
		return nil, 0, fail(OutcomeInvalid, 0, "invalid request URL")
	}
	for k, v := range req.headers {
		hr.Header.Set(k, v)
	}
	if req.body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL (may carry query secrets) from the message
		}
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, resp.StatusCode, nil
	}
	return raw, resp.StatusCode, classify(resp.StatusCode, raw)
}

func classify(status int, body []byte) error {
	detail := fmt.Sprintf("HTTP %d: %s", status, strings.TrimSpace(string(body)))
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fail(OutcomeAuthFailed, status, detail)
	case status == http.StatusTooManyRequests:
		return fail(OutcomeRateLimited, status, detail)
	case status == http.StatusNotFound:
		return fail(OutcomeNotFound, status, detail)
	default:
		return fail(OutcomeHTTPError, status, detail)
	}
}

func bearer(key string) map[string]string { return map[string]string{"Authorization": "Bearer " + key} }

// ---- built-in probes (ArcReel server/routers/providers.py connectivity checks) ----

// probeBuiltin runs the connectivity test the registry declares for m (Meta.Probe).
func (s *Service) probeBuiltin(ctx context.Context, m domain.Meta, r Resolved) ([]string, error) {
	key := r.Secrets[domain.FieldAPIKey]
	p := m.Probe
	host := stripFirstSuffix(r.BaseURL, p.StripSuffixes) + p.Path
	var filter *regexp.Regexp
	if p.ModelFilter != "" {
		filter = regexp.MustCompile(p.ModelFilter) // validated at start-up by ValidateRegistry
	}
	switch p.Kind {
	case domain.ProbeOpenAIModels:
		return s.openAIModels(ctx, host, key, filter)
	case domain.ProbeAnthropicModels:
		ids, err := s.anthropicModels(ctx, host, key)
		return filterIDs(ids, filter), err
	case domain.ProbeGeminiModels:
		models, err := s.geminiModels(ctx, host, key)
		return filterIDs(discoveredIDs(models), filter), err
	case domain.ProbeVertexToken:
		return nil, s.vertexToken(ctx, r.Secrets[domain.FieldCredentialsFile])
	case domain.ProbeArkTasks:
		_, _, err := s.do(ctx, request{method: http.MethodGet,
			url: host + "/contents/generations/tasks?page_size=1", headers: bearer(key)})
		return nil, err
	case domain.ProbeKlingCosts:
		return nil, s.probeKling(ctx, host, r)
	case domain.ProbeViduTasks:
		return nil, s.probeVidu(ctx, host, key)
	}
	return nil, fail(OutcomeInvalid, 0, "no connectivity test for this provider")
}

// stripFirstSuffix removes the first matching suffix (ArcReel _KNOWN_SUFFIXES / _minimax_host).
func stripFirstSuffix(base string, suffixes []string) string {
	base = strings.TrimRight(base, "/")
	for _, suf := range suffixes {
		if strings.HasSuffix(base, suf) {
			return strings.TrimSuffix(base, suf)
		}
	}
	return base
}

func (s *Service) openAIModels(ctx context.Context, base, key string, filter *regexp.Regexp) ([]string, error) {
	raw, _, err := s.do(ctx, request{method: http.MethodGet, url: strings.TrimRight(base, "/") + "/models", headers: bearer(key)})
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil, fail(OutcomeHTTPError, 200, "response is not an OpenAI model list")
	}
	ids := make([]string, 0, len(body.Data))
	for _, d := range body.Data {
		ids = append(ids, d.ID)
	}
	return filterIDs(ids, filter), nil
}

func filterIDs(ids []string, filter *regexp.Regexp) []string {
	out := []string{}
	for _, id := range ids {
		if filter == nil || filter.MatchString(strings.ToLower(id)) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Service) probeVidu(ctx context.Context, base, key string) error {
	// ArcReel test_vidu_connection: a valid token on a non-existent task returns 404.
	_, status, err := s.do(ctx, request{method: http.MethodGet, url: base + "/tasks/0/creations",
		headers: map[string]string{"Authorization": "Token " + key}})
	switch {
	case status == http.StatusNotFound:
		return nil
	case err == nil: // a 2xx on a non-existent task is not Vidu (proxy, captive portal): undecidable
		return fail(OutcomeHTTPError, status, "unexpected response; cannot confirm the key")
	}
	return err
}

func (s *Service) probeKling(ctx context.Context, root string, r Resolved) error {
	token := r.Secrets[domain.FieldAPIKey]
	if token == "" {
		var err error
		if token, err = klingJWT(r.Secrets[domain.FieldAccessKey], r.Secrets[domain.FieldSecretKey], time.Now()); err != nil {
			return fail(OutcomeInvalid, 0, err.Error())
		}
	}
	now := time.Now().UnixMilli()
	raw, _, err := s.do(ctx, request{method: http.MethodGet,
		url:     fmt.Sprintf("%s/account/costs?start_time=%d&end_time=%d", root, now-86_400_000, now),
		headers: bearer(token)})
	if err != nil {
		return err
	}
	var env struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Code != nil && *env.Code != 0 {
		return fail(OutcomeAuthFailed, 200, fmt.Sprintf("Kling code %d: %s", *env.Code, env.Message))
	}
	return nil
}

// KlingJWT mints an HS256 bearer JWT for Kling's API from an access_key and secret_key pair.
func KlingJWT(ak, sk string, now time.Time) (string, error) {
	return domain.KlingJWT(ak, sk, now)
}

func klingJWT(ak, sk string, now time.Time) (string, error) {
	return domain.KlingJWT(ak, sk, now)
}

func signJWT(header map[string]string, claims map[string]any, sign func([]byte) ([]byte, error)) (string, error) {
	return domain.SignJWT(header, claims, sign)
}

// ---- Gemini / Vertex ----

func (s *Service) geminiModels(ctx context.Context, base, key string) ([]DiscoveredModel, error) {
	base = strings.TrimRight(cmp.Or(base, defaultBaseURL(domain.IDGeminiAIStudio)), "/")
	base = strings.TrimSuffix(strings.TrimSuffix(base, "/v1beta"), "/v1")
	raw, _, err := s.do(ctx, request{method: http.MethodGet, url: base + "/v1beta/models?pageSize=1000",
		headers: map[string]string{"x-goog-api-key": key}})
	if err != nil {
		return nil, err
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil, fail(OutcomeHTTPError, 200, "response is not a Gemini model list")
	}
	var out []DiscoveredModel
	for _, m := range body.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		ep := InferGeminiEndpoint(id)
		if Usable(id) {
			out = append(out, discovered(id, ep))
		}
	}
	return out, nil
}

type serviceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

const maxServiceAccountBytes = 64 << 10

func parseServiceAccount(raw string) (serviceAccount, error) {
	var sa serviceAccount
	if len(raw) > maxServiceAccountBytes || json.Unmarshal([]byte(raw), &sa) != nil {
		return sa, apperr.New(apperr.CodeValidation, "The credentials file is not valid JSON").
			WithDetails(map[string]any{"field": domain.FieldCredentialsFile}).WithMsg("errors.provider.credentials_not_json", nil)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return sa, apperr.New(apperr.CodeValidation, "The credentials file must be a service-account JSON with project_id").
			WithDetails(map[string]any{"field": domain.FieldCredentialsFile}).WithMsg("errors.provider.credentials_missing_project", nil)
	}
	return sa, nil
}

func validateServiceAccount(raw string) error { _, err := parseServiceAccount(raw); return err }

// googleTokenURI is Google's OAuth endpoint, used when a service account omits token_uri.
const googleTokenURI = "https://oauth2.googleapis.com/token"

// defaultBaseURL reads a provider's default URL from the registry (custom discovery reuses it).
func defaultBaseURL(id string) string {
	m, _ := domain.Lookup(id)
	return m.DefaultBaseURL
}

// VertexScopes are the OAuth scopes ArcReel requests (lib VERTEX_SCOPES).
var VertexScopes = []string{"https://www.googleapis.com/auth/cloud-platform", "https://www.googleapis.com/auth/generative-language"}

// VertexToken exchanges a Google service-account JSON for an OAuth access token, project ID, and TTL.
func (s *Service) VertexToken(ctx context.Context, raw string) (accessToken, projectID string, expiresIn time.Duration, err error) {
	return s.exchangeVertexToken(ctx, raw)
}

// vertexToken exchanges the service account for an OAuth token; success proves the key is valid.
func (s *Service) vertexToken(ctx context.Context, raw string) error {
	_, _, _, err := s.exchangeVertexToken(ctx, raw)
	return err
}

func (s *Service) exchangeVertexToken(ctx context.Context, raw string) (string, string, time.Duration, error) {
	sa, err := parseServiceAccount(raw)
	if err != nil {
		return "", "", 0, fail(OutcomeInvalid, 0, "invalid service-account JSON")
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return "", "", 0, fail(OutcomeInvalid, 0, "service account private key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", "", 0, fail(OutcomeInvalid, 0, "service account private key cannot be parsed")
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return "", "", 0, fail(OutcomeInvalid, 0, "service account private key is not RSA")
	}
	tokenURI := cmp.Or(sa.TokenURI, googleTokenURI)
	if err := domain.ValidateBaseURL(tokenURI); err != nil {
		return "", "", 0, fail(OutcomeInvalid, 0, "invalid token_uri")
	}
	now := time.Now().Unix()
	assertion, err := signJWT(map[string]string{"alg": "RS256", "typ": "JWT"}, map[string]any{
		"iss": sa.ClientEmail, "scope": strings.Join(VertexScopes, " "), "aud": tokenURI, "iat": now, "exp": now + 3600,
	}, func(in []byte) ([]byte, error) {
		sum := sha256.Sum256(in)
		return rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, sum[:])
	})
	if err != nil {
		return "", "", 0, fail(OutcomeInvalid, 0, "cannot sign token request")
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", 0, fail(OutcomeInvalid, 0, "invalid token_uri")
	}
	hr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return "", "", 0, ctx.Err()
		}
		return "", "", 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		return "", "", 0, fail(OutcomeAuthFailed, resp.StatusCode, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, body))
	}
	if resp.StatusCode/100 != 2 {
		return "", "", 0, classify(resp.StatusCode, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", "", 0, fail(OutcomeHTTPError, resp.StatusCode, "response did not contain access_token")
	}
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 50 * time.Minute
	}
	return tok.AccessToken, sa.ProjectID, ttl, nil
}

// ---- Anthropic ----

func (s *Service) anthropicModels(ctx context.Context, base, key string) ([]string, error) {
	base = strings.TrimRight(cmp.Or(base, defaultBaseURL(domain.IDAnthropic)), "/")
	h := map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}
	raw, status, err := s.do(ctx, request{method: http.MethodGet, url: base + "/v1/models", headers: h})
	if status == http.StatusUnauthorized {
		// Some Anthropic-compatible gateways only accept Bearer (ArcReel discovery retry).
		h2 := bearer(key)
		h2["anthropic-version"] = "2023-06-01"
		raw, _, err = s.do(ctx, request{method: http.MethodGet, url: base + "/v1/models", headers: h2})
	}
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil, fail(OutcomeHTTPError, 200, "response is not an Anthropic model list")
	}
	ids := []string{}
	for _, d := range body.Data {
		if d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// ---- custom discovery ----

// EnsureOpenAIBase appends /v1 when base has no /v<n> suffix.
func EnsureOpenAIBase(base string) string { return domain.EnsureOpenAIBase(base) }

func ensureOpenAIBase(base string) string { return domain.EnsureOpenAIBase(base) }

func (s *Service) discover(ctx context.Context, f domain.DiscoveryFormat, base, key string) ([]DiscoveredModel, error) {
	switch f {
	case domain.DiscoveryOpenAI:
		ids, err := s.openAIModels(ctx, ensureOpenAIBase(base), key, nil)
		out := make([]DiscoveredModel, 0, len(ids))
		for _, id := range ids {
			if Usable(id) {
				out = append(out, discovered(id, InferOpenAIEndpoint(id)))
			}
		}
		return out, err
	case domain.DiscoveryGemini:
		return s.geminiModels(ctx, base, key)
	case domain.DiscoveryAnthropic:
		ids, err := s.anthropicModels(ctx, base, key)
		out := make([]DiscoveredModel, 0, len(ids))
		for _, id := range ids {
			out = append(out, discovered(id, domain.EndpointAnthropicMessages))
		}
		return out, err
	}
	return nil, fail(OutcomeInvalid, 0, "unknown protocol")
}

// InferGeminiEndpoint guesses the endpoint of a Gemini-API model from its id.
func InferGeminiEndpoint(id string) domain.Endpoint {
	l := strings.ToLower(id)
	switch {
	case strings.Contains(l, "veo"):
		return domain.EndpointGeminiVideo
	case strings.Contains(l, "tts"):
		return domain.EndpointGeminiTTS
	case strings.Contains(l, "image") || strings.Contains(l, "imagen"):
		return domain.EndpointGeminiImage
	}
	return domain.EndpointGeminiGenerate
}

// nonGenerative matches model ids that are not usable for generation (embeddings, speech-to-text,
// moderation...), so discovery does not list them as LLMs.
var nonGenerative = regexp.MustCompile(`embed|whisper|transcri|moderation|rerank|search|realtime|audio-preview|similarity|davinci|babbage`)

// Usable reports whether a discovered model id is a generation model.
func Usable(id string) bool { return !nonGenerative.MatchString(strings.ToLower(id)) }

// InferOpenAIEndpoint guesses the endpoint of an OpenAI-compatible model from its id.
func InferOpenAIEndpoint(id string) domain.Endpoint {
	l := strings.ToLower(id)
	switch {
	case strings.Contains(l, "tts") || strings.Contains(l, "speech"):
		return domain.EndpointOpenAITTS
	case strings.Contains(l, "sora") || strings.Contains(l, "video") || strings.Contains(l, "veo") ||
		strings.Contains(l, "kling") || strings.Contains(l, "seedance") || strings.Contains(l, "hailuo"):
		return domain.EndpointOpenAIVideo
	case strings.Contains(l, "dall-e") || strings.Contains(l, "gpt-image") || strings.Contains(l, "image") ||
		strings.Contains(l, "flux") || strings.Contains(l, "seedream") || strings.Contains(l, "imagen"):
		return domain.EndpointOpenAIImages
	}
	return domain.EndpointOpenAIChat
}

func discoveredIDs(ms []DiscoveredModel) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ModelID)
	}
	return out
}
