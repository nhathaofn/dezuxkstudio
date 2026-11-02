package core_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"app/internal/config"
	"app/internal/contracts"
	"app/internal/core"
	"app/internal/engine"
	"app/internal/paths"
	"app/internal/security"
	"app/internal/tools/gateway"
)

func generateTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 50, G: 120, B: 200, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestCore_OptionsValidation(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default(config.ModeDesktop)
	p := paths.Paths{Data: t.TempDir()}

	// Missing Config
	if _, err := core.New(ctx, core.Options{Paths: p, Logger: logger, OpenDirectory: func(string) error { return nil }, HTTPClient: http.DefaultClient}); err == nil {
		t.Error("expected error with missing Config, got nil")
	}

	// Missing Logger
	if _, err := core.New(ctx, core.Options{Config: cfg, Paths: p, OpenDirectory: func(string) error { return nil }, HTTPClient: http.DefaultClient}); err == nil {
		t.Error("expected error with missing Logger, got nil")
	}

	// Missing OpenDirectory
	if _, err := core.New(ctx, core.Options{Config: cfg, Paths: p, Logger: logger, HTTPClient: http.DefaultClient}); err == nil {
		t.Error("expected error with missing OpenDirectory, got nil")
	}

	// Missing HTTPClient
	if _, err := core.New(ctx, core.Options{Config: cfg, Paths: p, Logger: logger, OpenDirectory: func(string) error { return nil }}); err == nil {
		t.Error("expected error with missing HTTPClient, got nil")
	}
}

type receivedGatewayRequest struct {
	Path        string
	AuthHeader  string
	ContentType string
	Body        map[string]any
}

type testFrame struct {
	Type    contracts.MessageType `json:"type"`
	ID      string                `json:"id"`
	Success bool                  `json:"success"`
	Data    json.RawMessage       `json:"data"`
	Error   *contracts.ErrorBody  `json:"error"`
}

func TestCore_WorkflowPreferenceRuntimeWiring(t *testing.T) {
	tempDir := t.TempDir()
	p, err := paths.Resolve("core_test", paths.Overrides{DataDir: filepath.Join(tempDir, "data"), LogDir: filepath.Join(tempDir, "logs")})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	p.Config = filepath.Join(tempDir, "config")
	if err := p.Ensure(); err != nil {
		t.Fatalf("ensure paths: %v", err)
	}

	testPNGBytes := generateTestPNG(t)
	b64Image := base64.StdEncoding.EncodeToString(testPNGBytes)

	var mu sync.Mutex
	var received []receivedGatewayRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)

		mu.Lock()
		received = append(received, receivedGatewayRequest{
			Path:        r.URL.Path,
			AuthHeader:  r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"),
			Body:        parsed,
		})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Created:  time.Now().Unix(),
			Provider: "flow",
			Data: []gateway.GenerationItem{
				{B64JSON: b64Image},
			},
		})
	}))
	defer srv.Close()

	cfg := config.Default(config.ModeDesktop)
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0 // random port
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	token, _ := security.NewToken()

	c, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        logger,
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    srv.Client(),
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("core.Start: %v", err)
	}
	defer func() {
		if err := c.Close(); err != nil {
			t.Errorf("core.Close: %v", err)
		}
	}()

	// Connect WebSocket client
	wsConn, _, err := websocket.Dial(context.Background(), c.WSURL(), &websocket.DialOptions{
		Subprotocols: []string{contracts.ProtocolVersion, contracts.AuthSubprotocolPrefix + token},
	})
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer wsConn.CloseNow()

	seq := 0
	call := func(action string, payload any) (testFrame, error) {
		t.Helper()
		seq++
		raw, err := json.Marshal(payload)
		if err != nil {
			return testFrame{}, err
		}
		id := action + "-" + string(rune('a'+seq))
		req := contracts.ClientMessage{
			Type:    contracts.MessageAction,
			ID:      id,
			Action:  action,
			Payload: raw,
		}
		data, err := json.Marshal(req)
		if err != nil {
			return testFrame{}, err
		}
		if err := wsConn.Write(context.Background(), websocket.MessageText, data); err != nil {
			return testFrame{}, err
		}

		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, respBytes, err := wsConn.Read(ctx)
			cancel()
			if err != nil {
				return testFrame{}, err
			}
			var resp testFrame
			if err := json.Unmarshal(respBytes, &resp); err != nil {
				continue
			}
			if resp.Type == contracts.MessageResponse && resp.ID == id {
				return resp, nil
			}
		}
	}

	// 1. Initial State: Set custom GatewayURL and GatewayAPIKey via contracts.ActionPreferencesUpdate
	initialAPIKey := "sk-initial-token-test-123"
	initialURL := srv.URL
	patchResp, err := call(contracts.ActionPreferencesUpdate, contracts.PreferencesPatch{
		GatewayURL:    &initialURL,
		GatewayAPIKey: &initialAPIKey,
	})
	if err != nil {
		t.Fatalf("preferences:update failed: %v", err)
	}
	if !patchResp.Success {
		t.Fatalf("preferences:update returned failure: %+v", patchResp.Error)
	}

	// Verify preferences were persisted and returned
	getResp, err := call(contracts.ActionPreferencesGet, contracts.Empty{})
	if err != nil {
		t.Fatalf("preferences:get failed: %v", err)
	}
	var prefs contracts.Preferences
	if err := json.Unmarshal(getResp.Data, &prefs); err != nil {
		t.Fatalf("unmarshal preferences: %v", err)
	}
	if prefs.GatewayURL != initialURL || !prefs.HasGatewayApiKey {
		t.Fatalf("expected preferences to match patched values, got URL=%q, HasKey=%v", prefs.GatewayURL, prefs.HasGatewayApiKey)
	}

	// 2. Run Workflow Node tool.ai_image_generator (without overriding gateway_url param)
	// This exercises GetPreferences: prefSvc.Get wired in workflow.New!
	runResp, err := call(contracts.ActionWorkflowRunNode, contracts.WorkflowRunNodePayload{
		Type:  gateway.NodeType,
		Input: json.RawMessage(`{"prompt": "emerald dragon in lush forest", "aspect_ratio": "16:9"}`),
	})
	if err != nil {
		t.Fatalf("workflow:run_node failed: %v", err)
	}
	if !runResp.Success {
		t.Fatalf("workflow:run_node returned failure: %+v", runResp.Error)
	}

	mu.Lock()
	if len(received) != 1 {
		t.Fatalf("expected 1 gateway HTTP request, got %d", len(received))
	}
	firstReq := received[0]
	mu.Unlock()

	if firstReq.Path != "/v1/images/generations" {
		t.Errorf("expected path /v1/images/generations, got %q", firstReq.Path)
	}
	if firstReq.AuthHeader != "Bearer "+initialAPIKey {
		t.Errorf("expected AuthHeader 'Bearer %s', got %q", initialAPIKey, firstReq.AuthHeader)
	}
	if firstReq.Body["prompt"] != "emerald dragon in lush forest" {
		t.Errorf("expected prompt 'emerald dragon in lush forest', got %v", firstReq.Body["prompt"])
	}

	// Verify node result image file was saved to disk
	var nodeResult contracts.WorkflowRunNodeResult
	if err := json.Unmarshal(runResp.Data, &nodeResult); err != nil {
		t.Fatalf("unmarshal node result: %v", err)
	}
	var out gateway.NodeOutput
	outRaw, _ := json.Marshal(nodeResult.Output)
	if err := json.Unmarshal(outRaw, &out); err != nil {
		t.Fatalf("unmarshal output MediaRef: %v", err)
	}
	if out.Image.Kind != engine.MediaImage {
		t.Errorf("expected MediaImage kind, got %s", out.Image.Kind)
	}
	if _, err := os.Stat(out.Image.LocalURL); err != nil {
		t.Errorf("generated output file does not exist on disk: %v", err)
	}

	// 3. Dynamic Preference Rotation: Update API Key at runtime
	rotatedAPIKey := "sk-rotated-token-xyz-789"
	patchResp2, err := call(contracts.ActionPreferencesUpdate, contracts.PreferencesPatch{
		GatewayAPIKey: &rotatedAPIKey,
	})
	if err != nil {
		t.Fatalf("second preferences:update failed: %v", err)
	}
	if !patchResp2.Success {
		t.Fatalf("second preferences:update returned failure: %+v", patchResp2.Error)
	}

	// 4. Run Workflow Node again — it must dynamically pick up rotatedAPIKey without restarting core!
	runResp2, err := call(contracts.ActionWorkflowRunNode, contracts.WorkflowRunNodePayload{
		Type:  gateway.NodeType,
		Input: json.RawMessage(`{"prompt": "crystal castle at dawn"}`),
	})
	if err != nil {
		t.Fatalf("second workflow:run_node failed: %v", err)
	}
	if !runResp2.Success {
		t.Fatalf("second workflow:run_node returned failure: %+v", runResp2.Error)
	}

	mu.Lock()
	if len(received) != 2 {
		t.Fatalf("expected 2 gateway HTTP requests, got %d", len(received))
	}
	secondReq := received[1]
	mu.Unlock()

	if secondReq.AuthHeader != "Bearer "+rotatedAPIKey {
		t.Errorf("expected rotated AuthHeader 'Bearer %s', got %q", rotatedAPIKey, secondReq.AuthHeader)
	}
	if secondReq.Body["prompt"] != "crystal castle at dawn" {
		t.Errorf("expected prompt 'crystal castle at dawn', got %v", secondReq.Body["prompt"])
	}
}
