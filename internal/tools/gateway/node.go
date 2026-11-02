// Package gateway provides the AI image generator DAG node connecting DezuxkStudio
// to the Dezuxk Gateway headless server (/v1/images/generations).
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/domain/preferences"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/secrets"
)

// NodeType is the unique identifier for the AI image generator node.
const NodeType = "tool.ai_image_generator"

// DefaultGatewayURL is the default endpoint URL for the Dezuxk Gateway server.
const DefaultGatewayURL = "http://127.0.0.1:8080"

// Options configures runtime dependencies for the gateway DAG node.
type Options struct {
	MediaDir       string
	GatewayURL     string
	GatewayAPIKey  string
	HTTPClient     *http.Client
	GetPreferences func(ctx context.Context) (preferences.Preferences, error)
	Secrets        secrets.Store
}

// NodeInput represents input ports and parameters for tool.ai_image_generator.
type NodeInput struct {
	Prompt         engine.Texts `json:"prompt"`
	NegativePrompt engine.Texts `json:"negative_prompt,omitempty"`
	AspectRatio    string       `json:"aspect_ratio,omitempty"`
	GatewayURL     string       `json:"gateway_url,omitempty"`
	Provider       string       `json:"provider,omitempty"`
}

// NodeOutput represents output ports produced by tool.ai_image_generator.
type NodeOutput struct {
	Image engine.MediaRef `json:"image"`
}

// GenerationItem represents an image entry in the gateway response.
type GenerationItem struct {
	URL     string `json:"url,omitempty"`
	B64JSON string `json:"b64_json,omitempty"`
}

// GenerationResponse models the JSON payload returned by POST /v1/images/generations.
type GenerationResponse struct {
	Created  int64            `json:"created"`
	Data     []GenerationItem `json:"data"`
	Provider string           `json:"provider"`
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// NewNode constructs an engine.Node for generating images via Dezuxk Gateway.
func NewNode(opts Options) engine.Node {
	spec := engine.Spec{
		Type:        NodeType,
		Provider:    "tool",
		Label:       "AI Image Generator",
		Description: "Generate images using Dezuxk Gateway (/v1/images/generations)",
		Inputs: []engine.Port{
			{Name: "prompt", Label: "Prompt", Type: engine.PortText, Multiple: false, Optional: false},
			{Name: "negative_prompt", Label: "Negative prompt", Type: engine.PortText, Multiple: false, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "image", Label: "Generated image", Type: engine.PortImage, Multiple: false},
		},
		Params: []engine.Param{
			{Name: "aspect_ratio", Label: "Aspect ratio", Kind: engine.ParamSelect},
			{Name: "gateway_url", Label: "Gateway URL", Kind: engine.ParamText},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in NodeInput) (NodeOutput, error) {
		prompts := in.Prompt.NonEmpty()
		if len(prompts) == 0 {
			return NodeOutput{}, engine.Invalid("AI Image Generator input port 'prompt' is required")
		}
		promptStr := strings.Join(prompts, " ")

		negPrompts := in.NegativePrompt.NonEmpty()
		negPromptStr := strings.Join(negPrompts, " ")

		aspectRatio := strings.TrimSpace(in.AspectRatio)
		if aspectRatio == "" {
			aspectRatio = "16:9"
		}

		// Determine official configured gateway URL
		officialGWURL := ""
		if opts.GetPreferences != nil {
			if prefs, err := opts.GetPreferences(ctx); err == nil && prefs.GatewayURL != "" {
				officialGWURL = strings.TrimSpace(prefs.GatewayURL)
			}
		}
		if officialGWURL == "" {
			if opts.GatewayURL != "" {
				officialGWURL = strings.TrimSpace(opts.GatewayURL)
			} else {
				officialGWURL = DefaultGatewayURL
			}
		}

		targetGWURL := strings.TrimSpace(in.GatewayURL)
		if targetGWURL == "" {
			targetGWURL = officialGWURL
		}

		// Scheme and host validation
		parsedTarget, err := url.Parse(targetGWURL)
		if err != nil || (parsedTarget.Scheme != "http" && parsedTarget.Scheme != "https") || parsedTarget.Host == "" {
			return NodeOutput{}, engine.Invalid(fmt.Sprintf("invalid gateway URL %q: scheme must be http or https", targetGWURL))
		}

		// Retrieve secret from secrets.Store if available, fallback to opts.GatewayAPIKey
		var apiKey string
		if opts.Secrets != nil {
			if key, err := opts.Secrets.Get(secrets.KeyGatewayAPIKey); err == nil {
				apiKey = strings.TrimSpace(key)
			}
		}
		if apiKey == "" && opts.GatewayAPIKey != "" {
			apiKey = strings.TrimSpace(opts.GatewayAPIKey)
		}

		// SSRF Defense: attach Bearer token ONLY if target URL matches the official configured gateway URL
		attachToken := false
		if apiKey != "" {
			if parsedOfficial, err := url.Parse(officialGWURL); err == nil {
				if strings.EqualFold(parsedTarget.Scheme, parsedOfficial.Scheme) && strings.EqualFold(parsedTarget.Host, parsedOfficial.Host) {
					attachToken = true
				}
			}
		}

		endpoint := strings.TrimRight(targetGWURL, "/") + "/v1/images/generations"

		provider := in.Provider
		if provider == "" {
			provider = "flow"
		}

		reqBody := map[string]any{
			"prompt":       promptStr,
			"aspect_ratio": aspectRatio,
			"provider":     provider,
		}
		if negPromptStr != "" {
			reqBody["negative_prompt"] = negPromptStr
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return NodeOutput{}, fmt.Errorf("marshal gateway request: %w", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return NodeOutput{}, fmt.Errorf("create gateway request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if attachToken {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		client := opts.HTTPClient
		if client == nil {
			client = http.DefaultClient
		}

		resp, err := client.Do(httpReq)
		if err != nil {
			return NodeOutput{}, fmt.Errorf("gateway request failed: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return NodeOutput{}, fmt.Errorf("read gateway response: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return NodeOutput{}, fmt.Errorf("gateway error (HTTP %d): %s", resp.StatusCode, string(respBytes))
		}

		var genResp GenerationResponse
		if err := json.Unmarshal(respBytes, &genResp); err != nil {
			return NodeOutput{}, fmt.Errorf("parse gateway response: %w", err)
		}

		if len(genResp.Data) == 0 {
			return NodeOutput{}, errors.New("gateway returned no image data")
		}

		item := genResp.Data[0]
		var imgBytes []byte

		switch {
		case item.B64JSON != "":
			imgBytes, err = base64.StdEncoding.DecodeString(item.B64JSON)
			if err != nil {
				return NodeOutput{}, fmt.Errorf("decode b64_json image: %w", err)
			}
		case item.URL != "":
			dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
			if err != nil {
				return NodeOutput{}, fmt.Errorf("create image download request: %w", err)
			}
			dlResp, err := client.Do(dlReq)
			if err != nil {
				return NodeOutput{}, fmt.Errorf("download image: %w", err)
			}
			defer dlResp.Body.Close()

			if dlResp.StatusCode != http.StatusOK {
				return NodeOutput{}, fmt.Errorf("image download failed (HTTP %d)", dlResp.StatusCode)
			}
			imgBytes, err = io.ReadAll(dlResp.Body)
			if err != nil {
				return NodeOutput{}, fmt.Errorf("read downloaded image: %w", err)
			}
		default:
			return NodeOutput{}, errors.New("gateway image data contains neither b64_json nor url")
		}

		mediaDir := opts.MediaDir
		if mediaDir == "" {
			return NodeOutput{}, errors.New("media directory not configured")
		}
		if err := os.MkdirAll(mediaDir, 0o755); err != nil {
			return NodeOutput{}, fmt.Errorf("create media directory: %w", err)
		}

		fileName := fmt.Sprintf("gen_%d_%s.png", time.Now().UnixNano(), randomHex(4))
		destPath := filepath.Join(mediaDir, fileName)

		if err := fsutil.WriteFileAtomic(destPath, imgBytes, 0o644); err != nil {
			return NodeOutput{}, fmt.Errorf("write generated image: %w", err)
		}

		ref := engine.RefFor(destPath, engine.MediaImage)
		return NodeOutput{Image: ref}, nil
	})
}
