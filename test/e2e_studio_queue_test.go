package test

import (
	"bytes"
	"encoding/json"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/config"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
)

func TestE2EStudioQueuePricingAndHTTPMediaUploadDownload(t *testing.T) {
	h := startCore(t, func(c *config.Config) {
		c.Providers.AutoCheck = false
		c.Providers.CheckOnStartup = false
	})
	ws := h.dial()

	// 1. Create Studio project & episode.
	projRes, _ := ws.call(contracts.ActionStudioProjectCreate, contracts.StudioProjectCreatePayload{
		Title:              "Queue E2E Film",
		Genre:              domainstudio.GenreDrama,
		ProductionMode:     domainstudio.ModeStoryboardI2V,
		AutoApproveBatches: true,
	})
	if !projRes.Success {
		t.Fatalf("studio:project_create failed: %+v", projRes.Error)
	}
	var detail contracts.StudioProjectDetailResult
	if err := json.Unmarshal(projRes.Data, &detail); err != nil {
		t.Fatalf("unmarshal project detail: %v", err)
	}
	projID := detail.Project.ID
	epID := detail.ActiveEpisode.Episode.ID

	// 2. Pricing upsert + cost summary + batch preflight.
	priceRes, _ := ws.call(contracts.ActionStudioPricingUpsert, contracts.StudioPricingUpsertPayload{
		ProviderID:   "stub-prov",
		ModelID:      "stub-model",
		Capability:   "image",
		Unit:         "per_image",
		UnitPriceUSD: 0.05,
	})
	if !priceRes.Success {
		t.Fatalf("studio:pricing_upsert failed: %+v", priceRes.Error)
	}

	costRes, _ := ws.call(contracts.ActionStudioCostSummary, contracts.StudioCostSummaryPayload{
		ProjectID: projID,
		EpisodeID: epID,
	})
	if !costRes.Success {
		t.Fatalf("studio:cost_summary failed: %+v", costRes.Error)
	}

	// 3. Upsert script & enqueue durable queue list/control operations.
	rawScript := "Scene 1: Protagonist enters the station."
	saveRes, _ := ws.call(contracts.ActionStudioEpisodeSaveScript, contracts.StudioEpisodeSaveScriptPayload{
		EpisodeID: epID,
		RawScript: &rawScript,
	})
	if !saveRes.Success {
		t.Fatalf("studio:episode_save_script failed: %+v", saveRes.Error)
	}

	assetRes, _ := ws.call(contracts.ActionStudioAssetUpsert, contracts.StudioAssetUpsertPayload{
		ProjectID: projID,
		Kind:      domainstudio.AssetCharacter,
		Name:      "Hero",
		CodeTag:   "@Hero",
		Prompt:    "portrait",
	})
	if !assetRes.Success {
		t.Fatalf("studio:asset_upsert failed: %+v", assetRes.Error)
	}

	enqueueRes, _ := ws.call(contracts.ActionStudioQueueEnqueue, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID,
		Stage:     string(contracts.StudioStageGenerateAssets),
		Approved:  true,
	})
	if !enqueueRes.Success {
		t.Fatalf("studio:queue_enqueue failed: %+v", enqueueRes.Error)
	}
	var enqueueOut contracts.StudioQueueEnqueueResult
	if err := json.Unmarshal(enqueueRes.Data, &enqueueOut); err != nil {
		t.Fatalf("unmarshal enqueue result: %v", err)
	}

	listQRes, _ := ws.call(contracts.ActionStudioQueueList, contracts.StudioQueueListPayload{
		ProjectID: projID,
		EpisodeID: epID,
	})
	if !listQRes.Success {
		t.Fatalf("studio:queue_list failed: %+v", listQRes.Error)
	}

	// Pause episode jobs and cancel an enqueued job safely through studio:queue_control.
	pauseRes, _ := ws.call(contracts.ActionStudioQueueControl, contracts.StudioQueueControlPayload{
		ProjectID: projID,
		EpisodeID: epID,
		Action:    contracts.QueueActionPause,
	})
	if !pauseRes.Success {
		t.Fatalf("studio:queue_control pause failed: %+v", pauseRes.Error)
	}
	if len(enqueueOut.Jobs) > 0 {
		ctrlRes, _ := ws.call(contracts.ActionStudioQueueControl, contracts.StudioQueueControlPayload{
			ProjectID: projID,
			EpisodeID: epID,
			JobID:     enqueueOut.Jobs[0].ID,
			Action:    contracts.QueueActionCancel,
		})
		if !ctrlRes.Success {
			t.Fatalf("studio:queue_control cancel failed: %+v", ctrlRes.Error)
		}
	}

	// Verify paid stage without provider returns clear error rather than panicking.
	stageRes, _ := ws.call(contracts.ActionStudioEpisodeRunStage, contracts.StudioEpisodeRunStagePayload{
		EpisodeID: epID,
		Stage:     contracts.StudioStageScriptNormalize,
	})
	if stageRes.Success {
		t.Fatalf("expected script_normalize without configured text provider to fail gracefully")
	}

	// 4. HTTP POST /api/media/upload + GET /api/media/download (Range + single-use download vs reusable preview + 403 oracle).
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "upload_test.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	srcPNG := createCustomPNG(t, filepath.Join(t.TempDir(), "src.png"), color.RGBA{R: 12, G: 34, B: 56, A: 255})
	if _, err := fw.Write(srcPNG); err != nil {
		t.Fatalf("write png part: %v", err)
	}
	_ = mw.Close()

	httpClient := &http.Client{Timeout: 5 * time.Second}
	upReq, _ := http.NewRequest(http.MethodPost, h.httpURL("/api/media/upload"), &body)
	upReq.Header.Set("Authorization", "Bearer "+h.token)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upRes, err := httpClient.Do(upReq)
	if err != nil {
		t.Fatalf("POST /api/media/upload: %v", err)
	}
	upBody, _ := io.ReadAll(upRes.Body)
	_ = upRes.Body.Close()
	if upRes.StatusCode != http.StatusOK {
		t.Fatalf("upload status %d: %s", upRes.StatusCode, string(upBody))
	}
	var upOut struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(upBody, &upOut); err != nil || upOut.Path == "" {
		t.Fatalf("decode upload output: %v (%s)", err, string(upBody))
	}

	// Preview registration + Range request support.
	regRes, _ := ws.call(contracts.ActionMediaRegisterInput, contracts.MediaRegisterInputPayload{
		FilePath:   upOut.Path,
		Kind:       "image",
		SourceNode: "e2e_upload",
	})
	if !regRes.Success {
		t.Fatalf("media:register_input failed: %+v", regRes.Error)
	}
	var asset contracts.MediaAssetItem
	if err := json.Unmarshal(regRes.Data, &asset); err != nil {
		t.Fatalf("unmarshal asset: %v", err)
	}

	signPrev, _ := ws.call(contracts.ActionMediaSignURL, contracts.MediaSignURLPayload{
		AssetID: asset.ID,
		Purpose: "preview",
	})
	if !signPrev.Success {
		t.Fatalf("sign preview failed: %+v", signPrev.Error)
	}
	var prevSigned contracts.MediaSignedURLResult
	_ = json.Unmarshal(signPrev.Data, &prevSigned)

	// Two preview range requests with the same ticket must succeed.
	for attempt := 0; attempt < 2; attempt++ {
		dlReq, _ := http.NewRequest(http.MethodGet, h.httpURL(prevSigned.URL), nil)
		dlReq.Header.Set("Range", "bytes=0-3")
		dlRes, err := httpClient.Do(dlReq)
		if err != nil {
			t.Fatalf("preview range attempt %d: %v", attempt, err)
		}
		_ = dlRes.Body.Close()
		if dlRes.StatusCode != http.StatusPartialContent && dlRes.StatusCode != http.StatusOK {
			t.Fatalf("preview range status %d", dlRes.StatusCode)
		}
	}

	// Single-use download ticket succeeds once and rejects replay.
	signDL, _ := ws.call(contracts.ActionMediaSignURL, contracts.MediaSignURLPayload{
		AssetID: asset.ID,
		Purpose: "download",
	})
	var dlSigned contracts.MediaSignedURLResult
	_ = json.Unmarshal(signDL.Data, &dlSigned)

	req1, _ := http.NewRequest(http.MethodGet, h.httpURL(dlSigned.URL), nil)
	res1, err := httpClient.Do(req1)
	if err != nil {
		t.Fatalf("download 1: %v", err)
	}
	_ = res1.Body.Close()
	if res1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on first download, got %d", res1.StatusCode)
	}
	req2, _ := http.NewRequest(http.MethodGet, h.httpURL(dlSigned.URL), nil)
	res2, err := httpClient.Do(req2)
	if err != nil {
		t.Fatalf("download 2: %v", err)
	}
	_ = res2.Body.Close()
	if res2.StatusCode == http.StatusOK {
		t.Fatalf("expected download ticket replay to be rejected, got 200")
	}

	// Missing vs Forbidden oracle: both existing and missing outside paths return 403.
	missingOutside := filepath.Join(h.paths.Data, "..", "nonexistent_outside_probe.png")
	reqOracle, _ := http.NewRequest(http.MethodGet, h.httpURL("/api/media/download?path="+missingOutside), nil)
	reqOracle.Header.Set("Authorization", "Bearer "+h.token)
	resOracle, err := httpClient.Do(reqOracle)
	if err != nil {
		t.Fatalf("oracle req: %v", err)
	}
	_ = resOracle.Body.Close()
	if resOracle.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden (no 404 oracle) for outside path, got %d", resOracle.StatusCode)
	}

	// 5. workflow:run_graph validates cleanly via WS.
	graphRes, _ := ws.call(contracts.ActionWorkflowRunGraph, contracts.WorkflowRunGraphPayload{})
	if graphRes.Success || graphRes.Error == nil || !strings.Contains(string(graphRes.Error.Code), "ERR_") {
		t.Fatalf("expected structured validation error from workflow:run_graph, got %+v", graphRes)
	}
}
