package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	domainmedia "app/internal/domain/mediagen"
	mediagennodes "app/internal/tools/mediagen"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpStubMediaSvc struct{}

func (m *mcpStubMediaSvc) GenerateImage(_ context.Context, req domainmedia.ImageRequest) (domainmedia.Result, error) {
	return domainmedia.Result{Kind: domainmedia.KindImage, Path: filepath.Join(req.OutputDir, "a.png"), MimeType: "image/png", Provider: "mock", Model: "img-m"}, nil
}
func (m *mcpStubMediaSvc) GenerateVideo(_ context.Context, req domainmedia.VideoRequest) (domainmedia.Result, error) {
	return domainmedia.Result{Kind: domainmedia.KindVideo, Path: filepath.Join(req.OutputDir, "v.mp4"), MimeType: "video/mp4", Provider: "mock", Model: "vid-m"}, nil
}
func (m *mcpStubMediaSvc) GenerateTTS(_ context.Context, req domainmedia.TTSRequest) (domainmedia.Result, error) {
	return domainmedia.Result{Kind: domainmedia.KindTTS, Path: filepath.Join(req.OutputDir, "t.wav"), MimeType: "audio/wav", Provider: "mock", Model: "tts-m"}, nil
}

func callMCPText(t *testing.T, f *accessFixture, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := f.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text, res.IsError
		}
	}
	return "", res.IsError
}

func TestAgentMCPCanInvokeMediaGenNodes(t *testing.T) {
	f := newAccessFixture(t)
	nodes := mediagennodes.NewNodes(mediagennodes.Options{MediaDir: f.media, Service: &mcpStubMediaSvc{}})
	for _, n := range nodes {
		f.wf.specs = append(f.wf.specs, n.Spec())
	}

	// Verify workflow_list_specs exposes all 3 nodes
	specsTxt, isErr := callMCPText(t, f, "workflow_list_specs", map[string]any{})
	if isErr {
		t.Fatalf("workflow_list_specs failed: %s", specsTxt)
	}
	for _, want := range []string{mediagennodes.NodeTypeImage, mediagennodes.NodeTypeVideo, mediagennodes.NodeTypeTTS} {
		if !strings.Contains(specsTxt, want) {
			t.Fatalf("expected spec %s in workflow_list_specs output: %s", want, specsTxt)
		}
	}

	// Verify agent runs image without reference_images
	imgTxt, isErr := callMCPText(t, f, "workflow_run_node", map[string]any{
		"type": mediagennodes.NodeTypeImage,
		"input": map[string]any{
			"prompt":       "neon city",
			"aspect_ratio": "16:9",
			"resolution":   "2K",
		},
	})
	if isErr || f.wf.lastNode != mediagennodes.NodeTypeImage {
		t.Fatalf("expected agent workflow_run_node(image) to succeed (isErr=%v lastNode=%s text=%s)", isErr, f.wf.lastNode, imgTxt)
	}
	var imgPayload map[string]any
	_ = json.Unmarshal(f.wf.lastInput, &imgPayload)
	if imgPayload["prompt"] != "neon city" || imgPayload["aspect_ratio"] != "16:9" {
		t.Fatalf("unexpected merged image input: %+v", imgPayload)
	}

	// Verify agent runs video with prompt only
	vidTxt, isErr := callMCPText(t, f, "workflow_run_node", map[string]any{
		"type": mediagennodes.NodeTypeVideo,
		"input": map[string]any{
			"prompt":   "drone shot",
			"duration": 5,
		},
	})
	if isErr || f.wf.lastNode != mediagennodes.NodeTypeVideo {
		t.Fatalf("expected agent workflow_run_node(video) to succeed (isErr=%v text=%s)", isErr, vidTxt)
	}

	// Verify agent runs TTS with text
	ttsTxt, isErr := callMCPText(t, f, "workflow_run_node", map[string]any{
		"type": mediagennodes.NodeTypeTTS,
		"input": map[string]any{
			"text":   "xin chao",
			"format": "wav",
		},
	})
	if isErr || f.wf.lastNode != mediagennodes.NodeTypeTTS {
		t.Fatalf("expected agent workflow_run_node(tts) to succeed (isErr=%v text=%s)", isErr, ttsTxt)
	}

	// Verify AccessPolicy blocks writing output_dir into protected folders
	f.wf.lastNode = ""
	outTxt, isErr := callMCPText(t, f, "workflow_run_node", map[string]any{
		"type": mediagennodes.NodeTypeImage,
		"input": map[string]any{
			"prompt":     "bad",
			"output_dir": filepath.Join(f.system, "hack"),
		},
	})
	if !isErr {
		t.Fatalf("expected protected output_dir to be blocked by AccessPolicy: %s", outTxt)
	}
	if f.wf.lastNode != "" || strings.Contains(outTxt, "mock") {
		t.Fatalf("expected AccessPolicy to stop before RunNode")
	}
}
