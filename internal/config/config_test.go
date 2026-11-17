package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/buildinfo"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsAreValidForEveryMode(t *testing.T) {
	for _, m := range Modes() {
		c := Default(m)
		if m == ModeClient {
			c.Client.RemoteURL = "wss://example.com/ws"
		}
		if err := c.Validate(); err != nil {
			t.Errorf("mode %s: %v", m, err)
		}
	}
}

func TestLayerPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, FileName, `{"server":{"port":1111},"log":{"level":"warn"},"realtime":{"action_timeout":"7s"}}`)

	res, err := Load(Source{
		Mode:      ModeDesktop,
		ConfigDir: dir,
		EnvPrefix: "T_",
		LookupEnv: env(map[string]string{"T_SERVER_PORT": "2222", "T_LOG_LEVEL": "debug"}),
		Args:      []string{"--server-port=3333"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Config
	if c.Server.Port != 3333 {
		t.Errorf("flag must win: port = %d", c.Server.Port)
	}
	if c.Log.Level != "debug" {
		t.Errorf("env must beat file: level = %s", c.Log.Level)
	}
	if c.Realtime.ActionTimeout != 7*time.Second {
		t.Errorf("file must beat default: timeout = %s", c.Realtime.ActionTimeout)
	}
	if res.File != filepath.Join(dir, FileName) {
		t.Errorf("file = %q", res.File)
	}
}

func TestFileErrorsAreReported(t *testing.T) {
	cases := map[string]string{
		"unknown key":  `{"server":{"prot":1}}`,
		"invalid json": `{"server":`,
		"bad type":     `{"server":{"port":"abc"}}`,
		"null":         `{"log":{"level":null}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, FileName, body)
			if _, err := Load(Source{Mode: ModeDesktop, ConfigDir: dir}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestExplicitConfigMustExist(t *testing.T) {
	_, err := Load(Source{Mode: ModeDesktop, Args: []string{"--config", filepath.Join(t.TempDir(), "missing.json")}})
	if err == nil || !strings.Contains(err.Error(), "explicit config file") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnvErrorsAreReported(t *testing.T) {
	_, err := Load(Source{Mode: ModeDesktop, EnvPrefix: "T_", LookupEnv: env(map[string]string{"T_SERVER_PORT": "x"})})
	if err == nil {
		t.Fatal("expected error for non-numeric env value")
	}
}

func TestHelpAndUnknownFlags(t *testing.T) {
	if _, err := Load(Source{Mode: ModeDesktop, Args: []string{"-h"}}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help: %v", err)
	}
	if _, err := Load(Source{Mode: ModeDesktop, Args: []string{"--nope"}}); err == nil {
		t.Fatal("unknown flag must fail")
	}
}

func TestValidationRules(t *testing.T) {
	cases := map[string]func(c *Config){
		"desktop non-loopback":              func(c *Config) { c.Server.Host = "0.0.0.0" },
		"server public no token":            func(c *Config) { c.Mode = ModeServer; c.Server.Host = "0.0.0.0" },
		"broadcast without token":           func(c *Config) { c.Mode = ModeServer; c.Server.EnableBroadcast = true },
		"client without remote":             func(c *Config) { c.Mode = ModeClient },
		"client http remote":                func(c *Config) { c.Mode = ModeClient; c.Client.RemoteURL = "http://x" },
		"client creds in url":               func(c *Config) { c.Mode = ModeClient; c.Client.RemoteURL = "ws://u:p@x/ws" },
		"wildcard origin desktop":           func(c *Config) { c.Server.AllowedOrigins = []string{"*"} },
		"window below minimum":              func(c *Config) { c.Desktop.Width = 100 },
		"bad colour":                        func(c *Config) { c.Desktop.Background = "red" },
		"db file with path":                 func(c *Config) { c.Storage.DBFile = "../x.db" },
		"http update feed":                  func(c *Config) { c.Update.FeedURL = "http://example.com/feed" },
		"zero timeout":                      func(c *Config) { c.Realtime.ActionTimeout = 0 },
		"zero long action timeout":          func(c *Config) { c.Realtime.LongActionTimeout = 0 },
		"unknown log level":                 func(c *Config) { c.Log.Level = "loud" },
		"port out of range":                 func(c *Config) { c.Server.Port = 70000 },
		"tiny max message":                  func(c *Config) { c.Realtime.MaxMessageBytes = 10 },
		"negative log backups":              func(c *Config) { c.Log.MaxBackups = -1 },
		"zero backups to keep":              func(c *Config) { c.Storage.BackupsToKeep = 0 },
		"zero diagnostics keep":             func(c *Config) { c.Diagnostics.Keep = 0 },
		"zero ready timeout":                func(c *Config) { c.Desktop.ReadyTimeout = 0 },
		"size smaller than minimum":         func(c *Config) { c.Desktop.Width = c.Desktop.MinWidth - 1 },
		"negative window width":             func(c *Config) { c.Desktop.Width = -1 },
		"height below minimum":              func(c *Config) { c.Desktop.Height = 100 },
		"height smaller than min":           func(c *Config) { c.Desktop.Height = c.Desktop.MinHeight - 1 },
		"zero max media upload bytes":       func(c *Config) { c.Server.MaxMediaUploadBytes = 0 },
		"negative max media upload bytes":   func(c *Config) { c.Server.MaxMediaUploadBytes = -1 },
		"zero media staging ttl":            func(c *Config) { c.Server.MediaStagingTTL = 0 },
		"zero media staging clean interval": func(c *Config) { c.Server.MediaStagingCleanupInterval = 0 },
		"zero max concurrent subagents":     func(c *Config) { c.Agent.MaxConcurrentSubagents = 0 },
		"negative max concurrent subagents": func(c *Config) { c.Agent.MaxConcurrentSubagents = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := Default(ModeDesktop)
			mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenPath := writeFile(t, dir, "token", "  s3cret \n")
	res, err := Load(Source{Mode: ModeServer, Args: []string{"--server-auth-token-file", tokenPath}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Server.AuthToken != "s3cret" {
		t.Fatalf("token = %q", res.Config.Server.AuthToken)
	}

	_, err = Load(Source{Mode: ModeServer, Args: []string{"--server-auth-token-file", tokenPath, "--server-auth-token", "x"}})
	if err == nil {
		t.Fatal("both token sources must be rejected")
	}
}

func TestSecretsAreRedactedAndWarned(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, FileName, `{"server":{"auth_token":"abc"}}`)
	res, err := Load(Source{Mode: ModeServer, ConfigDir: dir, EnvPrefix: "T_"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Config.Redacted()["server.auth_token"]; got != "[REDACTED]" {
		t.Errorf("redacted = %q", got)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "T_SERVER_AUTH_TOKEN") {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestListValuesFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, FileName, `{"server":{"allowed_origins":["wails://wails"," https://a.test "]}}`)
	res, err := Load(Source{Mode: ModeDesktop, ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Config.Server.AllowedOrigins, "|"); got != "wails://wails|https://a.test" {
		t.Errorf("origins = %s", got)
	}
}

func TestLegacyModeAliases(t *testing.T) {
	for in, want := range map[string]Mode{"DezuxkStudio": ModeDesktop, "dezuxk-server": ModeServer, "client": ModeClient} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("%s → %s, %v", in, got, err)
		}
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Error("bogus mode accepted")
	}
}

func TestEnvPrefixAndHelpers(t *testing.T) {
	if got := EnvPrefixFor("Dezuxk-Studio"); got != "DEZUXK_STUDIO_" {
		t.Errorf("prefix = %s", got)
	}
	if c, err := ParseHexColor("#0f1117"); err != nil || c.A != 255 || c.R != 0x0f {
		t.Errorf("colour = %+v, %v", c, err)
	}
	for _, h := range []string{"127.0.0.1", "::1", "localhost", "[::1]"} {
		if !IsLoopbackHost(h) {
			t.Errorf("%s should be loopback", h)
		}
	}
	if IsLoopbackHost("0.0.0.0") {
		t.Error("0.0.0.0 is not loopback")
	}
	var sb strings.Builder
	Usage(&sb, ModeServer, "P_")
	if !strings.Contains(sb.String(), "--server-port") || !strings.Contains(sb.String(), "P_SERVER_PORT") {
		t.Error("usage misses derived names")
	}
}

func TestAgentModeValidation(t *testing.T) {
	c := Default(ModeDesktop)
	if c.DeveloperMode() {
		t.Fatal("default desktop mode must not enable developer mode")
	}
	c.Agent.Mode = "developer"
	if err := c.Validate(); err != nil {
		t.Fatalf("desktop developer mode should validate, got %v", err)
	}
	if !c.DeveloperMode() {
		t.Fatal("expected DeveloperMode() == true when Agent.Mode is developer on desktop")
	}

	srv := Default(ModeServer)
	srv.Server.AuthToken = "tok"
	srv.Agent.Mode = "developer"
	if err := srv.Validate(); err == nil {
		t.Fatal("developer mode must be rejected in headless server mode")
	}

	bad := Default(ModeDesktop)
	bad.Agent.Mode = "unsafe"
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown agent.mode must be rejected")
	}
}

func TestUpdateDefaultsFollowBuildKind(t *testing.T) {
	orig := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = orig })

	buildinfo.Version = "0.0.0-dev"
	devCfg := Default(ModeDesktop)
	if devCfg.Update.CheckOnStartup || devCfg.Agent.AutoUpdateCLI {
		t.Fatalf("dev build must disable startup auto-updates: update=%v cli=%v",
			devCfg.Update.CheckOnStartup, devCfg.Agent.AutoUpdateCLI)
	}

	buildinfo.Version = "1.0.0"
	relCfg := Default(ModeDesktop)
	want := !buildinfo.IsDev()
	if relCfg.Update.CheckOnStartup != want || relCfg.Agent.AutoUpdateCLI != want {
		t.Fatalf("release build startup auto-updates mismatch: got update=%v cli=%v want=%v",
			relCfg.Update.CheckOnStartup, relCfg.Agent.AutoUpdateCLI, want)
	}
}

func TestAllowHostAdminConfigAndWarnings(t *testing.T) {
	for _, m := range Modes() {
		if Default(m).Server.AllowHostAdmin {
			t.Fatalf("mode %s: expected AllowHostAdmin default to be false", m)
		}
	}

	// JSON file override to true in ModeServer emits warning and validates with token.
	dir := t.TempDir()
	writeFile(t, dir, FileName, `{"server":{"allow_host_admin":true}}`)
	jsonRes, err := Load(Source{
		Mode:      ModeServer,
		ConfigDir: dir,
		EnvPrefix: "T_",
		LookupEnv: env(map[string]string{"T_SERVER_AUTH_TOKEN": "tok"}),
	})
	if err != nil {
		t.Fatalf("json override Load failed: %v", err)
	}
	if !jsonRes.Config.Server.AllowHostAdmin {
		t.Fatal("expected AllowHostAdmin == true from JSON config")
	}
	if !hasWarningContaining(jsonRes.Warnings, "server.allow_host_admin") {
		t.Fatalf("expected ModeServer warning for server.allow_host_admin, got %v", jsonRes.Warnings)
	}

	// Env override beats JSON false; CLI flag overrides env.
	writeFile(t, dir, FileName, `{"server":{"allow_host_admin":false}}`)
	envRes, err := Load(Source{
		Mode:      ModeServer,
		ConfigDir: dir,
		EnvPrefix: "T_",
		LookupEnv: env(map[string]string{
			"T_SERVER_AUTH_TOKEN":       "tok",
			"T_SERVER_ALLOW_HOST_ADMIN": "true",
		}),
	})
	if err != nil {
		t.Fatalf("env override Load failed: %v", err)
	}
	if !envRes.Config.Server.AllowHostAdmin {
		t.Fatal("expected AllowHostAdmin == true from env")
	}
	if !hasWarningContaining(envRes.Warnings, "server.allow_host_admin") {
		t.Fatalf("expected ModeServer warning when enabled via env, got %v", envRes.Warnings)
	}

	cliRes, err := Load(Source{
		Mode:      ModeServer,
		EnvPrefix: "T_",
		LookupEnv: env(map[string]string{"T_SERVER_AUTH_TOKEN": "tok"}),
		Args:      []string{"--server-host=0.0.0.0", "--server-allow-host-admin"},
	})
	if err != nil {
		t.Fatalf("cli override Load failed: %v", err)
	}
	if !cliRes.Config.Server.AllowHostAdmin {
		t.Fatal("expected AllowHostAdmin == true from CLI flag")
	}
	if !hasWarningContaining(cliRes.Warnings, "server.allow_host_admin") {
		t.Fatalf("expected ModeServer warning when enabled via CLI, got %v", cliRes.Warnings)
	}

	// CLI false overrides env true and suppresses the warning.
	cliFalseRes, err := Load(Source{
		Mode:      ModeServer,
		EnvPrefix: "T_",
		LookupEnv: env(map[string]string{
			"T_SERVER_AUTH_TOKEN":       "tok",
			"T_SERVER_ALLOW_HOST_ADMIN": "true",
		}),
		Args: []string{"--server-allow-host-admin=false"},
	})
	if err != nil {
		t.Fatalf("cli false override Load failed: %v", err)
	}
	if cliFalseRes.Config.Server.AllowHostAdmin {
		t.Fatal("expected CLI false to override env true")
	}
	if hasWarningContaining(cliFalseRes.Warnings, "server.allow_host_admin") {
		t.Fatalf("unexpected server.allow_host_admin warning when disabled: %v", cliFalseRes.Warnings)
	}

	// Non-loopback ModeServer without auth_token still fails validation even if AllowHostAdmin is true.
	noTok := Default(ModeServer)
	noTok.Server.Host = "0.0.0.0"
	noTok.Server.AllowHostAdmin = true
	if err := noTok.Validate(); err == nil {
		t.Fatal("expected non-loopback ModeServer without auth token to fail validation")
	}

	// ModeDesktop with AllowHostAdmin true validates without emitting the ModeServer warning.
	deskRes, err := Load(Source{
		Mode: ModeDesktop,
		Args: []string{"--server-allow-host-admin"},
	})
	if err != nil {
		t.Fatalf("ModeDesktop Load failed: %v", err)
	}
	if hasWarningContaining(deskRes.Warnings, "server.allow_host_admin") {
		t.Fatalf("unexpected server.allow_host_admin warning in desktop mode: %v", deskRes.Warnings)
	}
}

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestMediaAndSubagentLimitConfig(t *testing.T) {
	def := Default(ModeDesktop)
	if def.Server.MaxMediaUploadBytes != 64<<20 {
		t.Fatalf("MaxMediaUploadBytes = %d, want %d", def.Server.MaxMediaUploadBytes, 64<<20)
	}
	if def.Server.MediaStagingTTL != 24*time.Hour {
		t.Fatalf("MediaStagingTTL = %v, want 24h", def.Server.MediaStagingTTL)
	}
	if def.Server.MediaStagingCleanupInterval != 30*time.Minute {
		t.Fatalf("MediaStagingCleanupInterval = %v, want 30m", def.Server.MediaStagingCleanupInterval)
	}
	if def.Agent.MaxConcurrentSubagents != 4 {
		t.Fatalf("MaxConcurrentSubagents = %d, want 4", def.Agent.MaxConcurrentSubagents)
	}

	dir := t.TempDir()
	writeFile(t, dir, FileName, `{
		"server": {
			"max_media_upload_bytes": 1048576,
			"media_staging_ttl": "12h",
			"media_staging_cleanup_interval": "5m"
		},
		"agent": {
			"max_concurrent_subagents": 6
		}
	}`)

	// File overrides defaults; env overrides file; CLI flag overrides env.
	res, err := Load(Source{
		Mode:      ModeDesktop,
		ConfigDir: dir,
		EnvPrefix: "T_",
		LookupEnv: env(map[string]string{
			"T_SERVER_MEDIA_STAGING_TTL":              "6h",
			"T_AGENT_MAX_CONCURRENT_SUBAGENTS":        "8",
			"T_SERVER_MEDIA_STAGING_CLEANUP_INTERVAL": "2m",
		}),
		Args: []string{"--server-media-staging-cleanup-interval=90s"},
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	c := res.Config
	if c.Server.MaxMediaUploadBytes != 1048576 {
		t.Errorf("MaxMediaUploadBytes = %d, want 1048576", c.Server.MaxMediaUploadBytes)
	}
	if c.Server.MediaStagingTTL != 6*time.Hour {
		t.Errorf("MediaStagingTTL = %v, want 6h", c.Server.MediaStagingTTL)
	}
	if c.Server.MediaStagingCleanupInterval != 90*time.Second {
		t.Errorf("MediaStagingCleanupInterval = %v, want 90s", c.Server.MediaStagingCleanupInterval)
	}
	if c.Agent.MaxConcurrentSubagents != 8 {
		t.Errorf("MaxConcurrentSubagents = %d, want 8", c.Agent.MaxConcurrentSubagents)
	}
}

func TestPhase3WaveAConfigFieldsAndAccessors(t *testing.T) {
	// 1. Verify defaults
	def := Default(ModeDesktop)
	if def.Watermark.MaxWorkers != 4 {
		t.Errorf("default Watermark.MaxWorkers = %d, want 4", def.Watermark.MaxWorkers)
	}
	if def.Watermark.MaxImagePixels != 100_000_000 {
		t.Errorf("default Watermark.MaxImagePixels = %d, want 100000000", def.Watermark.MaxImagePixels)
	}
	if def.Watermark.DecodeConcurrency != 2 {
		t.Errorf("default Watermark.DecodeConcurrency = %d, want 2", def.Watermark.DecodeConcurrency)
	}
	if !def.Binaries.DownloadResume {
		t.Errorf("default Binaries.DownloadResume = %v, want true", def.Binaries.DownloadResume)
	}
	if def.Binaries.DownloadRetryMax != 3 {
		t.Errorf("default Binaries.DownloadRetryMax = %d, want 3", def.Binaries.DownloadRetryMax)
	}
	if def.Binaries.DownloadRetryDelay != 1*time.Second {
		t.Errorf("default Binaries.DownloadRetryDelay = %v, want 1s", def.Binaries.DownloadRetryDelay)
	}
	if def.Media.StagingTTL != 24*time.Hour {
		t.Errorf("default Media.StagingTTL = %v, want 24h", def.Media.StagingTTL)
	}
	if def.Media.StagingGCInterval != 1*time.Hour {
		t.Errorf("default Media.StagingGCInterval = %v, want 1h", def.Media.StagingGCInterval)
	}

	// 2. Accessors on valid defaults
	if got := def.WatermarkMaxWorkers(); got != 4 {
		t.Errorf("WatermarkMaxWorkers() = %d, want 4", got)
	}
	if got := def.WatermarkMaxImagePixels(); got != 100_000_000 {
		t.Errorf("WatermarkMaxImagePixels() = %d, want 100000000", got)
	}
	if got := def.WatermarkDecodeConcurrency(); got != 2 {
		t.Errorf("WatermarkDecodeConcurrency() = %d, want 2", got)
	}
	if got := def.BinariesDownloadResume(); !got {
		t.Errorf("BinariesDownloadResume() = %v, want true", got)
	}
	if got := def.BinariesDownloadRetryMax(); got != 3 {
		t.Errorf("BinariesDownloadRetryMax() = %d, want 3", got)
	}
	if got := def.BinariesDownloadRetryDelay(); got != 1*time.Second {
		t.Errorf("BinariesDownloadRetryDelay() = %v, want 1s", got)
	}
	if got := def.MediaStagingTTL(); got != 24*time.Hour {
		t.Errorf("MediaStagingTTL() = %v, want 24h", got)
	}
	if got := def.MediaStagingGCInterval(); got != 1*time.Hour {
		t.Errorf("MediaStagingGCInterval() = %v, want 1h", got)
	}

	// 3. Accessors fallback on nil or zero/negative
	var nilCfg *Config
	if nilCfg.WatermarkMaxWorkers() != 4 ||
		nilCfg.WatermarkMaxImagePixels() != 100_000_000 ||
		nilCfg.WatermarkDecodeConcurrency() != 2 ||
		!nilCfg.BinariesDownloadResume() ||
		nilCfg.BinariesDownloadRetryMax() != 3 ||
		nilCfg.BinariesDownloadRetryDelay() != 1*time.Second ||
		nilCfg.MediaStagingTTL() != 24*time.Hour ||
		nilCfg.MediaStagingGCInterval() != 1*time.Hour {
		t.Error("nilCfg accessors failed to return safe fallback defaults")
	}

	zeroCfg := &Config{}
	if zeroCfg.WatermarkMaxWorkers() != 4 ||
		zeroCfg.WatermarkMaxImagePixels() != 100_000_000 ||
		zeroCfg.WatermarkDecodeConcurrency() != 2 ||
		zeroCfg.BinariesDownloadRetryMax() != 0 ||
		zeroCfg.BinariesDownloadRetryDelay() != 1*time.Second ||
		zeroCfg.MediaStagingTTL() != 24*time.Hour ||
		zeroCfg.MediaStagingGCInterval() != 1*time.Hour {
		t.Error("zeroCfg accessors failed to return safe fallback defaults")
	}

	// 4. Layer parsing (flags, env, file)
	dir := t.TempDir()
	writeFile(t, dir, FileName, `{
		"watermark": {"max_workers": 8, "max_image_pixels": 50000000},
		"binaries": {"download_resume": false, "download_retry_max": 5},
		"media": {"staging_ttl": "48h"}
	}`)

	res, err := Load(Source{
		Mode:      ModeDesktop,
		ConfigDir: dir,
		EnvPrefix: "T_",
		LookupEnv: env(map[string]string{
			"T_WATERMARK_DECODE_CONCURRENCY":  "6",
			"T_BINARIES_DOWNLOAD_RETRY_DELAY": "2500ms",
			"T_MEDIA_STAGING_GC_INTERVAL":     "30m",
		}),
		Args: []string{
			"--watermark-max-workers=12",
			"--binaries-download-retry-max=7",
		},
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	c := res.Config
	if c.Watermark.MaxWorkers != 12 {
		t.Errorf("Watermark.MaxWorkers = %d, want 12 (flag override)", c.Watermark.MaxWorkers)
	}
	if c.Watermark.MaxImagePixels != 50_000_000 {
		t.Errorf("Watermark.MaxImagePixels = %d, want 50000000 (file)", c.Watermark.MaxImagePixels)
	}
	if c.Watermark.DecodeConcurrency != 6 {
		t.Errorf("Watermark.DecodeConcurrency = %d, want 6 (env)", c.Watermark.DecodeConcurrency)
	}
	if c.Binaries.DownloadResume {
		t.Errorf("Binaries.DownloadResume = true, want false (file)")
	}
	if c.Binaries.DownloadRetryMax != 7 {
		t.Errorf("Binaries.DownloadRetryMax = %d, want 7 (flag override)", c.Binaries.DownloadRetryMax)
	}
	if c.Binaries.DownloadRetryDelay != 2500*time.Millisecond {
		t.Errorf("Binaries.DownloadRetryDelay = %v, want 2500ms (env)", c.Binaries.DownloadRetryDelay)
	}
	if c.Media.StagingTTL != 48*time.Hour {
		t.Errorf("Media.StagingTTL = %v, want 48h (file)", c.Media.StagingTTL)
	}
	if c.Media.StagingGCInterval != 30*time.Minute {
		t.Errorf("Media.StagingGCInterval = %v, want 30m (env)", c.Media.StagingGCInterval)
	}
}

func TestStudioAndTextMediaGenConfig(t *testing.T) {
	def := Default(ModeDesktop)
	if def.MediaGen.TextTimeout != 120*time.Second {
		t.Fatalf("MediaGen.TextTimeout = %v, want 120s", def.MediaGen.TextTimeout)
	}
	if def.MediaGen.TextMaxTokens != 8192 {
		t.Fatalf("MediaGen.TextMaxTokens = %d, want 8192", def.MediaGen.TextMaxTokens)
	}
	if def.MediaGen.TextTemperature != 0.4 {
		t.Fatalf("MediaGen.TextTemperature = %v, want 0.4", def.MediaGen.TextTemperature)
	}
	if def.Studio.DefaultFPS != 24 || def.Studio.DefaultResolution != "1920x1080" ||
		def.Studio.FFmpegPreset != "medium" || def.Studio.FFmpegCRF != 20 ||
		def.Studio.FFmpegTimeout != 15*time.Minute || def.Studio.FFprobeTimeout != 30*time.Second ||
		def.Studio.MaxConcurrentBatch != 4 || def.Studio.QueueWorkers != 2 ||
		def.Studio.QueuePollInterval != 2*time.Second || def.Studio.QueueMaxAttempts != 3 ||
		def.Studio.ExportMaxZipBytes != 512<<20 || def.Studio.ExportLoudnormLUFS != -14.0 ||
		def.Studio.ExportSubtitleFontSize != 24 || def.Studio.TimelineThumbIntervalSec != 2.0 ||
		def.Studio.TimelinePeakBins != 120 {
		t.Fatalf("unexpected default Studio config: %+v", def.Studio)
	}

	dir := t.TempDir()
	writeFile(t, dir, FileName, `{
		"mediagen": {"text_timeout": "90s", "text_max_tokens": 4096, "text_temperature": 0.25},
		"studio": {"default_fps": 30, "default_resolution": "1280x720", "ffmpeg_preset": "fast", "ffmpeg_crf": 18, "ffmpeg_timeout": "10m", "ffprobe_timeout": "20s", "max_concurrent_batch": 6, "queue_workers": 3, "queue_poll_interval": "5s", "queue_max_attempts": 5, "export_max_zip_bytes": 104857600, "export_loudnorm_lufs": -16.0, "export_subtitle_font_size": 28, "timeline_thumb_interval_sec": 1.5, "timeline_peak_bins": 180}
	}`)
	res, err := Load(Source{
		Mode:      ModeDesktop,
		ConfigDir: dir,
		EnvPrefix: "T_",
		LookupEnv: env(map[string]string{
			"T_MEDIAGEN_TEXT_TEMPERATURE": "0.7",
			"T_STUDIO_DEFAULT_FPS":        "60",
			"T_STUDIO_QUEUE_WORKERS":      "4",
		}),
		Args: []string{"--studio-ffmpeg-crf=22"},
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if res.Config.MediaGen.TextTimeout != 90*time.Second || res.Config.MediaGen.TextMaxTokens != 4096 || res.Config.MediaGen.TextTemperature != 0.7 {
		t.Fatalf("unexpected loaded MediaGen text config: %+v", res.Config.MediaGen)
	}
	if res.Config.Studio.DefaultFPS != 60 || res.Config.Studio.FFmpegCRF != 22 || res.Config.Studio.DefaultResolution != "1280x720" ||
		res.Config.Studio.MaxConcurrentBatch != 6 || res.Config.Studio.QueueWorkers != 4 ||
		res.Config.Studio.QueuePollInterval != 5*time.Second || res.Config.Studio.QueueMaxAttempts != 5 ||
		res.Config.Studio.ExportMaxZipBytes != 104857600 || res.Config.Studio.ExportLoudnormLUFS != -16.0 ||
		res.Config.Studio.ExportSubtitleFontSize != 28 || res.Config.Studio.TimelineThumbIntervalSec != 1.5 ||
		res.Config.Studio.TimelinePeakBins != 180 {
		t.Fatalf("unexpected loaded Studio config: %+v", res.Config.Studio)
	}

	for name, mutate := range map[string]func(*Config){
		"zero text_timeout":            func(c *Config) { c.MediaGen.TextTimeout = 0 },
		"zero text_max_tokens":         func(c *Config) { c.MediaGen.TextMaxTokens = 0 },
		"bad text_temperature":         func(c *Config) { c.MediaGen.TextTemperature = 2.5 },
		"negative text_temp":           func(c *Config) { c.MediaGen.TextTemperature = -0.1 },
		"zero studio fps":              func(c *Config) { c.Studio.DefaultFPS = 0 },
		"bad studio resolution":        func(c *Config) { c.Studio.DefaultResolution = "1080p" },
		"empty studio preset":          func(c *Config) { c.Studio.FFmpegPreset = " " },
		"bad studio crf":               func(c *Config) { c.Studio.FFmpegCRF = 52 },
		"zero studio ffmpeg":           func(c *Config) { c.Studio.FFmpegTimeout = 0 },
		"zero studio ffprobe":          func(c *Config) { c.Studio.FFprobeTimeout = 0 },
		"zero max_concurrent":          func(c *Config) { c.Studio.MaxConcurrentBatch = 0 },
		"zero queue_workers":           func(c *Config) { c.Studio.QueueWorkers = 0 },
		"zero queue_poll_intvl":        func(c *Config) { c.Studio.QueuePollInterval = 0 },
		"zero queue_max_attempt":       func(c *Config) { c.Studio.QueueMaxAttempts = 0 },
		"zero export_max_zip":          func(c *Config) { c.Studio.ExportMaxZipBytes = 0 },
		"bad export_loudnorm":          func(c *Config) { c.Studio.ExportLoudnormLUFS = -2.0 },
		"bad export_subtitle_font":     func(c *Config) { c.Studio.ExportSubtitleFontSize = 4 },
		"zero timeline_thumb_interval": func(c *Config) { c.Studio.TimelineThumbIntervalSec = 0 },
		"bad timeline_peak_bins":       func(c *Config) { c.Studio.TimelinePeakBins = 8 },
	} {
		t.Run(name, func(t *testing.T) {
			c := Default(ModeDesktop)
			mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatalf("%s: expected validation error", name)
			}
		})
	}
}
