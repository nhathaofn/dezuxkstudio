package mediagen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"app/internal/apperr"
	"app/internal/config"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/progress"
)

func TestSubmitVideoJobAndPollOnceAcrossRestart(t *testing.T) {
	var submitCount atomic.Int32
	var pollCount atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
			submitCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"job_sora_42","status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/job_sora_42":
			n := pollCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				_, _ = w.Write([]byte(`{"status":"in_progress"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"completed","url":"` + srv.URL + `/dl/video.mp4"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/dl/video.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("MP4VIDEO"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resolver := &fakeResolver{
		resolveFn: func(_ context.Context, p domainprovider.Purpose, ref string) (domainprovider.ResolvedModel, error) {
			return domainprovider.ResolvedModel{
				Purpose: p,
				Ref:     domainprovider.ModelRef{ProviderID: domainprovider.IDOpenAI, ModelID: "sora-2"},
				Model:   domainprovider.Model{ModelID: "sora-2", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "sk-test"}},
			}, nil
		},
	}

	// Service #1 submits once and serializes VideoSubmitToken
	svc1, outDir := testService(t, resolver)
	tok, err := svc1.SubmitVideoJob(context.Background(), domainmedia.VideoRequest{
		ModelRef:    "openai/sora-2",
		Prompt:      "camera flyover",
		DurationSec: 6,
		OutputDir:   outDir,
	})
	if err != nil {
		t.Fatalf("SubmitVideoJob: %v", err)
	}
	if tok.ProviderJobID != "job_sora_42" || tok.Protocol != "openai-video" || tok.ModelRef != "openai/sora-2" {
		t.Fatalf("unexpected token: %+v", tok)
	}

	rawToken, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	if strings.Contains(string(rawToken), "sk-test") {
		t.Fatal("submit token must not serialize provider secrets")
	}

	// Simulate crash + restart with a brand new Service #2 instance and usage hook
	svc2, _ := testService(t, resolver)
	var usage []domainmedia.UsageEvent
	svc2.SetUsageHook(func(_ context.Context, ev domainmedia.UsageEvent) {
		usage = append(usage, ev)
	})

	var restored domainmedia.VideoSubmitToken
	if err := json.Unmarshal(rawToken, &restored); err != nil {
		t.Fatalf("unmarshal token: %v", err)
	}

	ctx := domainmedia.ContextWithUsageMeta(context.Background(), domainmedia.UsageMeta{
		ProjectID: "proj1", EpisodeID: "ep1", ShotID: "shot1", JobID: "job1",
	})
	st1, err := svc2.PollVideoJobOnce(ctx, restored)
	if err != nil || st1.Done {
		t.Fatalf("first poll = %+v err=%v", st1, err)
	}
	st2, err := svc2.PollVideoJobOnce(ctx, restored)
	if err != nil || !st2.Done || st2.Result.Path == "" {
		t.Fatalf("second poll = %+v err=%v", st2, err)
	}

	if got := submitCount.Load(); got != 1 {
		t.Fatalf("Submit HTTP API called %d times, want exactly 1", got)
	}
	if got := pollCount.Load(); got != 2 {
		t.Fatalf("Poll HTTP API called %d times, want 2", got)
	}
	if len(usage) != 1 || usage[0].ProjectID != "proj1" || usage[0].Capability != "video" || usage[0].OutputUnits != 6 {
		t.Fatalf("unexpected usage events: %+v", usage)
	}
}

func TestImageMaskPathAndUnsupportedRejection(t *testing.T) {
	var gotMaskPart bool
	var gotImagePart bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/images/edits" {
			mr, err := r.MultipartReader()
			if err != nil {
				t.Fatalf("multipart reader: %v", err)
			}
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("next part: %v", err)
				}
				if p.FormName() == "mask" {
					gotMaskPart = true
				}
				if p.FormName() == "image" {
					gotImagePart = true
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	baseImg := writeTempPNG(t)
	maskImg := writeTempPNG(t)

	resolver := &fakeResolver{
		resolveFn: func(_ context.Context, p domainprovider.Purpose, ref string) (domainprovider.ResolvedModel, error) {
			if ref == "custom/gen-only" {
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "dall-e-3", Capability: domainprovider.CapabilityImage, Endpoint: domainprovider.EndpointOpenAIImagesGenerations},
					Custom:  &domainprovider.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "c1", BaseURL: srv.URL + "/v1"}, APIKey: "k"},
				}, nil
			}
			return domainprovider.ResolvedModel{
				Purpose: p,
				Ref:     domainprovider.ModelRef{ProviderID: domainprovider.IDOpenAI, ModelID: "gpt-image-1"},
				Model:   domainprovider.Model{ModelID: "gpt-image-1", Capability: domainprovider.CapabilityImage},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			}, nil
		},
	}

	svc, outDir := testService(t, resolver)
	var events []domainmedia.UsageEvent
	svc.SetUsageHook(func(_ context.Context, ev domainmedia.UsageEvent) { events = append(events, ev) })

	res, err := svc.GenerateImage(context.Background(), domainmedia.ImageRequest{
		ModelRef:        "openai/gpt-image-1",
		Prompt:          "replace background with neon street",
		ReferenceImages: []string{baseImg},
		MaskPath:        maskImg,
		OutputDir:       outDir,
	})
	if err != nil {
		t.Fatalf("GenerateImage with mask: %v", err)
	}
	if res.Path == "" || !gotImagePart || !gotMaskPart {
		t.Fatalf("expected image & mask multipart parts, got image=%v mask=%v path=%s", gotImagePart, gotMaskPart, res.Path)
	}
	if len(events) != 1 || events[0].Operation != "mask_edit" {
		t.Fatalf("expected mask_edit usage event, got %+v", events)
	}

	// EndpointOpenAIImagesGenerations must reject MaskPath cleanly
	if _, err := svc.GenerateImage(context.Background(), domainmedia.ImageRequest{
		ModelRef:        "custom/gen-only",
		Prompt:          "edit",
		ReferenceImages: []string{baseImg},
		MaskPath:        maskImg,
	}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for mask on generations-only endpoint, got %v", err)
	}
}

func TestGenerateLipSyncProtocolsAndUsage(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos/lip-sync":
			_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"ls_1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/lip-sync/ls_1":
			_, _ = w.Write([]byte(`{"data":{"task_status":"succeed","task_result":{"videos":[{"url":"` + srv.URL + `/dl/ls.mp4"}]}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos/lipsync":
			_, _ = w.Write([]byte(`{"id":"ols_1","url":"` + srv.URL + `/dl/ls.mp4"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/video_generation":
			_, _ = w.Write([]byte(`{"task_id":"mm_ls_1","base_resp":{"status_code":0}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/query/video_generation":
			_, _ = w.Write([]byte(`{"status":"Success","file_id":"mm_f1","base_resp":{"status_code":0}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/files/retrieve":
			_, _ = w.Write([]byte(`{"file":{"download_url":"` + srv.URL + `/dl/ls.mp4"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/dl/ls.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("LIPSYNCMP4"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	vidPath := filepath.Join(dir, "in.mp4")
	audPath := filepath.Join(dir, "in.mp3")
	_ = os.WriteFile(vidPath, []byte("MP4DATA"), 0o600)
	_ = os.WriteFile(audPath, []byte("ID3AUDIO"), 0o600)

	resolver := &fakeResolver{
		resolveFn: func(_ context.Context, p domainprovider.Purpose, ref string) (domainprovider.ResolvedModel, error) {
			if ref == "custom/ls" {
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "sync-v1", Capability: domainprovider.CapabilityLipSync, Endpoint: domainprovider.EndpointLipSync},
					Custom:  &domainprovider.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "c1", BaseURL: srv.URL + "/v1"}, APIKey: "k"},
				}, nil
			}
			if ref == "minimax/ls" {
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "MiniMax-LipSync", Capability: domainprovider.CapabilityLipSync},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
				}, nil
			}
			return domainprovider.ResolvedModel{
				Purpose: p,
				Model:   domainprovider.Model{ModelID: "kling-lip-sync", Capability: domainprovider.CapabilityLipSync},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDKling, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			}, nil
		},
	}

	svc, outDir := testService(t, resolver)
	var events []domainmedia.UsageEvent
	svc.SetUsageHook(func(_ context.Context, ev domainmedia.UsageEvent) { events = append(events, ev) })

	for _, ref := range []string{"kling/kling-lip-sync", "minimax/ls", "custom/ls"} {
		res, err := svc.GenerateLipSync(context.Background(), domainmedia.LipSyncRequest{
			ModelRef:  ref,
			VideoPath: vidPath,
			AudioPath: audPath,
			OutputDir: outDir,
		})
		if err != nil || res.Path == "" {
			t.Fatalf("%s GenerateLipSync = %+v err=%v", ref, res, err)
		}
	}
	if len(events) != 3 || events[0].Capability != "lipsync" || events[1].Capability != "lipsync" || events[2].Capability != "lipsync" {
		t.Fatalf("expected 3 lipsync usage events, got %+v", events)
	}
}

func TestVideoCustomDurationLimitsPollParamsAndErrorScrubbing(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
			_, _ = w.Write([]byte(`{"id":"job_90s"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/job_90s":
			_, _ = w.Write([]byte(`{"status":"failed","error":{"message":"upstream failed for https://cdn.example.com/v.mp4?token=secret123 with Bearer supersecret"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/job_progress":
			_, _ = w.Write([]byte(`{"status":"in_progress"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resolver := &fakeResolver{
		resolveFn: func(_ context.Context, p domainprovider.Purpose, _ string) (domainprovider.ResolvedModel, error) {
			return domainprovider.ResolvedModel{
				Purpose: p,
				Model:   domainprovider.Model{ModelID: "sora-2", Capability: domainprovider.CapabilityVideo},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			}, nil
		},
	}

	svc := New(Options{
		Providers:               resolver,
		MediaDir:                t.TempDir(),
		MaxVideoDurationSec:     90,
		DefaultVideoDurationSec: 8,
		Config: config.MediaGenConfig{
			VideoPollInterval: 5,
		},
	})

	tok, err := svc.SubmitVideoJob(context.Background(), domainmedia.VideoRequest{
		Prompt:      "long shot",
		DurationSec: 90,
	})
	if err != nil || tok.DurationSec != 90 {
		t.Fatalf("expected 90s duration to succeed with MaxVideoDurationSec=90, got tok=%+v err=%v", tok, err)
	}

	st, err := svc.PollVideoJobOnce(context.Background(), tok)
	if err == nil || !st.Failed {
		t.Fatalf("expected permanent provider failure on failed status, got st=%+v err=%v", st, err)
	}
	ae := apperr.From(err)
	if strings.Contains(ae.Message, "secret123") || strings.Contains(ae.Message, "supersecret") {
		t.Fatalf("provider error leaked secrets/tokens in Message: %q", ae.Message)
	}

	// Verify polling_video progress includes provider param and isVideo=true does not emit fake KeepAlive beats
	var sawProviderParam bool
	var beatCalls atomic.Int32
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pCtx := progress.With(baseCtx, progress.Sink{
		Interval: 5,
		Beat:     func() { beatCalls.Add(1) },
		Report: func(u progress.Update) {
			if u.Key == "mediagen.progress.polling_video" && u.Params["provider"] == "OpenAI" {
				sawProviderParam = true
				cancel()
			}
		},
	})
	jobCtx, cleanup := svc.prepareJobContext(pCtx, true)
	defer cleanup()
	_ = svc.pollUntil(jobCtx, "mediagen.progress.polling_video", "Generating…", map[string]string{"provider": "OpenAI"}, func() (bool, error) {
		return false, nil
	})
	if !sawProviderParam {
		t.Fatal("expected mediagen.progress.polling_video to emit provider param")
	}
	if beatCalls.Load() != 0 {
		t.Fatalf("expected zero KeepAlive beats on isVideo=true polling context, got %d", beatCalls.Load())
	}
}
