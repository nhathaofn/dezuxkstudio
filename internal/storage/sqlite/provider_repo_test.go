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
		Models: []provider.Model{
			{ModelID: "m1", Capability: provider.CapabilityLLM, Endpoint: provider.EndpointOpenAIChat, Enabled: true, IsDefault: true},
			{ModelID: "img", Capability: provider.CapabilityImage, Endpoint: provider.EndpointOpenAIImages, Enabled: false,
				Facts: provider.ModelFacts{Resolutions: []string{"1024x1024"}}},
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

func TestProviderModelsSettingsDefaults(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	r := NewProviderRepository(db)

	models := []provider.Model{
		{ModelID: "veo", Capability: provider.CapabilityVideo, Enabled: true, IsDefault: true,
			Facts: provider.ModelFacts{Durations: []int{4, 8}, Audio: true}},
		{ModelID: "gemini", Capability: provider.CapabilityLLM, Enabled: true, UserAdded: true, DisplayName: "Gemini"},
	}
	if err := r.ReplaceModels(ctx, "gemini-aistudio", models); err != nil {
		t.Fatal(err)
	}
	got, err := r.ListModels(ctx, "gemini-aistudio")
	if err != nil || len(got) != 2 || got[0].Facts.Durations[1] != 8 || !got[0].Facts.Audio || !got[1].UserAdded || got[1].DisplayName != "Gemini" {
		t.Fatalf("%+v %v", got, err)
	}

	if err := r.UpdateSettings(ctx, "openai", provider.Settings{"image_max_workers": 3, "image_rpm": 60}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateSettings(ctx, "openai", provider.Settings{"image_max_workers": 5}, []string{"image_rpm"}); err != nil {
		t.Fatal(err)
	}
	set, _ := r.GetSettings(ctx, "openai")
	if set["image_max_workers"] != 5 || len(set) != 1 {
		t.Fatalf("settings %v", set)
	}

	if err := r.SetDefault(ctx, provider.PurposeImage, "openai/gpt-image-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetDefault(ctx, provider.PurposeImage, "custom-id/flux/dev"); err != nil {
		t.Fatal(err)
	}
	defs, _ := r.GetDefaults(ctx)
	if defs[provider.PurposeImage] != "custom-id/flux/dev" {
		t.Fatalf("defaults %v", defs)
	}
	_ = r.SetDefault(ctx, provider.PurposeImage, "")
	if defs, _ = r.GetDefaults(ctx); len(defs) != 0 {
		t.Fatalf("auto must clear: %v", defs)
	}

	// deleting a custom provider removes its models, settings, and matching defaults
	p, _ := r.CreateCustom(ctx, provider.CustomProvider{Name: "x", DiscoveryFormat: provider.DiscoveryOpenAI, BaseURL: "http://x"})
	_ = r.UpdateSettings(ctx, p.ID, provider.Settings{"video_max_workers": 2}, nil)
	_ = r.ReplaceModels(ctx, p.ID, models[:1])
	_ = r.SetDefault(ctx, provider.PurposeImage, p.ID+"/gpt-image-1")
	_ = r.SetDefault(ctx, provider.PurposeText, "openai/gpt-4o")
	if err := r.DeleteCustom(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := r.ListModels(ctx, p.ID); len(m) != 0 {
		t.Fatal("models left")
	}
	if s, _ := r.GetSettings(ctx, p.ID); len(s) != 0 {
		t.Fatal("settings left")
	}
	if defs, _ := r.GetDefaults(ctx); defs[provider.PurposeImage] != "" || defs[provider.PurposeText] != "openai/gpt-4o" {
		t.Fatalf("custom default not cleaned or unrelated default lost: %v", defs)
	}
}
