package mediagen

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
)

type fakeMediaSvc struct {
	mediaDir  string
	lastImage domainmedia.ImageRequest
	lastVideo domainmedia.VideoRequest
	lastTTS   domainmedia.TTSRequest
}

func (f *fakeMediaSvc) GenerateImage(_ context.Context, req domainmedia.ImageRequest) (domainmedia.Result, error) {
	f.lastImage = req
	out := filepath.Join(req.OutputDir, "out.png")
	_ = os.MkdirAll(filepath.Dir(out), 0o750)
	_ = os.WriteFile(out, []byte("png"), 0o600)
	return domainmedia.Result{
		Kind:      domainmedia.KindImage,
		Path:      out,
		MimeType:  "image/png",
		Provider:  "MockProvider",
		Model:     "mock-img",
		SizeBytes: 3,
	}, nil
}

func (f *fakeMediaSvc) GenerateVideo(_ context.Context, req domainmedia.VideoRequest) (domainmedia.Result, error) {
	f.lastVideo = req
	out := filepath.Join(req.OutputDir, "out.mp4")
	_ = os.MkdirAll(filepath.Dir(out), 0o750)
	_ = os.WriteFile(out, []byte("mp4"), 0o600)
	return domainmedia.Result{
		Kind:      domainmedia.KindVideo,
		Path:      out,
		MimeType:  "video/mp4",
		Provider:  "MockProvider",
		Model:     "mock-vid",
		Duration:  float64(req.DurationSec),
		SizeBytes: 3,
	}, nil
}

func (f *fakeMediaSvc) GenerateTTS(_ context.Context, req domainmedia.TTSRequest) (domainmedia.Result, error) {
	f.lastTTS = req
	out := filepath.Join(req.OutputDir, "out.wav")
	_ = os.MkdirAll(filepath.Dir(out), 0o750)
	_ = os.WriteFile(out, []byte("RIFF"), 0o600)
	return domainmedia.Result{
		Kind:      domainmedia.KindTTS,
		Path:      out,
		MimeType:  "audio/wav",
		Provider:  "MockProvider",
		Model:     "mock-tts",
		SizeBytes: 4,
	}, nil
}

func TestNodesValidateAndRun(t *testing.T) {
	mediaDir := t.TempDir()
	svc := &fakeMediaSvc{mediaDir: mediaDir}
	reg := engine.NewRegistry()
	for _, n := range NewNodes(Options{MediaDir: mediaDir, Service: svc}) {
		if err := engine.ValidateSpec(n.Spec()); err != nil {
			t.Fatalf("ValidateSpec(%s): %v", n.Spec().Type, err)
		}
		if err := reg.Register(n); err != nil {
			t.Fatalf("Register(%s): %v", n.Spec().Type, err)
		}
	}

	refFile := filepath.Join(mediaDir, "ref.png")
	if err := os.WriteFile(refFile, []byte("ref"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Image node accepts string path reference & relative output dir
	imgRaw, err := reg.Run(context.Background(), NodeTypeImage, json.RawMessage(`{
		"prompt": "cyberpunk street",
		"reference_images": ["ref.png"],
		"aspect_ratio": "16:9",
		"resolution": "2K",
		"seed": 42
	}`))
	if err != nil {
		t.Fatalf("Run(image): %v", err)
	}
	imgOut := imgRaw.(ImageGenerateOutput)
	if imgOut.Image.LocalURL == "" || imgOut.Provider != "MockProvider" {
		t.Fatalf("unexpected image output: %+v", imgOut)
	}
	if len(svc.lastImage.ReferenceImages) != 1 || svc.lastImage.ReferenceImages[0] != refFile {
		t.Fatalf("expected relative ref resolved inside mediaDir, got %v", svc.lastImage.ReferenceImages)
	}

	// 2. Graph chaining image output -> video start_image
	graphRes, err := reg.RunGraph(context.Background(), engine.Graph{
		Nodes: []engine.GraphNode{
			{
				ID:     "img",
				Type:   NodeTypeImage,
				Params: json.RawMessage(`{"prompt":"opening frame"}`),
			},
			{
				ID:     "vid",
				Type:   NodeTypeVideo,
				Params: json.RawMessage(`{"prompt":"slow push in","duration":8}`),
			},
		},
		Edges: []engine.Edge{
			{From: engine.Endpoint{Node: "img", Port: "image"}, To: engine.Endpoint{Node: "vid", Port: "start_image"}},
		},
	})
	if err != nil || !graphRes.Done() {
		t.Fatalf("RunGraph failed: err=%v res=%+v", err, graphRes)
	}
	if svc.lastVideo.StartImage == "" || svc.lastVideo.DurationSec != 8 {
		t.Fatalf("chained video input mismatch: %+v", svc.lastVideo)
	}

	// 3. TTS node (accepts string speed too)
	ttsRaw, err := reg.Run(context.Background(), NodeTypeTTS, json.RawMessage(`{
		"text": "xin chao",
		"voice": "Kore",
		"format": "wav",
		"speed": "1.1"
	}`))
	if err != nil {
		t.Fatalf("Run(tts): %v", err)
	}
	ttsOut := ttsRaw.(TTSGenerateOutput)
	if ttsOut.Audio.Kind != engine.MediaAudio || ttsOut.MimeType != "audio/wav" {
		t.Fatalf("unexpected tts output: %+v", ttsOut)
	}
}

func TestNodesFileGuardAndTraversal(t *testing.T) {
	mediaDir := t.TempDir()
	svc := &fakeMediaSvc{mediaDir: mediaDir}
	node := NewImageNode(Options{MediaDir: mediaDir, Service: svc})

	// Traversal escape must fail
	if _, err := node.Run(context.Background(), json.RawMessage(`{
		"prompt": "hi",
		"output_dir": "../../outside"
	}`)); err == nil {
		t.Fatal("expected traversal path outside mediaDir to be rejected")
	}

	// FileGuard block must stop execution
	sentinel := errors.New("blocked by policy")
	guardedCtx := engine.WithFileGuard(context.Background(), func(path string, write bool) error {
		if write && strings.Contains(path, "forbidden") {
			return sentinel
		}
		return nil
	})
	_, err := node.Run(guardedCtx, json.RawMessage(`{
		"prompt": "hi",
		"output_dir": "forbidden"
	}`))
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected FileGuard error, got %v", err)
	}
}
