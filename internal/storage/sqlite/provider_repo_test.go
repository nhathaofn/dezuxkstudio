package sqlite

import (
	"context"
	"errors"
	"testing"

	"app/internal/apperr"
	"app/internal/domain/provider"
)

func TestProviderCredentialActiveLifecycle(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	r := NewProviderRepository(db)

	a, err := r.CreateCredential(ctx, provider.Credential{ProviderID: "openai", Name: "a", Values: map[string]string{"base_url": "x"}})
	if err != nil || !a.Active {
		t.Fatalf("first credential must be active: %+v %v", a, err)
	}
	b, _ := r.CreateCredential(ctx, provider.Credential{ProviderID: "openai", Name: "b"})
	if b.Active {
		t.Fatal("second credential must not be active")
	}
	other, _ := r.CreateCredential(ctx, provider.Credential{ProviderID: "kling", Name: "k"})
	if !other.Active {
		t.Fatal("active is per provider")
	}
	if err := r.ActivateCredential(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetCredential(ctx, a.ID); got.Active {
		t.Fatal("a must be deactivated")
	}
	if err := r.DeleteCredential(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetCredential(ctx, a.ID)
	if !got.Active || got.Values["base_url"] != "x" {
		t.Fatalf("a must be promoted: %+v", got)
	}
	var ae *apperr.Error
	if err := r.ActivateCredential(ctx, "missing"); !errors.As(err, &ae) || ae.Code != apperr.CodeNotFound {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestCustomProviderRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	r := NewProviderRepository(db)
	p, err := r.CreateCustom(ctx, provider.CustomProvider{
		Name: "Local", DiscoveryFormat: provider.DiscoveryOpenAI, BaseURL: "http://localhost:1234/v1", KeyHint: "••••",
		Models: []provider.CustomModel{
			{ModelID: "m1", Endpoint: provider.EndpointOpenAIChat, Enabled: true, IsDefault: true},
			{ModelID: "img", Endpoint: provider.EndpointOpenAIImages, Enabled: false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Models = p.Models[:1]
	p.Name = "Renamed"
	if err := r.UpdateCustom(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetCustom(ctx, p.ID)
	if err != nil || got.Name != "Renamed" || len(got.Models) != 1 || !got.Models[0].IsDefault {
		t.Fatalf("%+v %v", got, err)
	}
	if err := r.DeleteCustom(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.ListCustom(ctx); len(list) != 0 {
		t.Fatal("not deleted")
	}
}
