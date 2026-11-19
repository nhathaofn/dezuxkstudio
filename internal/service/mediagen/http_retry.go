package mediagen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/httpclient"
	"app/internal/logger"
)

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
