package studio

import "testing"

func TestSlugifyAndCodeTag(t *testing.T) {
	if got := Slugify("Chuyến Tàu Đêm Số 9!"); got != "chuyen-tau-dem-so-9" {
		t.Fatalf("Slugify = %q, want chuyen-tau-dem-so-9", got)
	}
	if got := Slugify("   "); got != "project" {
		t.Fatalf("Slugify blank = %q, want project", got)
	}
	if got := NormalizeCodeTag("", "Nhân vật Minh"); got != "@Nhan_vat_Minh" {
		t.Fatalf("NormalizeCodeTag = %q, want @Nhan_vat_Minh", got)
	}
	if got := NormalizeCodeTag("@Hero_01", "ignored"); got != "@Hero_01" {
		t.Fatalf("NormalizeCodeTag = %q, want @Hero_01", got)
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
}
