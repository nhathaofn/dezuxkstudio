package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"app/internal/config"
	"app/internal/contracts"
	"app/internal/core"
	"app/internal/paths"
	"app/internal/security"
	"app/internal/tools/watermark"
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

type testFrame struct {
	Type    contracts.MessageType `json:"type"`
	ID      string                `json:"id"`
	Success bool                  `json:"success"`
	Data    json.RawMessage       `json:"data"`
	Error   *contracts.ErrorBody  `json:"error"`
}

func TestCore_PreferencesAndWatermarkWiring(t *testing.T) {
	tempDir := t.TempDir()
	p, err := paths.Resolve("core_test", paths.Overrides{DataDir: filepath.Join(tempDir, "data"), LogDir: filepath.Join(tempDir, "logs")})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	p.Config = filepath.Join(tempDir, "config")
	if err := p.Ensure(); err != nil {
		t.Fatalf("ensure paths: %v", err)
	}

	cfg := config.Default(config.ModeDesktop)
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	token, _ := security.NewToken()

	c, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        logger,
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
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

	// 1. Update theme & language preferences
	patchResp, err := call(contracts.ActionPreferencesUpdate, map[string]string{
		"theme":    "dark",
		"language": "vi",
	})
	if err != nil {
		t.Fatalf("preferences:update failed: %v", err)
	}
	if !patchResp.Success {
		t.Fatalf("preferences:update returned failure: %+v", patchResp.Error)
	}

	getResp, err := call(contracts.ActionPreferencesGet, contracts.Empty{})
	if err != nil {
		t.Fatalf("preferences:get failed: %v", err)
	}
	var prefs contracts.Preferences
	if err := json.Unmarshal(getResp.Data, &prefs); err != nil {
		t.Fatalf("unmarshal preferences: %v", err)
	}
	if prefs.Theme != "dark" || prefs.Language != "vi" {
		t.Fatalf("expected dark/vi preferences, got %+v", prefs)
	}

	// 2. Run watermark remover node
	imgPath := filepath.Join(tempDir, "sample.png")
	if err := os.WriteFile(imgPath, generateTestPNG(t), 0o644); err != nil {
		t.Fatalf("write sample png: %v", err)
	}
	runResp, err := call(contracts.ActionWorkflowRunNode, contracts.WorkflowRunNodePayload{
		Type:  watermark.NodeType,
		Input: json.RawMessage(`{"image": "` + filepath.ToSlash(imgPath) + `"}`),
	})
	if err != nil {
		t.Fatalf("workflow:run_node failed: %v", err)
	}
	if !runResp.Success {
		t.Fatalf("workflow:run_node returned failure: %+v", runResp.Error)
	}
}
