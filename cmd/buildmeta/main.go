// Command buildmeta keeps packaging metadata consistent with the release version.
//
//	go run ./cmd/buildmeta name                 # prints wails.json "outputfilename"
//	go run ./cmd/buildmeta sync -version 1.2.3  # writes the version into wails.json and frontend/package.json
//
// The git tag is the single source of the version; these files only mirror it
// because Wails (Windows resources, NSIS, Info.plist) and npm read them.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"app/internal/fsutil"
)

const (
	wailsFile   = "wails.json"
	packageFile = "frontend/package.json"
)

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "buildmeta:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: buildmeta name | sync -version X.Y.Z")
	}
	switch args[0] {
	case "name":
		doc, err := readJSON(wailsFile)
		if err != nil {
			return err
		}
		name, _ := doc["outputfilename"].(string)
		if name == "" {
			return errors.New("wails.json has no outputfilename")
		}
		fmt.Print(name)
		return nil
	case "sync":
		fs := flag.NewFlagSet("sync", flag.ContinueOnError)
		version := fs.String("version", "", "semantic version (leading v allowed)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return syncVersion(*version)
	}
	return fmt.Errorf("unknown command %q", args[0])
}

// syncVersion writes the full semver to package.json and the numeric X.Y.Z core to
// wails.json (Windows version resources and NSIS accept digits only).
func syncVersion(v string) error {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return fmt.Errorf("invalid semantic version %q", v)
	}
	full := strings.TrimPrefix(v, "v")
	numeric := strings.Join(m[1:4], ".")

	if err := update(wailsFile, func(doc map[string]any) error {
		info, ok := doc["info"].(map[string]any)
		if !ok {
			return errors.New("wails.json has no info object")
		}
		info["productVersion"] = numeric
		return nil
	}); err != nil {
		return err
	}
	return update(packageFile, func(doc map[string]any) error {
		doc["version"] = full
		return nil
	})
}

func readJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

func update(path string, mutate func(map[string]any) error) error {
	doc, err := readJSON(path)
	if err != nil {
		return err
	}
	if err := mutate(doc); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, buf.Bytes(), 0o644)
}
