package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// FileName is the configuration file name searched in the config and executable directories.
const FileName = "config.json"

// configKey is the meta option selecting an explicit config file.
const configKey = "config"

// Source describes where configuration comes from. Zero values disable a layer.
type Source struct {
	Mode      Mode                         // default mode of the binary
	Args      []string                     // CLI arguments without the program name
	LookupEnv func(string) (string, bool)  // usually os.LookupEnv
	EnvPrefix string                       // e.g. "DEZUXKSTUDIO_"
	ConfigDir string                       // per-user config directory
	ExeDir    string                       // executable directory (portable installs)
	ReadFile  func(string) ([]byte, error) // usually os.ReadFile
}

// Result is a loaded and validated configuration plus provenance information.
type Result struct {
	Config   *Config
	File     string   // config file that was applied ("" if none)
	Warnings []string // non-fatal findings worth logging
}

// EnvPrefixFor derives the environment variable prefix from an app id ("DezuxkStudio" → "DEZUXKSTUDIO_").
func EnvPrefixFor(appID string) string {
	var b strings.Builder
	for _, r := range appID {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		} else {
			b.WriteByte('_')
		}
	}
	return b.String() + "_"
}

// Load applies defaults → file → environment → flags, resolves secrets and validates.
// It returns flag.ErrHelp when -h/--help is requested.
func Load(src Source) (*Result, error) {
	if src.ReadFile == nil {
		src.ReadFile = os.ReadFile
	}
	cfg := Default(src.Mode)
	res := &Result{Config: cfg}
	origin := map[string]string{} // key → layer that last set it

	// Flags are parsed twice: first to discover --config and fail fast on bad flags,
	// then for real after the lower layers have been applied.
	explicitFile, err := preParseConfigFlag(src)
	if err != nil {
		return nil, err
	}
	if explicitFile == "" && src.LookupEnv != nil {
		explicitFile, _ = src.LookupEnv(src.EnvPrefix + strings.ToUpper(configKey))
	}

	file, err := locateFile(explicitFile, src)
	if err != nil {
		return nil, err
	}
	if file != "" {
		if err := applyFile(cfg, file, src.ReadFile, origin); err != nil {
			return nil, err
		}
		res.File = file
	}
	if err := applyEnv(cfg, src, origin); err != nil {
		return nil, err
	}
	if err := applyFlags(cfg, src.Args, origin); err != nil {
		return nil, err
	}
	if err := resolveTokenFile(cfg, src.ReadFile); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	for _, f := range fields(cfg) {
		if f.secret && f.val.String() != "" && (origin[f.key] == "file" || origin[f.key] == "flag") {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s is set via %s; prefer %s or the OS credential store", f.key, origin[f.key], f.envName(src.EnvPrefix)))
		}
	}
	res.Warnings = append(res.Warnings, cfg.warnings()...)
	return res, nil
}

// Usage writes every option with its flag, environment variable and default value.
func Usage(w io.Writer, mode Mode, envPrefix string) {
	def := Default(mode)
	fmt.Fprintf(w, "Options (flag / environment / default):\n")
	fmt.Fprintf(w, "  --%s  %s%s\n      path to %s\n", configKey, envPrefix, strings.ToUpper(configKey), FileName)
	for _, f := range fields(def) {
		fmt.Fprintf(w, "  --%s  %s  [%s]\n      %s\n", f.flagName(), f.envName(envPrefix), f.val.String(), f.usage)
	}
}

func newFlagSet(cfg *Config, configPath *string) *flag.FlagSet {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(configPath, configKey, "", "path to "+FileName)
	for _, f := range fields(cfg) {
		fs.Var(f.val, f.flagName(), f.usage)
	}
	return fs
}

func preParseConfigFlag(src Source) (string, error) {
	var path string
	fs := newFlagSet(Default(src.Mode), &path)
	if err := fs.Parse(src.Args); err != nil {
		return "", err
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("config: unexpected arguments: %v", fs.Args())
	}
	return path, nil
}

func locateFile(explicit string, src Source) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("config: resolve %s: %w", explicit, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("config: explicit config file not usable: %w", err)
		}
		return abs, nil
	}
	for _, dir := range []string{src.ConfigDir, src.ExeDir} {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, FileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("config: stat %s: %w", candidate, err)
		}
	}
	return "", nil
}

func applyFile(cfg *Config, path string, read func(string) ([]byte, error), origin map[string]string) error {
	data, err := read(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}

	flat := map[string]any{}
	flatten("", root, flat)
	index := indexFields(cfg)

	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var errs []error
	for _, k := range keys {
		f, ok := index[k]
		if !ok {
			errs = append(errs, fmt.Errorf("unknown key %q", k))
			continue
		}
		if err := setFromJSON(f.val, flat[k]); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", k, err))
			continue
		}
		origin[k] = "file"
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %s: %w", path, errors.Join(errs...))
	}
	return nil
}

func flatten(prefix string, in map[string]any, out map[string]any) {
	for k, v := range in {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if nested, ok := v.(map[string]any); ok {
			flatten(key, nested, out)
			continue
		}
		out[key] = v
	}
}

func setFromJSON(v value, raw any) error {
	switch t := raw.(type) {
	case nil:
		return errors.New("null is not allowed")
	case string:
		return v.Set(t)
	case json.Number:
		return v.Set(t.String())
	case bool:
		if t {
			return v.Set("true")
		}
		return v.Set("false")
	case []any:
		lv, ok := v.(listValue)
		if !ok {
			return errors.New("array is not allowed for this option")
		}
		items := make([]string, 0, len(t))
		for _, it := range t {
			s, ok := it.(string)
			if !ok {
				return errors.New("array items must be strings")
			}
			items = append(items, s)
		}
		return lv.SetList(items)
	}
	return fmt.Errorf("unsupported JSON value %T", raw)
}

func applyEnv(cfg *Config, src Source, origin map[string]string) error {
	if src.LookupEnv == nil {
		return nil
	}
	var errs []error
	for _, f := range fields(cfg) {
		name := f.envName(src.EnvPrefix)
		raw, ok := src.LookupEnv(name)
		if !ok {
			continue
		}
		if err := f.val.Set(raw); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		origin[f.key] = "env"
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: environment: %w", errors.Join(errs...))
	}
	return nil
}

func applyFlags(cfg *Config, args []string, origin map[string]string) error {
	var ignored string
	fs := newFlagSet(cfg, &ignored)
	if err := fs.Parse(args); err != nil {
		return err
	}
	byFlag := map[string]string{}
	for _, f := range fields(cfg) {
		byFlag[f.flagName()] = f.key
	}
	fs.Visit(func(fl *flag.Flag) {
		if key, ok := byFlag[fl.Name]; ok {
			origin[key] = "flag"
		}
	})
	return nil
}

func indexFields(cfg *Config) map[string]field {
	idx := map[string]field{}
	for _, f := range fields(cfg) {
		idx[f.key] = f
	}
	return idx
}

func resolveTokenFile(cfg *Config, read func(string) ([]byte, error)) error {
	if cfg.Server.AuthTokenFile == "" {
		return nil
	}
	if cfg.Server.AuthToken != "" {
		return errors.New("config: set only one of server.auth_token and server.auth_token_file")
	}
	data, err := read(cfg.Server.AuthTokenFile)
	if err != nil {
		return fmt.Errorf("config: read server.auth_token_file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return errors.New("config: server.auth_token_file is empty")
	}
	cfg.Server.AuthToken = token
	return nil
}
