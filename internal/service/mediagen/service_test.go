package mediagen

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"app/internal/config"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/engine"
	"app/internal/service/media"
)

type fakeResolver struct {
	resolveFn func(ctx context.Context, p domainprovider.Purpose, ref string) (domainprovider.ResolvedModel, error)
	vertexFn  func(ctx context.Context, raw string) (string, string, time.Duration, error)
	vertexHit atomic.Int32
}

func (f *fakeResolver) ResolveModel(ctx context.Context, p domainprovider.Purpose, ref string) (domainprovider.ResolvedModel, error) {
	return f.resolveFn(ctx, p, ref)
}

func (f *fakeResolver) VertexToken(ctx context.Context, raw string) (string, string, time.Duration, error) {
	f.vertexHit.Add(1)
	if f.vertexFn != nil {
		return f.vertexFn(ctx, raw)
	}
	return "vtx-token", "test-proj", time.Hour, nil
}

func testService(t *testing.T, resolver *fakeResolver) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	svc := New(Options{
		Providers: resolver,
		MediaDir:  dir,
		Config: config.MediaGenConfig{
			RequestTimeout:    10 * time.Second,
			VideoTimeout:      10 * time.Second,
			VideoPollInterval: 5 * time.Millisecond,
			MaxOutputBytes:    4 << 20,
			MaxReferenceBytes: 1 << 20,
		},
	})
	return svc, dir
}

func writeTempPNG(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ref.png")
	// Minimal valid PNG header bytes so DetectContentType identifies image/png.
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'E', 'N', 'D'}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var fakePNG = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3, 4}
var fakeMP4 = []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'}

func TestImageGenerationAllProtocols(t *testing.T) {
	refPath := writeTempPNG(t)
	var b64PNG = base64.StdEncoding.EncodeToString(fakePNG)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/images/generations" && strings.Contains(r.Header.Get("Authorization"), "Bearer "):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			if body["model"] == "grok-image" {
				_, _ = w.Write([]byte(`{"data":[{"url":"` + srv.URL + `/dl/out.png"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64PNG + `"}]}`))

		case r.URL.Path == "/v1/images/edits":
			mr, err := r.MultipartReader()
			if err != nil {
				t.Fatalf("expected multipart edits: %v", err)
			}
			sawFile := false
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(p.FormName(), "image") {
					sawFile = true
				}
			}
			if !sawFile {
				t.Fatalf("missing image part in /images/edits")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64PNG + `"}]}`))

		case r.URL.Path == "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"images":[{"image_url":{"url":"data:image/png;base64,` + b64PNG + `"}}]}}]}`))

		case r.URL.Path == "/api/v1/images":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["input_references"] == nil {
				t.Fatalf("expected input_references in OpenRouter payload")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"url":"` + srv.URL + `/dl/out.png"}]}`))

		case r.URL.Path == "/v1beta/models/gemini-2.5-flash-image:generateContent":
			if r.Header.Get("x-goog-api-key") != "gem-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + b64PNG + `"}}]}}]}`))

		case r.URL.Path == "/v1beta1/projects/test-proj/locations/global/publishers/google/models/imagen-3:predict":
			if r.Header.Get("Authorization") != "Bearer vtx-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"predictions":[{"bytesBase64Encoded":"` + b64PNG + `","mimeType":"image/png"}]}`))

		case r.URL.Path == "/api/plan/v3/images/generations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64PNG + `"}]}`))

		case r.URL.Path == "/api/v1/services/aigc/multimodal-generation/generation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[{"image":"` + srv.URL + `/dl/out.png"}]}}]}}`))

		case r.URL.Path == "/v1/image_generation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"base_resp":{"status_code":0},"data":{"image_base64":["` + b64PNG + `"]}}`))

		case r.URL.Path == "/v1/images/generations" && r.Method == "POST":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"kling-1"}}`))
		case r.URL.Path == "/v1/images/generations/kling-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"task_status":"succeed","task_result":{"images":[{"url":"` + srv.URL + `/dl/out.png"}]}}}`))

		case r.URL.Path == "/ent/v2/reference2image":
			if r.Header.Get("Authorization") != "Token vidu-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"vidu-1"}`))
		case r.URL.Path == "/ent/v2/tasks/vidu-1/creations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"state":"success","creations":[{"url":"` + srv.URL + `/dl/out.png"}]}`))

		case r.URL.Path == "/dl/out.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(fakePNG)

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Note: Kling uses Bearer JWT in Authorization header so it matches r.URL.Path == "/v1/images/generations" only if checked cleanly;
	// verify Kling JWT path by handling task submit vs OpenAI/Grok cleanly: in srv above, any Bearer hits /v1/images/generations!
	// Wait: let's differentiate Kling via "model_name" in a separate server or route path prefix "/kling"!
	klingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				http.Error(w, "missing jwt", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"k-img-1"}}`))
		case "/v1/images/generations/k-img-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"task_status":"succeed","task_result":{"images":[{"url":"` + srv.URL + `/dl/out.png"}]}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer klingSrv.Close()

	cases := []struct {
		name     string
		resolved domainprovider.ResolvedModel
		req      domainmedia.ImageRequest
	}{
		{
			name: "openai-t2i",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gpt-image-1", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "sunshine"},
		},
		{
			name: "openai-i2i",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gpt-image-1", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "recolor", ReferenceImages: []string{refPath}},
		},
		{
			name: "custom-chat-image",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gemini-img", Capability: domainprovider.CapabilityImage, Endpoint: domainprovider.EndpointOpenAIChatImage},
				Custom:  &domainprovider.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "c1", Name: "Proxy", BaseURL: srv.URL}, APIKey: "k"},
			},
			req: domainmedia.ImageRequest{Prompt: "chat image"},
		},
		{
			name: "custom-openrouter-images",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "flux-2", Capability: domainprovider.CapabilityImage, Endpoint: domainprovider.EndpointOpenRouterImages},
				Custom:  &domainprovider.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "or", Name: "OR", BaseURL: srv.URL + "/api/v1"}, APIKey: "k"},
			},
			req: domainmedia.ImageRequest{Prompt: "or image", ReferenceImages: []string{refPath}},
		},
		{
			name: "gemini-aistudio",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gemini-2.5-flash-image", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGeminiAIStudio, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "gem-key"}},
			},
			req: domainmedia.ImageRequest{Prompt: "gem", ReferenceImages: []string{refPath}},
		},
		{
			name: "gemini-vertex-imagen-cached-token",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "imagen-3", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGeminiVertex, CredentialID: "v1", BaseURL: srv.URL, Secrets: map[string]string{domainprovider.FieldCredentialsFile: `{"project_id":"test-proj"}`}},
			},
			req: domainmedia.ImageRequest{Prompt: "imagen"},
		},
		{
			name: "ark-agent-plan",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "seedream-4", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDArkAgentPlan, BaseURL: srv.URL + "/api/plan/v3", Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "ark"},
		},
		{
			name: "dashscope",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "qwen-image", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDDashScope, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "dash"},
		},
		{
			name: "minimax",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "image-01", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "mm"},
		},
		{
			name: "kling-jwt",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "kling-v2", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDKling, BaseURL: klingSrv.URL, Secrets: map[string]string{"access_key": "ak", "secret_key": "sk"}},
			},
			req: domainmedia.ImageRequest{Prompt: "kling"},
		},
		{
			name: "vidu",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "viduq2", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDVidu, BaseURL: srv.URL + "/ent/v2", Secrets: map[string]string{"api_key": "vidu-key"}},
			},
			req: domainmedia.ImageRequest{Prompt: "vidu"},
		},
		{
			name: "grok",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "grok-image", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGrok, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "grok"},
		},
		{
			name: "agnes",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "agnes-img", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDAgnes, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "agnes"},
		},
	}

	resolver := &fakeResolver{}
	svc, mediaDir := testService(t, resolver)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver.resolveFn = func(context.Context, domainprovider.Purpose, string) (domainprovider.ResolvedModel, error) {
				return tc.resolved, nil
			}
			res, err := svc.GenerateImage(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("GenerateImage error: %v", err)
			}
			if !strings.HasPrefix(res.Path, filepath.Join(mediaDir, "images")) {
				t.Fatalf("unexpected output dir: %s", res.Path)
			}
			raw, err := os.ReadFile(res.Path)
			if err != nil || len(raw) == 0 {
				t.Fatalf("failed reading generated file: %v", err)
			}
			if res.MimeType != "image/png" {
				t.Fatalf("MimeType = %s, want image/png", res.MimeType)
			}
		})
	}
}

func TestVideoGenerationAllProtocols(t *testing.T) {
	refPath := writeTempPNG(t)
	var polls atomic.Int32

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/videos" && r.Method == "POST":
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["model"] == "agnes-2.5-vid" {
					_, _ = w.Write([]byte(`{"task_id":"ag-v1"}`))
					return
				}
			}
			_, _ = w.Write([]byte(`{"id":"oai-vid-1"}`))
		case r.URL.Path == "/agnesapi" && r.Method == "GET":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"completed","video_url":"` + srv.URL + `/dl/video.mp4"}`))
		case r.URL.Path == "/v1/videos/oai-vid-1" && r.Method == "GET":
			w.Header().Set("Content-Type", "application/json")
			if polls.Add(1) < 2 {
				_, _ = w.Write([]byte(`{"status":"in_progress"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"completed"}`))
		case r.URL.Path == "/v1/videos/oai-vid-1/content":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(fakeMP4)

		case r.URL.Path == "/v1beta/models/veo-3:predictLongRunning":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"operations/v1"}`))
		case r.URL.Path == "/v1beta/operations/v1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":"` + srv.URL + `/dl/video.mp4","mimeType":"video/mp4"}}]}}}`))

		case r.URL.Path == "/v1/projects/test-proj/locations/global/publishers/google/models/veo-vertex:predictLongRunning":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"op-vtx"}`))
		case r.URL.Path == "/v1/projects/test-proj/locations/global/publishers/google/models/veo-vertex:fetchPredictOperation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"done":true,"response":{"videos":[{"bytesBase64Encoded":"` + base64.StdEncoding.EncodeToString(fakeMP4) + `","mimeType":"video/mp4"}]}}`))

		case r.URL.Path == "/api/v3/contents/generations/tasks" && r.Method == "POST":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ark-1"}`))
		case r.URL.Path == "/api/v3/contents/generations/tasks/ark-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"succeeded","content":{"video_url":"` + srv.URL + `/dl/video.mp4"}}`))

		case r.URL.Path == "/api/v1/services/aigc/video-generation/video-synthesis":
			if r.Header.Get("X-DashScope-Async") != "enable" {
				http.Error(w, "missing async header", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":{"task_id":"ds-v-1"}}`))
		case r.URL.Path == "/api/v1/tasks/ds-v-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":{"task_status":"SUCCEEDED","video_url":"` + srv.URL + `/dl/video.mp4"}}`))

		case r.URL.Path == "/v1/video_generation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"mm-v1","base_resp":{"status_code":0}}`))
		case r.URL.Path == "/v1/query/video_generation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"Success","file_id":"f1"}`))
		case r.URL.Path == "/v1/files/retrieve":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"file":{"download_url":"` + srv.URL + `/dl/video.mp4"}}`))

		case r.URL.Path == "/v2/video_generation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"mm-h3","base_resp":{"status_code":0}}`))
		case r.URL.Path == "/v2/query/video_generation/mm-h3":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task":{"status":"succeeded","content":{"url":"` + srv.URL + `/dl/video.mp4"}}}`))

		case r.URL.Path == "/v1/videos/image2video":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"kl-1"}}`))
		case r.URL.Path == "/v1/videos/image2video/kl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"task_status":"succeed","task_result":{"videos":[{"url":"` + srv.URL + `/dl/video.mp4"}]}}}`))

		case r.URL.Path == "/ent/v2/start-end2video":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"vd-1"}`))
		case r.URL.Path == "/ent/v2/tasks/vd-1/creations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"state":"success","creations":[{"url":"` + srv.URL + `/dl/video.mp4"}]}`))

		case r.URL.Path == "/v1/videos/generations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"request_id":"grk-1"}`))
		case r.URL.Path == "/v1/videos/grk-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"done","video":{"url":"` + srv.URL + `/dl/video.mp4"}}`))

		case r.URL.Path == "/dl/video.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(fakeMP4)

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resolver := &fakeResolver{}
	svc, mediaDir := testService(t, resolver)

	cases := []struct {
		name     string
		resolved domainprovider.ResolvedModel
		req      domainmedia.VideoRequest
		wantDur  float64
	}{
		{
			name: "openai-sora",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "sora-2", Capability: domainprovider.CapabilityVideo, Facts: domainprovider.ModelFacts{Durations: []int{4, 8}}},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "ocean wave", StartImage: refPath},
			wantDur: 4, // snapped nearest to 5 among {4, 8}
		},
		{
			name: "gemini-aistudio",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "veo-3", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGeminiAIStudio, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "gk"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "city flyover"},
			wantDur: 5,
		},
		{
			name: "gemini-vertex",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "veo-vertex", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGeminiVertex, BaseURL: srv.URL, Secrets: map[string]string{domainprovider.FieldCredentialsFile: `{"project_id":"test-proj"}`}},
			},
			req:     domainmedia.VideoRequest{Prompt: "forest"},
			wantDur: 5,
		},
		{
			name: "ark",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "seedance", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDArk, BaseURL: srv.URL + "/api/v3", Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "seedance", StartImage: refPath, EndImage: refPath},
			wantDur: 5,
		},
		{
			name: "dashscope",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "wan2.6", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDDashScope, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "wan"},
			wantDur: 5,
		},
		{
			name: "minimax-v1",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "MiniMax-Hailuo-2.3", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "hailuo"},
			wantDur: 5,
		},
		{
			name: "minimax-h3",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "MiniMax-H3", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "h3"},
			wantDur: 5,
		},
		{
			name: "kling-i2v",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "kling-v2", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDKling, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "kling", StartImage: refPath},
			wantDur: 5,
		},
		{
			name: "vidu-start-end",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "viduq3", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDVidu, BaseURL: srv.URL + "/ent/v2", Secrets: map[string]string{"api_key": "vk"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "vidu", StartImage: refPath, EndImage: refPath},
			wantDur: 5,
		},
		{
			name: "grok",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "grok-imagine-video", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGrok, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "grok"},
			wantDur: 5,
		},
		{
			name: "agnes-video",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "agnes-2.5-vid", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDAgnes, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "agnes", StartImage: refPath},
			wantDur: 5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver.resolveFn = func(context.Context, domainprovider.Purpose, string) (domainprovider.ResolvedModel, error) {
				return tc.resolved, nil
			}
			res, err := svc.GenerateVideo(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("GenerateVideo error: %v", err)
			}
			if res.Duration != tc.wantDur {
				t.Fatalf("Duration = %.0f, want %.0f", res.Duration, tc.wantDur)
			}
			if !strings.HasPrefix(res.Path, filepath.Join(mediaDir, "videos")) {
				t.Fatalf("unexpected output dir: %s", res.Path)
			}
		})
	}
}

func TestTTSGenerationAndPCMToWAV(t *testing.T) {
	rawPCM := []byte{0x00, 0x10, 0x20, 0x30}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("FAKE-MP3"))
		case "/v1beta/models/gemini-2.5-flash-tts:generateContent":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/L16;codec=pcm;rate=24000;channels=1","data":"` + base64.StdEncoding.EncodeToString(rawPCM) + `"}}]}}]}`))
		case "/api/v1/services/aigc/multimodal-generation/generation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":{"audio":{"url":"` + srv.URL + `/dl/tts.wav"}}}`))
		case "/v1/t2a_v2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"base_resp":{"status_code":0},"data":{"audio":"` + hex.EncodeToString([]byte("MINIMAX-WAV")) + `"}}`))
		case "/dl/tts.wav":
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write([]byte("RIFFxxxxWAVE"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resolver := &fakeResolver{}
	svc, mediaDir := testService(t, resolver)

	cases := []struct {
		name     string
		resolved domainprovider.ResolvedModel
		req      domainmedia.TTSRequest
		wantExt  string
		wantHead string
	}{
		{
			name: "openai-tts-mp3",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeTTS,
				Model:   domainprovider.Model{ModelID: "gpt-4o-mini-tts", Capability: domainprovider.CapabilityTTS, Endpoint: domainprovider.EndpointOpenAITTS},
				Custom:  &domainprovider.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "c1", Name: "OAI", BaseURL: srv.URL}, APIKey: "k"},
			},
			req:      domainmedia.TTSRequest{Text: "hello", Format: "mp3"},
			wantExt:  ".mp3",
			wantHead: "FAKE-MP3",
		},
		{
			name: "gemini-tts-pcm-wrapped-wav",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeTTS,
				Model:   domainprovider.Model{ModelID: "gemini-2.5-flash-tts", Capability: domainprovider.CapabilityTTS},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGeminiAIStudio, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "gk"}},
			},
			req:      domainmedia.TTSRequest{Text: "xin chào"},
			wantExt:  ".wav",
			wantHead: "RIFF",
		},
		{
			name: "dashscope-tts",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeTTS,
				Model:   domainprovider.Model{ModelID: "qwen3-tts-flash", Capability: domainprovider.CapabilityTTS},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDDashScope, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:      domainmedia.TTSRequest{Text: "hello"},
			wantExt:  ".wav",
			wantHead: "RIFF",
		},
		{
			name: "minimax-tts-hex",
			resolved: domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeTTS,
				Model:   domainprovider.Model{ModelID: "speech-2.8-hd", Capability: domainprovider.CapabilityTTS},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:      domainmedia.TTSRequest{Text: "hello", Format: "wav"},
			wantExt:  ".wav",
			wantHead: "MINIMAX",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver.resolveFn = func(context.Context, domainprovider.Purpose, string) (domainprovider.ResolvedModel, error) {
				return tc.resolved, nil
			}
			res, err := svc.GenerateTTS(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("GenerateTTS error: %v", err)
			}
			if !strings.HasPrefix(res.Path, filepath.Join(mediaDir, "tts")) {
				t.Fatalf("unexpected path: %s", res.Path)
			}
			if filepath.Ext(res.Path) != tc.wantExt {
				t.Fatalf("ext = %s, want %s", filepath.Ext(res.Path), tc.wantExt)
			}
			raw, err := os.ReadFile(res.Path)
			if err != nil || !strings.HasPrefix(string(raw), tc.wantHead) {
				t.Fatalf("content mismatch: got %q (err=%v)", raw, err)
			}
		})
	}
}

func TestSSRFMetadataBlockedAndSecurity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"http://169.254.169.254/latest/meta-data/"}]}`))
	}))
	defer srv.Close()

	resolver := &fakeResolver{
		resolveFn: func(context.Context, domainprovider.Purpose, string) (domainprovider.ResolvedModel, error) {
			return domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "m", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "supersecret"}},
			}, nil
		},
	}
	svc, _ := testService(t, resolver)
	_, err := svc.GenerateImage(context.Background(), domainmedia.ImageRequest{Prompt: "test"})
	if err == nil {
		t.Fatal("expected SSRF link-local metadata URL to be blocked")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("secret leaked in error message: %v", err)
	}
}

func TestRateLimiterCancellation(t *testing.T) {
	def := config.Default(config.ModeDesktop)
	l := newRateLimiter(def.MediaGen.LimiterPollInterval, def.Providers.DefaultMaxWorkers)
	lim := domainprovider.Limits{
		MaxWorkers: map[domainprovider.Capability]int{domainprovider.CapabilityImage: 1},
	}
	release, err := l.acquire(context.Background(), "p1", domainprovider.CapabilityImage, lim)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = l.acquire(ctx, "p1", domainprovider.CapabilityImage, lim)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded while slot full, got %v", err)
	}
	release()

	release2, err := l.acquire(context.Background(), "p1", domainprovider.CapabilityImage, lim)
	if err != nil {
		t.Fatalf("expected acquire to succeed after release: %v", err)
	}
	release2()
}

func TestVertexTokenCaching(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + base64.StdEncoding.EncodeToString(fakePNG) + `"}}]}}]}`))
	}))
	defer srv.Close()

	resolver := &fakeResolver{
		resolveFn: func(context.Context, domainprovider.Purpose, string) (domainprovider.ResolvedModel, error) {
			return domainprovider.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gemini-3-pro-image-preview", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGeminiVertex, CredentialID: "cred-1", BaseURL: srv.URL, Secrets: map[string]string{domainprovider.FieldCredentialsFile: "{}"}},
			}, nil
		},
	}
	svc, _ := testService(t, resolver)
	for range 2 {
		if _, err := svc.GenerateImage(context.Background(), domainmedia.ImageRequest{Prompt: "hi"}); err != nil {
			t.Fatal(err)
		}
	}
	if resolver.vertexHit.Load() != 1 {
		t.Fatalf("expected VertexToken to be cached (calls=%d, want 1)", resolver.vertexHit.Load())
	}
}

func TestNewDerivesDefaultsFromConfig(t *testing.T) {
	def := config.Default(config.ModeDesktop).MediaGen

	svc := New(Options{})
	if svc.cfg.RequestTimeout != def.RequestTimeout {
		t.Fatalf("RequestTimeout = %v, want %v", svc.cfg.RequestTimeout, def.RequestTimeout)
	}
	if svc.cfg.VideoTimeout != def.VideoTimeout {
		t.Fatalf("VideoTimeout = %v, want %v", svc.cfg.VideoTimeout, def.VideoTimeout)
	}
	if svc.cfg.VideoPollInterval != def.VideoPollInterval {
		t.Fatalf("VideoPollInterval = %v, want %v", svc.cfg.VideoPollInterval, def.VideoPollInterval)
	}
	if svc.cfg.MaxOutputBytes != def.MaxOutputBytes {
		t.Fatalf("MaxOutputBytes = %d, want %d", svc.cfg.MaxOutputBytes, def.MaxOutputBytes)
	}
	if svc.cfg.MaxReferenceBytes != def.MaxReferenceBytes {
		t.Fatalf("MaxReferenceBytes = %d, want %d", svc.cfg.MaxReferenceBytes, def.MaxReferenceBytes)
	}
	if svc.httpClient == nil {
		t.Fatal("expected default httpClient to be constructed")
	}
	if svc.httpClient.Timeout != 0 {
		t.Fatalf("httpClient.Timeout = %v, want 0 (streaming client from httpclient.New)", svc.httpClient.Timeout)
	}
	if svc.httpClient.CheckRedirect == nil {
		t.Fatal("expected CheckRedirect guard on default httpClient")
	}

	partial := New(Options{
		Config: config.MediaGenConfig{
			RequestTimeout: 45 * time.Second,
			MaxOutputBytes: 128 << 20,
		},
	})
	if partial.cfg.RequestTimeout != 45*time.Second {
		t.Fatalf("partial RequestTimeout = %v, want 45s", partial.cfg.RequestTimeout)
	}
	if partial.cfg.MaxOutputBytes != 128<<20 {
		t.Fatalf("partial MaxOutputBytes = %d, want %d", partial.cfg.MaxOutputBytes, 128<<20)
	}
	if partial.cfg.VideoTimeout != def.VideoTimeout ||
		partial.cfg.VideoPollInterval != def.VideoPollInterval ||
		partial.cfg.MaxReferenceBytes != def.MaxReferenceBytes {
		t.Fatalf("partial config did not fall back to config.Default: %+v", partial.cfg)
	}

	baseClient := &http.Client{Timeout: 12 * time.Second}
	withBase := New(Options{HTTPClient: baseClient})
	if baseClient.CheckRedirect != nil {
		t.Fatal("New must not mutate caller-supplied HTTPClient")
	}
	if withBase.httpClient.Timeout != 12*time.Second || withBase.httpClient.CheckRedirect == nil {
		t.Fatalf("expected cloned client preserving timeout and adding CheckRedirect, got timeout=%v guard=%v",
			withBase.httpClient.Timeout, withBase.httpClient.CheckRedirect != nil)
	}
}

func TestMediaGenRedirectValidationAndHopCap(t *testing.T) {
	var loopHits atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/unsafe/v1/images/generations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"url":"` + srv.URL + `/dl/meta-redir"}]}`))
		case "/dl/meta-redir":
			http.Redirect(w, r, "http://metadata.google.internal/computeMetadata/v1/", http.StatusFound)
		case "/loop/v1/images/generations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"url":"` + srv.URL + `/dl/loop"}]}`))
		case "/dl/loop":
			loopHits.Add(1)
			http.Redirect(w, r, srv.URL+"/dl/loop", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Run("blocks unsafe redirect target", func(t *testing.T) {
		resolver := &fakeResolver{
			resolveFn: func(context.Context, domainprovider.Purpose, string) (domainprovider.ResolvedModel, error) {
				return domainprovider.ResolvedModel{
					Purpose: domainprovider.PurposeImage,
					Model:   domainprovider.Model{ModelID: "m", Capability: domainprovider.CapabilityImage},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL + "/unsafe", Secrets: map[string]string{"api_key": "supersecret"}},
				}, nil
			},
		}
		svc, _ := testService(t, resolver)
		_, err := svc.GenerateImage(context.Background(), domainmedia.ImageRequest{Prompt: "test"})
		if err == nil {
			t.Fatal("expected unsafe metadata redirect to be blocked")
		}
		if !strings.Contains(err.Error(), "blocked redirect target") {
			t.Fatalf("expected blocked redirect target error, got %v", err)
		}
		if strings.Contains(err.Error(), "supersecret") {
			t.Fatalf("secret leaked in error message: %v", err)
		}
	})

	t.Run("enforces 10 redirect hop cap", func(t *testing.T) {
		loopHits.Store(0)
		resolver := &fakeResolver{
			resolveFn: func(context.Context, domainprovider.Purpose, string) (domainprovider.ResolvedModel, error) {
				return domainprovider.ResolvedModel{
					Purpose: domainprovider.PurposeImage,
					Model:   domainprovider.Model{ModelID: "m", Capability: domainprovider.CapabilityImage},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL + "/loop", Secrets: map[string]string{"api_key": "k"}},
				}, nil
			},
		}
		svc, _ := testService(t, resolver)
		_, err := svc.GenerateImage(context.Background(), domainmedia.ImageRequest{Prompt: "test"})
		if err == nil {
			t.Fatal("expected redirect loop to stop after 10 redirects")
		}
		if !strings.Contains(err.Error(), "stopped after 10 redirects") {
			t.Fatalf("expected 10-redirect cap error, got %v", err)
		}
		if loopHits.Load() != 10 {
			t.Fatalf("loopHits = %d, want 10", loopHits.Load())
		}
	})
}

func TestSharedRetryHelper_503And429AndConnectionReset(t *testing.T) {
	t.Run("retries on 503 Service Unavailable and succeeds", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			att := attempts.Add(1)
			if att < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"service unavailable"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"result":"ok"}`))
		}))
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		cfg := RetryConfig{
			MaxRetries:    3,
			InitialDelay:  5 * time.Millisecond,
			MaxDelay:      50 * time.Millisecond,
			BackoffFactor: 2.0,
		}

		raw, ct, status, err := ExecHTTPWithRetry(context.Background(), srv.Client(), req, 1<<20, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		if !strings.Contains(ct, "application/json") {
			t.Fatalf("Content-Type = %s, want json", ct)
		}
		if !strings.Contains(string(raw), `"result":"ok"`) {
			t.Fatalf("body = %s, want ok", string(raw))
		}
		if attempts.Load() != 3 {
			t.Fatalf("attempts = %d, want 3", attempts.Load())
		}
	})

	t.Run("retries on 429 Too Many Requests with Retry-After header", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			att := attempts.Add(1)
			if att == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limited"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"result":"rate_limit_recovered"}`))
		}))
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader(`{"hello":"world"}`))
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		cfg := RetryConfig{
			MaxRetries:    2,
			InitialDelay:  5 * time.Millisecond,
			MaxDelay:      50 * time.Millisecond,
			BackoffFactor: 2.0,
		}

		raw, _, status, err := ExecHTTPWithRetry(context.Background(), srv.Client(), req, 1<<20, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		if !strings.Contains(string(raw), "rate_limit_recovered") {
			t.Fatalf("body = %s, want rate_limit_recovered", string(raw))
		}
		if attempts.Load() != 2 {
			t.Fatalf("attempts = %d, want 2", attempts.Load())
		}
	})

	t.Run("retries on connection reset network error and succeeds", func(t *testing.T) {
		var attempts atomic.Int32
		client := &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				att := attempts.Add(1)
				if att == 1 {
					return nil, &net.OpError{Op: "read", Err: syscall.ECONNRESET}
				}
				if att == 2 {
					return nil, errors.New("read: connection reset by peer")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"result":"conn_reset_recovered"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}),
		}

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://fake.test/api", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		cfg := RetryConfig{
			MaxRetries:    3,
			InitialDelay:  5 * time.Millisecond,
			MaxDelay:      50 * time.Millisecond,
			BackoffFactor: 2.0,
		}

		raw, _, status, err := ExecHTTPWithRetry(context.Background(), client, req, 1<<20, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		if !strings.Contains(string(raw), "conn_reset_recovered") {
			t.Fatalf("body = %s, want conn_reset_recovered", string(raw))
		}
		if attempts.Load() != 3 {
			t.Fatalf("attempts = %d, want 3", attempts.Load())
		}
	})

	t.Run("exhausts retries and returns final error or status", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
		}))
		defer srv.Close()

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		cfg := RetryConfig{
			MaxRetries:    2,
			InitialDelay:  5 * time.Millisecond,
			MaxDelay:      20 * time.Millisecond,
			BackoffFactor: 1.5,
		}

		_, _, status, err := ExecHTTPWithRetry(context.Background(), srv.Client(), req, 1<<20, cfg)
		if err != nil {
			t.Fatalf("expected status 503 response without transport error, got: %v", err)
		}
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", status)
		}
		if attempts.Load() != 3 { // 1 initial + 2 retries
			t.Fatalf("attempts = %d, want 3", attempts.Load())
		}
	})

	t.Run("context cancellation stops retry loop promptly", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		cfg := RetryConfig{
			MaxRetries:   5,
			InitialDelay: 200 * time.Millisecond,
			MaxDelay:     1 * time.Second,
		}

		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		_, _, _, err := ExecHTTPWithRetry(ctx, srv.Client(), req, 1<<20, cfg)
		if err == nil {
			t.Fatal("expected context canceled error, got nil")
		}
		if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("expected context canceled, got: %v", err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestStartStagingGCAndPruneStaging(t *testing.T) {
	t.Run("confirms expired unleased files are deleted", func(t *testing.T) {
		dir := t.TempDir()
		expiredFile := filepath.Join(dir, "expired.png")
		if err := os.WriteFile(expiredFile, []byte("expired image data"), 0o600); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-48 * time.Hour)
		if err := os.Chtimes(expiredFile, past, past); err != nil {
			t.Fatal(err)
		}

		pruned, err := PruneStaging(dir, 24*time.Hour)
		if err != nil {
			t.Fatalf("PruneStaging failed: %v", err)
		}
		if pruned != 1 {
			t.Fatalf("pruned = %d, want 1", pruned)
		}

		if _, err := os.Stat(expiredFile); !os.IsNotExist(err) {
			t.Fatalf("expected expired unleased file to be deleted, got err: %v", err)
		}
	})

	t.Run("confirms unexpired files are preserved", func(t *testing.T) {
		dir := t.TempDir()
		freshFile := filepath.Join(dir, "fresh.png")
		if err := os.WriteFile(freshFile, []byte("fresh image data"), 0o600); err != nil {
			t.Fatal(err)
		}
		// mtime is current (now)
		pruned, err := PruneStaging(dir, 24*time.Hour)
		if err != nil {
			t.Fatalf("PruneStaging failed: %v", err)
		}
		if pruned != 0 {
			t.Fatalf("pruned = %d, want 0", pruned)
		}

		if _, err := os.Stat(freshFile); err != nil {
			t.Fatalf("expected unexpired file to be preserved, got err: %v", err)
		}
	})

	t.Run("confirms expired files WITH active lease are strictly protected from deletion", func(t *testing.T) {
		dir := t.TempDir()
		leasedExpiredFile := filepath.Join(dir, "leased_expired.mp4")
		if err := os.WriteFile(leasedExpiredFile, []byte("leased video frame"), 0o600); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-72 * time.Hour)
		if err := os.Chtimes(leasedExpiredFile, past, past); err != nil {
			t.Fatal(err)
		}

		// Acquire active lease
		release := LeaseFile(leasedExpiredFile)
		defer release()

		if !IsLeased(leasedExpiredFile) {
			t.Fatal("expected file to be reported as leased")
		}

		// Run pruning with TTL = 24h
		pruned, err := PruneStaging(dir, 24*time.Hour)
		if err != nil {
			t.Fatalf("PruneStaging failed: %v", err)
		}
		if pruned != 0 {
			t.Fatalf("pruned = %d, want 0 (leased file must be protected)", pruned)
		}

		// Verify file still exists on disk despite being 72 hours old
		if _, err := os.Stat(leasedExpiredFile); err != nil {
			t.Fatalf("expected leased expired file to be preserved on disk, got err: %v", err)
		}

		// Now release lease and prune again
		release()
		if IsLeased(leasedExpiredFile) {
			t.Fatal("expected file to not be leased after release")
		}

		prunedAfter, err := PruneStaging(dir, 24*time.Hour)
		if err != nil {
			t.Fatalf("PruneStaging failed after release: %v", err)
		}
		if prunedAfter != 1 {
			t.Fatalf("prunedAfter = %d, want 1", prunedAfter)
		}
		if _, err := os.Stat(leasedExpiredFile); !os.IsNotExist(err) {
			t.Fatalf("expected file to be deleted once lease was released, got err: %v", err)
		}
	})

	t.Run("confirms context cancellation stops the GC ticker promptly without goroutine leak", func(t *testing.T) {
		dir := t.TempDir()
		expiredFile := filepath.Join(dir, "temp_render.bin")
		if err := os.WriteFile(expiredFile, []byte("intermediate staged data"), 0o600); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-10 * time.Minute)
		if err := os.Chtimes(expiredFile, past, past); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})

		go func() {
			StartStagingGC(ctx, dir, 5*time.Minute, 15*time.Millisecond)
			close(done)
		}()

		// Wait briefly for initial GC pass
		time.Sleep(30 * time.Millisecond)
		if _, err := os.Stat(expiredFile); !os.IsNotExist(err) {
			t.Fatalf("expected file to be pruned during GC pass, got err: %v", err)
		}

		// Cancel context to stop GC ticker
		cancel()

		select {
		case <-done:
			// Passed! Stopped promptly
		case <-time.After(1 * time.Second):
			t.Fatal("StartStagingGC did not terminate promptly after context cancellation")
		}
	})

	t.Run("confirms active generation job holds lease on inputs for job duration", func(t *testing.T) {
		refFile := writeTempPNG(t)
		var wasLeasedDuringJob atomic.Bool

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/images/edits") || strings.Contains(r.URL.Path, "/images") {
				// While handling the job, verify the input reference file is actively leased
				if IsLeased(refFile) {
					wasLeasedDuringJob.Store(true)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString([]byte("fake-png")) + `"}]}`))
				return
			}
			http.NotFound(w, r)
		}))
		defer srv.Close()

		resolver := &fakeResolver{
			resolveFn: func(context.Context, domainprovider.Purpose, string) (domainprovider.ResolvedModel, error) {
				return domainprovider.ResolvedModel{
					Purpose: domainprovider.PurposeImage,
					Model:   domainprovider.Model{ModelID: "dall-e-2", Capability: domainprovider.CapabilityImage},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
				}, nil
			},
		}

		svc, _ := testService(t, resolver)

		if svc.IsLeased(refFile) {
			t.Fatal("expected file not to be leased before job start")
		}

		res, err := svc.GenerateImage(context.Background(), domainmedia.ImageRequest{
			Prompt:          "edit prompt",
			ReferenceImages: []string{refFile},
		})
		if err != nil {
			t.Fatalf("GenerateImage failed: %v", err)
		}
		if res.Path == "" {
			t.Fatal("expected non-empty output path")
		}

		if !wasLeasedDuringJob.Load() {
			t.Fatal("expected reference image to be actively leased during job execution")
		}

		if svc.IsLeased(refFile) {
			t.Fatal("expected lease on input file to be released after job completion")
		}
	})
}

func TestMediaGenDynamicMediaDirAndStagingIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	mediaDir1 := filepath.Join(tmpDir, "media1")
	mediaDir2 := filepath.Join(tmpDir, "media2")
	stagingDir := filepath.Join(tmpDir, "custom_staging")

	svc := New(Options{
		MediaDir:   mediaDir1,
		StagingDir: stagingDir,
	})

	if svc.MediaDir() != mediaDir1 {
		t.Fatalf("expected MediaDir=%s, got %s", mediaDir1, svc.MediaDir())
	}
	if svc.StagingDir() != stagingDir {
		t.Fatalf("expected StagingDir=%s, got %s", stagingDir, svc.StagingDir())
	}

	// Update MediaDir dynamically
	svc.SetMediaDir(mediaDir2)
	if svc.MediaDir() != mediaDir2 {
		t.Fatalf("expected updated MediaDir=%s, got %s", mediaDir2, svc.MediaDir())
	}
	// StagingDir should remain isolated and unaffected
	if svc.StagingDir() != stagingDir {
		t.Fatalf("expected StagingDir to remain %s, got %s", stagingDir, svc.StagingDir())
	}

	// Test fallback when StagingDir is empty: SetMediaDir must update StagingDir dynamically!
	svcDefault := New(Options{
		MediaDir: mediaDir1,
	})
	expectedFallback := filepath.Join(mediaDir1, "staging")
	if svcDefault.StagingDir() != expectedFallback {
		t.Fatalf("expected fallback StagingDir=%s, got %s", expectedFallback, svcDefault.StagingDir())
	}
	svcDefault.SetMediaDir(mediaDir2)
	expectedFallback2 := filepath.Join(mediaDir2, "staging")
	if svcDefault.StagingDir() != expectedFallback2 {
		t.Fatalf("expected updated fallback StagingDir=%s after SetMediaDir, got %s", expectedFallback2, svcDefault.StagingDir())
	}
}

type noopRegistrar struct {
	calls atomic.Int32
}

func (n *noopRegistrar) RegisterAsset(_ context.Context, p media.RegisterAssetParams) (*contracts.MediaAssetItem, error) {
	id := n.calls.Add(1)
	return &contracts.MediaAssetItem{ID: "asset-" + filepath.Base(p.FilePath) + "-" + string(rune('0'+(id%10)))}, nil
}

func TestMediaGenConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	reg1 := &noopRegistrar{}
	reg2 := &noopRegistrar{}
	svc := New(Options{
		MediaDir: filepath.Join(tmpDir, "initial"),
	})

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			svc.SetMediaDir(filepath.Join(tmpDir, "media", "dynamic"))
			if i%2 == 0 {
				svc.SetRegistrar(reg1)
			} else {
				svc.SetRegistrar(reg2)
			}
		}
		close(done)
	}()

	for i := 0; i < 200; i++ {
		_ = svc.MediaDir()
		_ = svc.StagingDir()
		_, _ = svc.saveOutput(context.Background(), domainmedia.KindImage, filepath.Join(tmpDir, "out"), "img", "image/png", ".png", fakePNG, targetSpec{providerName: "test", modelID: "m"})
	}
	<-done
}

func TestStageStreamLimitsAndAssetCacheEviction(t *testing.T) {
	svc := New(Options{
		MediaDir: t.TempDir(),
		Config: config.MediaGenConfig{
			MaxOutputBytes: 16,
		},
	})

	// StageFile exceeds MaxOutputBytes
	if _, _, err := svc.StageFile("sub", "big.bin", make([]byte, 32)); err == nil {
		t.Fatal("expected StageFile size limit error")
	}

	// StageStream exceeds maxBytes and cleans up temp file
	if _, _, err := svc.StageStream("sub", "stream_big.bin", strings.NewReader(strings.Repeat("x", 32)), 16); err == nil {
		t.Fatal("expected StageStream size limit error")
	}
	entries, _ := os.ReadDir(filepath.Join(svc.StagingDir(), "sub"))
	if len(entries) != 0 {
		t.Fatalf("expected zero leftover temp files after failed StageStream, got %d", len(entries))
	}

	// StageStream within limit succeeds and leases file
	staged, release, err := svc.StageStream("sub", "ok.bin", strings.NewReader("hello"), 16)
	if err != nil {
		t.Fatalf("StageStream valid: %v", err)
	}
	if !svc.IsLeased(staged) {
		t.Fatal("expected staged file to hold active lease")
	}
	release()
	if svc.IsLeased(staged) {
		t.Fatal("expected staged file lease to release")
	}

	// Bounded asset cache eviction
	for i := 0; i < maxCachedAssetIDs+25; i++ {
		p := filepath.Join(svc.MediaDir(), "f-"+strings.Repeat("a", i%5)+string(rune(i)))
		svc.setAssetID(p, "id")
	}
	svc.assetsMu.RLock()
	gotLen := len(svc.assets)
	svc.assetsMu.RUnlock()
	if gotLen > maxCachedAssetIDs {
		t.Fatalf("assets cache len = %d, exceeds maxCachedAssetIDs=%d", gotLen, maxCachedAssetIDs)
	}
}

func TestRateLimiterGapRPMAnd429Backoff(t *testing.T) {
	l := newRateLimiter(5*time.Millisecond, 2)
	lim := domainprovider.Limits{
		MaxWorkers:   map[domainprovider.Capability]int{domainprovider.CapabilityImage: 2},
		RequestGapMs: 35,
	}

	t0 := time.Now()
	rel1, err := l.acquire(context.Background(), "p-gap", domainprovider.CapabilityImage, lim)
	if err != nil {
		t.Fatal(err)
	}
	rel1()

	// Record explicit 429 Retry-After backoff even when RequestGapMs is 0
	l.recordBackoff("p-429", domainprovider.CapabilityImage, 40*time.Millisecond)
	tBackoff := time.Now()
	rel2, err := l.acquire(context.Background(), "p-429", domainprovider.CapabilityImage, domainprovider.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rel2()
	if elapsed := time.Since(tBackoff); elapsed < 30*time.Millisecond {
		t.Fatalf("expected 429 recordBackoff to delay acquire, elapsed=%v", elapsed)
	}

	// Verify RequestGapMs spacing on p-gap
	rel3, err := l.acquire(context.Background(), "p-gap", domainprovider.CapabilityImage, lim)
	if err != nil {
		t.Fatal(err)
	}
	rel3()
	if elapsed := time.Since(t0); elapsed < 30*time.Millisecond {
		t.Fatalf("expected RequestGapMs spacing, elapsed=%v", elapsed)
	}

	// Verify RPM exhaustion cancels promptly when context times out
	rpmLim := domainprovider.Limits{
		RPM: map[domainprovider.Capability]int{domainprovider.CapabilityImage: 1},
	}
	relRPM, err := l.acquire(context.Background(), "p-rpm", domainprovider.CapabilityImage, rpmLim)
	if err != nil {
		t.Fatal(err)
	}
	relRPM()
	shortCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.acquire(shortCtx, "p-rpm", domainprovider.CapabilityImage, rpmLim); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded when RPM bucket full, got %v", err)
	}
}

func TestMediaGenHardeningAuthHeadersAndGCSEscaping(t *testing.T) {
	hdr := map[string]string{"Authorization": "Bearer secret", "x-goog-api-key": "AIzaSyTest123"}
	if got := filterTrustedHeaders("https://cdn.other.example/vid.mp4", hdr, "https://api.openai.com/v1"); got != nil {
		t.Fatalf("expected untrusted host headers omitted, got %v", got)
	}
	if got := filterTrustedHeaders("http://api.openai.com/v1/vid.mp4", hdr, "https://api.openai.com/v1"); got != nil {
		t.Fatalf("expected scheme downgrade headers omitted, got %v", got)
	}
	if got := filterTrustedHeaders("https://api.openai.com/v1/files/1", hdr, "https://api.openai.com/v1"); got == nil {
		t.Fatal("expected matching scheme+host to keep headers")
	}

	gotGCS := escapeGCSObjectPath("/dir name/file #1?.mp4")
	if gotGCS != "dir%20name/file%20%231%3F.mp4" {
		t.Fatalf("unexpected escaped GCS object path: %q", gotGCS)
	}

	snip := sanitizeProviderSnippet("bad key AIzaSyDummyKeyValue1234567890123 ticket=tok123 signature=abc456")
	if strings.Contains(snip, "AIzaSyDummyKeyValue1234567890123") || strings.Contains(snip, "tok123") || strings.Contains(snip, "abc456") {
		t.Fatalf("expected secrets redacted from snippet, got %q", snip)
	}
}

func TestMediaGenStageTraversalFileGuardAndStreamDownload(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default(config.ModeDesktop).MediaGen
	cfg.MaxOutputBytes = 16
	cfg.RetryMax = 0
	svc := New(Options{
		Config:   cfg,
		MediaDir: tmpDir,
	})
	svc.httpClient = nil

	for _, bad := range []struct{ sub, name string }{
		{"../escape", "ok.bin"},
		{"sub", "../escape.bin"},
		{"sub", "nested/file.bin"},
		{"/abs", "ok.bin"},
	} {
		if _, _, err := svc.StageFile(bad.sub, bad.name, []byte("hi")); err == nil {
			t.Fatalf("StageFile(%q, %q) should fail traversal check", bad.sub, bad.name)
		}
		if _, _, err := svc.StageStream(bad.sub, bad.name, strings.NewReader("hi"), 16); err == nil {
			t.Fatalf("StageStream(%q, %q) should fail traversal check", bad.sub, bad.name)
		}
	}

	// FileGuard denies write=true in saveOutput before creating file
	guardedCtx := engine.WithFileGuard(context.Background(), func(path string, write bool) error {
		if write {
			return errors.New("denied by guard")
		}
		return nil
	})
	outDir := filepath.Join(tmpDir, "guarded-out")
	if _, err := svc.saveOutput(guardedCtx, domainmedia.KindImage, outDir, "img", "image/png", ".png", fakePNG, targetSpec{providerName: "test", modelID: "m"}); err == nil {
		t.Fatal("expected FileGuard error from saveOutput")
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("expected guarded output dir not created, stat err=%v", err)
	}

	// Local httptest server downloadURLToFile (bypassing outbound guard via direct httpClient)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			if r.Header.Get("Authorization") != "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("12345678"))
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte(strings.Repeat("x", 32)))
	}))
	defer srv.Close()
	svc.httpClient = srv.Client()

	out, err := svc.downloadURLToFile(context.Background(), srv.URL+"/ok", map[string]string{"Authorization": "Bearer secret"}, "https://trusted.example")
	if err != nil {
		t.Fatalf("downloadURLToFile failed: %v", err)
	}
	res, err := svc.saveMediaOutput(context.Background(), domainmedia.KindVideo, tmpDir, "vid", ".mp4", out, targetSpec{providerName: "test", modelID: "m"})
	if err != nil {
		t.Fatalf("saveMediaOutput failed: %v", err)
	}
	if res.SizeBytes != 8 {
		t.Fatalf("SizeBytes = %d, want 8", res.SizeBytes)
	}
	if _, err := os.Stat(out.filePath); err == nil && out.filePath != "" {
		t.Fatalf("expected temp file %q cleaned up", out.filePath)
	}
	if _, err := svc.downloadURLToFile(context.Background(), srv.URL+"/big", nil); err == nil {
		t.Fatal("expected size limit error for oversized stream")
	}
}
