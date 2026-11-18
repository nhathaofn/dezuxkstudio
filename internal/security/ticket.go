// Package security holds authentication primitives shared by HTTP and WebSocket transports.
package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Standard errors returned during ticket verification.
var (
	ErrInvalidTicket    = errors.New("security: invalid ticket format")
	ErrExpiredTicket    = errors.New("security: ticket expired")
	ErrInvalidSignature = errors.New("security: invalid ticket signature")
	ErrAssetMismatch    = errors.New("security: ticket asset mismatch")
	ErrPurposeMismatch  = errors.New("security: ticket purpose mismatch")
	ErrInvalidSecret    = errors.New("security: ticket signing secret cannot be empty")
	ErrTicketReplayed   = errors.New("security: download ticket already used")
)

const maxTrackedDownloadNonces = 10000

// Allowed purposes for signed media tickets.
const (
	PurposePreview  = "preview"
	PurposeDownload = "download"
)

// TicketClaims contains the authenticated payload embedded in a signed media ticket.
type TicketClaims struct {
	AssetID   string `json:"asset_id"`
	Purpose   string `json:"purpose"`
	ExpiresAt int64  `json:"exp"`
	Nonce     string `json:"nonce"`
}

// TicketSigner generates and verifies HMAC-SHA256 signed tickets for media streaming/download.
type TicketSigner struct {
	secret     []byte
	nonceMu    sync.Mutex
	usedNonces map[string]int64
}

// NewTicketSigner constructs a TicketSigner using the provided secret key.
func NewTicketSigner(secret []byte) *TicketSigner {
	s := make([]byte, len(secret))
	copy(s, secret)
	return &TicketSigner{
		secret:     s,
		usedNonces: make(map[string]int64),
	}
}

// SignTicket issues an HMAC-SHA256 signed ticket string for the given assetID, purpose, and TTL.
func (s *TicketSigner) SignTicket(assetID, purpose string, ttl time.Duration) (string, error) {
	if len(s.secret) == 0 {
		return "", ErrInvalidSecret
	}
	if strings.TrimSpace(assetID) == "" {
		return "", errors.New("security: asset_id cannot be empty")
	}
	if purpose != PurposePreview && purpose != PurposeDownload {
		return "", fmt.Errorf("security: invalid purpose %q, must be %q or %q", purpose, PurposePreview, PurposeDownload)
	}
	if ttl <= 0 {
		return "", errors.New("security: ttl must be positive")
	}

	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("security: generate nonce: %w", err)
	}

	claims := TicketClaims{
		AssetID:   assetID,
		Purpose:   purpose,
		ExpiresAt: time.Now().Add(ttl).Unix(),
		Nonce:     hex.EncodeToString(nonceBytes),
	}

	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("security: marshal ticket claims: %w", err)
	}

	payloadPart := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payloadPart))
	sigPart := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return payloadPart + "." + sigPart, nil
}

// VerifyTicket validates the ticket signature, expiration, and strict equality of asset_id and purpose.
func (s *TicketSigner) VerifyTicket(ticketStr, expectedAssetID, expectedPurpose string) (*TicketClaims, error) {
	if len(s.secret) == 0 {
		return nil, ErrInvalidSecret
	}
	parts := strings.Split(ticketStr, ".")
	if len(parts) != 2 {
		return nil, ErrInvalidTicket
	}

	payloadPart, sigPart := parts[0], parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return nil, ErrInvalidTicket
	}

	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payloadPart))
	expectedSig := mac.Sum(nil)

	if !hmac.Equal(sig, expectedSig) {
		return nil, ErrInvalidSignature
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return nil, ErrInvalidTicket
	}

	var claims TicketClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, ErrInvalidTicket
	}

	now := time.Now().Unix()
	if now > claims.ExpiresAt {
		return nil, ErrExpiredTicket
	}

	if expectedAssetID == "" || claims.AssetID != expectedAssetID {
		return nil, ErrAssetMismatch
	}

	if expectedPurpose == "" || claims.Purpose != expectedPurpose {
		return nil, ErrPurposeMismatch
	}

	return &claims, nil
}

// VerifyAndConsumeTicket verifies the ticket like VerifyTicket and enforces
// single-use nonce consumption for purpose="download" tickets.
// Purpose="preview" tickets remain reusable within their TTL to support HTTP Range streaming.
func (s *TicketSigner) VerifyAndConsumeTicket(ticketStr, expectedAssetID, expectedPurpose string) (*TicketClaims, error) {
	claims, err := s.VerifyTicket(ticketStr, expectedAssetID, expectedPurpose)
	if err != nil {
		return nil, err
	}
	if claims.Purpose == PurposeDownload {
		if claims.Nonce == "" {
			return nil, ErrInvalidTicket
		}
		now := time.Now().Unix()
		s.nonceMu.Lock()
		defer s.nonceMu.Unlock()
		if s.usedNonces == nil {
			s.usedNonces = make(map[string]int64)
		}
		if len(s.usedNonces) >= 1024 {
			for k, exp := range s.usedNonces {
				if exp < now {
					delete(s.usedNonces, k)
				}
			}
			if len(s.usedNonces) >= maxTrackedDownloadNonces {
				s.usedNonces = make(map[string]int64)
			}
		}
		if _, used := s.usedNonces[claims.Nonce]; used {
			return nil, ErrTicketReplayed
		}
		s.usedNonces[claims.Nonce] = claims.ExpiresAt
	}
	return claims, nil
}
