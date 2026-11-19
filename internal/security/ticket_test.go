package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTicketSignAndVerify_Success(t *testing.T) {
	secret := []byte("super-secret-key-for-testing-123456")
	signer := NewTicketSigner(secret)

	assetID := "asset-uuid-1234"
	purpose := PurposePreview
	ttl := 10 * time.Minute

	ticket, err := signer.SignTicket(assetID, purpose, ttl)
	if err != nil {
		t.Fatalf("SignTicket failed: %v", err)
	}
	if ticket == "" {
		t.Fatal("expected non-empty ticket")
	}

	claims, err := signer.VerifyTicket(ticket, assetID, purpose)
	if err != nil {
		t.Fatalf("VerifyTicket failed: %v", err)
	}

	if claims.AssetID != assetID {
		t.Errorf("expected assetID %q, got %q", assetID, claims.AssetID)
	}
	if claims.Purpose != purpose {
		t.Errorf("expected purpose %q, got %q", purpose, claims.Purpose)
	}
	if claims.Nonce == "" {
		t.Error("expected non-empty nonce")
	}
	if claims.ExpiresAt <= time.Now().Unix() {
		t.Error("expected expiresAt in the future")
	}
}

func TestTicketVerify_ExpiredTicket(t *testing.T) {
	secret := []byte("super-secret-key-for-testing-123456")
	signer := NewTicketSigner(secret)

	// Create a ticket that expires immediately in the past
	claims := TicketClaims{
		AssetID:   "asset-1",
		Purpose:   PurposeDownload,
		ExpiresAt: time.Now().Unix() - 10,
		Nonce:     "1234567890abcdef",
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims failed: %v", err)
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payloadPart))
	sigPart := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	expiredTicket := payloadPart + "." + sigPart

	_, err = signer.VerifyTicket(expiredTicket, "asset-1", PurposeDownload)
	if !errors.Is(err, ErrExpiredTicket) {
		t.Fatalf("expected ErrExpiredTicket, got: %v", err)
	}
}

func TestTicketVerify_WrongAssetID(t *testing.T) {
	secret := []byte("super-secret-key-for-testing-123456")
	signer := NewTicketSigner(secret)

	ticket, err := signer.SignTicket("asset-1", PurposePreview, 5*time.Minute)
	if err != nil {
		t.Fatalf("SignTicket failed: %v", err)
	}

	_, err = signer.VerifyTicket(ticket, "asset-2", PurposePreview)
	if !errors.Is(err, ErrAssetMismatch) {
		t.Fatalf("expected ErrAssetMismatch, got: %v", err)
	}
}

func TestTicketVerify_WrongPurpose(t *testing.T) {
	secret := []byte("super-secret-key-for-testing-123456")
	signer := NewTicketSigner(secret)

	ticket, err := signer.SignTicket("asset-1", PurposePreview, 5*time.Minute)
	if err != nil {
		t.Fatalf("SignTicket failed: %v", err)
	}

	_, err = signer.VerifyTicket(ticket, "asset-1", PurposeDownload)
	if !errors.Is(err, ErrPurposeMismatch) {
		t.Fatalf("expected ErrPurposeMismatch, got: %v", err)
	}
}

func TestTicketVerify_TamperedSignature(t *testing.T) {
	secret := []byte("super-secret-key-for-testing-123456")
	signer := NewTicketSigner(secret)

	ticket, err := signer.SignTicket("asset-1", PurposePreview, 5*time.Minute)
	if err != nil {
		t.Fatalf("SignTicket failed: %v", err)
	}

	parts := strings.Split(ticket, ".")
	tamperedSig := parts[1][:len(parts[1])-2] + "AA"
	tamperedTicket := parts[0] + "." + tamperedSig

	_, err = signer.VerifyTicket(tamperedTicket, "asset-1", PurposePreview)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got: %v", err)
	}
}

func TestTicketVerify_TamperedPayload(t *testing.T) {
	secret := []byte("super-secret-key-for-testing-123456")
	signer := NewTicketSigner(secret)

	ticket, err := signer.SignTicket("asset-1", PurposePreview, 5*time.Minute)
	if err != nil {
		t.Fatalf("SignTicket failed: %v", err)
	}

	parts := strings.Split(ticket, ".")
	// Tamper payload with another assetID
	claims := TicketClaims{
		AssetID:   "asset-evil",
		Purpose:   PurposePreview,
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
		Nonce:     "1234567890abcdef",
	}
	b, _ := json.Marshal(claims)
	tamperedPayload := base64.RawURLEncoding.EncodeToString(b)
	tamperedTicket := tamperedPayload + "." + parts[1]

	_, err = signer.VerifyTicket(tamperedTicket, "asset-evil", PurposePreview)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature on tampered payload, got: %v", err)
	}
}

func TestTicketVerify_InvalidFormats(t *testing.T) {
	secret := []byte("super-secret-key-for-testing-123456")
	signer := NewTicketSigner(secret)

	invalidTickets := []string{
		"",
		"nodot",
		"too.many.dots.here",
		"invalid-base64-payload.invalid-base64-sig!@#$",
	}

	for _, tc := range invalidTickets {
		_, err := signer.VerifyTicket(tc, "asset-1", PurposePreview)
		if err == nil {
			t.Errorf("expected error for ticket %q, got nil", tc)
		}
	}
}

func TestTicketSigner_InputValidation(t *testing.T) {
	secret := []byte("test-key")
	signer := NewTicketSigner(secret)

	// Empty asset ID
	if _, err := signer.SignTicket("", PurposePreview, time.Minute); err == nil {
		t.Error("expected error for empty assetID")
	}

	// Invalid purpose
	if _, err := signer.SignTicket("asset-1", "admin", time.Minute); err == nil {
		t.Error("expected error for invalid purpose")
	}

	// Non-positive TTL
	if _, err := signer.SignTicket("asset-1", PurposePreview, 0); err == nil {
		t.Error("expected error for zero TTL")
	}
	if _, err := signer.SignTicket("asset-1", PurposePreview, -time.Minute); err == nil {
		t.Error("expected error for negative TTL")
	}

	// Empty secret signer
	emptySigner := NewTicketSigner(nil)
	if _, err := emptySigner.SignTicket("asset-1", PurposePreview, time.Minute); !errors.Is(err, ErrInvalidSecret) {
		t.Errorf("expected ErrInvalidSecret, got %v", err)
	}
	if _, err := emptySigner.VerifyTicket("any.ticket", "asset-1", PurposePreview); !errors.Is(err, ErrInvalidSecret) {
		t.Errorf("expected ErrInvalidSecret, got %v", err)
	}
}

func TestTicketSigner_NonceUniqueness(t *testing.T) {
	secret := []byte("test-key")
	signer := NewTicketSigner(secret)

	t1, err := signer.SignTicket("asset-1", PurposePreview, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := signer.SignTicket("asset-1", PurposePreview, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if t1 == t2 {
		t.Fatal("expected tickets generated for same parameters to differ due to nonces")
	}
}

func TestTicketVerifyAndConsume_SingleUseDownloadVsReusablePreview(t *testing.T) {
	signer := NewTicketSigner([]byte("secret-key-for-replay-test"))

	dlTicket, err := signer.SignTicket("asset-dl", PurposeDownload, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.VerifyAndConsumeTicket(dlTicket, "asset-dl", PurposeDownload); err != nil {
		t.Fatalf("first download consume failed: %v", err)
	}
	if _, err := signer.VerifyAndConsumeTicket(dlTicket, "asset-dl", PurposeDownload); !errors.Is(err, ErrTicketReplayed) {
		t.Fatalf("expected ErrTicketReplayed on second download use, got %v", err)
	}

	prevTicket, err := signer.SignTicket("asset-prev", PurposePreview, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := signer.VerifyAndConsumeTicket(prevTicket, "asset-prev", PurposePreview); err != nil {
			t.Fatalf("preview range use %d failed: %v", i, err)
		}
	}
}
