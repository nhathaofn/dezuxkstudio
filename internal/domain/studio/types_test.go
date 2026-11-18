package studio

import (
	"errors"
	"strings"
	"testing"

	"app/internal/apperr"
	domainprovider "app/internal/domain/provider"
)

func TestSlugifyAndCodeTag(t *testing.T) {
	if got := Slugify("Chuyến Tàu Đêm Số 9!"); got != "chuyen-tau-dem-so-9" {
		t.Fatalf("Slugify = %q, want chuyen-tau-dem-so-9", got)
	}
	if got := Slugify("   "); got != "project" {
		t.Fatalf("Slugify blank = %q, want project", got)
	}
	longTitle := strings.Repeat("canh-quay-", 12)
	slug := Slugify(longTitle)
	if len(slug) > MaxSlugLength || strings.HasSuffix(slug, "-") {
		t.Fatalf("Slugify long = %q (len=%d), want <= %d without trailing dash", slug, len(slug), MaxSlugLength)
	}
	if got := NormalizeCodeTag("", "Nhân vật Minh"); got != "@Nhan_vat_Minh" {
		t.Fatalf("NormalizeCodeTag = %q, want @Nhan_vat_Minh", got)
	}
	if got := NormalizeCodeTag("@Hero_01", "ignored"); got != "@Hero_01" {
		t.Fatalf("NormalizeCodeTag = %q, want @Hero_01", got)
	}
}

func TestResolutionForAspectRatioAndBGMDefault(t *testing.T) {
	if DefaultBGMVolume != 0.22 {
		t.Fatalf("DefaultBGMVolume = %v, want 0.22", DefaultBGMVolume)
	}

	cases := []struct {
		aspect   string
		fallback string
		want     string
	}{
		{"16:9", "1920x1080", "1920x1080"},
		{"9:16", "1920x1080", "1080x1920"},
		{"1:1", "1920x1080", "1080x1080"},
		{"4:3", "1920x1080", "1440x1080"},
		{"3:4", "1920x1080", "1080x1440"},
		{"21:9", "1920x1080", "1920x824"},
		{"16:9", "3840x2160", "3840x2160"},
		{"9:16", "3840x2160", "2160x3840"},
		{"1:1", "3840x2160", "2160x2160"},
		{"4:3", "3840x2160", "2880x2160"},
		{"3:4", "3840x2160", "2160x2880"},
		{"16:9", "1280x720", "1280x720"},
		{"9:16", "1280x720", "720x1280"},
		{"1:1", "1280x720", "720x720"},
		{"invalid", "1280x720", "1280x720"},
	}
	for _, tc := range cases {
		if got := ResolutionForAspectRatio(tc.aspect, tc.fallback); got != tc.want {
			t.Errorf("ResolutionForAspectRatio(%q, %q) = %q, want %q", tc.aspect, tc.fallback, got, tc.want)
		}
	}
}

func TestProjectValidation(t *testing.T) {
	p := Project{Title: "Demo Film"}
	if err := p.Validate(); err != nil {
		t.Fatalf("valid project failed: %v", err)
	}
	if p.Slug != "demo-film" || p.Genre != GenreDrama || p.ProductionMode != ModeStoryboardI2V || p.AspectRatio != "16:9" {
		t.Fatalf("unexpected defaults: %+v", p)
	}

	for _, bad := range []Project{
		{Title: "   "},
		{Title: "x", Genre: "documentary"},
		{Title: "x", ProductionMode: "magic"},
		{Title: "x", AspectRatio: "99:1"},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("expected validation error for %+v", bad)
		}
	}
}

func TestEpisodeAssetShotValidation(t *testing.T) {
	ep := Episode{ProjectID: "p1"}
	if err := ep.Validate(); err != nil {
		t.Fatalf("valid episode failed: %v", err)
	}
	if ep.Code != "ep01" || ep.Stage != StageScript {
		t.Fatalf("unexpected episode defaults: %+v", ep)
	}

	asset := AssetEntry{ProjectID: "p1", Name: "Cô Gái Áo Dài"}
	if err := asset.Validate(); err != nil {
		t.Fatalf("valid asset failed: %v", err)
	}
	if asset.CodeTag != "@Co_Gai_Ao_Dai" || asset.Kind != AssetCharacter {
		t.Fatalf("unexpected asset defaults: %+v", asset)
	}

	shot := Shot{EpisodeID: "e1", RefAssetTags: []string{"Hero", "@hero", "Cafe"}}
	if err := shot.Validate(); err != nil {
		t.Fatalf("valid shot failed: %v", err)
	}
	if shot.ShotMode != ShotModeSingle || shot.DurationSec != 5 || shot.Status != ShotPending || len(shot.RefAssetTags) != 2 {
		t.Fatalf("unexpected shot defaults: %+v", shot)
	}

	longShot := Shot{EpisodeID: "e1", DurationSec: 90}
	if err := longShot.Validate(); err == nil {
		t.Fatal("expected default Validate() to reject 90s shot (>60s)")
	}
	if err := longShot.ValidateWithLimits(120, 8); err != nil {
		t.Fatalf("ValidateWithLimits(120, 8) failed for 90s shot: %v", err)
	}

	unsetShot := Shot{EpisodeID: "e1"}
	if err := unsetShot.ValidateWithLimits(120, 8); err != nil || unsetShot.DurationSec != 8 {
		t.Fatalf("expected default duration 8s, got %d err=%v", unsetShot.DurationSec, err)
	}

	negSeq := Shot{EpisodeID: "e1", Seq: -1}
	if err := negSeq.Validate(); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected CodeValidation for negative seq, got %v", err)
	}
}

func TestJobValidationAndPricingCatalogParity(t *testing.T) {
	for _, bad := range []Job{
		{},
		{ProjectID: "p1", JobKind: "unknown", IdempotencyKey: "k"},
		{ProjectID: "p1", JobKind: JobVideo, Status: "bogus", IdempotencyKey: "k"},
		{ProjectID: "p1", JobKind: JobVideo, IdempotencyKey: "   "},
	} {
		err := bad.Validate()
		if !apperr.IsCode(err, apperr.CodeValidation) {
			t.Fatalf("expected apperr.CodeValidation for %+v, got %v", bad, err)
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Details["msg_key"] == nil {
			t.Fatalf("expected i18n msg_key on job validation error, got %+v", ae)
		}
	}

	j := Job{ProjectID: "p1", JobKind: JobTTS, IdempotencyKey: "idem-1"}
	if err := j.ValidateWithMaxAttempts(5); err != nil {
		t.Fatalf("valid job failed: %v", err)
	}
	if j.Status != JobQueued || j.MaxAttempts != 5 || j.RequestJSON != "{}" || j.OutputJSON != "{}" {
		t.Fatalf("unexpected job defaults: %+v", j)
	}
	jExplicit := Job{ProjectID: "p1", JobKind: JobTTS, IdempotencyKey: "idem-2", MaxAttempts: 7}
	if err := jExplicit.ValidateWithMaxAttempts(5); err != nil || jExplicit.MaxAttempts != 7 {
		t.Fatalf("expected explicit MaxAttempts=7 preserved, got %+v err=%v", jExplicit, err)
	}

	knownProviders := make(map[string]bool)
	for _, spec := range domainprovider.Registry() {
		knownProviders[spec.ID] = true
	}
	catalog := DefaultPricingCatalog()
	var sawAIStudio, sawVertex bool
	for _, p := range catalog {
		if p.ProviderID == "google" {
			t.Fatalf("obsolete provider_id 'google' must not appear in DefaultPricingCatalog: %+v", p)
		}
		if p.ProviderID != "*" && !knownProviders[p.ProviderID] {
			t.Fatalf("unknown provider_id %q in DefaultPricingCatalog", p.ProviderID)
		}
		if p.ProviderID == domainprovider.IDGeminiAIStudio {
			sawAIStudio = true
		}
		if p.ProviderID == domainprovider.IDGeminiVertex {
			sawVertex = true
		}
	}
	if !sawAIStudio || !sawVertex {
		t.Fatalf("expected both gemini-aistudio and gemini-vertex in DefaultPricingCatalog (aistudio=%v vertex=%v)", sawAIStudio, sawVertex)
	}

	vidSpec := PreflightSpecForCapabilityWithDuration("video", 8)
	if vidSpec.DefaultUnitsPerItem != 8.0 {
		t.Fatalf("PreflightSpecForCapabilityWithDuration(video, 8) = %v, want 8.0", vidSpec.DefaultUnitsPerItem)
	}
}
