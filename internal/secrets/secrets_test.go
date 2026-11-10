package secrets

import (
	"errors"
	"os"
	"testing"
)

func TestHybridStore(t *testing.T) {
	mem1 := NewMemory()
	mem2 := NewMemory()
	hybrid := NewHybridStore(mem1, mem2)

	// Set stores in both
	if err := hybrid.Set(KeyRemoteAuthToken, "test-secret"); err != nil {
		t.Fatalf("hybrid.Set failed: %v", err)
	}

	v, err := hybrid.Get(KeyRemoteAuthToken)
	if err != nil || v != "test-secret" {
		t.Fatalf("expected test-secret, got %q, err=%v", v, err)
	}

	// Fallback check when primary empty
	memPrimary := NewMemory()
	memFallback := NewMemory()
	_ = memFallback.Set(KeyRemoteAuthToken, "fallback-secret")
	h2 := NewHybridStore(memPrimary, memFallback)

	v2, err := h2.Get(KeyRemoteAuthToken)
	if err != nil || v2 != "fallback-secret" {
		t.Fatalf("expected fallback-secret, got %q, err=%v", v2, err)
	}

	// Delete removes from both
	if err := h2.Delete(KeyRemoteAuthToken); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	_, err = h2.Get(KeyRemoteAuthToken)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestEnvStore(t *testing.T) {
	mem := NewMemory()
	const envKey = "TEST_DEZUXKSTUDIO_REMOTE_TOKEN"
	store := NewEnvStore(KeyRemoteAuthToken, envKey, mem)

	_ = os.Unsetenv(envKey)
	// Without env, falls back to memory which is empty
	_, err := store.Get(KeyRemoteAuthToken)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when env unset and mem empty, got %v", err)
	}

	// Set in memory
	_ = store.Set(KeyRemoteAuthToken, "mem-val")
	v, err := store.Get(KeyRemoteAuthToken)
	if err != nil || v != "mem-val" {
		t.Fatalf("expected mem-val, got %q, err=%v", v, err)
	}

	// Set in env (takes precedence)
	_ = os.Setenv(envKey, "env-val")
	defer os.Unsetenv(envKey)

	v, err = store.Get(KeyRemoteAuthToken)
	if err != nil || v != "env-val" {
		t.Fatalf("expected env-val, got %q, err=%v", v, err)
	}
}
