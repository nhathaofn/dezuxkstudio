package contracts

import (
	"encoding/json"
	"strings"
	"testing"

	domain "app/internal/domain/provider"
	"app/internal/secrets"
)

func mustMeta(t *testing.T, id string) domain.Meta {
	t.Helper()
	m, ok := domain.Lookup(id)
	if !ok {
		t.Fatalf("unknown provider %s", id)
	}
	return m
}

func TestCheckingFlagsCarriedIntoDTOs(t *testing.T) {
	c := domain.Credential{ID: "c1", ProviderID: domain.IDOpenAI, Checking: true}
	if !NewProviderCredential(c).Checking {
		t.Fatal("credential checking lost")
	}
	c.Checking = false
	if NewProviderCredential(c).Checking {
		t.Fatal("credential checking invented")
	}
	if !NewCustomProvider(domain.CustomProvider{ID: "x", Checking: true}).Checking {
		t.Fatal("custom checking lost")
	}
	if NewCustomProvider(domain.CustomProvider{ID: "x"}).Checking {
		t.Fatal("custom checking invented")
	}

	res := NewProvidersListResult(secrets.BackendMemory,
		[]BuiltinWithCredentials{{Meta: mustMeta(t, domain.IDOpenAI), Credentials: []domain.Credential{c, {ID: "c2", Checking: true}}}},
		[]domain.CustomProvider{{ID: "x", Checking: true}}, domain.LimitDefaults{})
	if res.Builtins[0].Credentials[0].Checking || !res.Builtins[0].Credentials[1].Checking || !res.Custom[0].Checking {
		t.Fatalf("list result = %+v", res)
	}
	raw, _ := json.Marshal(res)
	if !strings.Contains(string(raw), `"checking":true`) || !strings.Contains(string(raw), `"checking":false`) {
		t.Fatalf("checking not serialized: %s", raw)
	}
}

func TestDiscoverableFlagInDTO(t *testing.T) {
	res := listResult(t, domain.LimitDefaults{}, nil)
	want := map[string]bool{domain.IDOpenAI: true, domain.IDAnthropic: true, domain.IDGeminiAIStudio: true, domain.IDKling: false, domain.IDVidu: false}
	for _, b := range res.Builtins {
		if m := mustMeta(t, b.ID); b.Discoverable != m.Discoverable() {
			t.Errorf("%s: discoverable = %v, want %v", b.ID, b.Discoverable, m.Discoverable())
		}
		if w, ok := want[b.ID]; ok && b.Discoverable != w {
			t.Errorf("%s: discoverable = %v, want %v", b.ID, b.Discoverable, w)
		}
	}
}
