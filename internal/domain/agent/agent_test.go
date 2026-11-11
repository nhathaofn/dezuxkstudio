package agent

import "testing"

func TestValidateProviderID(t *testing.T) {
	for _, id := range AllProviderIDs() {
		if err := ValidateProviderID(id); err != nil {
			t.Errorf("ValidateProviderID(%q) returned unexpected error: %v", id, err)
		}
		if _, ok := Registry[id]; !ok {
			t.Errorf("Registry missing spec for %q", id)
		}
	}

	if err := ValidateProviderID(ProviderID("unknown")); err == nil {
		t.Error("expected validation error for unknown provider id")
	}

	if !Registry[ProviderAntigravity].Supported || !Registry[ProviderCodex].Supported || !Registry[ProviderClaude].Supported {
		t.Error("expected antigravity, codex, and claude to be supported")
	}
}
