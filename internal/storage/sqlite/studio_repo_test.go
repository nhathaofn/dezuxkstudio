package sqlite

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"app/internal/domain/studio"
)

func openTestStudioDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(context.Background(), Options{
		Path:          filepath.Join(dir, "studio.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 2,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestStudioRepositoryCRUDAndCascade(t *testing.T) {
	ctx := context.Background()
	db := openTestStudioDB(t)
	repo := NewStudioRepository(db)

	proj, err := repo.CreateProject(ctx, studio.Project{
		Title:          "Cánh Đồng Mây",
		Genre:          studio.GenreNarration,
		ProductionMode: studio.ModeStoryboardI2V,
		AspectRatio:    "16:9",
		StylePrompt:    "cinematic watercolor",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.Slug != "canh-dong-may" {
		t.Fatalf("unexpected slug: %q", proj.Slug)
	}

	gotBySlug, err := repo.GetProject(ctx, "canh-dong-may")
	if err != nil || gotBySlug.ID != proj.ID {
		t.Fatalf("GetProject by slug: %+v err=%v", gotBySlug, err)
	}

	// Duplicate slug fails with conflict
	if _, err := repo.CreateProject(ctx, studio.Project{Title: "Cánh Đồng Mây"}); err == nil {
		t.Fatal("expected duplicate slug error")
	}

	ep, err := repo.CreateEpisode(ctx, studio.Episode{
		ProjectID: proj.ID,
		Code:      "ep01",
		Title:     "Tập 1",
	})
	if err != nil {
		t.Fatalf("CreateEpisode: %v", err)
	}

	asset, err := repo.UpsertAsset(ctx, studio.AssetEntry{
		ProjectID:   proj.ID,
		Kind:        studio.AssetCharacter,
		Name:        "Minh",
		CodeTag:     "@Minh",
		Description: "Young traveler",
		Prompt:      "character turnaround sheet",
	})
	if err != nil {
		t.Fatalf("UpsertAsset: %v", err)
	}
	// Upsert same code_tag without ID updates the existing row
	asset2, err := repo.UpsertAsset(ctx, studio.AssetEntry{
		ProjectID:   proj.ID,
		Kind:        studio.AssetCharacter,
		Name:        "Minh",
		CodeTag:     "@Minh",
		Description: "Updated description",
		Prompt:      "updated turnaround",
	})
	if err != nil || asset2.ID != asset.ID || asset2.Description != "Updated description" {
		t.Fatalf("UpsertAsset update by tag failed: %+v err=%v", asset2, err)
	}

	shots, err := repo.ReplaceShots(ctx, ep.ID, []studio.Shot{
		{
			SceneCode:     "S01",
			Title:         "Opening",
			DurationSec:   5,
			ImagePrompt:   "@Minh standing on hill",
			VideoPrompt:   "slow push in",
			NarrationText: "Chuyện bắt đầu từ sáng sớm.",
			RefAssetTags:  []string{"@Minh"},
			Dialogues:     []studio.DialogueLine{{Speaker: "Minh", Line: "Đi thôi"}},
		},
		{
			SceneCode:   "S01",
			Title:       "Depart",
			DurationSec: 6,
			ImagePrompt: "train leaving station",
		},
	})
	if err != nil {
		t.Fatalf("ReplaceShots: %v", err)
	}
	if len(shots) != 2 || shots[0].Seq != 1 || shots[1].Seq != 2 {
		t.Fatalf("unexpected shots after ReplaceShots: %+v", shots)
	}

	reordered, err := repo.ReorderShots(ctx, ep.ID, []string{shots[1].ID, shots[0].ID})
	if err != nil {
		t.Fatalf("ReorderShots: %v", err)
	}
	if reordered[0].ID != shots[1].ID || reordered[0].Seq != 1 || reordered[1].Seq != 2 {
		t.Fatalf("unexpected shot order: %+v", reordered)
	}

	// Cascade delete removes episodes, assets, and shots
	if err := repo.DeleteProject(ctx, proj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if eps, _ := repo.ListEpisodes(ctx, proj.ID); len(eps) != 0 {
		t.Fatalf("expected episodes deleted on cascade, got %d", len(eps))
	}
	if shs, _ := repo.ListShots(ctx, ep.ID); len(shs) != 0 {
		t.Fatalf("expected shots deleted on cascade, got %d", len(shs))
	}
	if asts, _ := repo.ListAssets(ctx, proj.ID); len(asts) != 0 {
		t.Fatalf("expected assets deleted on cascade, got %d", len(asts))
	}
}

func TestStudioPhase2JobsBillingVersionsQC(t *testing.T) {
	ctx := context.Background()
	db := openTestStudioDB(t)
	repo := NewStudioRepository(db)

	var schemaVer int
	if err := db.sql.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM _schema_migrations`).Scan(&schemaVer); err != nil || schemaVer < 20 {
		t.Fatalf("schema version = %d, err = %v, want >= 20", schemaVer, err)
	}

	proj, err := repo.CreateProject(ctx, studio.Project{Title: "Phase2 Film"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ep, err := repo.CreateEpisode(ctx, studio.Episode{ProjectID: proj.ID, Code: "ep01"})
	if err != nil {
		t.Fatalf("CreateEpisode: %v", err)
	}

	// Queue lifecycle + idempotency + crash recovery lookup + pause/resume preserving provider_job_id
	j1, err := repo.CreateJob(ctx, studio.Job{
		ProjectID:      proj.ID,
		EpisodeID:      ep.ID,
		ShotID:         "shot-1",
		JobKind:        studio.JobVideo,
		ProviderID:     "kling",
		ModelRef:       "kling:kling-v2",
		IdempotencyKey: "ep01-shot1-v1",
		EstimatedUSD:   0.35,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if _, err := repo.CreateJob(ctx, studio.Job{
		ProjectID:      proj.ID,
		JobKind:        studio.JobVideo,
		IdempotencyKey: "ep01-shot1-v1",
	}); err == nil {
		t.Fatal("expected duplicate idempotency_key conflict")
	}

	claimed, ok, err := repo.ClaimNextQueuedJob(ctx, 1000)
	if err != nil || !ok || claimed.ID != j1.ID || claimed.Status != studio.JobSubmitted || claimed.Attempts != 1 {
		t.Fatalf("ClaimNextQueuedJob = %+v ok=%v err=%v", claimed, ok, err)
	}
	// Second claim sees nothing queued
	if _, ok2, _ := repo.ClaimNextQueuedJob(ctx, 1001); ok2 {
		t.Fatal("expected no second claim for already claimed job")
	}

	sub, err := repo.SetJobProviderSubmission(ctx, j1.ID, "remote-123", `{"protocol":"kling-video"}`, studio.JobPolling, 2000)
	if err != nil || sub.ProviderJobID != "remote-123" || sub.Status != studio.JobPolling {
		t.Fatalf("SetJobProviderSubmission = %+v err=%v", sub, err)
	}

	rec, err := repo.ListRecoverableJobs(ctx)
	if err != nil || len(rec) != 1 || rec[0].ProviderJobID != "remote-123" {
		t.Fatalf("ListRecoverableJobs = %+v err=%v", rec, err)
	}

	if paused, err := repo.PauseEpisodeJobs(ctx, proj.ID, ep.ID); err != nil || paused != 1 {
		t.Fatalf("PauseEpisodeJobs = %d err=%v", paused, err)
	}
	if resumed, err := repo.ResumeEpisodeJobs(ctx, proj.ID, ep.ID); err != nil || resumed != 1 {
		t.Fatalf("ResumeEpisodeJobs = %d err=%v", resumed, err)
	}
	afterResume, _ := repo.GetJob(ctx, j1.ID)
	if afterResume.Status != studio.JobPolling || afterResume.ProviderJobID != "remote-123" {
		t.Fatalf("resumed job with provider_job_id must return to polling without losing handle: %+v", afterResume)
	}

	pollClaim, ok, err := repo.ClaimNextPollJob(ctx, afterResume.NextPollAt+1, 30_000)
	if err != nil || !ok || pollClaim.ID != j1.ID {
		t.Fatalf("ClaimNextPollJob = %+v ok=%v err=%v", pollClaim, ok, err)
	}

	// Pricing lookup specificity & Cost ledger
	price, ok, err := repo.ResolveUnitPrice(ctx, "openai", "sora-2", "video", "second")
	if err != nil || !ok || price.UnitPriceUSD != 0.10 {
		t.Fatalf("ResolveUnitPrice exact = %+v ok=%v err=%v", price, ok, err)
	}
	wildPrice, ok, err := repo.ResolveUnitPrice(ctx, "custom:1", "unknown-model", "image", "image")
	if err != nil || !ok || wildPrice.UnitPriceUSD != 0.04 {
		t.Fatalf("ResolveUnitPrice wildcard = %+v ok=%v err=%v", wildPrice, ok, err)
	}
	if _, err := repo.RecordCost(ctx, studio.CostLedgerEntry{
		ProjectID: proj.ID, EpisodeID: ep.ID, JobID: j1.ID,
		ProviderID: "openai", ModelID: "sora-2", Capability: "video",
		Operation: "generate_video", OutputUnits: 5, Unit: "second",
		EstimatedUSD: 0.50, ActualUSD: 0.50,
	}); err != nil {
		t.Fatalf("RecordCost: %v", err)
	}
	sum, err := repo.SummarizeCosts(ctx, proj.ID, ep.ID)
	if err != nil || sum.ActualTotalUSD != 0.50 || sum.EntryCount != 1 {
		t.Fatalf("SummarizeCosts = %+v err=%v", sum, err)
	}

	// Media versions atomic selection & QC link
	seed1, seed2 := 42, 99
	v1, err := repo.CreateMediaVersion(ctx, studio.MediaVersion{
		ProjectID: proj.ID, EpisodeID: ep.ID, TargetKind: studio.TargetKeyframe, TargetID: "shot-1",
		MediaPath: "/tmp/k1.png", Seed: &seed1, IsSelected: true,
	})
	if err != nil || v1.VersionNum != 1 || !v1.IsSelected {
		t.Fatalf("CreateMediaVersion v1 = %+v err=%v", v1, err)
	}
	v2, err := repo.CreateMediaVersion(ctx, studio.MediaVersion{
		ProjectID: proj.ID, EpisodeID: ep.ID, TargetKind: studio.TargetKeyframe, TargetID: "shot-1",
		MediaPath: "/tmp/k2.png", Seed: &seed2, IsSelected: false,
	})
	if err != nil || v2.VersionNum != 2 || v2.IsSelected {
		t.Fatalf("CreateMediaVersion v2 = %+v err=%v", v2, err)
	}
	if _, err := repo.SelectMediaVersion(ctx, v2.ID); err != nil {
		t.Fatalf("SelectMediaVersion: %v", err)
	}
	vers, _ := repo.ListMediaVersions(ctx, studio.TargetKeyframe, "shot-1")
	if len(vers) != 2 || !vers[0].IsSelected || vers[0].ID != v2.ID || vers[1].IsSelected {
		t.Fatalf("expected v2 selected exclusively, got %+v", vers)
	}

	rep, err := repo.CreateQCReport(ctx, studio.QCReport{
		ProjectID: proj.ID, EpisodeID: ep.ID, TargetKind: studio.TargetKeyframe, TargetID: "shot-1",
		MediaVersionID: v2.ID, MediaPath: "/tmp/k2.png", Verdict: studio.QCPass, OverallScore: 9.2,
		Issues:       []studio.QCIssue{{Severity: "info", Category: "technical", Message: "crisp"}},
		SampleFrames: []string{"/tmp/f1.png"},
	})
	if err != nil || rep.ID == "" {
		t.Fatalf("CreateQCReport: %v", err)
	}
	v2After, _ := repo.GetMediaVersion(ctx, v2.ID)
	if v2After.QCStatus != studio.QCPass {
		t.Fatalf("expected version qc_status updated to pass, got %q", v2After.QCStatus)
	}
}
