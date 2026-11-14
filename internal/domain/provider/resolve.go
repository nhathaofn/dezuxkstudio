package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Resolved is a ready-to-use credential. Secrets holds plain secret values: keep it in memory only.
type Resolved struct {
	ProviderID   string
	CredentialID string
	BaseURL      string
	Values       map[string]string
	Secrets      map[string]string
}

// ResolvedCustom is a ready-to-use custom provider.
type ResolvedCustom struct {
	Provider CustomProvider
	APIKey   string
}

// ResolvedModel is everything a generation feature needs to call a model. Secrets stay in memory only.
type ResolvedModel struct {
	Purpose Purpose
	Ref     ModelRef
	Model   Model
	Limits  Limits
	// Exactly one of Builtin / Custom is set.
	Builtin *Resolved
	Custom  *ResolvedCustom
}

// KlingJWT mints an HS256 bearer JWT for Kling's API from an access_key and secret_key pair
// (ArcReel kling_shared.py: iss=AK, exp=now+1800, nbf=now-5).
func KlingJWT(ak, sk string, now time.Time) (string, error) {
	ak, sk = strings.TrimSpace(ak), strings.TrimSpace(sk)
	if ak == "" || sk == "" {
		return "", errors.New("access_key and secret_key are required")
	}
	claims := map[string]any{"iss": ak, "exp": now.Unix() + 1800, "nbf": now.Unix() - 5}
	return SignJWT(map[string]string{"alg": "HS256", "typ": "JWT"}, claims, func(in []byte) ([]byte, error) {
		h := hmac.New(sha256.New, []byte(sk))
		h.Write(in)
		return h.Sum(nil), nil
	})
}

// SignJWT encodes a compact JWS (header.claims.signature) using unpadded base64url encoding.
func SignJWT(header map[string]string, claims map[string]any, sign func([]byte) ([]byte, error)) (string, error) {
	if sign == nil {
		return "", errors.New("sign callback is required")
	}
	enc := base64.RawURLEncoding
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	sig, err := sign([]byte(unsigned))
	if err != nil {
		return "", err
	}
	return unsigned + "." + enc.EncodeToString(sig), nil
}

var versionSuffix = regexp.MustCompile(`/v\d+$`) // ArcReel ensure_openai_base_url

// EnsureOpenAIBase appends /v1 when base has no /v<n> suffix (ArcReel ensure_openai_base_url).
func EnsureOpenAIBase(base string) string {
	base = strings.TrimRight(base, "/")
	if versionSuffix.MatchString(base) {
		return base
	}
	return base + "/v1"
}
