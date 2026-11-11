package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
)

// Model lists always come from the provider itself:
//   - Antigravity: the `models.availableModels` list returned by ACP session/new (effort is part of the model id);
//   - Codex: `codex debug models` (the CLI's own catalog, visibility=list) with each model's reasoning levels;
//   - Claude: the CLI's model aliases (resolved by Claude Code to the latest model) and the `--effort` levels
//     parsed from `claude --help`.

var reClaudeEffortHelp = regexp.MustCompile(`(?s)--effort\s+<level>.*?\(([a-z, ]+)\)`)

type modelState struct {
	mu        sync.Mutex
	catalogs  map[domainagent.ProviderID][]domainagent.ModelOption
	catalogOf map[domainagent.ProviderID]string // executable the catalog was read from
	loadedAt  map[domainagent.ProviderID]time.Time
	current   map[domainagent.ProviderID]string // model the provider reports as active (Antigravity)
	selection map[domainagent.ProviderID]domainagent.ModelSelection
	// codeMode records, per provider, which models only reach tools through a code runner (Codex
	// "tool_mode": "code_mode_only"); such models are not shown tool definitions natively.
	codeMode map[domainagent.ProviderID]map[string]bool
}

func newModelState() *modelState {
	return &modelState{
		catalogs:  make(map[domainagent.ProviderID][]domainagent.ModelOption),
		catalogOf: make(map[domainagent.ProviderID]string),
		loadedAt:  make(map[domainagent.ProviderID]time.Time),
		current:   make(map[domainagent.ProviderID]string),
		selection: make(map[domainagent.ProviderID]domainagent.ModelSelection),
		codeMode:  make(map[domainagent.ProviderID]map[string]bool),
	}
}

func (s *Service) loadModelSelections(ctx context.Context) {
	for _, id := range domainagent.AllProviderIDs() {
		sel, err := s.repo.GetModelSelection(ctx, id)
		if err != nil {
			s.log.Warn("agent: load model selection", "provider", id, "err", err)
			continue
		}
		s.models.mu.Lock()
		s.models.selection[id] = sel
		s.models.mu.Unlock()
	}
}

// fillModelInfo adds model catalog and selection to a ProviderInfo.
func (s *Service) fillModelInfo(info *domainagent.ProviderInfo) {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	info.Models = append([]domainagent.ModelOption(nil), s.models.catalogs[info.ID]...)
	if info.Models == nil {
		info.Models = []domainagent.ModelOption{}
	}
	info.CurrentModel = s.models.current[info.ID]
	sel := s.models.selection[info.ID]
	// A saved choice the current account does not offer (e.g. a Plus model after switching to a free
	// account) is not shown as selected; the provider default is used instead.
	if sel.Model != "" && len(info.Models) > 0 {
		if opt, ok := findModel(info.Models, sel.Model); !ok {
			sel = domainagent.ModelSelection{}
		} else if sel.Effort != "" && !containsString(opt.Efforts, sel.Effort) {
			sel.Effort = ""
		}
	}
	info.SelectedModel = sel.Model
	info.SelectedEffort = sel.Effort
}

// invalidateModelCatalog drops a provider's model list so the next probe reads it again. The list
// depends on the signed-in account's plan, so it must not survive a sign-in, sign-out or path change.
func (s *Service) invalidateModelCatalog(id domainagent.ProviderID) {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	if id == domainagent.ProviderAntigravity {
		return // Antigravity's list comes from each ACP session and is replaced on connect
	}
	delete(s.models.catalogs, id)
	delete(s.models.catalogOf, id)
	delete(s.models.loadedAt, id)
	delete(s.models.codeMode, id)
}

// effectiveSelection returns the saved selection if it is still valid for the provider's current catalog.
func (s *Service) effectiveSelection(id domainagent.ProviderID) domainagent.ModelSelection {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	sel := s.models.selection[id]
	catalog := s.models.catalogs[id]
	if sel.Model == "" || len(catalog) == 0 {
		if sel.Model == "" {
			return domainagent.ModelSelection{}
		}
		return sel // catalog not loaded yet; the CLI validates the name itself
	}
	opt, ok := findModel(catalog, sel.Model)
	if !ok {
		return domainagent.ModelSelection{}
	}
	if sel.Effort != "" && !containsString(opt.Efforts, sel.Effort) {
		sel.Effort = ""
	}
	return sel
}

// SetModel validates and stores the model choice; an active Antigravity session switches immediately.
func (s *Service) SetModel(ctx context.Context, id domainagent.ProviderID, model, effort string) (domainagent.ProviderInfo, error) {
	if err := domainagent.ValidateProviderID(id); err != nil {
		return domainagent.ProviderInfo{}, err
	}
	model, effort = strings.TrimSpace(model), strings.TrimSpace(effort)
	spec := domainagent.Registry[id]
	if resolved, _, _, err := s.resolveExecutable(ctx, spec); err == nil && resolved != "" {
		s.ensureModelCatalog(ctx, id, resolved)
	}

	s.models.mu.Lock()
	catalog := s.models.catalogs[id]
	s.models.mu.Unlock()
	if model != "" {
		opt, ok := findModel(catalog, model)
		if !ok {
			if len(catalog) == 0 {
				return domainagent.ProviderInfo{}, apperr.Validation("the model list is not available yet; connect the provider first")
			}
			return domainagent.ProviderInfo{}, apperr.Validation(fmt.Sprintf("model %q is not offered by %s", model, spec.Name))
		}
		if effort != "" && !containsString(opt.Efforts, effort) {
			return domainagent.ProviderInfo{}, apperr.Validation(fmt.Sprintf("model %q does not support reasoning effort %q", model, effort))
		}
	} else {
		effort = ""
	}

	s.mu.Lock()
	busy := s.busy && s.activeID == id
	client := s.activeClient
	sessionID := s.activeSessionID
	isActiveACP := s.activeID == id && !s.isPerTurn(id) && client != nil
	s.mu.Unlock()
	if busy {
		return domainagent.ProviderInfo{}, userError(apperr.CodeConflict, msgModelChangeBusy, "wait for the current reply to finish before changing the model", nil)
	}
	if isActiveACP && sessionID != "" {
		target := model
		if target == "" {
			target = s.agyDefaultModel()
		}
		if target != "" {
			if err := client.SetModel(ctx, sessionID, target); err != nil {
				return domainagent.ProviderInfo{}, apperr.Unavailable(fmt.Sprintf("switch model: %v", err))
			}
			s.models.mu.Lock()
			s.models.current[id] = target
			s.models.mu.Unlock()
		}
	}

	sel := domainagent.ModelSelection{Model: model, Effort: effort}
	if err := s.repo.SetModelSelection(ctx, id, sel); err != nil {
		return domainagent.ProviderInfo{}, err
	}
	s.models.mu.Lock()
	s.models.selection[id] = sel
	s.models.mu.Unlock()

	info, err := s.Probe(ctx, id)
	if err == nil {
		s.emitStatus(info)
	}
	return info, err
}

// agyDefaultModel is the model Antigravity reported as its own default for new sessions.
func (s *Service) agyDefaultModel() string {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	return s.models.catalogOf[domainagent.ProviderAntigravity]
}

// applyACPSessionModels records the models an ACP session offers and applies the user's selection.
func (s *Service) applyACPSessionModels(ctx context.Context, id domainagent.ProviderID, client Transport, sessionID string) {
	models, current := client.AvailableModels()
	if len(models) == 0 {
		return
	}
	s.models.mu.Lock()
	s.models.catalogs[id] = models
	if s.models.catalogOf[id] == "" {
		s.models.catalogOf[id] = current // provider default (first session's currentModelId)
	}
	s.models.current[id] = current
	s.models.mu.Unlock()

	sel := s.effectiveSelection(id)
	if sel.Model == "" || sel.Model == current {
		return
	}
	if err := client.SetModel(ctx, sessionID, sel.Model); err != nil {
		s.log.Warn("agent: apply selected model", "provider", id, "model", sel.Model, "err", err)
		return
	}
	s.models.mu.Lock()
	s.models.current[id] = sel.Model
	s.models.mu.Unlock()
}

// ensureModelCatalog loads the per-turn CLI model catalog once per executable.
func (s *Service) ensureModelCatalog(ctx context.Context, id domainagent.ProviderID, executable string) {
	if id == domainagent.ProviderAntigravity || executable == "" {
		return
	}
	s.models.mu.Lock()
	loaded := s.models.catalogOf[id] == executable && len(s.models.catalogs[id]) > 0 &&
		time.Since(s.models.loadedAt[id]) < s.limits.StatusCacheTTL
	s.models.mu.Unlock()
	if loaded {
		return
	}
	var (
		models []domainagent.ModelOption
		err    error
	)
	ctx, cancel := context.WithTimeout(ctx, s.limits.ModelCatalogTimeout)
	defer cancel()
	switch id {
	case domainagent.ProviderCodex:
		models, err = s.loadCodexModels(ctx, executable)
	case domainagent.ProviderClaude:
		models, err = loadClaudeModels(ctx, executable)
	}
	if err != nil || len(models) == 0 {
		s.log.Warn("agent: load model catalog", "provider", id, "err", err)
		return
	}
	s.models.mu.Lock()
	s.models.catalogs[id] = models
	s.models.catalogOf[id] = executable
	s.models.loadedAt[id] = time.Now()
	s.models.mu.Unlock()
}

type codexCatalogJSON struct {
	Models []struct {
		Slug                     string `json:"slug"`
		DisplayName              string `json:"display_name"`
		Description              string `json:"description"`
		Visibility               string `json:"visibility"`
		Priority                 int    `json:"priority"`
		ToolMode                 string `json:"tool_mode"`
		DefaultReasoningLevel    string `json:"default_reasoning_level"`
		SupportedReasoningLevels []struct {
			Effort string `json:"effort"`
		} `json:"supported_reasoning_levels"`
	} `json:"models"`
}

func (s *Service) loadCodexModels(ctx context.Context, executable string) ([]domainagent.ModelOption, error) {
	envMap, err := prepareCodexProfile(s.profileDirFor(domainagent.ProviderCodex))
	if err != nil {
		return nil, err
	}
	cmd := buildCommand(ctx, executable, "debug", "models") // ensureModelCatalog bounds ctx
	cmd.Env = providerEnv(domainagent.ProviderCodex, envMap)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("codex debug models: %w", err)
	}
	models, err := parseCodexCatalog(out)
	if err == nil {
		s.models.mu.Lock()
		s.models.codeMode[domainagent.ProviderCodex] = parseCodexCodeModeModels(out)
		s.models.mu.Unlock()
	}
	return models, err
}

// parseCodexCodeModeModels returns the listed models whose tool_mode is code-mode only.
func parseCodexCodeModeModels(raw []byte) map[string]bool {
	var cat codexCatalogJSON
	if json.Unmarshal(raw, &cat) != nil {
		return nil
	}
	out := make(map[string]bool)
	for _, m := range cat.Models {
		if m.Visibility == "list" && m.Slug != "" {
			out[m.Slug] = strings.HasPrefix(m.ToolMode, "code_mode")
		}
	}
	return out
}

// toolsVisibility reports whether the model a provider will use sees tool definitions natively:
// "native", "code" (code runner only) or "unknown" (default model with a mixed or unloaded catalog).
func (s *Service) toolsVisibility(id domainagent.ProviderID) string {
	sel := s.effectiveSelection(id)
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	modes := s.models.codeMode[id]
	if len(modes) == 0 {
		return "unknown"
	}
	if sel.Model != "" {
		code, known := modes[sel.Model]
		switch {
		case !known:
			return "unknown"
		case code:
			return "code"
		default:
			return "native"
		}
	}
	// Provider default model: decided only when every listed model agrees.
	allCode, anyCode := true, false
	for _, code := range modes {
		allCode = allCode && code
		anyCode = anyCode || code
	}
	switch {
	case allCode:
		return "code"
	case !anyCode:
		return "native"
	}
	return "unknown"
}

func parseCodexCatalog(raw []byte) ([]domainagent.ModelOption, error) {
	var cat codexCatalogJSON
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("parse codex model catalog: %w", err)
	}
	sort.SliceStable(cat.Models, func(i, j int) bool { return cat.Models[i].Priority < cat.Models[j].Priority })
	var out []domainagent.ModelOption
	for _, m := range cat.Models {
		if m.Visibility != "list" || m.Slug == "" {
			continue
		}
		opt := domainagent.ModelOption{ID: m.Slug, Name: m.DisplayName, Description: m.Description, DefaultEffort: m.DefaultReasoningLevel}
		if opt.Name == "" {
			opt.Name = m.Slug
		}
		for _, lvl := range m.SupportedReasoningLevels {
			if lvl.Effort != "" {
				opt.Efforts = append(opt.Efforts, lvl.Effort)
			}
		}
		out = append(out, opt)
	}
	return out, nil
}

func loadClaudeModels(ctx context.Context, executable string) ([]domainagent.ModelOption, error) {
	cmd := buildCommand(ctx, executable, "--help") // ensureModelCatalog bounds ctx
	cmd.Env = providerEnv(domainagent.ProviderClaude, nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("claude --help: %w", err)
	}
	efforts := parseClaudeEfforts(string(out))
	aliases := domainagent.Registry[domainagent.ProviderClaude].ModelAliases
	models := make([]domainagent.ModelOption, 0, len(aliases))
	for _, m := range aliases {
		m.Efforts = efforts
		models = append(models, m)
	}
	return models, nil
}

func parseClaudeEfforts(help string) []string {
	m := reClaudeEffortHelp.FindStringSubmatch(help)
	if m == nil {
		return nil
	}
	var out []string
	for _, part := range strings.Split(m[1], ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseACPSessionModels reads the `models` block of an ACP session/new response.
func parseACPSessionModels(raw json.RawMessage) ([]domainagent.ModelOption, string) {
	var res struct {
		Models struct {
			AvailableModels []struct {
				ModelID     string `json:"modelId"`
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"availableModels"`
			CurrentModelID string `json:"currentModelId"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return nil, ""
	}
	out := make([]domainagent.ModelOption, 0, len(res.Models.AvailableModels))
	for _, m := range res.Models.AvailableModels {
		if m.ModelID == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ModelID
		}
		out = append(out, domainagent.ModelOption{ID: m.ModelID, Name: name, Description: m.Description})
	}
	return out, res.Models.CurrentModelID
}

func codexModelArgs(sel domainagent.ModelSelection) []string {
	var args []string
	if sel.Model != "" {
		args = append(args, "-m", sel.Model)
	}
	if sel.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(sel.Effort))
	}
	return args
}

func claudeModelArgs(sel domainagent.ModelSelection) []string {
	var args []string
	if sel.Model != "" {
		args = append(args, "--model", sel.Model)
	}
	if sel.Effort != "" {
		args = append(args, "--effort", sel.Effort)
	}
	return args
}

func findModel(models []domainagent.ModelOption, id string) (domainagent.ModelOption, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return domainagent.ModelOption{}, false
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
