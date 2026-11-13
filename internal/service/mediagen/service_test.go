package mediagen

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"app/internal/config"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	providersvc "app/internal/service/provider"
)

type fakeResolver struct {
	resolveFn func(ctx context.Context, p domainprovider.Purpose, ref string) (providersvc.ResolvedModel, error)
	vertexFn  func(ctx context.Context, raw string) (string, string, time.Duration, error)
	vertexHit atomic.Int32
}

func (f *fakeResolver) ResolveModel(ctx context.Context, p domainprovider.Purpose, ref string) (providersvc.ResolvedModel, error) {
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
		resolved providersvc.ResolvedModel
		req      domainmedia.ImageRequest
	}{
		{
			name: "openai-t2i",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gpt-image-1", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "sunshine"},
		},
		{
			name: "openai-i2i",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gpt-image-1", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "recolor", ReferenceImages: []string{refPath}},
		},
		{
			name: "custom-chat-image",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gemini-img", Capability: domainprovider.CapabilityImage, Endpoint: domainprovider.EndpointOpenAIChatImage},
				Custom:  &providersvc.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "c1", Name: "Proxy", BaseURL: srv.URL}, APIKey: "k"},
			},
			req: domainmedia.ImageRequest{Prompt: "chat image"},
		},
		{
			name: "custom-openrouter-images",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "flux-2", Capability: domainprovider.CapabilityImage, Endpoint: domainprovider.EndpointOpenRouterImages},
				Custom:  &providersvc.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "or", Name: "OR", BaseURL: srv.URL + "/api/v1"}, APIKey: "k"},
			},
			req: domainmedia.ImageRequest{Prompt: "or image", ReferenceImages: []string{refPath}},
		},
		{
			name: "gemini-aistudio",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gemini-2.5-flash-image", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDGeminiAIStudio, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "gem-key"}},
			},
			req: domainmedia.ImageRequest{Prompt: "gem", ReferenceImages: []string{refPath}},
		},
		{
			name: "gemini-vertex-imagen-cached-token",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "imagen-3", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDGeminiVertex, CredentialID: "v1", BaseURL: srv.URL, Secrets: map[string]string{domainprovider.FieldCredentialsFile: `{"project_id":"test-proj"}`}},
			},
			req: domainmedia.ImageRequest{Prompt: "imagen"},
		},
		{
			name: "ark-agent-plan",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "seedream-4", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDArkAgentPlan, BaseURL: srv.URL + "/api/plan/v3", Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "ark"},
		},
		{
			name: "dashscope",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "qwen-image", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDDashScope, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "dash"},
		},
		{
			name: "minimax",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "image-01", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "mm"},
		},
		{
			name: "kling-jwt",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "kling-v2", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDKling, BaseURL: klingSrv.URL, Secrets: map[string]string{"access_key": "ak", "secret_key": "sk"}},
			},
			req: domainmedia.ImageRequest{Prompt: "kling"},
		},
		{
			name: "vidu",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "viduq2", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDVidu, BaseURL: srv.URL + "/ent/v2", Secrets: map[string]string{"api_key": "vidu-key"}},
			},
			req: domainmedia.ImageRequest{Prompt: "vidu"},
		},
		{
			name: "grok",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "grok-image", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDGrok, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "grok"},
		},
		{
			name: "agnes",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "agnes-img", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDAgnes, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			},
			req: domainmedia.ImageRequest{Prompt: "agnes"},
		},
	}

	resolver := &fakeResolver{}
	svc, mediaDir := testService(t, resolver)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver.resolveFn = func(context.Context, domainprovider.Purpose, string) (providersvc.ResolvedModel, error) {
				return tc.resolved, nil
			}
			res, err := svc.GenerateImage(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("GenerateImage error: %v", err)
			}
			if !strings.HasPrefix(res.Path, filepath.Join(mediaDir, "generated", "images")) {
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
			_, _ = w.Write([]byte(`{"id":"oai-vid-1"}`))
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
		resolved providersvc.ResolvedModel
		req      domainmedia.VideoRequest
		wantDur  float64
	}{
		{
			name: "openai-sora",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "sora-2", Capability: domainprovider.CapabilityVideo, Facts: domainprovider.ModelFacts{Durations: []int{4, 8}}},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "ocean wave", StartImage: refPath},
			wantDur: 4, // snapped nearest to 5 among {4, 8}
		},
		{
			name: "gemini-aistudio",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "veo-3", Capability: domainprovider.CapabilityVideo},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDGeminiAIStudio, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "gk"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "city flyover"},
			wantDur: 5,
		},
		{
			name: "gemini-vertex",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "veo-vertex", Capability: domainprovider.CapabilityVideo},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDGeminiVertex, BaseURL: srv.URL, Secrets: map[string]string{domainprovider.FieldCredentialsFile: `{"project_id":"test-proj"}`}},
			},
			req:     domainmedia.VideoRequest{Prompt: "forest"},
			wantDur: 5,
		},
		{
			name: "ark",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "seedance", Capability: domainprovider.CapabilityVideo},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDArk, BaseURL: srv.URL + "/api/v3", Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "seedance", StartImage: refPath, EndImage: refPath},
			wantDur: 5,
		},
		{
			name: "dashscope",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "wan2.6", Capability: domainprovider.CapabilityVideo},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDDashScope, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "wan"},
			wantDur: 5,
		},
		{
			name: "minimax-v1",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "MiniMax-Hailuo-2.3", Capability: domainprovider.CapabilityVideo},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "hailuo"},
			wantDur: 5,
		},
		{
			name: "minimax-h3",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "MiniMax-H3", Capability: domainprovider.CapabilityVideo},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "h3"},
			wantDur: 5,
		},
		{
			name: "kling-i2v",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "kling-v2", Capability: domainprovider.CapabilityVideo},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDKling, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "kling", StartImage: refPath},
			wantDur: 5,
		},
		{
			name: "vidu-start-end",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "viduq3", Capability: domainprovider.CapabilityVideo},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDVidu, BaseURL: srv.URL + "/ent/v2", Secrets: map[string]string{"api_key": "vk"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "vidu", StartImage: refPath, EndImage: refPath},
			wantDur: 5,
		},
		{
			name: "grok",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeVideo,
				Model:   domainprovider.Model{ModelID: "grok-imagine-video", Capability: domainprovider.CapabilityVideo},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDGrok, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			},
			req:     domainmedia.VideoRequest{Prompt: "grok"},
			wantDur: 5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver.resolveFn = func(context.Context, domainprovider.Purpose, string) (providersvc.ResolvedModel, error) {
				return tc.resolved, nil
			}
			res, err := svc.GenerateVideo(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("GenerateVideo error: %v", err)
			}
			if res.Duration != tc.wantDur {
				t.Fatalf("Duration = %.0f, want %.0f", res.Duration, tc.wantDur)
			}
			if !strings.HasPrefix(res.Path, filepath.Join(mediaDir, "generated", "videos")) {
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
		resolved providersvc.ResolvedModel
		req      domainmedia.TTSRequest
		wantExt  string
		wantHead string
	}{
		{
			name: "openai-tts-mp3",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeTTS,
				Model:   domainprovider.Model{ModelID: "gpt-4o-mini-tts", Capability: domainprovider.CapabilityTTS, Endpoint: domainprovider.EndpointOpenAITTS},
				Custom:  &providersvc.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "c1", Name: "OAI", BaseURL: srv.URL}, APIKey: "k"},
			},
			req:      domainmedia.TTSRequest{Text: "hello", Format: "mp3"},
			wantExt:  ".mp3",
			wantHead: "FAKE-MP3",
		},
		{
			name: "gemini-tts-pcm-wrapped-wav",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeTTS,
				Model:   domainprovider.Model{ModelID: "gemini-2.5-flash-tts", Capability: domainprovider.CapabilityTTS},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDGeminiAIStudio, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "gk"}},
			},
			req:      domainmedia.TTSRequest{Text: "xin chào"},
			wantExt:  ".wav",
			wantHead: "RIFF",
		},
		{
			name: "dashscope-tts",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeTTS,
				Model:   domainprovider.Model{ModelID: "qwen3-tts-flash", Capability: domainprovider.CapabilityTTS},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDDashScope, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:      domainmedia.TTSRequest{Text: "hello"},
			wantExt:  ".wav",
			wantHead: "RIFF",
		},
		{
			name: "minimax-tts-hex",
			resolved: providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeTTS,
				Model:   domainprovider.Model{ModelID: "speech-2.8-hd", Capability: domainprovider.CapabilityTTS},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "k"}},
			},
			req:      domainmedia.TTSRequest{Text: "hello", Format: "wav"},
			wantExt:  ".wav",
			wantHead: "MINIMAX",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver.resolveFn = func(context.Context, domainprovider.Purpose, string) (providersvc.ResolvedModel, error) {
				return tc.resolved, nil
			}
			res, err := svc.GenerateTTS(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("GenerateTTS error: %v", err)
			}
			if !strings.HasPrefix(res.Path, filepath.Join(mediaDir, "generated", "tts")) {
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
		resolveFn: func(context.Context, domainprovider.Purpose, string) (providersvc.ResolvedModel, error) {
			return providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "m", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "supersecret"}},
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
	l := newRateLimiter()
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
		resolveFn: func(context.Context, domainprovider.Purpose, string) (providersvc.ResolvedModel, error) {
			return providersvc.ResolvedModel{
				Purpose: domainprovider.PurposeImage,
				Model:   domainprovider.Model{ModelID: "gemini-3-pro-image-preview", Capability: domainprovider.CapabilityImage},
				Builtin: &providersvc.Resolved{ProviderID: domainprovider.IDGeminiVertex, CredentialID: "cred-1", BaseURL: srv.URL, Secrets: map[string]string{domainprovider.FieldCredentialsFile: "{}"}},
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
