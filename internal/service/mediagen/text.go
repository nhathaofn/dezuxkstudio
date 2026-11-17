package mediagen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/progress"
)

type textProviderOutcome struct {
	text         string
	inputTokens  int
	outputTokens int
}

// GenerateText runs a structured or free-form LLM request through the configured PurposeText provider,
// respecting CapabilityLLM rate limits, JSON Schema constraints, and optional multimodal image inputs.
func (s *Service) GenerateText(ctx context.Context, req domainmedia.TextRequest) (domainmedia.TextResult, error) {
	norm, err := domainmedia.NormalizeTextRequest(req)
	if err != nil {
		return domainmedia.TextResult{}, err
	}
	releaseLeases := s.leaseInputs(norm.ImageInputs...)
	defer releaseLeases()

	images, err := s.readReferenceImages(norm.ImageInputs)
	if err != nil {
		return domainmedia.TextResult{}, err
	}

	target, limits, err := s.resolveTarget(ctx, domainprovider.PurposeText, norm.ModelRef)
	if err != nil {
		return domainmedia.TextResult{}, err
	}

	release, err := s.acquireLimit(ctx, target, domainprovider.CapabilityLLM, limits)
	if err != nil {
		return domainmedia.TextResult{}, err
	}
	defer release()

	callCtx, cancel := s.textTimeoutCtx(ctx)
	defer cancel()
	defer progress.KeepAlive(callCtx)()

	temp := s.cfg.TextTemperature
	if norm.Temperature != nil {
		temp = *norm.Temperature
	}
	maxTokens := norm.MaxTokens
	if maxTokens <= 0 {
		maxTokens = s.cfg.TextMaxTokens
	}
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	if target.modelFacts.Facts.MaxOutputTokens > 0 && maxTokens > target.modelFacts.Facts.MaxOutputTokens {
		maxTokens = target.modelFacts.Facts.MaxOutputTokens
	}

	var schemaObj map[string]any
	if len(norm.JSONSchema) > 0 {
		if err := json.Unmarshal(norm.JSONSchema, &schemaObj); err != nil {
			return domainmedia.TextResult{}, apperr.Validation("JSON schema must be a JSON object")
		}
	}

	start := time.Now()
	var out textProviderOutcome
	switch target.protocol {
	case "openai-chat", "ark-chat", "dashscope-chat":
		out, err = s.genOpenAIChatText(callCtx, target, norm, schemaObj, images, temp, maxTokens)
	case "openai-responses":
		out, err = s.genOpenAIResponsesText(callCtx, target, norm, schemaObj, images, temp, maxTokens)
	case "gemini-aistudio-text", "gemini-vertex-text":
		out, err = s.genGeminiText(callCtx, target, norm, schemaObj, images, temp, maxTokens)
	case "anthropic-messages":
		out, err = s.genAnthropicText(callCtx, target, norm, schemaObj, images, temp, maxTokens)
	case "minimax-text":
		out, err = s.genMiniMaxText(callCtx, target, norm, schemaObj, temp, maxTokens)
	default:
		return domainmedia.TextResult{}, apperr.Validation("Unsupported text protocol: " + target.protocol)
	}
	if err != nil {
		return domainmedia.TextResult{}, err
	}

	elapsedMs := time.Since(start).Milliseconds()
	text := strings.TrimSpace(out.text)
	var parsedJSON json.RawMessage
	if len(norm.JSONSchema) > 0 {
		cleaned := sanitizeJSONPayload(text)
		if !json.Valid([]byte(cleaned)) {
			return domainmedia.TextResult{}, apperr.Unavailable("LLM returned invalid JSON output")
		}
		if err := verifyRequiredSchemaKeys([]byte(cleaned), schemaObj); err != nil {
			return domainmedia.TextResult{}, apperr.Unavailable("LLM JSON output missed required fields: " + err.Error())
		}
		text = cleaned
		parsedJSON = json.RawMessage(cleaned)
	}

	return domainmedia.TextResult{
		Text:         text,
		ParsedJSON:   parsedJSON,
		InputTokens:  out.inputTokens,
		OutputTokens: out.outputTokens,
		Provider:     target.providerName,
		Model:        target.modelID,
		ElapsedMs:    elapsedMs,
	}, nil
}

func (s *Service) textTimeoutCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	d := s.cfg.TextTimeout
	if d <= 0 {
		d = s.cfg.RequestTimeout
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= d {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

func (s *Service) genOpenAIChatText(
	ctx context.Context,
	t targetSpec,
	req domainmedia.TextRequest,
	schemaObj map[string]any,
	images []localImageData,
	temp float64,
	maxTokens int,
) (textProviderOutcome, error) {
	var endpoint string
	switch t.protocol {
	case "ark-chat":
		endpoint = ensureArkBase(t.baseURL) + "/chat/completions"
	case "dashscope-chat":
		root := ensureRootStrippingSuffixes(t.baseURL, "/compatible-mode/v1", "/api/v1", "/v1")
		endpoint = root + "/compatible-mode/v1/chat/completions"
	default:
		endpoint = domainprovider.EnsureOpenAIBase(t.baseURL) + "/chat/completions"
	}
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	buildPayload := func(mode string) map[string]any {
		msgs := make([]map[string]any, 0, 2)
		sysPrompt := req.SystemPrompt
		if mode != "json_schema" && len(req.JSONSchema) > 0 {
			hint := "Return ONLY valid JSON conforming strictly to this JSON Schema:\n" + string(req.JSONSchema)
			if sysPrompt != "" {
				sysPrompt += "\n\n" + hint
			} else {
				sysPrompt = hint
			}
		}
		if sysPrompt != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": sysPrompt})
		}

		if len(images) == 0 {
			msgs = append(msgs, map[string]any{"role": "user", "content": req.UserPrompt})
		} else {
			parts := make([]map[string]any, 0, len(images)+1)
			parts = append(parts, map[string]any{"type": "text", "text": req.UserPrompt})
			for _, img := range images {
				parts = append(parts, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": img.DataURI},
				})
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": parts})
		}

		payload := map[string]any{
			"model":       t.modelID,
			"messages":    msgs,
			"temperature": temp,
			"max_tokens":  maxTokens,
		}
		switch mode {
		case "json_schema":
			payload["response_format"] = map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name":   req.JSONSchemaName,
					"strict": true,
					"schema": schemaObj,
				},
			}
		case "json_object":
			payload["response_format"] = map[string]any{"type": "json_object"}
		}
		return payload
	}

	modes := []string{"plain"}
	if len(schemaObj) > 0 {
		modes = []string{"json_schema", "json_object", "plain"}
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	var lastErr error
	for i, mode := range modes {
		err := s.doJSON(ctx, "POST", endpoint, headers, buildPayload(mode), &resp)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if i < len(modes)-1 && isBadRequestErr(err) {
			continue
		}
		return textProviderOutcome{}, err
	}
	if lastErr != nil {
		return textProviderOutcome{}, lastErr
	}
	if len(resp.Choices) == 0 {
		return textProviderOutcome{}, apperr.Unavailable("LLM provider returned no choices")
	}
	txt := extractOpenAIMessageContent(resp.Choices[0].Message.Content)
	if strings.TrimSpace(txt) == "" {
		return textProviderOutcome{}, apperr.Unavailable("LLM provider returned empty content")
	}
	return textProviderOutcome{
		text:         txt,
		inputTokens:  resp.Usage.PromptTokens,
		outputTokens: resp.Usage.CompletionTokens,
	}, nil
}

func (s *Service) genOpenAIResponsesText(
	ctx context.Context,
	t targetSpec,
	req domainmedia.TextRequest,
	schemaObj map[string]any,
	images []localImageData,
	temp float64,
	maxTokens int,
) (textProviderOutcome, error) {
	endpoint := domainprovider.EnsureOpenAIBase(t.baseURL) + "/responses"
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	content := make([]map[string]any, 0, len(images)+1)
	content = append(content, map[string]any{"type": "input_text", "text": req.UserPrompt})
	for _, img := range images {
		content = append(content, map[string]any{"type": "input_image", "image_url": img.DataURI})
	}
	payload := map[string]any{
		"model":             t.modelID,
		"input":             []map[string]any{{"role": "user", "content": content}},
		"temperature":       temp,
		"max_output_tokens": maxTokens,
	}
	if req.SystemPrompt != "" {
		payload["instructions"] = req.SystemPrompt
	}
	if len(schemaObj) > 0 {
		payload["text"] = map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   req.JSONSchemaName,
				"strict": true,
				"schema": schemaObj,
			},
		}
	}

	var resp struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := s.doJSON(ctx, "POST", endpoint, headers, payload, &resp); err != nil {
		return textProviderOutcome{}, err
	}
	txt := strings.TrimSpace(resp.OutputText)
	if txt == "" {
		var parts []string
		for _, item := range resp.Output {
			for _, c := range item.Content {
				if strings.TrimSpace(c.Text) != "" {
					parts = append(parts, c.Text)
				}
			}
		}
		txt = strings.Join(parts, "\n")
	}
	if strings.TrimSpace(txt) == "" {
		return textProviderOutcome{}, apperr.Unavailable("OpenAI Responses API returned empty text")
	}
	return textProviderOutcome{
		text:         txt,
		inputTokens:  resp.Usage.InputTokens,
		outputTokens: resp.Usage.OutputTokens,
	}, nil
}

func (s *Service) genGeminiText(
	ctx context.Context,
	t targetSpec,
	req domainmedia.TextRequest,
	schemaObj map[string]any,
	images []localImageData,
	temp float64,
	maxTokens int,
) (textProviderOutcome, error) {
	modelID := geminiModelName(t.modelID)
	headers := map[string]string{}
	var targetURL string
	if t.protocol == "gemini-vertex-text" {
		tok, proj, err := s.getVertexToken(ctx, t)
		if err != nil {
			return textProviderOutcome{}, err
		}
		host := strings.TrimRight(t.baseURL, "/")
		if host == "" {
			host = "https://aiplatform.googleapis.com"
		}
		headers["Authorization"] = "Bearer " + tok
		targetURL = fmt.Sprintf("%s/v1beta1/projects/%s/locations/global/publishers/google/models/%s:generateContent",
			host, proj, modelID)
	} else {
		root := ensureRootStrippingSuffixes(t.baseURL, "/v1beta", "/v1")
		headers["x-goog-api-key"] = t.apiKey
		targetURL = fmt.Sprintf("%s/v1beta/models/%s:generateContent", root, modelID)
	}

	parts := make([]map[string]any, 0, len(images)+1)
	for _, img := range images {
		parts = append(parts, map[string]any{
			"inlineData": map[string]any{
				"mimeType": img.MimeType,
				"data":     img.Base64,
			},
		})
	}
	parts = append(parts, map[string]any{"text": req.UserPrompt})

	buildPayload := func(withSchema bool) map[string]any {
		genCfg := map[string]any{
			"temperature":     temp,
			"maxOutputTokens": maxTokens,
		}
		sysPrompt := req.SystemPrompt
		if len(schemaObj) > 0 {
			genCfg["responseMimeType"] = "application/json"
			if withSchema {
				genCfg["responseSchema"] = sanitizeGeminiSchema(schemaObj)
			} else {
				hint := "Return ONLY valid JSON conforming strictly to this JSON Schema:\n" + string(req.JSONSchema)
				if sysPrompt != "" {
					sysPrompt += "\n\n" + hint
				} else {
					sysPrompt = hint
				}
			}
		}
		p := map[string]any{
			"contents":         []map[string]any{{"role": "user", "parts": parts}},
			"generationConfig": genCfg,
		}
		if sysPrompt != "" {
			p["systemInstruction"] = map[string]any{
				"parts": []map[string]any{{"text": sysPrompt}},
			}
		}
		return p
	}

	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}

	if len(schemaObj) > 0 {
		err := s.doJSON(ctx, "POST", targetURL, headers, buildPayload(true), &resp)
		if err != nil && isBadRequestErr(err) {
			err = s.doJSON(ctx, "POST", targetURL, headers, buildPayload(false), &resp)
		}
		if err != nil {
			return textProviderOutcome{}, err
		}
	} else {
		if err := s.doJSON(ctx, "POST", targetURL, headers, buildPayload(false), &resp); err != nil {
			return textProviderOutcome{}, err
		}
	}

	var textParts []string
	for _, cand := range resp.Candidates {
		for _, p := range cand.Content.Parts {
			if p.Thought {
				continue
			}
			if strings.TrimSpace(p.Text) != "" {
				textParts = append(textParts, p.Text)
			}
		}
		if len(textParts) > 0 {
			break
		}
	}
	txt := strings.Join(textParts, "")
	if strings.TrimSpace(txt) == "" {
		return textProviderOutcome{}, apperr.Unavailable("Gemini returned no text output")
	}
	return textProviderOutcome{
		text:         txt,
		inputTokens:  resp.UsageMetadata.PromptTokenCount,
		outputTokens: resp.UsageMetadata.CandidatesTokenCount,
	}, nil
}

func (s *Service) genAnthropicText(
	ctx context.Context,
	t targetSpec,
	req domainmedia.TextRequest,
	schemaObj map[string]any,
	images []localImageData,
	temp float64,
	maxTokens int,
) (textProviderOutcome, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	endpoint := root + "/v1/messages"
	headers := map[string]string{
		"x-api-key":         t.apiKey,
		"anthropic-version": "2023-06-01",
	}

	content := make([]map[string]any, 0, len(images)+1)
	for _, img := range images {
		content = append(content, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": img.MimeType,
				"data":       img.Base64,
			},
		})
	}
	content = append(content, map[string]any{"type": "text", "text": req.UserPrompt})

	payload := map[string]any{
		"model":       t.modelID,
		"max_tokens":  maxTokens,
		"temperature": temp,
		"messages":    []map[string]any{{"role": "user", "content": content}},
	}
	if req.SystemPrompt != "" {
		payload["system"] = req.SystemPrompt
	}
	if len(schemaObj) > 0 {
		toolName := req.JSONSchemaName
		if toolName == "" {
			toolName = "structured_output"
		}
		payload["tools"] = []map[string]any{{
			"name":         toolName,
			"description":  "Emit the final structured JSON response matching the schema.",
			"input_schema": schemaObj,
		}}
		payload["tool_choice"] = map[string]any{"type": "tool", "name": toolName}
	}

	var resp struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	err := s.doJSON(ctx, "POST", endpoint, headers, payload, &resp)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == apperr.CodeUnauthorized {
			bearerHeaders := map[string]string{
				"Authorization":     "Bearer " + t.apiKey,
				"anthropic-version": "2023-06-01",
			}
			err = s.doJSON(ctx, "POST", endpoint, bearerHeaders, payload, &resp)
		}
	}
	if err != nil {
		return textProviderOutcome{}, err
	}

	var outText string
	for _, blk := range resp.Content {
		if blk.Type == "tool_use" && len(blk.Input) > 0 && string(blk.Input) != "null" {
			outText = string(blk.Input)
			break
		}
		if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
			outText += blk.Text
		}
	}
	if strings.TrimSpace(outText) == "" {
		return textProviderOutcome{}, apperr.Unavailable("Anthropic returned empty content")
	}
	return textProviderOutcome{
		text:         outText,
		inputTokens:  resp.Usage.InputTokens,
		outputTokens: resp.Usage.OutputTokens,
	}, nil
}

func (s *Service) genMiniMaxText(
	ctx context.Context,
	t targetSpec,
	req domainmedia.TextRequest,
	schemaObj map[string]any,
	temp float64,
	maxTokens int,
) (textProviderOutcome, error) {
	root := ensureRootStrippingSuffixes(t.baseURL, "/v1")
	endpoint := root + "/v1/text/chatcompletion_v2"
	headers := map[string]string{"Authorization": "Bearer " + t.apiKey}

	sysPrompt := req.SystemPrompt
	if len(req.JSONSchema) > 0 {
		hint := "Return ONLY valid JSON conforming strictly to this JSON Schema:\n" + string(req.JSONSchema)
		if sysPrompt != "" {
			sysPrompt += "\n\n" + hint
		} else {
			sysPrompt = hint
		}
	}

	msgs := make([]map[string]any, 0, 2)
	if sysPrompt != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": sysPrompt})
	}
	msgs = append(msgs, map[string]any{"role": "user", "content": req.UserPrompt})

	buildPayload := func(withJSONMode bool) map[string]any {
		p := map[string]any{
			"model":       t.modelID,
			"messages":    msgs,
			"temperature": temp,
			"max_tokens":  maxTokens,
		}
		if withJSONMode {
			p["response_format"] = map[string]any{"type": "json_object"}
		}
		return p
	}

	var resp struct {
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens      int `json:"total_tokens"`
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	err := s.doJSON(ctx, "POST", endpoint, headers, buildPayload(len(schemaObj) > 0), &resp)
	if err != nil && len(schemaObj) > 0 && isBadRequestErr(err) {
		err = s.doJSON(ctx, "POST", endpoint, headers, buildPayload(false), &resp)
	}
	if err != nil {
		return textProviderOutcome{}, err
	}
	if resp.BaseResp.StatusCode != 0 {
		return textProviderOutcome{}, apperr.Unavailable(fmt.Sprintf("MiniMax (%d): %s", resp.BaseResp.StatusCode, resp.BaseResp.StatusMsg))
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return textProviderOutcome{}, apperr.Unavailable("MiniMax returned empty content")
	}
	return textProviderOutcome{
		text:         resp.Choices[0].Message.Content,
		inputTokens:  resp.Usage.PromptTokens,
		outputTokens: resp.Usage.CompletionTokens,
	}, nil
}

func isBadRequestErr(err error) bool {
	var ae *apperr.Error
	return errors.As(err, &ae) && ae.Code == apperr.CodeBadRequest
}

func extractOpenAIMessageContent(raw any) string {
	switch c := raw.(type) {
	case string:
		return c
	case []any:
		var sb strings.Builder
		for _, item := range c {
			obj, _ := item.(map[string]any)
			if obj == nil {
				continue
			}
			if txt, _ := obj["text"].(string); txt != "" {
				sb.WriteString(txt)
			}
		}
		return sb.String()
	default:
		return ""
	}
}

// sanitizeJSONPayload removes markdown code fences and surrounding prose around a JSON object/array.
func sanitizeJSONPayload(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		} else {
			s = strings.TrimPrefix(s, "```json")
			s = strings.TrimPrefix(s, "```")
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	if json.Valid([]byte(s)) {
		return s
	}
	startObj := strings.IndexByte(s, '{')
	startArr := strings.IndexByte(s, '[')
	start := -1
	var closeCh byte
	if startObj >= 0 && (startArr < 0 || startObj < startArr) {
		start = startObj
		closeCh = '}'
	} else if startArr >= 0 {
		start = startArr
		closeCh = ']'
	}
	if start >= 0 {
		if end := strings.LastIndexByte(s, closeCh); end > start {
			candidate := strings.TrimSpace(s[start : end+1])
			if json.Valid([]byte(candidate)) {
				return candidate
			}
		}
	}
	return s
}

func verifyRequiredSchemaKeys(data []byte, schemaObj map[string]any) error {
	if len(schemaObj) == 0 {
		return nil
	}
	reqRaw, hasReq := schemaObj["required"]
	if !hasReq {
		return nil
	}
	reqList, ok := reqRaw.([]any)
	if !ok || len(reqList) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("expected JSON object")
	}
	for _, r := range reqList {
		key, _ := r.(string)
		if key == "" {
			continue
		}
		if _, exists := obj[key]; !exists {
			return fmt.Errorf("missing %q", key)
		}
	}
	return nil
}

// sanitizeGeminiSchema strips JSON Schema keywords not accepted by Gemini's responseSchema field.
func sanitizeGeminiSchema(node any) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			switch k {
			case "$schema", "$id", "additionalProperties", "default", "title":
				continue
			}
			out[k] = sanitizeGeminiSchema(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeGeminiSchema(item)
		}
		return out
	default:
		return node
	}
}
