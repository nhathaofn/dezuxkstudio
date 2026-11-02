package test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"app/internal/contracts"
	"app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/tools/watermark"
)

var vietnameseDiacriticsRe = regexp.MustCompile(`(?i)[àáảãạăằắẳẵặâầấẩẫậèéẻẽẹêềếểễệìíỉĩịòóỏõọôồốổỗộơờớởỡợùúủũụưừứửữựỳýỷỹỵđ]`)

func assertNoVietnameseDiacritics(t *testing.T, text string, contextDesc string) {
	t.Helper()
	if vietnameseDiacriticsRe.MatchString(text) {
		t.Errorf("expected no Vietnamese diacritics in %s, got: %q", contextDesc, text)
	}
}

// ============================================================================
// Tier 1 — Feature Coverage
// ============================================================================

func TestE2E_Tier1_R2_BackendEnglishStandardization_ToolMetadata(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()

	resp, _ := c.call(contracts.ActionWorkflowSpecs, contracts.Empty{})
	if !resp.Success {
		t.Fatalf("workflow:specs failed: %+v", resp.Error)
	}

	var result contracts.WorkflowSpecsResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("unmarshal workflow specs: %v", err)
	}

	if len(result.Specs) == 0 {
		t.Fatal("expected at least one registered workflow node spec")
	}

	for _, spec := range result.Specs {
		assertNoVietnameseDiacritics(t, spec.Label, "node spec label ("+spec.Type+")")
		assertNoVietnameseDiacritics(t, spec.Description, "node spec description ("+spec.Type+")")

		for _, in := range spec.Inputs {
			assertNoVietnameseDiacritics(t, in.Label, "node input port label ("+spec.Type+":"+in.Name+")")
		}
		for _, out := range spec.Outputs {
			assertNoVietnameseDiacritics(t, out.Label, "node output port label ("+spec.Type+":"+out.Name+")")
		}
		for _, param := range spec.Params {
			assertNoVietnameseDiacritics(t, param.Label, "node param label ("+spec.Type+":"+param.Name+")")
		}

		if spec.Type == watermark.NodeType {
			if !strings.Contains(strings.ToLower(spec.Description), "watermark") {
				t.Errorf("expected watermark remover description to contain 'watermark', got: %q", spec.Description)
			}
		}
	}
}

func TestE2E_Tier1_R2_BackendEnglishStandardization_GraphErrors(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()

	// 1. Realtime Empty Graph Validation (Contracts DTO layer)
	emptyPayload := contracts.WorkflowRunGraphPayload{
		Graph: engine.Graph{
			Nodes: []engine.GraphNode{},
			Edges: []engine.Edge{},
		},
	}
	resp, _ := c.call(contracts.ActionWorkflowRunGraph, emptyPayload)
	if resp.Success {
		t.Fatal("expected empty graph to fail validation")
	}
	if resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("expected ERR_VALIDATION, got: %+v", resp.Error)
	}
	assertNoVietnameseDiacritics(t, resp.Error.Message, "empty graph error message")
	if !strings.Contains(strings.ToLower(resp.Error.Message), "node") {
		t.Errorf("unexpected empty graph error message: %q", resp.Error.Message)
	}

	// 2. Engine Layer: Cycle Detection Error
	reg := engine.NewRegistry()
	_ = reg.Register(watermark.NewNode(watermark.Options{}))

	cycleGraph := engine.Graph{
		Nodes: []engine.GraphNode{
			{ID: "node_a", Type: watermark.NodeType},
			{ID: "node_b", Type: watermark.NodeType},
		},
		Edges: []engine.Edge{
			{From: engine.Endpoint{Node: "node_a", Port: "image"}, To: engine.Endpoint{Node: "node_b", Port: "image"}},
			{From: engine.Endpoint{Node: "node_b", Port: "image"}, To: engine.Endpoint{Node: "node_a", Port: "image"}},
		},
	}
	_, cycleErr := reg.RunGraph(context.Background(), cycleGraph)
	if cycleErr == nil {
		t.Fatal("expected cycle error from engine.RunGraph")
	}
	assertNoVietnameseDiacritics(t, cycleErr.Error(), "engine cycle error")
	if !strings.Contains(strings.ToLower(cycleErr.Error()), "cycle") {
		t.Errorf("expected cycle error to contain 'cycle', got: %q", cycleErr.Error())
	}

	// 3. Engine Layer: Duplicate Node ID Error
	dupGraph := engine.Graph{
		Nodes: []engine.GraphNode{
			{ID: "node_dup", Type: watermark.NodeType},
			{ID: "node_dup", Type: watermark.NodeType},
		},
	}
	_, dupErr := reg.RunGraph(context.Background(), dupGraph)
	if dupErr == nil {
		t.Fatal("expected duplicate node error")
	}
	assertNoVietnameseDiacritics(t, dupErr.Error(), "engine duplicate node error")
	if !strings.Contains(strings.ToLower(dupErr.Error()), "duplicate") {
		t.Errorf("expected duplicate error, got: %q", dupErr.Error())
	}

	// 4. Engine Layer: Unknown Node Type Error
	unknownGraph := engine.Graph{
		Nodes: []engine.GraphNode{
			{ID: "node_x", Type: "nonexistent.type"},
		},
	}
	_, unkErr := reg.RunGraph(context.Background(), unknownGraph)
	if unkErr == nil {
		t.Fatal("expected unknown node error")
	}
	assertNoVietnameseDiacritics(t, unkErr.Error(), "engine unknown node error")
	if !strings.Contains(strings.ToLower(unkErr.Error()), "unknown") {
		t.Errorf("expected unknown node type error, got: %q", unkErr.Error())
	}

	// 5. Realtime Invocation Failure
	resp, _ = c.call(contracts.ActionWorkflowRunGraph, contracts.WorkflowRunGraphPayload{Graph: cycleGraph})
	if resp.Success {
		t.Fatal("expected cycle graph over realtime to fail")
	}
	if resp.Error == nil {
		t.Fatal("expected error body for cycle graph over realtime")
	}
	assertNoVietnameseDiacritics(t, resp.Error.Message, "realtime cycle error message")
}

func TestE2E_Tier1_R3_CentralizedBinariesManifest(t *testing.T) {
	ids := binaries.AllIDs()
	if len(ids) < 3 {
		t.Fatalf("expected at least 3 registered binaries (ffmpeg, ffprobe, yt_dlp), got: %d", len(ids))
	}

	expectedIDs := []binaries.BinaryID{binaries.IDFFmpeg, binaries.IDFFprobe, binaries.IDYtDlp}
	for _, id := range expectedIDs {
		entry, ok := binaries.GetManifest(string(id))
		if !ok {
			t.Fatalf("binary %s not found in centralized manifest", id)
		}

		// Field completeness
		if entry.ID != id {
			t.Errorf("entry ID mismatch: expected %s, got %s", id, entry.ID)
		}
		if entry.Name == "" || entry.Executable == "" || entry.Version == "" {
			t.Errorf("entry %s has empty required fields", id)
		}
		assertNoVietnameseDiacritics(t, entry.Name, "manifest binary name ("+string(id)+")")
		assertNoVietnameseDiacritics(t, entry.Description, "manifest binary description ("+string(id)+")")

		// Platform support
		if len(entry.Downloads) == 0 {
			t.Fatalf("binary %s has no download specs defined", id)
		}

		for platformKey, dlSpec := range entry.Downloads {
			if dlSpec.URL == "" {
				t.Errorf("binary %s platform %s has empty URL", id, platformKey)
			}
			parsed, err := url.Parse(dlSpec.URL)
			if err != nil {
				t.Errorf("binary %s platform %s has invalid URL %q: %v", id, platformKey, dlSpec.URL, err)
			}
			if parsed.Scheme != "https" {
				t.Errorf("binary %s platform %s URL must use HTTPS, got %q", id, platformKey, dlSpec.URL)
			}
			if parsed.Host == "" {
				t.Errorf("binary %s platform %s URL host is empty: %q", id, platformKey, dlSpec.URL)
			}
			if dlSpec.ArchiveFormat != binaries.ArchiveFormatZip && dlSpec.ArchiveFormat != binaries.ArchiveFormatRaw {
				t.Errorf("binary %s platform %s has invalid ArchiveFormat %q", id, platformKey, dlSpec.ArchiveFormat)
			}
		}

		// Windows platforms must exist
		if _, ok := entry.Downloads[binaries.PlatformWindowsAmd64]; !ok {
			t.Errorf("binary %s missing %s download spec", id, binaries.PlatformWindowsAmd64)
		}
		if _, ok := entry.Downloads[binaries.PlatformWindowsArm64]; !ok {
			t.Errorf("binary %s missing %s download spec", id, binaries.PlatformWindowsArm64)
		}

		// Version Regex verification
		if entry.VersionRegex == nil {
			t.Fatalf("binary %s has nil VersionRegex", id)
		}
	}

	// Canonical version extraction tests
	ffmpegEntry, _ := binaries.GetManifest(string(binaries.IDFFmpeg))
	parsedFfmpeg := ffmpegEntry.ExtractVersion("ffmpeg version 7.1-essentials_build Copyright (c)")
	if parsedFfmpeg != "7.1-essentials_build" {
		t.Errorf("ffmpeg version extraction failed: expected '7.1-essentials_build', got %q", parsedFfmpeg)
	}

	ffprobeEntry, _ := binaries.GetManifest(string(binaries.IDFFprobe))
	parsedFfprobe := ffprobeEntry.ExtractVersion("ffprobe version 7.1-essentials_build Copyright (c)")
	if parsedFfprobe != "7.1-essentials_build" {
		t.Errorf("ffprobe version extraction failed: expected '7.1-essentials_build', got %q", parsedFfprobe)
	}

	ytDlpEntry, _ := binaries.GetManifest(string(binaries.IDYtDlp))
	parsedYtDlp := ytDlpEntry.ExtractVersion("2024.10.22")
	if parsedYtDlp != "2024.10.22" {
		t.Errorf("yt-dlp version extraction failed: expected '2024.10.22', got %q", parsedYtDlp)
	}
}

func TestE2E_Tier1_R4_CoreLifecycleAndEndpoints(t *testing.T) {
	h := startCore(t, nil)

	// HTTP health check
	res, err := http.Get(h.httpURL("/health"))
	if err != nil {
		t.Fatalf("health check request failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health check returned non-200: %d", res.StatusCode)
	}

	// Realtime connection and basic list action
	c := h.dial()
	resp, _ := c.call(contracts.ActionBinariesList, contracts.Empty{})
	if !resp.Success {
		t.Fatalf("binaries:list failed: %+v", resp.Error)
	}
}

// ============================================================================
// Tier 2 — Boundary & Corner Cases
// ============================================================================

func TestE2E_Tier2_BoundaryAndCornerCases(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()

	// 1. Invalid / Empty Binary ID in Probe
	resp, _ := c.call(contracts.ActionBinariesProbe, contracts.BinaryIDPayload{ID: ""})
	if resp.Success {
		t.Fatal("expected empty binary id probe to fail validation")
	}
	if resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("expected ERR_VALIDATION for empty binary probe id, got: %+v", resp.Error)
	}

	// 2. Unsupported Binary ID in SetPath
	resp, _ = c.call(contracts.ActionBinariesSetPath, contracts.BinarySetPathPayload{
		ID:         "unknown_external_tool_xyz",
		CustomPath: "C:\\tools\\unknown.exe",
	})
	if resp.Success {
		t.Fatal("expected unknown binary set_path to fail validation")
	}
	if resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("expected ERR_VALIDATION for unknown binary id, got: %+v", resp.Error)
	}

	// 3. Manifest boundary: Non-existent tool lookup
	_, ok := binaries.GetManifest("completely_invalid_tool_identifier")
	if ok {
		t.Fatal("expected GetManifest for non-existent tool to return false")
	}

	// 4. ExtractVersion robustness against empty / garbage outputs
	ffmpegEntry, _ := binaries.GetManifest(string(binaries.IDFFmpeg))
	if ver := ffmpegEntry.ExtractVersion(""); ver != "" {
		t.Errorf("expected empty string for empty output, got: %q", ver)
	}
	if ver := ffmpegEntry.ExtractVersion("no matching regex patterns here 12345"); ver != "" {
		t.Errorf("expected empty string for non-matching output, got: %q", ver)
	}
	largeGarbage := strings.Repeat("A!@#$%^&*()_+{}[]:;<>?,./\n", 500)
	if ver := ffmpegEntry.ExtractVersion(largeGarbage); ver != "" {
		t.Errorf("expected empty string for large garbage output, got: %q", ver)
	}

	// 5. System open folder path traversal boundary
	resp, _ = c.call(contracts.ActionSystemOpenFolder, map[string]string{"target": "../../../windows/system32"})
	if resp.Success {
		t.Fatal("expected path traversal in open_folder to fail")
	}
	if resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("expected ERR_VALIDATION for path traversal, got: %+v", resp.Error)
	}
}

// ============================================================================
// Tier 3 — Cross-Feature Combinations
// ============================================================================

func TestE2E_Tier3_CrossFeature_ErrorContractsAndCodes(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()

	// Verify structured error responses across multiple actions
	actionsToTest := []struct {
		name    string
		payload any
	}{
		{contracts.ActionPreferencesUpdate, map[string]string{"theme": "invalid-theme-value"}},
		{contracts.ActionBinariesProbe, contracts.BinaryIDPayload{ID: "nonexistent_binary"}},
		{contracts.ActionWorkflowRunGraph, contracts.WorkflowRunGraphPayload{Graph: engine.Graph{}}},
	}

	for _, tc := range actionsToTest {
		resp, _ := c.call(tc.name, tc.payload)
		if resp.Success {
			t.Errorf("action %s with invalid payload should have returned success=false", tc.name)
		}
		if resp.Error == nil {
			t.Fatalf("action %s missing error body", tc.name)
		}
		if resp.Error.Code == "" {
			t.Errorf("action %s returned empty error code", tc.name)
		}
		if resp.Error.Message == "" {
			t.Errorf("action %s returned empty error message", tc.name)
		}
		assertNoVietnameseDiacritics(t, resp.Error.Message, "error message for "+tc.name)
	}
}

// ============================================================================
// Tier 4 — Real-World Application Scenarios
// ============================================================================

func TestE2E_Tier4_Scenario_EndToEndWorkflow(t *testing.T) {
	h := startCore(t, nil)
	clientA := h.dial()
	clientB := h.dial()

	// Step 1: Client A updates language to "vi"
	updateResp, _ := clientA.call(contracts.ActionPreferencesUpdate, map[string]string{"language": "vi"})
	if !updateResp.Success {
		t.Fatalf("update preferences failed: %+v", updateResp.Error)
	}

	// Step 2: Client B receives broadcast preferences event
	evt := clientB.next()
	if evt.Event != contracts.EventPreferencesUpdated {
		t.Fatalf("expected preferences:updated event on client B, got: %s", evt.Event)
	}
	if !strings.Contains(string(evt.Data), `"vi"`) {
		t.Fatalf("expected broadcast data to contain language vi, got: %s", evt.Data)
	}

	// Step 3: Client A queries binary manifest list
	listResp, _ := clientA.call(contracts.ActionBinariesList, contracts.Empty{})
	if !listResp.Success {
		t.Fatalf("binaries:list failed: %+v", listResp.Error)
	}
	var binList contracts.BinaryListResult
	if err := json.Unmarshal(listResp.Data, &binList); err != nil {
		t.Fatalf("unmarshal binary list: %v", err)
	}
	if len(binList.Binaries) < 3 {
		t.Fatalf("expected at least 3 binaries, got %d", len(binList.Binaries))
	}

	// Step 4: Client A sets custom path to a dummy executable in temp dir
	tmpDir := t.TempDir()
	dummyPath := filepath.Join(tmpDir, "ffmpeg.exe")
	if err := os.WriteFile(dummyPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write dummy executable: %v", err)
	}

	setPathResp, _ := clientA.call(contracts.ActionBinariesSetPath, contracts.BinarySetPathPayload{
		ID:         "ffmpeg",
		CustomPath: dummyPath,
	})
	if !setPathResp.Success {
		t.Fatalf("set custom path failed: %+v", setPathResp.Error)
	}
	var updatedInfo contracts.BinaryInfo
	if err := json.Unmarshal(setPathResp.Data, &updatedInfo); err != nil {
		t.Fatalf("unmarshal updated binary info: %v", err)
	}
	if updatedInfo.CustomPath != dummyPath {
		t.Errorf("expected custom_path %q, got %q", dummyPath, updatedInfo.CustomPath)
	}

	// Step 5: In Vietnamese UI state, Client A triggers workflow validation
	// Backend error responses must remain standardized in English (zero Vietnamese diacritics)
	valResp, _ := clientA.call(contracts.ActionWorkflowRunGraph, contracts.WorkflowRunGraphPayload{
		Graph: engine.Graph{},
	})
	if valResp.Success {
		t.Fatal("expected empty graph to fail validation")
	}
	if valResp.Error == nil || valResp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("expected ERR_VALIDATION, got: %+v", valResp.Error)
	}
	assertNoVietnameseDiacritics(t, valResp.Error.Message, "workflow validation error in Vietnamese UI session")
	if !strings.Contains(valResp.Error.Message, "Graph does not contain any nodes") &&
		!strings.Contains(strings.ToLower(valResp.Error.Message), "graph") {
		t.Errorf("unexpected validation error: %q", valResp.Error.Message)
	}
}
