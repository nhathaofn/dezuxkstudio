package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	domainagent "app/internal/domain/agent"
	"app/internal/fsutil"
)

func agyAPIKeyPath(profileRoot string) string {
	return filepath.Join(profileRoot, "gemini_api_key")
}

func agyOAuthPath(profileRoot string) string {
	return filepath.Join(profileRoot, "oauth_account")
}

func (ps profileSecrets) loadAgyAPIKey(profileRoot string) string {
	if profileRoot == "" {
		return ""
	}
	return ps.read(agyAPIKeyPath(profileRoot)) // OS credential store
}

func loadAgyOAuthLabel(profileRoot string) string {
	if profileRoot == "" {
		return ""
	}
	raw, err := os.ReadFile(agyOAuthPath(profileRoot))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (ps profileSecrets) probeAgyAuth(profileRoot string) (domainagent.AuthState, string) {
	if profileRoot == "" {
		return domainagent.AuthUnknown, ""
	}
	if ps.loadAgyAPIKey(profileRoot) != "" {
		return domainagent.AuthAuthenticated, "Isolated Gemini API key"
	}
	if label := loadAgyOAuthLabel(profileRoot); label != "" {
		return domainagent.AuthAuthenticated, label
	}
	return domainagent.AuthUnauthenticated, ""
}

func (ps profileSecrets) saveAgyAPIKey(profileRoot, apiKey string) error {
	if profileRoot == "" {
		return fmt.Errorf("agent: antigravity profile root is required")
	}
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return fmt.Errorf("gemini API key cannot be empty")
	}
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		return fmt.Errorf("agent: create antigravity profile dir: %w", err)
	}
	_ = os.Remove(agyOAuthPath(profileRoot))
	if err := ps.write(agyAPIKeyPath(profileRoot), trimmed); err != nil {
		return fmt.Errorf("agent: store isolated gemini api key: %w", err)
	}
	return nil
}

func (ps profileSecrets) saveAgyOAuth(profileRoot, tierName string) error {
	if profileRoot == "" {
		return fmt.Errorf("agent: antigravity profile root is required")
	}
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		return fmt.Errorf("agent: create antigravity profile dir: %w", err)
	}
	ps.remove(agyAPIKeyPath(profileRoot))
	label := "Google OAuth (OS keyring)"
	if trimmedTier := strings.TrimSpace(tierName); trimmedTier != "" {
		label = "Google OAuth · " + trimmedTier + " (OS keyring)"
	}
	if err := fsutil.WriteFileAtomic(agyOAuthPath(profileRoot), []byte(label+"\n"), 0o600); err != nil {
		return fmt.Errorf("agent: write oauth_account marker: %w", err)
	}
	return nil
}

func (ps profileSecrets) clearAgyAuth(profileRoot string) {
	if profileRoot == "" {
		return
	}
	ps.remove(agyAPIKeyPath(profileRoot))
	_ = os.Remove(agyOAuthPath(profileRoot))
}

// prepareAgyProfile configures an isolated Antigravity home directory under DezuxkStudio's DataDir.
// It injects ONLY DezuxkStudio's authenticated MCP server and denies built-in coding/shell tools,
// never touching the user's real ~/.gemini or %APPDATA%\antigravity-cli directories.
func (ps profileSecrets) prepareAgyProfile(profileRoot, mcpURL, mcpToken string) (string, map[string]string, error) {
	useOAuth := ps.loadAgyAPIKey(profileRoot) == "" && loadAgyOAuthLabel(profileRoot) != ""
	return ps.prepareAgyProfileWithMode(profileRoot, mcpURL, mcpToken, useOAuth)
}

func (ps profileSecrets) prepareAgyProfileWithMode(profileRoot, mcpURL, mcpToken string, useOAuth bool) (string, map[string]string, error) {
	if profileRoot == "" {
		return "", nil, fmt.Errorf("agent: antigravity profile root is required")
	}
	homeDir := filepath.Join(profileRoot, "home")
	geminiConfigDir := filepath.Join(homeDir, ".gemini", "config")
	cliDir := filepath.Join(homeDir, ".gemini", "antigravity-cli")
	appDataDir := filepath.Join(homeDir, "AppData", "Roaming")
	localAppDataDir := filepath.Join(homeDir, "AppData", "Local")
	xdgConfigDir := filepath.Join(homeDir, ".config")
	xdgDataDir := filepath.Join(homeDir, ".local", "share")

	for _, dir := range []string{homeDir, geminiConfigDir, cliDir, appDataDir, localAppDataDir, xdgConfigDir, xdgDataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", nil, fmt.Errorf("agent: create antigravity profile dir %s: %w", dir, err)
		}
	}

	// Write isolated MCP config pointing solely to DezuxkStudio's in-process HTTP MCP server.
	mcpCfg := map[string]any{
		"mcpServers": map[string]any{},
	}
	if mcpURL != "" && mcpToken != "" {
		mcpCfg["mcpServers"] = map[string]any{
			mcpServerName: map[string]any{
				"disabled":  false,
				"serverUrl": mcpURL,
				"headers": map[string]string{
					"Authorization": "Bearer " + mcpToken,
				},
			},
		}
	}
	mcpBytes, err := json.MarshalIndent(mcpCfg, "", "  ")
	if err != nil {
		return "", nil, err
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(geminiConfigDir, "mcp_config.json"), mcpBytes, 0o600); err != nil {
		return "", nil, fmt.Errorf("agent: write isolated mcp_config.json: %w", err)
	}

	// Unless user explicitly signed in via Google OAuth, force modelProvider="gemini" so agy never reads
	// Windows OS Credential Manager keyring behind the user's back; always deny built-in coding/shell tools.
	settingsCfg := map[string]any{
		"enableTelemetry": false,
		"toolPermission":  "request-review",
		"permissions": map[string]any{
			"deny": []string{
				"command(*)",
				"run_command(*)",
				"send_command_input(*)",
				"command_status(*)",
				"read_terminal(*)",
				"write_to_file(*)",
				"replace_file_content(*)",
				"multi_replace_file_content(*)",
				"view_file(*)",
				"view_file_outline(*)",
				"view_code_item(*)",
				"find_by_name(*)",
				"grep_search(*)",
				"list_dir(*)",
				"read_file(*)",
				"write_file(*)",
				"edit_file(*)",
			},
		},
	}
	if !useOAuth {
		settingsCfg["modelProvider"] = "gemini"
	}
	settingsBytes, err := json.MarshalIndent(settingsCfg, "", "  ")
	if err != nil {
		return "", nil, err
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(cliDir, "settings.json"), settingsBytes, 0o600); err != nil {
		return "", nil, fmt.Errorf("agent: write isolated settings.json: %w", err)
	}

	vol := filepath.VolumeName(homeDir)
	homePath := homeDir[len(vol):]
	envOverrides := map[string]string{
		"HOME":            homeDir,
		"USERPROFILE":     homeDir,
		"APPDATA":         appDataDir,
		"LOCALAPPDATA":    localAppDataDir,
		"XDG_CONFIG_HOME": xdgConfigDir,
		"XDG_DATA_HOME":   xdgDataDir,
	}
	if vol != "" {
		envOverrides["HOMEDRIVE"] = vol
		envOverrides["HOMEPATH"] = homePath
	}
	if !useOAuth {
		if apiKey := ps.loadAgyAPIKey(profileRoot); apiKey != "" {
			envOverrides["GEMINI_API_KEY"] = apiKey
		}
	}
	return homeDir, envOverrides, nil
}

func cleanupAgyProfileToken(profileRoot string) {
	if profileRoot == "" {
		return
	}
	mcpFile := filepath.Join(profileRoot, "home", ".gemini", "config", "mcp_config.json")
	if _, err := os.Stat(mcpFile); err == nil {
		_ = fsutil.WriteFileAtomic(mcpFile, []byte("{\"mcpServers\":{}}\n"), 0o600)
	}
}

// loginAgyOAuth signs the isolated profile in with Google through the agent's own ACP authenticate flow.
func (s *Service) loginAgyOAuth(ctx context.Context, spec domainagent.ProviderSpec, executable string) error {
	workDir, err := s.ensureWorkspaceDir()
	if err != nil {
		return err
	}
	profileRoot := s.profileDirFor(domainagent.ProviderAntigravity)
	_, agyEnvMap, err := s.secrets.prepareAgyProfileWithMode(profileRoot, "", "", true)
	if err != nil {
		return err
	}
	procEnv := providerEnv(domainagent.ProviderAntigravity, agyEnvMap)

	// ctx is already bounded by Login (Limits.LoginTimeout).
	transport, err := s.spawnTransport(ctx, executable, spec.ACPArgs, workDir, procEnv, nil, nil)
	if err != nil {
		return fmt.Errorf("agy login failed: %w", err)
	}
	defer func() { _ = transport.Close() }()

	initCtx, initCancel := context.WithTimeout(ctx, s.limits.InitializeTimeout)
	defer initCancel()
	initRes, err := transport.Initialize(initCtx, clientName, s.appVersion)
	if err != nil {
		return fmt.Errorf("agy login initialize failed: %w", err)
	}
	methodID, err := pickAuthMethod(spec, initRes)
	if err != nil {
		return err
	}

	tierName, err := transport.Authenticate(ctx, methodID)
	if err != nil {
		return fmt.Errorf("agy Google sign-in failed: %w", err)
	}
	return s.secrets.saveAgyOAuth(profileRoot, tierName)
}
