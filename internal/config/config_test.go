package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		"desktop non-loopback":      func(c *Config) { c.Server.Host = "0.0.0.0" },
		"server public no token":    func(c *Config) { c.Mode = ModeServer; c.Server.Host = "0.0.0.0" },
		"broadcast without token":   func(c *Config) { c.Mode = ModeServer; c.Server.EnableBroadcast = true },
		"client without remote":     func(c *Config) { c.Mode = ModeClient },
		"client http remote":        func(c *Config) { c.Mode = ModeClient; c.Client.RemoteURL = "http://x" },
		"client creds in url":       func(c *Config) { c.Mode = ModeClient; c.Client.RemoteURL = "ws://u:p@x/ws" },
		"wildcard origin desktop":   func(c *Config) { c.Server.AllowedOrigins = []string{"*"} },
		"window below minimum":      func(c *Config) { c.Desktop.Width = 100 },
		"bad colour":                func(c *Config) { c.Desktop.Background = "red" },
		"db file with path":         func(c *Config) { c.Storage.DBFile = "../x.db" },
		"http update feed":          func(c *Config) { c.Update.FeedURL = "http://example.com/feed" },
		"zero timeout":              func(c *Config) { c.Realtime.ActionTimeout = 0 },
		"unknown log level":         func(c *Config) { c.Log.Level = "loud" },
		"port out of range":         func(c *Config) { c.Server.Port = 70000 },
		"tiny max message":          func(c *Config) { c.Realtime.MaxMessageBytes = 10 },
		"negative log backups":      func(c *Config) { c.Log.MaxBackups = -1 },
		"zero backups to keep":      func(c *Config) { c.Storage.BackupsToKeep = 0 },
		"zero diagnostics keep":     func(c *Config) { c.Diagnostics.Keep = 0 },
		"zero ready timeout":        func(c *Config) { c.Desktop.ReadyTimeout = 0 },
		"size smaller than minimum": func(c *Config) { c.Desktop.Width = c.Desktop.MinWidth - 1 },
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
